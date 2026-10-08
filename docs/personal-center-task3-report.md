# 个人中心 Task 3：页面与图表实现验证

日期：2026-10-04 开始，2026-10-05 完成开发与本任务验证。前端交付为真实 Vue 页面，沿用蓝白研习主题，以今日双目标和 90 日学习日历作为学习工作台的主线。没有安装依赖、修改后端/schema/构建配置、提交、推送或部署。保留 Task 3 开始前的旧修复。

## 页面与接口接入

`/account` 标题与导航改为“个人中心”。桌面侧栏和顶部用户区可进入；页面四个页签在本地切换，不通过 query 重建表单。

| 页签 | 已实现行为 |
| --- | --- |
| 学习总览 | 资料、注册信息、未来/已到/已过考试日期；今日作答与分钟双目标；期间/累计计数、上期绝对增量、双趋势、模块画像、90 日学习日历、连续练习和真实成就 |
| 复习助手 | 最近未完成练习续做；自动错题优先复练；达到 5 次样本的模块/有效考点复练；标注覆盖、未形成映射提示；收藏和记录入口 |
| 资料与偏好 | 全字段资料/目标/考试/偏好保存、8 个受控头像、版本提示、字段错误、冲突确认重载、CSV 导出 |
| 账户安全 | 保留原改密标签、原密码 400 和统计重算；活跃会话分页、单份撤销确认、退出其他确认、当前会话退出 |

API 客户端新增：

| JS 方法 | HTTP | 返回/参数 |
| --- | --- | --- |
| `profile()` | GET `/api/account/profile` | Task 1 的完整 Profile |
| `saveProfile(data)` | PUT `/api/account/profile` | 完整 ProfileUpdate + revision，返回保存后的 Profile |
| `dashboard(days)` | GET `/api/account/dashboard?days=...` | Task 2 的 Dashboard，7/30/90 |
| `loginSessions(page)` | GET `/api/account/sessions?page=...` | items,total,page,size，固定每页 20 |
| `revokeSession(public_id)` | DELETE `/api/account/sessions/{public_id}` | 只提供本人其他会话的删除入口 |
| `revokeOtherSessions()` | POST `/api/account/sessions/revoke-others` | revoked，保留当前 Cookie |
| `prioritizedWrongbook()` | POST `/api/account/review/wrongbook` | 现有 Session，进入原练习室 |
| `exportRecords(days)` | GET `/api/account/export?days=...` | Blob，7/30/90/all |

ProfileUpdate 明确只发送 `nickname,bio,avatar_id,daily_questions,daily_minutes,exam_name,exam_date,default_limit,default_module,reading_size,revision`。不发送 username、created_at 或用户 ID。头像为编号 0–7，字号 16/18/20、题量 10/20/50，未引入 URL 头像或外部图片。

资料 400/409 保留输入；409 不递增本地版本、不自动重发，重载需要确认。保存中禁用字段、重复保存和页签/路由离开。未保存资料或密码输入有换页签、路由、刷新和退出保护；取消可回到页面保存，确认可放弃。退出 API 失败后，不保留任何永久跳过保护的标记。

偏好仅按当前已验证用户 ID 缓存在内存，跨设备恢复依赖服务端 GET。缓存 owner 与 `auth.user` 分开记录，避免登录页先赋新 user 导致旧偏好保留。退出/401 清除缓存；异步响应有 generation/页面存活/用户 ID/用户名检查。保存昵称与头像同步顶部。

题库默认题量/模块读取保存偏好，URL 的明确 `module`/`limit` 优先。用户明确选择全部模块时，保留 `module=`，刷新也不会恢复默认模块。Practice 的题干、材料、选项、官方解析及标注正文实际应用保存字号，不更改整站控件字号。

## 图表与真实口径

