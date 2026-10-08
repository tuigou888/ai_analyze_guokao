# 个人中心 Task 2：学习看板、规则复习与 CSV

日期：2026-10-04。范围：本轮仅新增统计、复习、导出及对应测试，扩展已有账户路由；未修改 schema、认证、资料、登录会话、前端或原练习评分。全部测试使用 `t.TempDir()` 合成数据库与配置密钥，未读取或改写正式 `var/` 数据。

## 接口与服务

| HTTP | 服务方法 | 行为 |
| --- | --- | --- |
| GET `/api/account/dashboard?days=30` | `Dashboard(ctx, user int64, days int) (Dashboard, error)` | 默认 30；仅 7/30/90；本人一致性只读看板 |
| POST `/api/account/review/wrongbook` | `PrioritizedWrongbook(ctx, user int64) (Session, error)` | application/json 与原有 Origin 校验；本人待订正自动错题；无候选 400 |
| GET `/api/account/export?days=30` | `ExportCSV(ctx, user int64, rangeDays string) ([]byte, error)` | 默认 30；7/30/90/all；CSV 附件；超过 5,000 行先返回 400 |

所有路由在已有 `requireUser` 组内，身份取自验证后的普通用户 Cookie，`user_id` 查询参数不参与筛选。未登录或管理员 Cookie 不能进入；写接口继承现有 Origin、503/Retry-After 错误处理。错题接口返回现有 Session（`session_id`、`question_ids`、`draft_revision` 等），不新建交卷或判分协议。

## DTO 合同

- `Dashboard`：`timezone,start_date,end_date,days,profile,lifetime,period,previous,today,trend,modules,activity,streak,achievements,review`。
- `StudyCounts`：`answered,correct,accuracy,duration_ms,sessions`，accuracy 为 0–100 数值或 null。
- `trend` / `activity`：计数组加 `date`；趋势长度为 days；活动固定最近 90 天，均从旧到新排列。
- `modules`：计数组加 `module,sufficient`；六真实模块固定政治理论、常识判断、言语理解与表达、数量关系、判断推理、资料分析；随后追加题库未来实际模块。
- `streak`：`current,longest,practiced_today`。
- `achievements`：`id,name,current,target,unlocked`。ID 为 `first_answer,answers_100,answers_1000,streak_7,streak_30,six_modules`。
- `review`：`unfinished,weak_modules,weak_concepts,labeled_answered,unmapped_labeled_answered,label_coverage,wrong,favorites,wrong_candidates`。空列表都是 `[]`。
- `unfinished`：最多 3，`session_id,kind,total,saved_answers,started_at,draft_revision`；仅返回已保存的非空答案数量，不返回草稿内容。
- `weak_modules`：最多 3 个模块计数组，至少 5 次作答，正确率由低到高。
- `weak_concepts`：最多 3，`concept_id,name,module,answered,correct,accuracy,sufficient`；至少 5 次，仅返回现有可操作稳定考点映射。
- `wrong_candidates`：最多 20，`question_id,module,wrong_count,last_wrong_at`，与实际创建错题练习的选择规则相同。

## 统计与边界决定

