# 实施规格：进入演示阶段与全量蒸馏

> **2026-09-30 网站实现更新**：任务 A/B/C 已完成，验收记录见 [website-completion.md](website-completion.md)，接口见 [api-design.md](api-design.md)。按用户最新要求，任务 D 的实际运行后置到网站服务器部署验收之后，本次未启动蒸馏。2 核 4GB 部署教程见 [deployment-2c4g.md](deployment-2c4g.md)。以下保留原任务规格作为验收依据。

> 本文档是**给执行者（Codex）看的任务规格**。每项都有明确的验收标准。
> 前置阅读：[`p3-distill-architecture.md`](./p3-distill-architecture.md)、[`p3-test-results.md`](./p3-test-results.md)

---

# 第一部分：当前状态

## 已完成

| 阶段 | 内容 | 验收 |
|---|---|---|
| P0 | 解析入湖 | 2,956 篇笔记 → 58,890 题块 → 27,449 题实体，契约 7 项全过 |
| P1 | 去重与统计 | 指纹口径经 qid 基准实测选定（题干+选项，-0.09% 偏差） |
| P2 | 公式还原 | 公式图 33,309 + 题目图 6,152 全部识别，占位符残留率 **0.0%** |
| P3 | 蒸馏管线 | 500 题实测：吞吐 15 题/分钟、完备率 100%、题型内 1:1 率 20.7% |
| — | 管理入口 | Vue3 + embed.FS，密钥加密存储，五条安全约束已验证 |
| — | 规范表 v1.5 | 126 三级考点，10 条边界规则，自洽测试通过 |

## 未完成（演示所需）

| # | 缺口 | 影响 |
|---|---|---|
| 1 | **题目查询 API** | 浏览器里看不到任何题 |
| 2 | **练习/判分 API** | 无法做题 |
| 3 | **搜索 API** | 无法检索 |
| 4 | **前端业务页面** | 只有 Login/Settings 两个管理页 |
| 5 | **用户体系** | 无法区分谁做的题 |
| 6 | 全量蒸馏 | 仅 546/27,449 道有标注（2%） |

**结论：能演示"管理后台"，不能演示"刷题站"。** 下面按依赖顺序给出补齐方案。

---

# 第二部分：任务清单

## 任务 A：题目查询与练习 API

**前置**：无（可立即开始）
**预估**：4–6 小时
**不依赖**：蒸馏产物、全量蒸馏

### A1. 建表（用户与练习）

```sql
-- 迁移 v7
CREATE TABLE app_user (
  id            INTEGER PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,          -- argon2id 或 bcrypt
  nickname      TEXT,
  created_at    TEXT,
  last_login_at TEXT
);

CREATE TABLE practice_session (           -- 一次练习/模考
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES app_user(id),
  kind          TEXT NOT NULL,          -- single | paperset | concept | wrongbook
  spec          TEXT,                   -- JSON：组卷条件快照（保证可复现）
  question_ids  TEXT,                   -- JSON 数组：冻结的题序
  started_at    TEXT,
  submitted_at  TEXT,
  total         INTEGER, correct INTEGER, duration_ms INTEGER
);

CREATE TABLE practice_answer (            -- 一次作答
  id            INTEGER PRIMARY KEY,
  session_id    INTEGER NOT NULL REFERENCES practice_session(id),
  question_id   INTEGER NOT NULL REFERENCES question(id),  -- 引用题实体，不存副本
  ord           INTEGER NOT NULL,        -- 卷内题号
  user_answer   TEXT,                    -- 用户提交的答案字母
  is_correct    INTEGER,
  duration_ms   INTEGER,
  answered_at   TEXT,
  UNIQUE(session_id, ord)
);

CREATE TABLE wrongbook (                  -- 错题本
  user_id       INTEGER NOT NULL,
  question_id   INTEGER NOT NULL REFERENCES question(id),
  source        TEXT,                    -- auto（判错收录）| manual（收藏）
  wrong_count   INTEGER DEFAULT 1,
  last_wrong_at TEXT,
  resolved      INTEGER DEFAULT 0,       -- 是否已订正
  PRIMARY KEY (user_id, question_id)
);

CREATE TABLE user_concept_stat (          -- 考点掌握度（由作答聚合，可重算）
  user_id       INTEGER NOT NULL,
  concept_id    INTEGER NOT NULL,
  answered      INTEGER, correct INTEGER,
  mastery       REAL,                    -- 0..1
  updated_at    TEXT,
  PRIMARY KEY (user_id, concept_id)
);
```

**设计要点**：

- `practice_answer` 引用 `question_id` 而非拷题目副本。代价是题目被修正后历史展示会变；收益是库不膨胀（否则每次练习存一份题干）。
- `practice_session.question_ids` 冻结了**当时的题序**，已足够追溯"当时考的是哪套题"。
- `user_concept_stat` 是**派生视图**，真相源是 `practice_answer`。必须能重算。

