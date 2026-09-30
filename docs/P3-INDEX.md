# P3 蒸馏管线 - 详细实施文档

本目录存放 P3 阶段的完整实施文档，包括蒸馏管线架构、实测数据、规范表版本变更、以及下一步开发计划。

## 文档索引

| 文件名 | 说明 |
|---|---|
| [`p3-distill-architecture.md`](./p3-distill-architecture.md) | 蒸馏管线的技术架构、调度算法、数据库 schema |
| [`p3-test-results.md`](./p3-test-results.md) | 四轮实测数据（50→300→500 题）、规范表 v1.2→v1.5 变更 |
| [`p3-taxonomy-guide.md`](./p3-taxonomy-guide.md) | 规范表的 126 个三级考点、字段语义、边界规则 |
| [`p3-next-steps.md`](./p3-next-steps.md) | 进入演示阶段的 API 开发任务清单 |
| [`website-completion.md`](./website-completion.md) | 最新网站交付与验收记录 |

## 当前交付

- [网站完成与验收记录](website-completion.md)
- [网站 API 设计](api-design.md)
- [2 核 4GB 服务器部署教程](deployment-2c4g.md)
- 全量蒸馏待网站部署验收后手动启动。本次未新增模型调用或批次。

## 快速导航

- **全量蒸馏启动**：`gk distill run --models auto --limit 27449 --run full-gk-YYYYMMDD`
- **中断后续跑**：`gk distill run --run <existing-id>`（归属粘滞自动恢复）
- **质检报告**：`gk distill report --run <id>`
- **端点探测**：`gk llm models` / `gk llm ping --model X`

## 核心概念

### 桶（Bucket）

模块 × 题型的组合，是蒸馏的最小不可拆分单元。同一考点的措辞会因模型而异（一个写「实词辨析」、另一个写「词语辨析」），混着跑会让考点收敛度变差、聚合层被污染。因此以桶为单位分配，保证同一题型内的标注始终出自同一个模型。

### 动态领取

桶（模块 × 题型）是不可拆最小单元，进共享队列，大桶优先，谁空谁领。不用静态均分——那假设各模型吞吐相同，而实测差异很大（300 题验证里慢模型落后近一倍，成了整批的关键路径）。实测快模型最终领走 14/21 个桶，负载自动跟着实际配额走。

### 归属粘滞

领取时把「桶 → 模型」落盘（`bucket_owner.json` 原子写），续跑时已归属桶回原模型，只有没领取过的桶才进队列。否则中断后续跑会让桶被两个模型各做一半。

## 实测摘要

| 批次 | 题数 | 规范表 | 题型内 1:1 率 | 疑点率 | 吞吐量 |
|---|---|---|---|---|---|
| mvp-50-multi | 41 | v1.2 | 74.3% | 7.3% | 8 题/分钟 |
| verify-300-dyn | 289 | v1.3 | 28.1% | 10.4% | 15 题/分钟 |
| verify-500 | 460 | v1.4 | 20.7% | 10.9% | 15 题/分钟 |

> **注**：1:1 率的下降是因为样本扩大后，槽位粒度暴露出来。50 题时很多题型只有 1 题，无法统计；500 题覆盖了 95 个考点，更能反映真实分布。

---

## 文件列表

```
docs/
├── p3-distill-architecture.md    # 本文档
├── p3-test-results.md            # 实测数据
├── p3-taxonomy-guide.md          # 规范表详解
├── p3-next-steps.md              # 下一步计划
└── api-design.md                 # REST API 设计（已完成）
```

## 联系方式

项目根目录：`/home/tuigou/桌面/Code/play/go-study/ai_analyze_guokao`