1. 固定北京时间自然日；7 天含今天与前六天。上一期间为紧邻的前一个等长区间。用 SQLite 时间解析支持 RFC3339 UTC 与带偏移的历史记录；未来自然日异常记录不计入看板和导出。
2. 仅 submitted_at 非空的会话计成果；非空答案计作答次数，空答案即使误带 is_correct=1 也不计正确。全空提交仍计一个完成会话及该会话的记录时长，但不算活动日；零分母正确率 null。
3. 看板累计、期间、上期、今日和每日时长先按 session 聚合，再接答案汇总，避免 answer join 倍增。模块附带的 duration_ms 是本模块有效答案记录耗时之和，sessions 是含本模块有效答案的不同会话数；跨模块会话的模块 sessions 不可直接相加。目标时长消费 today.duration_ms。
4. 最长连续根据所有历史非空作答日；今天未练但昨天练过保留截至昨天的 current。7/30 天连续成就使用历史 longest，不会因暂停重新锁定。六模块成就依赖累计样本，与所选窗口无关。
5. 考点统计从个人原作答与当前 latest_label 推导，绕过 user_concept_stat 缓存。标注覆盖率为所选期间已有任一最新标签的非空作答 / 期间全部非空作答，不能宣传为全题库诊断。未形成稳定 concept 映射的最新标签仍计覆盖率，计入 `unmapped_labeled_answered`，从可练考点建议中排除；GET 不隐式写入映射。
6. 错题只选本人 source=auto、resolved=0、题型 single/multi/judge；累计错数降序、最近错时降序、题 ID 稳定兜底，最多 20。手工 resolved 不被解释为复练答对率；pending 数量允许包含当前不可练题，候选数组只含可练题。
7. Dashboard 与 CSV 都用 `BeginTx(..., &sql.TxOptions{ReadOnly:true})`。固定批量查询，无按日期逐次查询，rows 每条异常/超限分支关闭，事务 defer Rollback，成功 Commit。
8. CSV 每行是本人已提交会话的答案记录，包括已交卷的空答案记录；列为北京时间提交时间、类型、session ID、题 ID、模块、本人答案、正确 0/1、答案记录耗时。没有题干、原答案、解析、草稿、token/hash/密钥。UTF-8 BOM 与 encoding/csv 转义；所有单元格防御 =/+/-/@、前置空白/控制字符及 tab/CR/LF 公式前缀。固定文件名 `study-records.csv`，不含用户名或凭据，Cache-Control no-store。
9. CSV 查询最多取 5,001 行并在内存缓冲；第 5,001 行使整个请求返回 400，发送任何附件头和文件内容之前完成校验。5,000 行成功，无静默截断。

## 固定 Clock 的实际 HTTP 样例

Clock 为 `2026-10-04T05:00:00Z`。Alice 四个已提交会话时间为 9/27 UTC 15:59:59、9/27 UTC 16:00、10/3 UTC 15:59:59、10/3 UTC 16:00；时长分别 4,000、3,000、1,000、2,000ms。答案分别 1 对、1 错、2 答 1 对另有 1 空、2 答全对；另有未交卷与 Bob 私人记录。

以下为真实 HTTP 响应字段摘录；activity 仅展示最后 4 个点，完整响应固定有 90 点：

```json
{
  "timezone": "Asia/Shanghai",
  "start_date": "2026-09-28",
  "end_date": "2026-10-04",
  "days": 7,
  "profile": {
    "username": "dashboard_alice", "nickname": "", "bio": "", "avatar_id": 0,
    "created_at": "2026-10-04T05:00:00Z", "updated_at": "2026-10-04T05:00:00Z",
    "daily_questions": 20, "daily_minutes": 30, "exam_name": "", "exam_date": "",
    "default_limit": 20, "default_module": "", "reading_size": 16, "revision": 0
  },
  "lifetime": {"answered":6,"correct":4,"accuracy":66.66666666666667,"duration_ms":10000,"sessions":4},
  "period": {"answered":5,"correct":3,"accuracy":60,"duration_ms":6000,"sessions":3},
  "previous": {"answered":1,"correct":1,"accuracy":100,"duration_ms":4000,"sessions":1},
  "today": {"answered":2,"correct":2,"accuracy":100,"duration_ms":2000,"sessions":1},
  "trend": [
    {"date":"2026-09-28","answered":1,"correct":0,"accuracy":0,"duration_ms":3000,"sessions":1},
    {"date":"2026-09-29","answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"date":"2026-09-30","answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"date":"2026-10-01","answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"date":"2026-10-02","answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"date":"2026-10-03","answered":2,"correct":1,"accuracy":50,"duration_ms":1000,"sessions":1},
    {"date":"2026-10-04","answered":2,"correct":2,"accuracy":100,"duration_ms":2000,"sessions":1}
  ],
  "modules": [
    {"module":"政治理论","sufficient":false,"answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"module":"常识判断","sufficient":false,"answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"module":"言语理解与表达","sufficient":false,"answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"module":"数量关系","sufficient":false,"answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"module":"判断推理","sufficient":false,"answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"module":"资料分析","sufficient":true,"answered":5,"correct":3,"accuracy":60,"duration_ms":500,"sessions":3}
  ],
  "activity": [
    {"date":"2026-10-01","answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"date":"2026-10-02","answered":0,"correct":0,"accuracy":null,"duration_ms":0,"sessions":0},
    {"date":"2026-10-03","answered":2,"correct":1,"accuracy":50,"duration_ms":1000,"sessions":1},
    {"date":"2026-10-04","answered":2,"correct":2,"accuracy":100,"duration_ms":2000,"sessions":1}
  ],
  "streak": {"current":2,"longest":2,"practiced_today":true},
  "achievements": [
    {"id":"first_answer","name":"首次作答","current":1,"target":1,"unlocked":true},
    {"id":"answers_100","name":"累计 100 次作答","current":6,"target":100,"unlocked":false},
    {"id":"answers_1000","name":"累计 1,000 次作答","current":6,"target":1000,"unlocked":false},
    {"id":"streak_7","name":"连续练习 7 天","current":2,"target":7,"unlocked":false},
    {"id":"streak_30","name":"连续练习 30 天","current":2,"target":30,"unlocked":false},
    {"id":"six_modules","name":"六模块各作答 5 次","current":1,"target":6,"unlocked":false}
  ],
  "review": {
    "unfinished":[{"session_id":5,"kind":"single","total":1,"saved_answers":0,"started_at":"2026-10-01T00:00:00Z","draft_revision":0}],
    "weak_modules":[{"module":"资料分析","sufficient":true,"answered":5,"correct":3,"accuracy":60,"duration_ms":500,"sessions":3}],
    "weak_concepts":[],"labeled_answered":3,"unmapped_labeled_answered":0,"label_coverage":60,
    "wrong":0,"favorites":0,"wrong_candidates":[]
  }
}
```