### A2. 查询接口

**注意答案隔离**：`GET /api/questions/:id` 绝不能返回答案与解析，否则前端可抓到答案。

```
GET /api/papers?exam_type=&year=&region=&module=&variant=
    → [{paper_id, name, exam_type, year, region, module, question_count}]

GET /api/questions?module=&year=&exam_type=&concept_id=&page=&size=&order=
    → {total, items: [{id, module, stem, option_count, has_figure, has_label}]}
    ※ 不含 answer / explanation

GET /api/questions/:id
    → {id, module, stem, stem_with_text, material_body, options:[{label,content}],
       has_figure, has_label, label:{...}}
    ※ 仍不含 answer / explanation

GET /api/questions/:id/reveal
    → 上面全部 + {answer, answer_type, explanation_with_formula, label}
    ※ 单独接口，前端在"提交作答后"才调用；服务端应校验该用户确实作答过

GET /api/concepts?module=&secondary=
    → [{id, name, module, secondary, question_count}]

GET /api/concepts/:id
    → {id, name, ...,  summary, key_features, boundary, confusable,
       related:[{concept_id, name, kind, signal}], sample_questions:[...]}
```

`label` 表的**实际列**（不要凭想象设计）：

```
subject, secondary, tertiary, detail, question_model, reasoning_chain(JSON数组),
fastest_solution, pitfalls(JSON数组), template, key_features(JSON数组),
boundary, confusable(JSON数组:{考点,区分信号}), typical_ask(JSON数组), doubt,
model, run_id, tokens_in, tokens_out, latency_ms
```

**考点 id 从哪来**：当前 `label.tertiary` 存的是**字符串**（如 `增长-基期量计算`），不是数值 id。P5 聚合层尚未实现（`concept` 表还没建）。所以现在只能按 tertiary 字符串分组：

```sql
SELECT tertiary, COUNT(*) FROM label GROUP BY tertiary
```

如果演示需要稳定的 concept_id，可以先建一个轻量映射表（见任务 C）。

### A3. 练习与判分接口

```
POST /api/practice/sessions
    body: {kind: "single"|"paperset"|"concept"|"wrongbook",
           spec: {...}}                  // 组卷条件
    → {session_id, questions: [...]}     // 冻结题序，不含答案

POST /api/practice/sessions/:id/submit
    body: {answers: [{question_id, answer, duration_ms}]}
    → {correct, total, results: [{question_id, is_correct, correct_answer,
                                 explanation, label}]}
    ※ 判分在服务端做；顺手把错题写进 wrongbook（source=auto）

GET  /api/practice/sessions/:id           // 恢复未提交的练习
GET  /api/practice/records?limit=&offset= // 历史记录
```

**判分必须支持**（实测数据里的四种形态）：

| answer_type | 数量 | 判分方式 |
|---|---|---|
| `single` | 26,989 | 字符串相等 |
| `multi` | 349 | 字母集合相等（顺序无关） |
| `judge` | 107 | 答案 A=正确、B=错误，**前端要渲染「正确/错误」按钮** |
| `other` | 4 | 答案缺失（`（缺）`），前端禁用作答 |

选项标签**可超出 A–D**：陕西省考有 8 个选项（A–H），答案是 H 合法。判分与渲染都不能按 A–D 写死。

### A4. 搜索接口

```
GET /api/search?q=&mode=keyword|semantic|concept&module=&limit=
    → [{question_id, brief, module, tertiary, score, highlights}]
```

**当前只能做关键词**。原因：
- 概念表（`concept`）还没建（P5）
- 嵌入模型还没接（P7）
- 465 道题的向量检索意义不大

关键词用 SQLite **FTS5**。需要先建索引（迁移 v7）：

```sql
CREATE VIRTUAL TABLE question_fts USING fts5(
  stem, explanation, content='question', content_rowid='id',
  tokenize='unicode61 remove_diacritics 2'
);
-- 中文需要额外处理：unicode61 不切分中文。
-- 两个可行方案：
--   (a) 按二元组（bigram）索引：对中文串生成相邻字符对作为 token
--   (b) 演示阶段先只搜数字/英文/考点名，中文搜索后置
```

**建议演示阶段先只支持"按考点名 + 模块 + 年份 + 地区筛选"**，这已足够展示价值，全文检索可以放到 P6.5。

---

## 任务 B：前端业务页面

**前置**：任务 A1–A3
**预估**：6–8 小时

### B1. 路由（新建 `web/src/router/index.js`）

