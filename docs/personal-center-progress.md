# SDD ledger — plan: docs/superpowers/plans/2026-10-04-personal-center.md

2026-10-04：用户确认完整学习工作台并明确要求开始代码开发。按已展示规格执行，开发阶段不重复请求功能范围确认。保留此前工作区修复，不提交/推送/部署。

| 任务 | 状态 | 验证/审查 |
|---|---|---|
| 1 迁移、资料、会话 | 已完成 | 15项定向、Go全量、相关包race/vet通过；独立规格/质量审查通过 |
| 2 看板、复习、导出 | 已完成 | R1/R2已红绿修复；独立复审通过，全量及相关race/vet通过 |
| 3 页面与图表 | 已完成 | R1正式红→绿且定向复审通过；4组乱序、原17组及旧6组回归通过 |
| 4 集成与交付 | 已完成 | 终审R1已修复且独立定向复审通过；52个唯一UI场景分阶段通过，Go/恢复/race/vet/构建及本地包验证通过 |

起飞前接口核对：

| 任务/接口 | 核对结果 |
|---|---|
| 1→2 Profile/Clock | Profile字段与头像整数编号统一；Clock可选，既有Service{DB:...}兼容 |
| 1→2 accountRoutes | 任务2顺序扩展同文件，不并行修改 |
| 1/2→3 API | 前端消费实际DTO；正确率统一0–100/null，不用0–1混合 |
| 3→4 回归 | 保留密码字段标签/路由，必要更新旧回归的页签定位 |
| 1 自洽 | 资料revision与draft_revision独立，昵称单一来源，注册原子默认资料 |
| 2 自洽 | period与90日activity各自窗口；duration按session汇总，无N+1 |
| 3 自洽 | 本地页签保护输入，图表数据只读，不将表单保存与换日期混用 |
| 4 自洽 | 源码/本地包交付，正式环境验收单独说明 |

执行适配：当前项目修复均在共享工作目录且Git区域只读；按用户要求继续在此目录开发，审查用任务文件快照diff而非历史HEAD，保留用户已有修改。最终由独立审查者复核。

Task 1: Ruling: 已存考试日期过期时允许保留原值保存其他资料，新日期仍限制未来 — 规格要求过期倒计时继续展示，改昵称不应被阻塞 — 错误代价为日期校验回归及表单调整；增加固定Clock回归，后端/前端共用口径。

Task 1: complete — docs/personal-center-task1-report.md 与 task1-review.md；工作区源码交付，未提交。

Task 2: Ruling: 尚无稳定concept映射的latest_label只计入标注覆盖，不进入可操作weak_concepts建议 — 看板保持只读且推荐入口必须有效 — 代价是部分新标签暂不出现考点建议，提供模块复练及明确提示；补映射缺失回归。

执行工具修正：审查快照含.go文件的 work 目录被go test ./...识别为包；改为Go会忽略的 _work，保留快照，不改变产品源码。后续全量重新验证。

Task 2: complete — 两项审查缺陷已修复且复审通过，report/review文件保留；模块缺失不会击穿看板或导出。

2026-10-05 集成预检：根代理 Go 全量、核心 serve/study/store/setting race、Go vet 退出 0，证据见 test-artifacts/personal-center/task4-root-precheck.json。前端审查未完成时不标记 Task 3 或整个功能完成。

Task 3: fix round 1/5 — 唯一R1已按同owner最高确认revision统一接收，正式初始化GET红灯与四路径绿灯保存；原17+旧6通过，等待独立定向复审。报告末尾及task3-fix1-review.diff可追溯。

Task 3: complete — R1定向复审ADDRESSED，无修复新增破坏，规格及质量通过；Task1/2跨任务边界由已通过后端审查和Task4集成覆盖。Task3临时服务3833已正常关闭，退出0。

Task 4: implementation ready, pending independent final review — docs/personal-center-task4-report.md；逐条覆盖矩阵 task4-coverage.md；task4-results.json 与 root-precheck 记录最新全量/恢复/race/vet/构建/45组真实UI；task4-package-manifest.json 记录Linux包18文件、无DB/密钥/备份/fixture及SHA。服务42574已正常关闭exit0，未提交/推送/部署。根代理安排唯一任务及宽范围终审，不将全部功能标记完成。

2026-10-06 Final review: 0 Critical / 1 Important / 0 Minor — docs/personal-center-final-review.md，唯一R1为同浏览器另一标签切换Cookie账号后，旧表单可写入新账号（revision相同）。进入一次完整最终修复及定向复审，不重做已完成任务。

Final fix: Ruling: 页面预期账号可作为拒绝请求的前提，由服务器与已验证Cookie身份比较；不得用它选择查询用户，不向ProfileUpdate添加身份字段 — 解决跨标签错误账户写入，同时保持既有鉴权和完整字段合同 — 代价为额外请求前提与新旧客户端兼容回归；补共享Cookie双页测试及无前提既有API兼容验证。

Final fix: Ruling: 携带页面身份前提的现代logout只撤销已验证token，不发送删除共享Cookie的Set-Cookie；无前提旧API保留原删除Cookie行为 — 避免旧A退出响应迟到删除后来B登录Cookie，认证仍以服务端会话撤销为准 — 代价为现代客户端退出后可能保留无效HttpOnly Cookie至过期/下次登录，增加撤销后401与迟到退出不影响B的回归；无效token不能继续认证，不保存新凭据。

Final fix wave1: implemented and verified, pending independent focused review — docs/personal-center-final-fix-report.md；原R1真实双标签红→绿，Cookie主体前提统一拒绝；迟到409/改密/撤销/退出与只退出通知保留输入边界已补。主修复稳定版原45通过，最后仅known-stale分支调整后最终7+ordering4通过，Go全量/相关race/vet/npm/三平台编译通过。新本地包及sealed manifest/results/diff独立封存，旧包历史保留；全部自有服务正常关闭exit0。原最终报告不改，唯一独立定向复审由根安排，不标最终complete，未提交/推送/部署。

2026-10-06 Final focused review: R1 ADDRESSED；0 Critical / 0 Important / 0 Minor，Task4规格及质量通过，完整个人中心功能与本地交付可关闭。依据 docs/personal-center-final-review.md 最新复审节；审查者完整读修复diff并只读核验43个封存文件及19个包成员。

Task 4: complete — 根代理最终Go全量-count=1退出0（serve10.355s）；发布包19文件与当前文档/二进制一致，无DB/密钥/备份/fixture，SHA256=5ee21b47fd71cc25206e4918e3d51d690aea35a87fa120ec64975cd32d9b2b4b。源码、使用说明、API、部署及修复/审查证据已交付；未提交、推送、部署或改正式数据，全部自有临时服务已正常关闭。

收尾说明：封存目录保留复审时的不可变快照；本账本及计划勾选为复审通过后的流程状态更新，不改变已审查源码或发布包。最终完成记录及状态差异见 docs/personal-center-completion.md 与 test-artifacts/personal-center/personal-completion-verification.json。正式HTTPS/代理、容量、异机恢复及目标平台原生运行仍需目标环境验收。