## 红绿与测试覆盖

真实本地 `httptest.NewServer` 请求先于实现执行，两条功能红灯：

```text
TestPersonalDashboardHTTPFixedClock: dashboard route: want 200 got 404: {"error":"接口不存在"}
TestPersonalExportAndReviewHTTP: export route want 200 got 404: {"error":"接口不存在"}
FAIL ai_analyze_guokao/internal/serve 0.534s
```

实现后这两项转绿。追加 latest_label 缺稳定映射覆盖计数测试，实际缺少 `unmapped_labeled_answered` 时失败，新增计数后转绿。测试搭建期间曾修正未用 import 和重复 label(question_id,run_id) 的 fixture 错误，未将这类构建/fixture 问题算作功能红灯。

| 测试 | 捕获的破坏 |
| --- | --- |
| DashboardHTTPFixedClock | HTTP 路由、北京时间午夜、空答案、草稿/他人混入、时间 join 倍增、0/null、7/30/90、模块顺序 |
| DashboardLatestLabelsAndResume | 旧考点缓存、稀疏覆盖、未映射标签、最新映射、3 个续做与保存数量/版本、收藏/错题隔离 |
| ReviewSessionCompatibility | 创建复习后原草稿 CAS 与真实重复交卷幂等，统计不重复计次/计时 |
| DashboardEmptyAndAllHistoryStreak | 无数据、全空提交、昨日保留/断档、90 日之外历史最长 30 天、7/30 天徽章 |
| DashboardAchievementThresholds | 99/100 与 999/1000 边界，六真实模块累计各 5 次 |
| ExportLimitAndDates | 5,000 成功 / 5,001 先 400；7/30/90/all 的北京时间边界与偏移时间 |
| DashboardConcurrentHistoryAndCancellation | 非写锁读取、一致快照、16 组多用户看板/导出并发、取消与连接释放 |
| ExportAndReviewHTTP | BOM、逗号/引号/换行/Unicode、公式防护、隐私/草稿/他人排除、无错题 400、优先排序 |
| DashboardWindowBoundaries | 三窗口及等长上期起点、前一秒、未来自然日排除 |
| ReviewMaxTwentyAndSecurity | 25 候选只选 20，manual/resolved/他人排除、原 session 隔离、Origin/JSON/普通用户 Cookie |
| WeakSuggestionsSampleThresholdAndLimit | 4 与 5 样本、最多 3、低正确率排序、未来模块追加、考点可启动原练习 |
| SafeCSVCellFormulaPrefixes | ASCII 公式、Unicode 空白、BOM、tab/CR/LF 前缀，普通文本保持 |

## 验证输出与过程中的问题

