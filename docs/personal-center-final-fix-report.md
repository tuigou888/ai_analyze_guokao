# 个人中心最终修复记录

日期：2026-10-06。范围：最终审查唯一 Important R1 及同一页面身份绑定的请求/迟到回调边界。状态：实现及本地验收通过，待根代理安排唯一独立定向复审；不标记完整功能最终完成。原最终审查报告保持不变。未提交、推送、远程部署或操作正式数据库/密钥。

## 根因与修复

完整阅读最终审查 R1、规格/计划/账本与实际调用：同浏览器另一标签把共享 Cookie 从 A 换成 B，旧 A 页面内存与表单仍属于 A。两账户 revision 同为0时，服务器按 Cookie B 的主体处理旧 A 的 PUT，B 的CAS也成功；收到响应后的owner校验只能丢ACK，不能撤回已完成写入。

新增可选 `X-GK-Expected-User` 页面前提。统一 `requireUser` 在 Cookie 验证成功后、业务调用前比较该前提，非法格式400、不符409且 `code=account_changed`；原401、写请求Origin403和既有JSON校验保留。前提只用于拒绝，不能选择查询用户或授予权限；全部业务仍取已验证Cookie的主体。覆盖个人资料/看板/重载、CSV、会话管理、改密、统计重算、复习和其他原普通用户业务，logout在组外单独复用相同边界。ProfileUpdate字段合同未添加用户名/user_id，没有schema、依赖或新凭据。

个人中心与资料/安全/复习组件的API客户端固定于创建该页的原owner；默认普通用户客户端在请求发起时携带当前已验证owner，身份发现和管理员保持独立。跨标签明确登录公告只提示异owner失效，退出/改密公告带发起owner只提示同owner标签；信号不能建立登录身份或给旧表单套新owner。本地401/路由校验失败不广播Cookie退出。账号变化提示保留输入，用户记录后重新进入；不走同账号CAS的重载/自动重发路径。

身份不符/401事件带发起owner，迟到旧A错误不清除当前B偏好。Security成功回调检查页面存活和固定owner，旧改密不能清B身份，旧撤销不能继续读取B会话。App.logout在等待输入保护前固定role/owner，发送和响应均受owner保护；退出失败保留尚未保存输入。

账本Ruling：带前提的现代logout仅撤销已验证token，不发删除Cookie的Set-Cookie，防止晚到A退出响应删除后来B登录Cookie。现代页成功后清理本地身份并发owner限定通知；残留HttpOnly Cookie对应token已失效、不能认证，允许留至过期/下次登录。无header旧API保留原幂等和删除Cookie行为。

## 红绿证据

正式脚本 `test-artifacts/personal_center_regression.py --cross-tab-account` 首次真实红灯exit1：同context双页、A/B revision0，旧A保存后B昵称变为“A跨标签未保存输入”、revision升1。原始JSON封存 `final-fix-cross-tab-red.json`；最初最小用例随后扩为两条正式回归。最终版silent原始fetch换Cookie（无BC信号）及notified另一页真实UI登录均exit0：silent真实旧页PUT包含固定A owner且服务器409，notified保存在本地拒绝、不发PUT；B前后全部字段相同，提示与输入保留，已知stale的CSV/退出拒绝且B Cookie有效。

同根因回调扩展也有真实红灯：迟到A的真实409在B登录后释放会误清B缓存/显示身份变化；迟到A改密真实200会把B跳/login；迟到A logout真实200的删除Cookie响应会令B `/auth/me` 401。分别封存 `final-fix-late-identity-red.json`、`final-fix-late-password-red.json`、`final-fix-late-logout-red.json`。修复后四条 `--late-identity-conflict/--late-security-password/--late-security-revoke/--late-current-logout` exit0，B昵称/头像/20px/50题保持、无错误身份提示或跳转、B Cookie200，旧安全回调没有新会话重载请求。真实HTTP响应通过route.fetch完成后延迟释放；仅切到登录页阶段使用人工session-expired事件或CSV401故障注入，不能把这一导航注入描述为真实服务器认证失败。

新增 `internal/serve/personal_identity_test.go` 使用fixture的真实 `t.TempDir()` SQLite/HTTP路由：Cookie B+expected A在11个读取/副作用路径执行前409，B资料/密码摘要/两份登录会话/统计sentinel不变，真实B已交卷CSV不能泄漏；无前提PUT/logout仍可用、匹配前提只返回B、非法前提400、匿名401/非法Origin403/admin独立。原定向测试红灯明确得到错误资料写入、CSV200和改密200；绿灯三测试通过。Guarded logout另有HTTP红灯（成功仍返回删除Cookie），绿灯验证无Set-Cookie、原token随后401、legacy仍删除Cookie。