```js
import { createRouter, createWebHistory } from 'vue-router'
import Login from '../views/Login.vue'
import Settings from '../views/Settings.vue'
import QuestionList from '../views/QuestionList.vue'     // 新建
import Practice from '../views/Practice.vue'           // 新建
import WrongBook from '../views/WrongBook.vue'         // 新建
import ConceptList from '../views/ConceptList.vue'     // 新建
import ConceptDetail from '../views/ConceptDetail.vue' // 新建

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    { path: '/admin', component: Settings },
    { path: '/', redirect: '/questions' },
    { path: '/questions', component: QuestionList },
    { path: '/practice', component: Practice },
    { path: '/wrongbook', component: WrongBook },
    { path: '/concepts', component: ConceptList },
    { path: '/concepts/:id', component: ConceptDetail },
  ],
})
```

`App.vue` 需加路由出口 `<router-view />`，并把 `admin` 路由与业务路由区分（admin 要鉴权）。

### B2. 页面清单

| 页面 | 核心功能 | 关键点 |
|---|---|---|
| `QuestionList.vue` | 按模块/年份/地区/考试类型筛选，列表展示题干摘要 | **只调不含答案的接口** |
| `Practice.vue` | 单题练习：题干 + 选项 + 提交 + 判分 + 解析 | 提交后才调 `/reveal` |
| `WrongBook.vue` | 错题列表 + 重做 | 从 `wrongbook` 表读 |
| `ConceptList.vue` | 考点树（6 模块 → 26 二级 → 126 三级） | 数据来自 `label.tertiary` 分组 |
| `ConceptDetail.vue` | 考点卡：问法模型/边界/易混考点 + 实例题 | 展示蒸馏产出的 12 字段 |

### B3. 公式渲染

P2 已把公式图还原成 LaTeX（`$...$`），但前端**还没有 KaTeX**。

```bash
cd web && npm install katex
```

```js
// main.js
import 'katex/dist/katex.min.css'
// 渲染时：import katex from 'katex'; katex.renderToString(tex, {throwOnError:false})
```

**注意**：`DisplayTeX` 只对公式图包 `$`，对含中文的片段不包（避免 KaTeX 渲染汉字）。前端要容错：渲染失败时退回显示原文，而不是白屏。

### B4. 构建与嵌入

```bash
cd web && npm run build     # 产物直接输出到 ../cmd/gk/web_dist
cd .. && make build        # go:embed 重新打包进二进制
```

`vite.config.js` 已配好 `outDir: '../cmd/gk/web_dist'`，无需改。

---

## 任务 C：轻量考点视图（可选，但强烈建议）

**前置**：任务 A2
**预估**：1–2 小时

### 为什么需要

用户问的通常是"增长量和增长率怎么区分"——**这是考点级问题，不是某道题**。而 `label.tertiary` 是字符串，没有稳定 id 供前端路由。

### 最小实现

```sql
-- 迁移 v7（可选）
CREATE TABLE concept (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,   -- 即 tertiary 全名，如 '增长-基期量计算'
  module        TEXT NOT NULL,
  secondary     TEXT NOT NULL,
  question_count INTEGER NOT NULL DEFAULT 0,
  avg_confidence REAL                  -- 留空
);
```

从 `label` 聚合生成：

```sql
INSERT OR IGNORE INTO concept(name, module, secondary)
SELECT tertiary, subject, secondary FROM label WHERE tertiary IS NOT NULL;

UPDATE concept SET question_count = (
  SELECT COUNT(DISTINCT question_id) FROM label WHERE tertiary = concept.name
);
```

完整版（考点实体 + 晋级门 + 关系图）是 **P5**，工作量大得多。演示阶段用上面的轻量版即可。

---

## 任务 D：全量蒸馏

**前置**：无（但建议等端点额度充足时再启动）
**预估**：约 30 小时有效跑批 + 额度耗尽的等待

### D1. 启动

```bash
cd /home/tuigou/桌面/Code/play/go-study/ai_analyze_guokao

# 先用 --dry-run 看工作单元
./var/gk distill run --models auto --limit 27449 --dry-run --run full-gk-20260930

# 正式启动（必须用 setsid 脱离会话，否则终端断开会连带杀进程）
setsid nohup ./var/gk distill run \
  --db var/db/gk.sqlite \
  --models "sensenova-6.8-flash-lite,glm-5.2,deepseek-v4-flash" \
  --limit 27449 \
  --wait-available 30m \
  --run full-gk-20260930 \
  > var/logs/full.log 2>&1 < /dev/null &
```

**必须用 `setsid`**：实测中进程曾因监控命令被取消而连带被杀（`nohup` 不够，它仍在同一进程组）。

### D2. 续跑

中断后重跑**同一条命令**（同 `--run`）：

```bash
setsid nohup ./var/gk distill run --db var/db/gk.sqlite \
  --models "..." --limit 27449 --wait-available 30m \
  --run full-gk-20260930 >> var/logs/full.log 2>&1 < /dev/null &
```

