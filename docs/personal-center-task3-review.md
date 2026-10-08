# 个人中心 Task 3 独立审查

## 规格合规性

- ❌ 需要修复：同一账号的迟到资料 GET 可以让成功保存后的客户端资料版本倒退，破坏“保存后同步顶部资料”和保存版本一致性。定位：`web/src/views/Account.vue:19–20`、`web/src/components/account/ProfileForm.vue:11`，具体触发见 R1。
- ✅ 四个本地页签、未保存离开保护、范围切换和读取重试已实现：`web/src/views/Account.vue:18–28`、`:33–40`；顶部与侧栏入口见 `web/src/App.vue:30–34`。
- ✅ ProfileUpdate 白名单、PUT、400/409 输入保留、确认重载、保存中禁用及受控枚举均有对应实现：`web/src/components/account/ProfileForm.vue:6–16`、`:20`，`web/src/api.js:39`；未增加身份字段或 POST/PATCH 保存接口。
- ✅ 四类图形分别有组件、数字摘要和数据表，趋势 null 不绘为 0%，模块有样本不足说明，目标时长使用会话总时长：`web/src/components/account/Overview.vue:10`、`charts/TrendChart.vue:13`、`charts/ModuleChart.vue:8`、`charts/ActivityCalendar.vue:12`、`charts/GoalRing.vue:7`（charts 路径均位于 `web/src/components/account/`）。
- ✅ 续做、规则错题、模块/考点与收藏/记录入口接入已有业务；会话分页、确认撤销、保留当前会话入口及原改密/重算入口齐备：`web/src/components/account/Review.vue:8–10`、`Security.vue:11–20`。
- ✅ 退出与账号切换清理偏好，题库显式 URL 筛选优先，练习字号限定 16/18/20：`web/src/auth.js:7–29`、`web/src/views/QuestionList.vue:10–14`、`web/src/views/Practice.vue:14`、`:71`。
- ⚠️ 本快照不含后端变更，不能独立核实 Cookie 身份过滤、Origin、北京时间统计 SQL、草稿 revision/交卷幂等、CSV 公式防护及 5,000 条上限的全部实现；需沿用 Task 1/2 审查与 Task 4 集成证据。接口调用点见 `web/src/api.js:38–45`；交付级验收边界见 `docs/personal-center-task3-report.md:159`。

## 优点

- `web/src/auth.js:5–29` 将偏好缓存 owner 与登录页赋值分开，并使用 generation 和用户身份限制异步写回；`test-artifacts/personal_center_regression.py:394–494` 实际覆盖资料读取失败时切换账号及迟到 GET/PUT ACK，针对了真实的缓存串号风险。
- `web/src/views/Account.vue:18` 对看板请求使用 generation，`test-artifacts/personal_center_regression.py:189–207` 保留真实旧请求后释放，验证快切和失败重试；没有用静态示例图冒充数据接入。
- `web/src/components/account/charts/ActivityCalendar.vue:4–12` 分离鼠标预览与键盘选中日期，保留一个 Tab 目标及方向键；其他图形同样提供数据表。测试保留真实提交、练习创建和会话撤销成功路径，见 `test-artifacts/personal_center_regression.py:135–187`、`:209–245`、`:304–346`。
- 既有草稿与改密回归只增加新页签定位，没有删除原断言：`test-artifacts/round2_fixes_ui.py:138`。

## 问题

### Critical

- 无。

### Important

**R1：同账号迟到的初始化资料响应会覆盖已经成功保存的新资料。**

- 定位：`web/src/views/Account.vue:19–20`；触发入口 `web/src/views/Account.vue:28`；缓存写入 `web/src/auth.js:17–22`；表单回退 `web/src/components/account/ProfileForm.vue:11`。
- 触发：路由中的第一次 profile GET 暂时失败，`web/src/router/index.js:29` 允许非 401 失败后继续；Account 没有缓存资料，挂载时同时请求 dashboard 和 profile。dashboard 先返回 revision N 并通过 `saved(data.profile)` 建立表单；另一个 profile GET 已读出 N，但其响应延迟。用户此时编辑并成功 PUT，表单和顶部更新至 N+1。旧 GET 随后返回，`reloadProfile()` 无条件调用 `saved(p)`。
- 根因：`ownsPage()` 只检查页面存活与用户 ID，`saved()`/`applyProfile()` 只检查身份，没有阻止同账号 revision 回退。旧响应仍属于当前用户，因此会覆盖 `auth.profile`、`auth.user.nickname` 和页面 profile。成功保存后的表单已不 dirty，其 watcher 会进一步把表单重置为旧资料与 revision N。
- 影响：服务器仍是 N+1，但界面、顶部昵称/头像和偏好缓存退回 N，并可显示“已同步”。下一次编辑保存携带旧 revision，产生本可避免的 409；用户刚收到的成功保存结果在页面中被静默撤回。现有跨账号延迟响应场景无法覆盖这个同账号交错。
- 建议：统一资料接收路径，拒绝同一账号低于当前已确认 revision 的响应，或在成功保存后使所有先前资料读取失效；初始化 dashboard、profile GET 和保存 ACK 应遵守同一非回退规则。旧响应被忽略时不要误报“登录账户已变化”。增加上述交错的真实浏览器回归，断言表单、顶部、偏好和 revision 均保持 N+1，且后续保存成功。
- 证据级别：代码触发链确定；本次未取得浏览器复现结果，限制见下。

