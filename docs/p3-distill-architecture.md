# 蒸馏管线技术架构

> 对应源码：`internal/distill/`、`internal/llm/`、`internal/store/schema.go`（v3–v6 迁移）
> 状态：**已实现并经 500 题实测验证**

---

## 1. 总体流程

```
题目全集 (27,449 去重实体 / 58,890 出现)
  │
  ├─ ① 过滤：answer_type ∈ (single, multi, judge)
  │
  ├─ ② 抽样（可选）：按模块分层轮转打散，固定种子
  │
  ├─ ③ 分桶：按 (module, 源标签 raw_tag) 分成 ~35 个不可拆单元
  │
  ├─ ④ 计划：已归属桶 → 原模型；未归属桶 → 共享队列（大桶优先）
  │
  ├─ ⑤ 执行：每模型一个 Runner，各有独立客户端/并发/退避
  │     └─ 单题：构造 prompt → LLM → 解析 JSON → 校验 → 入库 → 落盘
  │
  └─ ⑥ 汇总：label_run.engines 记录按模型聚合的用量与质量
```

## 2. 调度算法

### 2.1 分桶（`internal/distill/partition.go`）

桶 = `(module, raw_tag)`。`raw_tag` 是源数据自带的粗标签（逻辑填空 / 片段阅读 / 增长 / 数学运算 …），不是蒸馏产出的二级题型。

**为什么用源标签而不是二级题型**：源标签在标注**之前**就存在，而二级题型正是待产出的东西。用未知的东西切已知的工作是切不动的。

实测全库分出 22–35 个桶（取决于抽样范围）。

### 2.2 动态领取 + 归属粘滞（`internal/distill/schedule.go`）

```go
owners := loadBucketOwners(runDir/runID/bucket_owner.json)
owned, queue, reassigned := buildWorkPlan(buckets, engines, owners)

// owned[i]  = 粘滞归属给 engines[i] 的桶（续跑时先做这些）
// queue     = 共享队列，大桶优先，各执行器谁空谁领
```

**关键不变式**（有测试守护）：

> 任何一个（模块 × 题型）桶内部的标注，**始终出自同一个模型**。

原因：不同模型对同一考点的措辞天然不同——一个写「实词辨析」、另一个可能写「词语辨析」。混着跑会让 1:1 率变差、聚合层被污染（参考文档里 11,282 题产出 10,375 个三级考点的翻版）。

归属落盘用「先写临时文件再原子改名」，因为**进程被杀时内存里的归属会丢**，而没落盘的归属等于没归属。

**原归属模型不在本次列表时**（如它被限流下线），桶重新分配并告警——桶内会出现两种模型的措辞，但 `label.model` 有记录，可事后过滤。

### 2.3 429 退避分级

三种失败模式的正确等待时间差三个数量级：

| 失败类型 | 判据 | 退避 | 理由 |
|---|---|---|---|
| 上游渠道故障 | `503 No available channel` | 60 秒 | 这是 outage，不是我们请求太快 |
| 配额限流 | `429 rpm/tpm` | 20/40/60 秒 | 配额窗口按分钟计，秒级退避撞不上下一个窗口；429 不消耗 token，多等多试是零成本 |
| 网关建议 | `Retry-After` 头 | 照它说的等 | 网关比我们清楚 |
| 其他可重试 | 5xx、网络 | 3/6/12/24/48 秒 | 指数退避 |

## 3. 数据模型（迁移 v5–v6）

```sql
-- 批次
CREATE TABLE label_run (
  id               TEXT PRIMARY KEY,      -- run_id
  scope            TEXT,                  -- JSON：过滤条件与调度元信息
  prompt_version   TEXT,
  taxonomy_version TEXT,
  model_config     TEXT,                  -- 逗号分隔的模型列表
  model_response   TEXT,
  base_url         TEXT,
  status           TEXT NOT NULL,         -- running / finished / failed
  total, ok, failed INTEGER,
  tokens_in, tokens_out INTEGER,
  cost_usd         REAL,
  engines          TEXT                   -- JSON：按模型聚合的统计
);

-- 标注（每题一条）
CREATE TABLE label (
  id               INTEGER PRIMARY KEY,
  question_id      INTEGER NOT NULL REFERENCES question(id),
  run_id           TEXT NOT NULL REFERENCES label_run(id),
  model            TEXT,                  -- ★ 产地标记：这条标注出自哪个模型
  subject, secondary, tertiary, detail TEXT,
  question_model, reasoning_chain, fastest_solution, pitfalls,
  template, key_features, boundary, confusable, typical_ask, doubt TEXT,
  tokens_in, tokens_out, latency_ms INTEGER,
  created_at       TEXT,
  UNIQUE(question_id, run_id)              -- ★ 断点续跑的依据
);
CREATE INDEX idx_label_tertiary ON label(tertiary);
CREATE INDEX idx_label_model     ON label(model);
```