已完成的题自动跳过（`label` 表唯一约束），桶回到原模型（`bucket_owner.json` 粘滞）。

### D3. 监控

```bash
# 进度（每 10 分钟看一次即可，别高频轮询）
sqlite3 var/db/gk.sqlite "SELECT COUNT(*) FROM label WHERE run_id='full-gk-20260930';"

# 按模型的分布（看动态调度是否健康）
sqlite3 var/db/gk.sqlite "SELECT model, COUNT(*) FROM label WHERE run_id='full-gk-20260930' GROUP BY 1;"

# 实时日志（每题一行）
tail -f var/logs/full.log
```

### D4. 批次切分（可选）

如果不打算一次跑完，可按模块切分（每批独立 run_id，互相不干扰）：

```bash
for m in 政治理论 常识判断 言语理解与表达 数量关系 判断推理 资料分析; do
  ./var/gk distill run --module "$m" --run "full-$m" --models auto
done
```

优点：单个模块失败不影响其它；缺点：跨模块的桶归属不共享（但桶本来就不跨模块，无影响）。

---

# 第三部分：优先级建议

## 如果只做一件事

**任务 A1 + A2 + A3 + B1 + B2（QuestionList + Practice）** —— 约 6–8 小时，能演示"选模块 → 刷题 → 判分 → 看解析"的完整闭环。数据已就绪（27,449 题带选项与解析），**不需要等全量蒸馏**。

`label`（考点标注）在演示中不是必需的——`explanation_with_formula`（官方解析 + 公式还原）已经足够展示。

## 如果做两件事

加上**任务 C**（轻量考点视图）+ `ConceptList.vue` + `ConceptDetail.vue` —— 再加 3–4 小时，能演示"按考点组织题库"。但要注意：**只有 546 道题有标注**，考点视图会显得稀疏。要么接受这个现状（数据真实），要么等全量蒸馏完成。

## 不建议现在做

- 智能问答/解析（P7）：依赖全量蒸馏的标注质量，现在样本太少
- 智能出题：依赖完整考点体系
- 语义检索：依赖向量索引 + 嵌入模型

---

# 第四部分：已知坑位

| 坑 | 说明 | 对策 |
|---|---|---|
| **不要用 `INSERT OR IGNORE` 写用户数据** | 会静默吞掉冲突 | 用户体系用标准事务 |
| **答案隔离** | 题目详情接口泄露答案 = 前端可抓包看到 | 单独 `/reveal` 接口 + 校验作答记录 |
| **判断题渲染** | 107 道判断题用 A/B 表示正确/错误 | 前端必须渲染「正确/错误」按钮，不是 A/B/C/D |
| **选项可超出 A–D** | 陕西卷有 A–H 八选项 | 判分与渲染都别写死 4 个选项 |
| **中文分词** | SQLite FTS5 的 unicode61 不切分中文 | 演示阶段先用筛选，不用全文检索 |
| **KaTeX 渲染失败** | 含中文的片段不该被包进 `$` | 渲染失败退回显示原文 |
| **`setsid`** | 终端断开/命令取消会连带杀进程 | 长跑批处理必须 `setsid nohup ... < /dev/null &` |
| **不要删 `data/`** | 816MB 数据集，解析入库约 6 秒可重建 | 但图片 OCR 结果在 `image.ocr_tex`，删了要重跑 81 分钟 |

---

# 第五部分：验收清单

跑完任务后逐条确认：

**任务 A**
- [x] `GET /api/questions?module=资料分析` 返回题目列表，**响应体里搜不到答案字段**
- [x] `GET /api/questions/:id/reveal` 在未作答时返回 403
- [x] 提交作答后 `/reveal` 返回 `explanation_with_formula`，其中 LaTeX 已还原（能看到 `$\frac{...}{...}$`）
- [x] 判分支持 single / multi / judge 三种形态；judge 的 A/B 正确映射为 正确/错误
- [x] 8 选项的题能正常判分（用陕西卷题目验证）
- [x] 错题自动进 `wrongbook`，重做后 `resolved` 可置 1

**任务 B**
- [x] 未登录访问 `/` 跳到 `/login`
- [x] `/questions` 能按模块/年份筛选，列表页不显示答案
- [x] `/practice` 提交后展示判分与解析，公式用 KaTeX 正确渲染
- [x] 解析里的中文片段没有被 KaTeX 破坏（显示为原文而非乱码）
- [x] `npm run build && make build` 后单二进制能独立提供前端（不依赖 node_modules）

**任务 D**
- [ ] `setsid` 启动后终端断开，进程仍存活
- [ ] 重跑同 `--run` 时已完成的题被跳过（日志出现"跳过（已完成）N"）
- [ ] 桶内标注不跨模型（抽查 `label.model` 分组）