### Minor

- 无单列项。

## 检查与限制

- 完整分块读取 `test-artifacts/personal-center/task3-review.diff:1–1054`，覆盖 20 个文件；审查基线是 Task 3 前快照，不是 Git HEAD。没有扩大为整库审查，没有修改源码、索引或分支。
- 核对简报、规格、UI 方向、账本及完整实现报告；核对 `test-artifacts/personal-center/task3-browser-results.json:2` 的 17 组检查与空 `console_errors`，以及 `test-artifacts/round2-fixes/ui-results.json:2` 的 6 组检查与空 `console_errors`。未重跑实现者已验证套件，构建成功依据为 `docs/personal-center-task3-report.md:87–105` 的既有记录。
- 针对 R1 尝试一次临时 stdin Playwright 脚本：仅面向 `http://127.0.0.1:18083` 合成隔离服务，计划注入首次 GET 500、保留第二次真实 GET，再释放至成功 PUT 之后。沙箱内首次运行无输出，已 Ctrl-C 终止（exit 130）；随后请求 `require_escalated`，审批等待约 583 秒后被用户中止。未获得任何可用于证明复现成功的输出，因此不宣称浏览器已复现，也不把运行阻塞当作产品失败。
- 本次没有读取真实密钥、修改正式 DB、提交、推送或部署；唯一工作区文件写入为本报告。R1 的正式红绿测试交实现者补齐。

## 评估

**规格合规性：需要修复。**

**任务质量：需要修复。**

主体功能与既有证据覆盖良好，但同账号的旧资料响应可让成功保存后的客户端状态倒退，影响保存反馈、偏好及后续 CAS。修复 R1 并补充这个具体交错的回归后再进行本任务复审。

## 修复轮 1/5 定向复审

### 各条发现的结论

- **R1：同账号迟到的初始化资料响应会覆盖已经成功保存的新资料 —— ADDRESSED（已解决）。** `web/src/auth.js:18–26` 在身份校验通过后保留同 owner 已确认的最高 revision，并将旧响应视为成功受理而非身份错误。`web/src/auth.js:28–33` 仍保留 generation/用户 ID 检查，返回最高版本缓存而非原始旧 GET；`:13–16` 也防止无 revision 的用户信息覆盖已确认昵称。`web/src/views/Account.vue:18–20` 将 dashboard、profile GET 和保存通知统一送入该规则，页面与 dashboard.profile 都采用确认资料；`web/src/components/account/ProfileForm.vue:10`、`:15–16`、`:20–21` 将保存 ACK、确认重载和 clean 表单更新纳入同一规则，并保留存活/身份、dirty、版本下界与 CAS 输入保护。旧版本不会让表单、顶部、偏好或 revision 回退，也不会误报“登录账户已变化”。
- **R1 回归覆盖匹配。** `test-artifacts/personal_center_regression.py:35–75` 精确覆盖首次资料失败、真实初始化 GET 读 N、真实 PUT 成功 N+1 后释放旧响应，并断言昵称/头像/偏好/版本及下一次保存；`:94–124`、`:126–153`、`:155–204` 分别覆盖迟到 dashboard、路由 loadProfile 和低于已确认版本的成功 PUT ACK，最后一项还断言等待 ACK 的 dirty 输入保留及明确同步提示。

### 修复 diff 里的新破坏

- 无。身份 owner/generation、400/409 输入保留、保存中保护与 ProfileUpdate 字段白名单仍保持；新增 dashboard 同步受最高版本和 dirty watcher 双重限制，没有发现本轮引入的 Critical/Important/Minor 问题。

### 范围外的观察

- 无。

### 实际检查与证据边界

- 完整分块读取 `test-artifacts/personal-center/task3-fix1-review.diff` 的全部 5 个文件（3 个源码文件、正式验收脚本及修复报告追加部分）；基线为修复前快照，未运行 Git diff 或扩大首次完整审查。
- 对照 `docs/personal-center-task3-report.md:161–211` 的真实红灯退出码 1、同一正式定向命令绿灯退出码 0、最新前端/嵌入 CLI 构建及原回归记录，核对 `task3-fix1-browser-results.json` 的 4 项、`task3-browser-results.json` 的原 17 项和 `round2-fixes/ui-results.json` 的原 6 项，三份结果均为 `console_errors: []`。这些运行证据由实现者提供，本复审实际执行的是代码与结果核验，未将其表述为本复审重新运行的测试。
- 针对资料接收与缓存写入做定向调用点检索，确认 profile GET、dashboard 内嵌资料、保存 ACK、确认重载均采用不回退规则。未出现现有结果无法回答的具体疑问，因此未重跑套件、启动临时浏览器或请求新审批。
- 本复审唯一写入为本节追加；未修改源码、索引、HEAD/分支，未操作真实 DB/密钥，未执行 Task 4、提交、推送或部署。

### 本轮结论

**本轮修复：所有发现均已解决，无新的 Critical/Important 破坏。**

**规格合规性：通过本任务定向复审。** R1 所要求的同账号版本不回退、旧响应不误报身份变化及正式交错回归均已满足；首次报告列出的 Task 4 交付验证边界仍然适用。

**任务质量：通过本任务定向复审。** 修复集中于共同接收规则与页面/表单同步，真实红绿和原回归证据与修改相符；无需进入下一轮修复。