所有命令使用 `GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2`。沙箱首次拒绝 TCP 监听，按现有权限机制获准后重跑真实 HTTP 测试；没有安装依赖。Go 全量首轮所有真实业务包通过，但审查快照 `test-artifacts/personal-center/work/task2-before/internal/serve/account.go` 被识别成独立 Go 包，因缺少 Server/currentUser 等编译失败。主代理将快照目录改为 `_work` 后全量重跑通过，快照仍保留。

非 race 的 16 组 dashboard+export 并发，Alice 5,000 条、Bob 1 条合成记录，实测约 481–782ms；每组核对自己的作答次数，测试结束 InUse=0。锁验证持有未提交写事务修改 profile，读取仍成功且只见旧快照值，证明未获取 immediate 写锁。

首次 race 将 5,000 条查询 CPU 时间混入 2 秒锁断言而超时（context deadline exceeded，已实际读到旧 profile）。锁断言调整到小快照、保持原 2 秒时限后通过；独立大历史并发在 race 下出现 15 秒计算时限，生产无 race 小于 1 秒。为仪器执行给大历史并发留 60 秒时限，不放宽锁断言，也不改生产代码。

最终验证：

```text
$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve ./internal/study -run 'TestPersonal(Dashboard|Export|Review|Weak)|TestSafeCSVCell' -count=1 -v
11 个 Task 2 HTTP/service 测试及 CSV 前缀单测全部 PASS
ok ai_analyze_guokao/internal/serve 3.777s
ok ai_analyze_guokao/internal/study 0.081s

$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test -race ./internal/study ./internal/serve -run 'TestPersonal(Dashboard|Export|Review|Weak)|TestSafeCSVCell' -count=1 -v
全部 PASS，无 DATA RACE
16 concurrent dashboard+export requests, 5000 Alice and 1 Bob records: 19.457503363s
ok ai_analyze_guokao/internal/study 1.943s
ok ai_analyze_guokao/internal/serve 65.260s

$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./... -count=1
ok ai_analyze_guokao/cmd/gk 0.378s
ok ai_analyze_guokao/internal/admin 0.405s
ok ai_analyze_guokao/internal/distill 3.270s
ok ai_analyze_guokao/internal/ingest 13.115s
ok ai_analyze_guokao/internal/llm 0.003s
ok ai_analyze_guokao/internal/media 0.374s
?  ai_analyze_guokao/internal/model [no test files]
ok ai_analyze_guokao/internal/serve 11.097s
ok ai_analyze_guokao/internal/setting 0.061s
ok ai_analyze_guokao/internal/store 0.796s
ok ai_analyze_guokao/internal/study 0.498s

$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go vet ./internal/study ./internal/serve
exit 0，无输出

$ git diff --check
exit 0，无输出
```

## 修改文件与自审

- 新增 `internal/study/dashboard.go`、`internal/study/review.go`、`internal/study/export.go`。
- 扩展 `internal/serve/account.go`，只新增上述 3 个路由及处理器。
- 新增 `internal/serve/personal_dashboard_test.go`、`internal/study/export_test.go`。
- 本报告 `docs/personal-center-task2-report.md`。

自审核对：SQL 用户过滤不接收客户端身份；最新标签不读缓存作答数；时长先聚合会话；推荐只返回已有可操作考点 ID；全部空列表与正确率 null 明确；CSV 附件输出前检查上限；rows、取消与只读事务均有测试；未复制评分逻辑。只读统计没有按日期 N+1，响应列表有明确上限。未提交、推送、部署或分派其他代理。

## 独立审查后修复：R1 / R2

已完整阅读 `docs/personal-center-task2-review.md`，将两项复现加入仓库回归后再改生产代码。新增测试均在临时 fixture 中操作合法 nullable 数据，没有外键破坏、Go overlay 或正式库操作。

### R1：NULL 模块兼容

`question.module` 允许 NULL，原新代码直接 Scan string。这项真实仓库红灯同时捕获空用户看板 500、本人已提交数据看板 Scan 错误、CSV 500 和错题入口 500：

```text
$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve -run 'TestPersonalReview(NullModuleCompatibility|UnmappedEmptyTertiary)$' -count=1 -v
TestPersonalReviewNullModuleCompatibility:
  empty user dashboard got 500: {"error":"操作失败，请稍后重试"}
  submitted nullable module dashboard: converting NULL to string is unsupported
  nullable module CSV got 500: {"error":"操作失败，请稍后重试"}
  nullable module wrongbook got 500: {"error":"操作失败，请稍后重试"}
FAIL
```