- 趋势使用两个独立 SVG 小图，次数与百分比分开坐标；accuracy 为 0–100/null，无样本日期不画 0% 点或连接线。
- 模块画像显示真实次数、正确率和样本状态；0 样本显示空缺，少于 5 次明确样本不足，不表示 0 分。
- 日历固定 90 个真实日期，按星期排布；鼠标预览与键盘选中位置分开，避免悬停抢走焦点；只有一个日历 Tab 目标，方向键/Home/End 可选择日期。
- 目标环最大 100%，同时保留实际分子/目标分母、剩余及超额数值。分钟只用 `today.duration_ms / 60000`，不加 modules 的答案时长或模块 sessions。
- 四类图表有 accessible name、可读数字摘要及折叠数据表；趋势日期下拉、模块按钮、日历方向键可查看具体值。数据表和日历在自身容器滚动，375px 没有整页横向溢出。
- 图表只消费实际 Dashboard；没有在生产组件写示例成绩、示例日历或成就。期间快切忽略旧 response，失败提供重试。

续做进入原 session；模块/考点练习调用已有 `createSession('single'/'concept', spec)`；优先错题调用后端规则入口并进入现有 wrongbook session。未映射标签只显示说明，不产生虚构的 concept_id。改密、统计重算、收藏/记录页面与草稿/交卷协议保持原入口。

CSV 请求使用 same-origin Cookie，不在 URL 放凭据；成功 Blob 与错误 JSON 分支共用原 `request()` 的 401 事件处理。客户端下载名固定为 `行测研习-个人作答记录.csv`，400 上限错误可见。下载通过浏览器生成对象 URL，随后释放。

## 测试环境与红灯

测试使用只读打开原库后生成的隔离快照 `test-artifacts/personal-center/_work/task3.sqlite`。隔离库清除了 setting、旧管理员/普通账号、旧会话及个人学习记录，使用新建合成管理员和合成用户。密钥由 CLI 在 `_work/task3-synthetic.key` 新生成；没有读取真实 secret.key，没有改写正式 `var/db`。原题库和已有标签用于有效练习组题，data 目录只读。未调用模型/OCR 或远程服务。

先新增真实 Chrome 验收脚本、构建旧 Account 并启动隔离 CLI，然后运行：

```sh
python3 test-artifacts/personal_center_regression.py http://127.0.0.1:18083 --red
```

实际退出码 1：

```text
AssertionError: Locator expected to be visible
element(s) not found: get_by_role("tab", name="学习总览", exact=True)
```

旧页面截图：[task3-red-old-account.png](../test-artifacts/personal-center/task3-red-old-account.png)。截图与断言均来自真实已登录页面，不是静态图稿。

补充跨账户偏好故障场景时，旧缓存逻辑出现第二条真实浏览器红灯：A 保存默认 10 题，登录页先赋 B 身份，B 的资料 GET 被注入 500，页面仍使用 A 的 10 题。

```text
Locator expected to have Value '20'
Actual value: 10
```

修复为独立 profileUserID owner 后，同一真实登录切换场景转绿。另自审核对并修复退出失败后保护豁免、迟到的资料/看板/保存响应身份检查、全部模块的空值 URL 覆盖和日历悬停与键盘焦点混用。

合法 40 字符 ASCII 考试名称另有真实 375px 红灯：`AssertionError: 40-character exam name overflow`。仅在账户标题/建议文本增加局部换行，修复后转绿。

日期上界另有真实浏览器红绿：将浏览器 JS 日期模拟为北京时间 2028-03-01，新选 2033-03-02 时，旧 UTC 年操作误把上界放宽一天，日期字段未给出预期行内错误。改为对 YYYY-MM-DD 日历日期执行 UTC 年操作后转绿；没有修改后端时钟。

首次监听与 Chrome 启动被沙箱拒绝，按工具要求获得批准后重跑；这些权限失败未计作功能红灯。验收中的临时测试定位错误也没有算成功能缺陷：筛选等待改为等待路由完成，新用户昵称断言按真实空字符串 DTO 修正。