**`label.model` 是硬要求**（参考文档 §3.10）：混用来源的数据必须标记产地，否则下游无法过滤和回溯，"质量口径会永久混在一起"。

**`UNIQUE(question_id, run_id)` 是断点续跑的全部机制**：续跑时查已完成的 question_id 集合并跳过，续写用 `INSERT OR REPLACE`。

## 4. 单题处理流程

```go
func (r *Runner) processOne(ctx, q, sysPrompt, out) {
    in := buildInput(q)                       // 用 stem_with_text / explanation_with_formula
    for attempt := 1..maxRetries+1 {
        resp, err := client.Chat(...)
        if err != nil { backoff(err, attempt); continue }

        label, err := ParseLabel(resp.Content)   // 剥壳 + JSON 解析 + 转义修复
        if err != nil { sleep(1s); continue }

        v := &Validation{}
        v.Validate(label, tax, q.Module, q.Answer)
        if v.FatalCount() > 0 { sleep(1s); continue }   // 错误回灌给模型

        r.record(ctx, q, label, v, resp, out)     // 入库 + 落盘
        return
    }
    // 全部失败 → 记入 failures.jsonl，区分可重试/不可重试
}
```

**重试的错误回灌**：第二次尝试会把上次的校验错误附在 prompt 末尾，让模型有机会修正格式而不是重复犯错。

## 5. 字段校验分级

**致命项**（必须重试）：一级科目不在规范表 / 与题目模块不一致、二级题型不在枚举、三级考点前缀缺失或与二级题型不符、推理链为空、题干关键特征为空、考点细节为空。

**警告项**（记账不重试）：适用边界为空、推理链少于 3 步、易错点为空、典型提问为空、conclusion 与官方答案不一致。

**规范表外新增**：三级考点不在表里但前缀合法 → 接受并记入 `NovelConcepts`，供人工审查后回流规范表。

## 6. 断点续跑

两个检查点，缺一不可：

1. **库**：`label` 表的 `UNIQUE(question_id, run_id)`。续跑时查已完成的 question_id 集合。
2. **盘**：`output.jsonl`（逐题追加 flush）+ `failures.jsonl`。盘上是审计凭据，库是查询入口。

**归属粘滞**（`bucket_owner.json`）保证续跑时桶回到原模型——否则中断续跑会让桶被两个模型各做一半。

实测验证：第一轮跑到 241/300 时进程被杀，续跑准确恢复（三个执行器各自拿回粘滞桶、241 题自动跳过、从桶内断点继续）。

## 7. 关键文件

| 文件 | 职责 |
|---|---|
| `internal/distill/partition.go` | 分桶（`BuildBuckets`） |
| `internal/distill/schedule.go` | 动态领取 + 归属粘滞（`buildWorkPlan`、`bucketOwners`） |
| `internal/distill/multi.go` | 多引擎编排（`RunMulti`） |
| `internal/distill/runner.go` | 单执行器、单题处理、重试退避、进度输出 |
| `internal/distill/prompt.go` | 提示词生成（由规范表程序化生成） |
| `internal/distill/schema.go` | 产出契约与校验（`ParseLabel`、`Validation`） |
| `internal/distill/taxonomy.go` | 规范表加载与三级考点校验 |
| `internal/llm/client.go` | OpenAI 兼容客户端、错误分级、`ListModels` |
| `tools/ocr_formula.py` / `tools/ocr_text.py` | PaddleOCR worker（公式/文本识别） |

## 8. 实测性能

| 指标 | 值 | 备注 |
|---|---|---|
| 单题 token | 约 3.3k（输入+输出） | 输入约 2.6k、输出约 1.4k（含重试） |
| 生成速度 | 约 8.6 tokens/秒 | 单请求 |
| 吞吐（3 模型动态） | 约 15 题/分钟 | 500 题实测：460 成功 / 30m27s |
| 成功率 | 95%（`sensenova-6.8-flash-lite`） | 失败几乎全是端点限流 |
| 全量 27,449 题 | 约 30 小时 | 不含端点额度耗尽的等待 |

## 9. 已知限制

1. **端点配额是主要瓶颈**。套餐级额度耗尽（`token plan entitlement exhausted`）时整批请求被拒，只能等周期性恢复。这不由代码决定。
2. **收尾成本**。批次最后 10% 通常是最慢的（长尾桶撞上额度耗尽），需要多轮续跑。实测 289/300 之后连续三轮几乎无进展。
3. **1:1 率需要 200+ 题才有意义**。50 题摊在 17 个题型上、其中 6 个只有 1 题，那个数字量的是抽样设计而不是规范表质量。
4. **槽位粒度会被做假**。把考点槽位做粗能让 1:1 率变漂亮（一个筐吃下所有题）。必须配合"每题考点数"和"抽查槽位内的题是否真属同类"一起看。
5. **疑点率是模型特异信号**，不能跨模型比。实测 deepseek 16% / glm 12% / sensenova 7%；"某模块疑点率 0%"往往是因为该模块全部由同一个模型标注。
