# 个人中心逐条规格覆盖矩阵

对照 `docs/superpowers/specs/2026-10-04-personal-center-design.md`，日期 2026-10-05。这里区分测试与源码/独立审查，未将源码核对写成实际运行。后端 TestPersonal 均在最终 Go 全量中运行；UI 证据是根代理本轮独立执行的四份 Task4 JSON，共 45 组，全部 exit 0 / console_errors=[]。该字段只监听 pageerror，表示未捕获页面异常为空，不表示全部控制台或网络日志均无错误。

缩写：P=`internal/serve/personal_profile_test.go`；D=`internal/serve/personal_dashboard_test.go`；A=`internal/study/personal_auth_test.go`；M=`internal/store/personal_migration_test.go`；B=`cmd/gk/backup_restore_test.go`；UI17/ORDER4/OLD18/OLD6 为对应 `task4-*-results.json`。UI 编号按 checks 数组从 1 开始。安全/事务结构核对另沿用 Task1/2/3 独立审查及本轮终审。

| 规格条款 | 对应证据 | 结论/边界 |
|---|---|---|
| §1 工作台全部已确认范围；真实数据、持久化、可练推荐、原协议兼容 | UI17 1–17、OLD18/6；P/D/B | 已覆盖；无在线模型调用 |
| §2 Go/Chi/SQLite、Vue/SVG/CSS；保留旧认证/草稿与六模块/稀疏标签 | Task1–3 diff/独立审查；D LatestLabelsAndResume；OLD6 | 已覆盖 |
| §2/§11 临时库、真实 DB/密钥及远程服务边界 | t.TempDir 测试；_work 合成服务、正常关闭；Task4 报告 | 未操作正式环境 |
| §3 /account、导航与顶部入口、四本地页签 | UI17 1、2；Account/App 源码审查 | 已覆盖 |
| §3 加载/失败/重试/空状态 | UI17 1、7、9；Task3 审查 | 已覆盖 |
| §3 dirty 换页签/路由/刷新保护、保存等待禁重复 | UI17 3、4、9 | 已覆盖 |
| §3 409 保留输入、确认重载、禁止自动重发；同账号版本不倒退 | P ConcurrentCAS；UI17 4；ORDER4 全部 | 已覆盖 |
| §3 鼠标/键盘图值、文字/表格、375px/日历局部滚动、非唯颜色 | UI17 5、6；图表独立审查、根桌面/移动截图检查 | 已覆盖 Chrome；未测真实读屏器 |
| §4.1 用户名只读；昵称40、简介200 Unicode；八受控头像 | P RequiresCompleteEditableFields/DateAndUnicodeBoundaries；UI17 2 | 已覆盖；不接受 URL/HTML |
| §4.1 注册日期、昵称/头像同步顶部；旧账户默认 | P DefaultsUpdateCAS；M；UI17 2；ORDER4 | 已覆盖 |
| §4.1 无上传服务/新图片依赖 | Task1/3 源码与依赖检查 | 已覆盖源码范围 |
| §4.2 题量10/20/50默认20；实际模块/全部；URL优先 | P RejectsInvalidAndUnauthorized；UI17 9、16；B | 已覆盖 |
| §4.2 字号16/18/20仅正文；跨设备读取、登出/换人清缓存 | UI17 8、11、15–17；ORDER4 3；B | 已覆盖 |
| §4.3 目标5–200/5–180默认20/30 | P DefaultsUpdateCAS/RejectsInvalidAndUnauthorized；UI17 2 | 已覆盖 |
| §4.3 考试名称40、未来北京时间日期/五年、可空、已存到期保留 | P DateAndUnicodeBoundaries/PreservesOwnExpiredExamDate；UI17 14 | 已覆盖 |
| §4.3 今日真实/剩余、进度最大100%、超额数；到期无负值/不删除 | UI17 6、14；GoalRing/Overview 独立审查 | 已覆盖源码与真实目标；超额主要源码核对 |
| §4.4 原密码错误400、改密撤销所有旧会话 | OLD6 6；原认证 Go 测试；UI17 4 | 已覆盖 |
| §4.4 当前标识/设备类别/登录到期/脱敏地址；旧信息未知 | P LoginMetadata/SessionsPaginationLegacyAndExpiry；A SessionClockAndControlledMetadata；UI17 12；B | 已覆盖 |
| §4.4 可信clientIP，不信任伪造XFF；IPv4三段/IPv6 /48，无外部定位 | P LoginMetadata/LoginMetadataTrustedIPv6；A；Task1审查 | 已覆盖 |
| §4.4 独立随机公开ID，无token/hash；固定20条分页 | M；P SessionsPaginationLegacyAndExpiry；UI17 12；B | 已覆盖 |
| §4.4 仅本人其他会话撤销、他人404、当前走logout | P SessionsIsolationAndRevocation；UI17 11、12、15 | 已覆盖 |
| §4.4 退出其他确认、保留当前Cookie、不新增凭据 | UI17 11；P SessionsIsolationAndRevocation | 已覆盖真实两Cookie |
| §4.5 导出7/30/90/all默认30，八列本人提交记录 | D ExportLimitAndDates/ExportAndReviewHTTP；UI17 10 | 已覆盖 |
| §4.5 排除密码/会话/密钥/解析/他人/草稿 | D ExportAndReviewHTTP；Task2独立审查 | 已覆盖 |
| §4.5 BOM/Unicode/csv转义/公式前缀 | D ExportAndReviewHTTP；TestSafeCSVCellFormulaPrefixes；UI17 10 | 已覆盖 |
| §4.5 5000成功、5001整体400不截断、固定文件名 | D ExportLimitAndDates；UI17 10 | 服务端真实边界；UI上限是明确故障注入 |
| §5.1 timezone/start/end、自然日含今天、7/30/90 | D HTTPFixedClock/WindowBoundaries/ExportLimitAndDates | 已覆盖 |
| §5.1 提交限定、非空次数、同题复练、零分母null | D HTTPFixedClock/ReviewSessionCompatibility/EmptyAndAllHistoryStreak | 已覆盖 |
| §5.1 session完成数、时长先聚合不倍增、午夜归提交日 | D HTTPFixedClock/ReviewSessionCompatibility；UI17 6 | 已覆盖 |
| §5.1 客户端时长非在线/监考；无答案正确率null、无活动为0 | 文档口径；D EmptyAndAllHistoryStreak；UI17 1 | 已覆盖 |
| §5.1 累计/期间/等长前期，上期零用绝对增量 | D HTTPFixedClock/WindowBoundaries；Overview审查 | 已覆盖 |
| §5.2 双小图分单位、范围/焦点值 | UI17 6、7；TrendChart审查 | 已覆盖 |
| §5.2 六模块正确率/样本，无样本空缺/<5不足 | D WeakSuggestionsSampleThresholdAndLimit/HTTPFixedClock；UI17 1、6 | 已覆盖 |
| §5.2 固定90日活动/记录时长；双目标环 | D HTTPFixedClock；UI17 1、6；ActivityCalendar/GoalRing审查 | 已覆盖 |
| §5.2 原生独立Vue SVG/CSS，无图表依赖；name/摘要/表格 | Task3审查、package.json；UI17 6 | 已覆盖 |
| §5.3 活动非空提交日；今天/昨天/断档/跨月、最长全历史 | D EmptyAndAllHistoryStreak/HTTPFixedClock | 已覆盖 |
| §5.3 首答/100/1000/7/30/六模块各5成就及条件/进度 | D AchievementThresholds/EmptyAndAllHistoryStreak；UI17 6 | 已覆盖 |
| §5.3 不允许客户端解锁，无去重题数误名/奖励排名 | 路由/Overview审查；D AchievementThresholds | 已覆盖源码 |
| §6 最近3续做、题量/保存数、原session/draft版本、记录入口 | D LatestLabelsAndResume；UI17 8；OLD18/6 | 已覆盖 |
| §6 本人auto待订正错数/最近错时排序、最多20；无题完成/400 | D ReviewMaxTwentyAndSecurity/ExportAndReviewHTTP；UI17 8；UI17 1 | 已覆盖 |
| §6 薄弱模块至少5/最多3按准确率，不足说明 | D WeakSuggestionsSampleThresholdAndLimit；UI17 6、8 | 已覆盖 |
| §6 实时latest标签、考点至少5/最多3，可创建练习；覆盖限制 | D LatestLabelsAndResume/WeakSuggestionsSampleThresholdAndLimit；UI17 8、13 | 已覆盖 |
| §6 缺稳定映射计覆盖但不建议；NULL/空tertiary | D ReviewUnmappedEmptyTertiary；UI17 13 | 已覆盖 |
| §6 收藏/待订正/历史，manual resolved不宣传真实答对 | D LatestLabelsAndResume/ReviewMaxTwentyAndSecurity；UI17 8；Task2/3审查 | 已覆盖 |
| §6 透明规则，不冒充在线AI/调用付费模型 | Review页面/API/服务源码及测试调用范围 | 已覆盖源码范围 |
| §7 v12仅追加、profile列/FK/昵称单源、默认回填/注册原子 | M；P DefaultsUpdateCAS/WriteAtomicity；A；Task1审查 | 已覆盖 |
| §7 GET不隐式写profile；publicID唯一随机，旧token/expiry保留 | P DefaultsUpdateCAS；M；Task1审查 | 已覆盖 |
| §7 统计/会话索引；业务/HTTP职责划分 | schema/profile/dashboard/sessions/export/account独立审查 | 已覆盖源码 |
| §7 revision事务/昵称CAS，WriteTx；保持交卷原子/幂等 | P ConcurrentCAS/WriteAtomicity；D ReviewSessionCompatibility；B | 已覆盖 |
| §7 明确ReadOnly短一致事务、按user/window、有界、无逐日N+1 | D ConcurrentHistoryAndCancellation；Task2审查 | 已覆盖；非生产压测 |
| §7 无缓存/队列/后台聚合依赖，合成历史并发 | D ConcurrentHistoryAndCancellation；依赖/源码审查 | 已覆盖16组并发；生产吞吐未测 |
| §8 八个新增路由均requireUser，methods/状态/DTO一致 | P/D HTTP测试；docs/api-design.md；Task1/2审查 | 已覆盖 |
| §8 薄弱/续做复用现有practice；auth兼容/Login密码CAS | D ReviewSessionCompatibility；A LoginPreservesHashCAS；OLD6 | 已覆盖 |
| §9 完全Cookie身份，管理员不混用；客户端user_id无效 | P RejectsInvalidAndUnauthorized；D ReviewMaxTwentyAndSecurity/ExportAndReviewHTTP | 已覆盖 |
| §9 Origin/application/json、无body DELETE检查 | P SessionWriteRequestSecurity；D ReviewMaxTwentyAndSecurity | 已覆盖 |
| §9 白名单/长度/枚举/Vue文本、受控CSS | P RequiresCompleteEditableFields/DateAndUnicodeBoundaries；Task3审查 | 已覆盖 |
| §9 隔离和对象404一致；不改草稿/清库/全局setting | P/D；OLD6；Task1–3 diff与审查 | 已覆盖 |
| §9 500异常，503/Retry-After；401专身份，400/409不误登出 | 原WriteTx/HTTP测试；UI17 3、4、9、15 | 已覆盖；UI故障注入边界明确 |
| §9 空集合/null/default，无假成绩；图组件独立防整页失败 | D EmptyAndAllHistoryStreak；UI17 1；Task3组件审查 | 组件容错源码核对；未逐图注入运行错误 |
| §9 改密/撤销让其他设备401，资料保存不撤销 | P SessionsIsolationAndRevocation/DefaultsUpdateCAS；UI17 4、11 | 已覆盖 |
| §9 CSV同源Cookie/无token URL/固定名 | D ExportAndReviewHTTP；UI17 10；api.js审查 | 已覆盖 |
| §9 v12备份/有效密钥/前后端新binary/副本迁移与bundle恢复 | M；B PersonalBackupBundleRestoresProfileSessionAndCAS；npm/构建/包清单 | 已覆盖隔离验收；正式迁移未执行 |
| §10后端 全部验收九条 | 上述M/P/D/A/B及race/vet结果 | 已覆盖，性能为合成验收 |
| §10页面 资料/偏好刷新、空图/范围/键盘/375/失败/徽章/推荐 | UI17全17；ORDER4全4 | 已覆盖Chrome |
| §10页面 多页CAS/离开/撤销/错原密；原24回归 | UI17 4、9、11；OLD18+OLD6共24 | 全部exit0 |
| §10交付 Go全量/race/vet/npm/Linux包；说明/API/验证记录 | task4-results.json、root-precheck、Task4报告、发布清单 | 本地验收；独立宽范围终审由根安排 |
| §11 排除外部认证/上传/私信/支付/公共排名/付费AI；源码本地交付 | 本任务范围及Task1–3审查 | 未提交/推送/部署 |