命令和退出码见 `test-artifacts/personal-center/final-fix-results.json`，各绿色JSON与红色JSON保留。脚本准备期间三个无效运行单列：合成用户名加长超32字符；首次改密仅等待100ms未完成；三个UI进程并行触及两并行密码计算上限。均修正测试准备/等待/串行运行后重跑，没有放宽产品断言，不计有效产品红灯。

最后补“另一页只退出、尚未登录B”已知stale分支。真实UI退出通知后旧页保存曾发PUT401并跳登录页、丢输入，红灯封 `final-fix-known-logout-red.json`。最终save/reload/CSV本地先检查固定owner与已知失效标记，拒绝且不发网络；已知stale的后台user401不自动跳转，普通active401保持原流程。`--notified-only-logout`绿灯保留输入、请求列表为空，注入迟到后台401也不跳转。

## 验证与交付

- Go最终全量 `go test ./... -count=1` exit0，serve10.572s；最终受影响身份/logout race exit0，serve11.365s。R1主修复阶段核心serve/study/store/setting全race也exit0，serve159.005s；该次早于guarded logout扩展，扩展以最终定向race覆盖，不冒称为扩展后的全核心race。
- `go vet ./...` exit0，最终前端 `npm run build` exit0，60 modules，1.44s。最后代码嵌入CLI后Linux/Windows/macOS amd64编译均exit0，实际文件类型ELF/PE32+/Mach-O；后两者只交叉编译，没有原生运行。
- 根代理稳定主修复版原45组（18+6+17+4）全部exit0，四份final-fix-*结果和root-verification保留。此后仅修改已知stale的ProfileForm早期拒绝及router401保留分支，按根裁定最终版补定向7组（双标签2、迟到回调4、只退出通知1）及同账号ordering4，全部exit0；没有宣称原45在这次狭窄分支调整后全部重跑。稳定45版二进制另存 `_work/final-fix-stable45-gk`及SHA，最终编译/包均为最后代码。唯一UI场景合计52组，最终版定向实际11组；`console_errors: []`只代表监听页面pageerror为空，未采集全部console.error/网络错误。

临时服务仅使用 `_work/task3.sqlite`、`task3-synthetic.key`、127.0.0.1:18083，逐次由自有tool session启动并Ctrl-C退出0。原45服务26275与最终20231/94372均已Ctrl-C退出0，所有本次自有服务已关闭；无其他进程操作。隔离库管理员webtester仅为旧18测试重置，没有访问正式数据。

原包/manifest/results保留于 `_work/pre-final-fix-release/`，原Task4的45组和包是历史版本证据，不包含本次修复。新包复用本轮已构建前端，使用 `make -o web release`和audit `--final-fix`，包含最终修复报告共19文件，报告不嵌自身包SHA。新 `final-fix-package-manifest.json`、`final-fix-results.json`和 `_work/final-fix-sealed/`封存快照独立记录，sealed-manifest记录包/证据/diff SHA；不包含运行时DB/密钥/fixture。API、个人说明、部署手册、Task4附录与覆盖矩阵随新包同步更新。

未测边界延续原交付：正式库/真实密钥升级与回滚、线上HTTPS/实际可信代理/压力/长时间运行、异机恢复、Windows/macOS原生运行、其他浏览器与真实读屏器。本轮真实本地HTTP、合成SQLite和Chrome条件化回调不提供这些生产证据。无header旧客户端/已打开旧缓存页面仍按兼容合同，发布新前后端后需刷新并重新进入页面。反向代理必须透传预期身份header且不能用它授权，正式代理仍未测；部署恢复节明确旧快照恢复可能恢复会话而令残留Cookie再有效，正式恢复按策略统一撤销历史会话重新登录，没有改恢复实现或正式操作。

修复diff以分派前60源/测试/文档快照为基线，新增身份源码/测试/报告及证据另纳入；包审计辅助脚本不在60清单，其首次完整读取的原文通过精确可逆变更复原为support-before补充比较，未用Git HEAD混入旧修改。`personal-final-fix.diff`供定向复审，seal manifest因记录diff SHA单列以避免自引用。账本仅标待定向复审，原最终审查报告不改。