## 绿灯与实际浏览器证据

最终前端构建：

```sh
cd web
npm run build
```

退出码 0：`✓ 60 modules transformed.`，`✓ built in 1.62s`。产物输出到既有 `cmd/gk/web_dist`，没有修改构建配置。

用于真实浏览器的嵌入 CLI：

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go build -o test-artifacts/personal-center/_work/gk-task3 ./cmd/gk
env GOMAXPROCS=2 test-artifacts/personal-center/_work/gk-task3 serve --db test-artifacts/personal-center/_work/task3.sqlite --secret-key-file test-artifacts/personal-center/_work/task3-synthetic.key --data data --addr 127.0.0.1:18083
```

编译退出码 0；本地服务正常启动，浏览器实际请求该 CLI 提供的构建页面。

```sh
python3 test-artifacts/personal_center_regression.py http://127.0.0.1:18083 test-artifacts/personal-center/_work/task3.sqlite
```

最终退出码 0，17 组场景通过，`console_errors: []`。结果：[task3-browser-results.json](../test-artifacts/personal-center/task3-browser-results.json)。覆盖：

1. 四页签、四类真实空数据图、页签方向键/Home/End。
2. 完整资料 PUT、刷新持久化、顶部昵称/头像、8 个头像、未来考试倒计时。
3. 退出 503 后身份保留，再编辑的页签/路由保护仍有效。
4. 两页真实 409、本地输入保留、确认重载；原密码错误 400 不误登出。
5. 四页签空状态、有数据状态以及合法 40 字符考试名称在 375px 无整页溢出。
6. 六份真实提交得到 6 次作答、1 次答对、16.7% 正确率、6 分钟；未提交草稿不混入；真实成就和图表数值。
7. 趋势/模块/日历的键盘数值、鼠标预览、数据表、日历单一 Tab 目标。
8. 实际 7 天请求延后返回不能覆盖 90 天结果；读取失败可重试。
9. 原 session 续做保留已保存答案，收藏 session 的真实 favorite 类型可识别并续做；优先错题、模块与有效考点均创建真实兼容 session；20px 实际作用于题干、选项、解析。
10. 注入 profile 400 后输入和登录保留；真实 PUT 延迟确认时字段、保存、离开禁用；刷新、路由、退出保护；默认值与明确 URL/全部模块刷新覆盖。
11. 实际 CSV 下载、固定文件名、BOM；注入上限 400 可见。
12. 第二浏览器 context 恢复服务端偏好；撤销确认、其他 Cookie 401、当前 Cookie 保留；原统计重算可用。
13. 隔离库 23 份活跃会话分页 20/3；历史 NULL 元数据显示未知；当前行无 DELETE 按钮；响应无 token/hash 字段。
14. 隔离库真实 latest_label 缺映射时显示说明并不生成无效考点；标签 fixture 在 finally 恢复。
15. 已存昨日考试日期可保留并改昵称；新改过去日期行内拒绝；以浏览器 JS 日期模拟 2028-03-01 北京时间，2033-03-02 超过五年日历上界，行内拒绝。
16. Blob 401 复用 session-expired；退出/换用户恢复本人默认值；B 的 profile GET 失败时也不继承 A 偏好。
17. A 的真实 dashboard/profile GET 与成功 PUT 响应在退出/401、B 登录后才返回，不能覆盖 B 的昵称、头像或字号。

500/400/503/401 UI 失败路径通过 Playwright 注入相应响应，明确与真实成功 API 数据区分。CSV 5,001 条边界的服务端正确性由 Task 2 测试验证，本任务没有伪称用 5,001 条浏览器导出。资料、CAS、练习提交/组题、下载、会话撤销和统计重算成功路径均走真实 HTTP 与 SQLite。

原草稿与改密回归：

```sh
python3 test-artifacts/round2_fixes_ui.py http://127.0.0.1:18083 test-artifacts/personal-center/_work/task3.sqlite
```

最新构建退出码 0，6 组通过，`console_errors: []`：迟到保存、清空多选、双页草稿冲突、交卷冻结、同路由 query 离开保存、原密码 400。该脚本只追加进入“账户安全”页签的定位，没有删除或放宽原断言。结果：[round2-fixes/ui-results.json](../test-artifacts/round2-fixes/ui-results.json)。

`git diff --check` 和两份脚本 `python3 -m py_compile` 均退出码 0。

截图来自真实浏览器：

- [空数据桌面总览](../test-artifacts/personal-center/account-desktop.png)
- [有数据桌面总览](../test-artifacts/personal-center/account-populated-desktop.png)
- [有数据 375px 总览](../test-artifacts/personal-center/account-populated-mobile-学习总览.png)
- [有数据 375px 复习助手](../test-artifacts/personal-center/account-populated-mobile-复习助手.png)
- [375px 资料与偏好](../test-artifacts/personal-center/account-populated-mobile-资料与偏好.png)
- [375px 账户安全](../test-artifacts/personal-center/account-populated-mobile-账户安全.png)

## 修改文件与自审

修改：`web/src/views/Account.vue`、`web/src/App.vue`、`web/src/api.js`、`web/src/auth.js`、`web/src/router/index.js`、`web/src/views/QuestionList.vue`、`web/src/views/Practice.vue`。

新增：`web/src/components/account/Avatar.vue`、`Overview.vue`、`Review.vue`、`ProfileForm.vue`、`Security.vue`、`account.css`；charts 下 `GoalRing.vue`、`TrendChart.vue`、`ModuleChart.vue`、`ActivityCalendar.vue`。

验收文件：新增 `test-artifacts/personal_center_regression.py`，最小调整 `test-artifacts/round2_fixes_ui.py` 页签定位；新增本报告及本任务截图/JSON/合成 CSV 证据。构建产物、临时 DB/密钥/CLI 在既有构建目录或 `_work`，不作为生产源码修改。任务修改对照 Task 3 开始快照生成 `test-artifacts/personal-center/task3-review.diff`。

自审确认：数据和 DTO 单位正确；零分母/少样本不伪造分数；profile 不发送身份字段；头像、字号受控；文本使用 Vue 转义；无付费诊断入口；无凭据 URL；全部复习进入既有判分链；退出失败与迟到响应不会跳过保护或写入别人的缓存；Practice 的旧保存/交卷逻辑没有重写。

本任务尚未执行正式库迁移/大库恢复、Linux 发布打包、完整旧 24 项浏览器回归、Go 全量/race/vet 或真实屏幕阅读器/其他浏览器测试，这些属于 Task 4 集成和交付。Chrome DOM 语义、键盘交互、reduced-motion、桌面与 375px 布局已有实际验证。独立规格/质量审查由主代理安排，本报告不将未执行的独立审查标为通过。

## 独立审查修复轮 1/5：R1 同账号资料版本不回退

已完整阅读 `docs/personal-center-task3-review.md`，按 receiving-code-review / systematic-debugging / TDD 核对唯一 Important R1。审查报告未修改。根因是原身份/存活检查无法区分同账号的旧 revision：初始化 profile GET 读出 N，dashboard 先建立表单后 PUT 已确认 N+1，旧 GET 仍被接收，并触发 clean 表单回退。

先在既有正式脚本新增 `--profile-ordering`，通过真实 Cookie、HTTP、PUT 与延期的真实 GET 响应复现，不用临时 stdin 浏览器程序。红灯命令：

```sh
python3 test-artifacts/personal_center_regression.py http://127.0.0.1:18083 test-artifacts/personal-center/_work/task3.sqlite --profile-ordering
```

修复前退出码 1：

```text
Locator expected to have Value '已经确认的新资料'
Actual value: ""
```

即服务器 PUT 已成功 N+1，旧初始化 GET 释放后，页面昵称退回 N 的空值，与审查链一致。

本轮仅修改：

- `web/src/auth.js`：`applyProfile` 在同 owner 下选择已确认最高 revision 的完整资料；较旧同账号响应视为正常忽略，返回成功受理，避免误报账号变化。nickname 同步最高版本；`setUser` 不用无 revision 的 `/me` 昵称覆盖确认资料；`loadProfile` 返回当前确认对象，保留原 generation/身份隔离。
- `web/src/views/Account.vue`：初始化 dashboard、profile GET 和保存通知统一经 `saved` 接收；页面与 dashboard.profile 引用当前确认对象，旧版本不能覆盖。
- `web/src/components/account/ProfileForm.vue`：重载/保存 ACK 经同一接收规则选用最高确认资料；watch/reset 拒绝低 revision；接收仍检查存活、用户 ID、用户名。若成功 ACK 比已确认版本旧，保留最新表单并明确显示“本次保存已完成…已同步较新资料”，不把旧 ACK 的字段作为最新保存状态。
- `test-artifacts/personal_center_regression.py`：新增正式定向模式和四个交错场景；保留原 17 组和旧断言。

原 CAS、dirty/400/409、跨账号清理、保存禁用、Cookie 与后端/schema 均未重写。本轮未修改 Go 源码，也未重复主代理已通过的 Go 全量/race/vet；仅重新编译嵌入前端的临时 CLI。

绿灯使用最新产物：`npm run build` 退出码 0（60 modules，1.79s）；`env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go build -o test-artifacts/personal-center/_work/gk-task3 ./cmd/gk` 退出码 0。主代理停止其工具会话中的旧服务后，本代理重新启动最新 binary，服务会话为 `3833`，仍使用既有隔离 task3.sqlite/合成 key/18083。

同一 `--profile-ordering` 命令最终退出码 0，4 组，`console_errors: []`：

1. 旧初始化 profile GET 在成功 PUT 后到达，表单、顶部昵称/头像、偏好、版本保持 N+1，下一次 PUT 成功。
2. 旧 dashboard 内嵌资料迟到，最新资料与双目标保持，下一次 PUT 成功。
3. 路由 `auth.loadProfile` 读出 N 后，同页成功保存 N+1，再释放路由 GET；题库默认值和顶部使用 N+1，回到表单继续保存成功。
4. 本页 PUT 的真实成功 ACK 被延期，另一个真实 PUT 的较新版本先由初始化 GET 确认；等待中的本页输入不被覆盖，旧 ACK 到达后保留最新资料并说明同步版本，下一次 PUT 成功。

定向结果：[task3-fix1-browser-results.json](../test-artifacts/personal-center/task3-fix1-browser-results.json)。

原回归最终命令均退出码 0：

```sh
python3 test-artifacts/personal_center_regression.py http://127.0.0.1:18083 test-artifacts/personal-center/_work/task3.sqlite
python3 test-artifacts/round2_fixes_ui.py http://127.0.0.1:18083 test-artifacts/personal-center/_work/task3.sqlite
git diff --check
python3 -m py_compile test-artifacts/personal_center_regression.py
```

原个人中心 17 组（包括跨账号迟到 GET/PUT、读取失败切换账号、CAS/dirty/退出失败保护）及原草稿/密码 6 组均通过，两个结果文件的 `console_errors` 都为空。本轮没有权限等待或 Chrome 运行阻塞；父代理旧服务 session 无法由子工具控制时即时报告，由父代理正常停止后继续。

本轮对照 `_work/task3-fix1-before` 快照生成 [task3-fix1-review.diff](../test-artifacts/personal-center/task3-fix1-review.diff)，涵盖上述 4 个文件及本报告追加部分。没有改独立审查报告、提交、推送、部署或操作正式 DB/原密钥。后续定向复审由主代理执行。