正式环境边界：未运行正式 DB/实际密钥迁移，未验线上 HTTPS/代理配置/压力/长时间运行/异机完整恢复，未验 Windows/macOS 原生运行、其他浏览器及真实读屏器。跨编译不等于原生运行；临时库恢复不等于生产大库恢复演练。最终独立终审状态由根代理另行记录，矩阵不替代终审。

## 2026-10-06 最终修复增量覆盖

旧矩阵与45组证据属于Task4历史版；以下补最终审查R1，不以旧版通过推断本修复通过。最终独立定向复审仍由根代理安排。

| 要求/根因 | 新验证 | 边界 |
|---|---|---|
| 共享Cookie换到B、旧A表单和B revision相同仍不可写B | 正式 `--cross-tab-account` silent/notified两页同context；silent真实PUT固定owner header/409，notified本地无PUT，B全字段不变 | 红灯B确被污染，绿灯两条；无PUT/Cookie/服务器业务替身 |
| 页前提只拒绝、不选用户；兼容原API | ExpectedIdentityRejectsBeforeEffects真实HTTP/SQLite，11路径、匹配/不符/非法/无前提；匿名401/Origin403/admin独立 | Cookie唯一鉴权，ProfileUpdate不加身份字段 |
| 资料读取、CSV、改密、会话撤销、统计重算的同根因 | 上述HTTP临时B已提交CSV、两会话、密码摘要、资料revision和统计sentinel | B不泄漏/不改变；全部业务前409 |
| 新owner不受旧owner迟到响应影响 | 正式 `--late-identity-conflict/--late-security-password/--late-security-revoke` | 业务响应route.fetch真实完成，导航401/事件为明确故障注入；B偏好/状态保留，无旧会话续读 |
| 现代logout晚到不能删除后来B Cookie；旧API兼容 | GuardedLogoutDoesNotDeleteLaterCookie HTTP红绿；正式 `--late-current-logout`真实200晚到Bme200 | 现代token已撤销后401，无Set-Cookie；旧无header仍删除Cookie |
| 原功能完整集成 | 稳定主修复版原45由根代理纯批准脚本重跑；最后仅known-stale分支调整，最终补7+ordering4，独立final-fix-*结果 | 原17/ordering4/旧18/旧6断言不放宽；历史task4-*保持 |
| 已收到只退出通知时仍保留未保存输入 | `--notified-only-logout`真实红绿，最终save/reload/CSV无网络，后台401不跳转 | 主修复稳定45后唯一狭窄stale分支改动；最终定向7+ordering4验证，未冒称再次全45 |
| 新二进制/嵌入前端/说明一致 | 最终Go全量/相关race/vet/npm/三平台编译、`make -o web release`、audit `--final-fix` | 新本地包与旧封存包分开，报告不嵌自身包SHA；待独立复审 |