修复覆盖全部新增 `question.module` 读取：累计六模块成就汇总、全题库实际模块枚举、期间模块汇总、CSV、错题候选均使用 COALESCE 到空字符串。模块枚举和期间模块画像跳过空模块，复习弱模块再明确排除空 module；不制造新的模块归属或无法筛选的模块建议。

本人缺模块答案仍保留在累计、期间、今日、趋势、活动日统计；5 次正确答案的实际回归断言为 answered=5、correct=5、accuracy=100、duration_ms=500、sessions=5，六核心模块均为 0 样本、六模块成就 current=0。CSV 的模块单元格为空且保留该答案。错题候选的 module 为空，仍能创建原 Session 并真实提交正确答案，验证现有题型/评分链路不受影响。共享该题的 Bob 无作答时看板正常，累计 0；未影响其他账号。

R1 单独改完即时回归：

```text
$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve -run 'TestPersonalReviewNullModuleCompatibility$' -count=1 -v
--- PASS: TestPersonalReviewNullModuleCompatibility (0.37s)
ok ai_analyze_guokao/internal/serve 0.455s
```

### R2：空三级考点未映射计数

新增 `TestPersonalReviewUnmappedEmptyTertiary` 的 null 与 empty 两个子测试，各写 5 条本人期间非空作答，并将当前 latest_label.tertiary 设置为 NULL 或空字符串。原红灯均为 LabeledAnswered=5、UnmappedLabeledAnswered=0、LabelCoverage=100、WeakConcepts=[]（未映射数量应为 5）。

未映射计数改为判断 LEFT JOIN 的 `c.id IS NULL`，移除额外三级考点非空限制。标注覆盖率仍按实际 latest_label 计算；薄弱考点查询维持稳定映射及非空考点条件，不产生无效 ID 建议。两个子测试修复后均断言 labeled_answered=5、unmapped_labeled_answered=5、label_coverage=100、weak_concepts=[]。

### 修复后的完整验证

```text
$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve -run 'TestPersonalReview(NullModuleCompatibility|UnmappedEmptyTertiary)$' -count=1 -v
--- PASS: TestPersonalReviewNullModuleCompatibility (0.29s)
--- PASS: TestPersonalReviewUnmappedEmptyTertiary (0.30s)
    --- PASS: TestPersonalReviewUnmappedEmptyTertiary/null (0.15s)
    --- PASS: TestPersonalReviewUnmappedEmptyTertiary/empty (0.15s)
PASS
ok ai_analyze_guokao/internal/serve 0.663s

$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve ./internal/study -run 'TestPersonal(Dashboard|Export|Review|Weak)|TestSafeCSVCell' -count=1
ok ai_analyze_guokao/internal/serve 4.275s
ok ai_analyze_guokao/internal/study 0.067s

$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test -race ./internal/serve -run 'TestPersonalReview(NullModuleCompatibility|UnmappedEmptyTertiary)$' -count=1
ok ai_analyze_guokao/internal/serve 12.559s

$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./... -count=1
ok ai_analyze_guokao/cmd/gk 0.470s
ok ai_analyze_guokao/internal/admin 0.465s
ok ai_analyze_guokao/internal/distill 3.320s
ok ai_analyze_guokao/internal/ingest 14.115s
ok ai_analyze_guokao/internal/llm 0.003s
ok ai_analyze_guokao/internal/media 0.395s
?  ai_analyze_guokao/internal/model [no test files]
ok ai_analyze_guokao/internal/serve 12.306s
ok ai_analyze_guokao/internal/setting 0.059s
ok ai_analyze_guokao/internal/store 0.803s
ok ai_analyze_guokao/internal/study 0.484s

$ env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go vet ./internal/study ./internal/serve
exit 0，无输出
$ gofmt -l internal/study/dashboard.go internal/study/export.go internal/study/review.go internal/serve/personal_dashboard_test.go
exit 0，无输出
$ git diff --check
exit 0，无输出
```

本轮仅修改 `internal/study/dashboard.go`、`internal/study/export.go`、`internal/study/review.go`、`internal/serve/personal_dashboard_test.go` 与本报告。HTTP/DTO 字段合同未变，缺失模块展示策略与未映射计数按上述修复定义。未扩展 Task 3、分派代理、提交、推送或部署。
