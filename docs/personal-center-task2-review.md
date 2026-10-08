# 个人中心 Task 2 独立审查

日期：2026-10-04。结论：**暂不通过，2 项 Important / P2 缺陷需修复后复审**。现有主要业务链路符合规格，但不能据现有绿灯认定边界完整。

审查范围为 `test-artifacts/personal-center/task2-review.diff`、任务 brief、Task 2 实现报告，以及设计规格 §§5/6/8/9/10（同时核对 §4.5 导出、§7 事务约束）。精确 DTO 以实现报告为准。本次未修改应用源码、测试源码或正式数据库/密钥，未要求尚未实施的 Task 3 前端内容；补充复现仅使用 `/tmp` Go overlay 和 `t.TempDir()` 数据库。

## 必须修复

### R1 · P2：合法的 NULL 题目模块会使整个看板失败，并中断相关导出/复习

- 位置：`internal/study/dashboard.go:229`、`:235`（全题库模块枚举）；同类位置 `dashboard.go:162`、`:170`、`:246`、`:255`；`internal/study/export.go:41`、`:62`；`internal/study/review.go:54`、`:62`。
- 触发：`question.module` 在现有 schema 中允许 NULL。题库任意一题模块为 NULL 时，即使当前用户从未作答该题、甚至没有任何历史，`dashboardModules` 也把全题库 DISTINCT module 直接扫描到 Go string，返回 `converting NULL to string is unsupported`，导致整份 dashboard 失败。本人已提交答案或待订正错题关联到该题时，CSV 和优先错题同样失败。
- 影响：一条共享题库的缺失模块记录可使所有用户的个人看板不可用；既有题目列表通过 `COALESCE(q.module,'')` 兼容该字段，新功能破坏了相同数据的兼容性。违反无数据可用及异常不击穿整页的要求。
- 复现：只在临时 fixture 中执行 `UPDATE question SET module=NULL WHERE id=1`。空用户 Dashboard、关联答案 ExportCSV、关联错题 PrioritizedWrongbook 分别报上述 Scan 错误；未借助无效外键或破坏数据库约束。
- 建议：所有新读取路径对 nullable module 使用一致的 `COALESCE` / `sql.NullString` 策略；统计总量保留此类答案，六核心模块成就不凭空归类。模块画像可跳过缺失模块或使用明确的未分类展示策略，但不得生成无法按模块筛选的薄弱模块建议。CSV 可以输出空模块，错题仍应按有效题型正常创建。补充共享未知模块与本人未知模块的回归。

### R2 · P2：有最新标签但 tertiary 为空的答案遗漏未映射计数

- 位置：`internal/study/review.go:126`。
- 触发：某题存在 latest_label，但三级考点为 NULL 或空字符串，因而没有可用稳定 concept 映射。SQL 已把该答案计入 `labeled_answered`，却额外要求 `COALESCE(l.tertiary,'')<>''` 才计入 `unmapped_labeled_answered`。
- 实际结果：一条本人期间非空作答，最新标签 `tertiary=NULL`，返回 `labeled_answered=1`、`label_coverage=100`、`unmapped_labeled_answered=0`、`weak_concepts=[]`。已标注但尚未形成稳定考点映射的数据被漏报，不能据该字段准确提示先进行模块练习。
- 规格依据：§6 要求未形成稳定映射的标签仍计覆盖而不生成不可操作建议；本次任务补充合同明确“未映射标签计入 unmapped_labeled_answered”。label.tertiary 本身是 nullable 字段，并未有完整非空标签的数据库约束。
- 建议：以当前 LEFT JOIN 是否得到有效 concept ID 作为未映射计数依据，不用 tertiary 非空额外排除；继续将不可映射标签排除在 weak_concepts 之外。新增 NULL 与空字符串两项回归，确认覆盖率和未映射数分别正确。

## 其余规格与质量核对

| 核对项 | 结论与依据 |
| --- | --- |
| 身份隔离 | 路由在 requireUser 内；新业务查询均取已验证用户 ID，未使用客户端 user_id。两用户、未登录、管理员 Cookie、Origin / JSON 检查已有覆盖。 |
| 北京时间与范围 | 7/30/90 包含今天；前期紧邻且等长；今日/累计/趋势按提交日；带偏移 RFC3339 经 SQLite 解析，未来自然日排除。边界专项通过。 |
| 作答、准确率和时长 | 非空答案才计 answered/correct；分母零为 null；会话先聚合避免时长倍增。模块 duration 为有效答案耗时，sessions 为触及模块会话数，符合报告的独立口径。 |
| 连续与成就 | 活动以非空作答日计算，昨日保留、断档归零、最长基于全历史；六个真实模块各 5 次按累计计算；7/30 连续成就取 longest。 |
| 续做与建议 | 最近 3 份续做仅给数量及 revision、不泄露草稿；薄弱模块/考点至少 5 次、最多 3；实时 latest_label，不使用旧统计缓存。缺映射建议排除正确，但未映射计数存在 R2。 |
| 错题优先 | 本人 auto + unresolved + 可练题型，按错数、最近错时、题 ID 排序，最多 20；复用 CreateSession，原草稿 CAS 与交卷幂等有实际回归。NULL module 兼容性存在 R1。 |
| 事务与资源 | Dashboard / CSV 明确 ReadOnly；固定批量查询，无逐日期 N+1；rows 异常和超限分支均关闭，事务 defer Rollback，完成后 Commit；独立重跑含写锁并存、两用户并发及取消后连接归零测试。 |
| CSV | 先缓冲，5,001 行整体 400，5,000 行成功；BOM、csv 转义、所有单元格公式前缀防护；仅本人已提交记录；未返回草稿、解析、凭据，固定文件名且 no-store。NULL module 兼容性存在 R1。 |

## 独立验证证据

复跑现有 Task 2 专项（HTTP 监听使用既有获准命令前缀和 require_escalated）：

```text
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve ./internal/study -run 'TestPersonal(Dashboard|Export|Review|Weak)|TestSafeCSVCell' -count=1
ok ai_analyze_guokao/internal/serve 3.026s
ok ai_analyze_guokao/internal/study 0.061s
```

两项补充复现使用 `/tmp/personal-task2-independent-review/overlay.json` 追加到现有 fixture 的临时测试视图，没有改动仓库测试文件：

```text
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test -overlay /tmp/personal-task2-independent-review/overlay.json ./internal/serve -run 'TestPersonalReview(NullModuleCompatibility|UnmappedEmptyTertiary)$' -count=1 -v

TestPersonalReviewNullModuleCompatibility:
  empty user dashboard: converting NULL to string is unsupported
  export: converting NULL to string is unsupported
  review: converting NULL to string is unsupported
FAIL
TestPersonalReviewUnmappedEmptyTertiary:
  LabeledAnswered:1 UnmappedLabeledAnswered:0 WeakConcepts:[]
FAIL
FAIL ai_analyze_guokao/internal/serve 0.322s
```

实现报告中的全量 / race / vet 结果已审阅，本次未重复这些更广的验证，也未将其表述为本次新跑结果。两项失败是新增断言捕获的实际缺陷，不是构建失败或 fixture 约束失败。

## R1 / R2 修复后定向复审（2026-10-04）

**复审结论：通过。R1、R2 均为 ADDRESSED；此前两项阻断已关闭，修复区域未发现新增实质缺陷。** 本节取代开头的首次审查结论，首次发现及红灯证据保留用于追溯。

本次核对 `test-artifacts/personal-center/task2-fix1-review.diff`、实际 `dashboard.go` / `export.go` / `review.go`、新增仓库测试与实现报告末尾修复记录；仅复核两项缺陷和修复区域，不扩展到旧未改功能或 Task 3。未修改应用/测试源码或正式数据。

- **R1 已解决。** 新模块查询均以 COALESCE 兼容 NULL，包括全题库模块枚举、期间模块统计、六模块成就汇总、CSV 与错题候选。缺失模块不进入画像或薄弱模块建议，也不会计为六核心模块之一。总量及按提交日的时长聚合未被过滤，继续包含缺模块答案；CSV 保留该记录且模块字段为空；错题继续复用原 CreateSession。新增 `TestPersonalReviewNullModuleCompatibility` 验证空用户看板、本人 5 次累计/期间/今日作答及今日 500ms/5 sessions、六模块均无虚构样本、CSV 空字段、错题创建及实际 Submit 成功。
- **R2 已解决。** `review.go:126` 使用 LEFT JOIN 的 `c.id IS NULL` 计数，不再排除 tertiary 为空的已标注答案。NULL / 空字符串两个子测试都验证 5 条记录对应 `labeled_answered=5`、`unmapped_labeled_answered=5`、`label_coverage=100`、`weak_concepts=[]`；可练考点过滤未被放宽。

本次独立重跑仓库新增回归，无 overlay、无需 TCP 监听，全部通过：

```text
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve -run 'TestPersonalReview(NullModuleCompatibility|UnmappedEmptyTertiary)$' -count=1 -v
--- PASS: TestPersonalReviewNullModuleCompatibility (0.24s)
--- PASS: TestPersonalReviewUnmappedEmptyTertiary (0.25s)
    --- PASS: TestPersonalReviewUnmappedEmptyTertiary/null (0.12s)
    --- PASS: TestPersonalReviewUnmappedEmptyTertiary/empty (0.13s)
PASS
ok ai_analyze_guokao/internal/serve 0.547s
```

已审阅实现报告的 RED→GREEN、Task 2 定向测试、Go 全量、新增两项 race、vet 和格式检查结果；这些较广结果为修复方提供的证据，本次未重复执行。当前审查无遗留阻断项。
