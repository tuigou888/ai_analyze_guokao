# 个人中心 Task4 集成与本地交付记录

日期：2026-10-05。状态：实现验收及本地交付，待独立终审；Task1/2/3 独立审查及 Task3 R1 定向复审已通过，最终宽范围审查由主代理另行记录。未提交、推送或部署，未操作正式数据库、实际密钥或远程服务。本任务只修改验收测试、文档和 Makefile，不修改生产业务或前端。

## 恢复验收与覆盖

新增 `cmd/gk/backup_restore_test.go::TestPersonalBackupBundleRestoresProfileSessionAndCAS`，全部 fixture 在 `t.TempDir()`：创建合成普通用户、加密设置、模块题目及已提交练习；连续保存非默认头像/资料/目标/考试名称与日期/题量/模块/字号到 revision 2，创建带脱敏设备元数据的会话。通过真实 `cmdBackup --bundle` 和 `--verify-bundle` 生成/验证完整快照与有效密钥，再从 bundle 复制数据库到新恢复目录并打开。

恢复后验证有效密钥能解密原设置、公开会话 ID/当前标识/设备/脱敏地址/登录到期信息原样保留、已提交练习/正确数/答案保留。通过恢复服务的实际 HTTP 路由和原始 `gk_user` Cookie GET `/auth/me` 与 `/account/profile` 验证身份及所有 Profile 字段与 revision 2；PUT 原版本成功得到 revision 3，再次提交旧版本为 409。恢复后的写入不会改变原 bundle，最后再次校验成功。请求经过真实 Cookie/Origin/JSON/requireUser 边界，使用 httptest Recorder，不需要监听公网或本地端口。此测试验收已有能力，未人为制造功能红灯、未改备份实现。

原 `internal/store/personal_migration_test.go::TestPersonalMigrationFromV11PreservesSecretsAndDraft` 使用真实前十一条迁移创建 v11 临时库，含旧用户/会话/草稿，随后运行 v12，验证旧密码摘要/token/昵称/草稿与 draft_revision 保留、profile 默认回填、历史会话新公开 ID 和 NULL 元数据。本轮定向重跑通过；这不是直接修改 schema_version 来模拟旧结构，也不是正式大库升级。

完整逐条覆盖矩阵见 [task4-coverage.md](../test-artifacts/personal-center/task4-coverage.md)，区分运行测试、源码审查和正式环境未测项。Task1/2 事务、时间、CSV/隔离和稀疏标签边界均在新增恢复测试后的 Go 全量中再次运行。

## 实际验证

命令/退出码及输出摘要保存在 [task4-results.json](../test-artifacts/personal-center/task4-results.json)，根代理预检和本轮浏览器命令在 [task4-root-precheck.json](../test-artifacts/personal-center/task4-root-precheck.json)。

| 检查 | 命令/证据 | 结果 |
|---|---|---|
| v11迁移+完整bundle恢复 | `env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./cmd/gk ./internal/store -run 'TestPersonal(BackupBundle\|Migration)' -count=1 -v` | exit 0，两项通过 |
| 新测试后Go全量 | `env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./... -count=1` | exit 0，全部包通过（serve 10.188s） |
| 核心race | 根代理 `go test -race ./internal/serve ./internal/study ./internal/store ./internal/setting` | exit 0，无竞态报告；本任务未改生产Go，沿用同版证据，不重复150秒检查 |
| 新增恢复相关race | `env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test -race ./cmd/gk -run 'Test(PersonalBackupBundle\|Backup)' -count=1` | exit 0，8.806s |
| 静态检查 | `env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go vet ./...` | exit 0，无输出 |
| 前端 | `cd web` 后 `npm run build` | exit 0，60 modules，1.60s；无新依赖安装 |
| Linux/Windows/macOS amd64 | `CGO_ENABLED=0 GOOS=linux/windows/darwin GOARCH=amd64 go build -buildvcs=false -trimpath` | 三平台均exit 0，ELF/PE32+/Mach-O类型正确；Windows/macOS仅交叉编译 |
| 原18+原6 | 根代理纯获批 `python3 test-artifacts/ui_regression.py` 与 `round2_fixes_ui.py`，参数为18083及隔离库 | exit 0，24组；原断言未放宽 |
| 个人中心17+资料乱序4 | 根代理 `python3 test-artifacts/personal_center_regression.py`，原模式与 `--profile-ordering` | exit 0，21组 |

四份本轮 UI 结果：[旧18](../test-artifacts/personal-center/task4-old18-results.json)、[旧6](../test-artifacts/personal-center/task4-old6-results.json)、[个人17](../test-artifacts/personal-center/task4-personal17-results.json)、[资料乱序4](../test-artifacts/personal-center/task4-ordering4-results.json)。总计45组。字段 `console_errors: []` 实际只监听 pageerror，表示未捕获页面异常为空；没有采集全部 console.error 或网络日志。截图由真实浏览器更新，包含桌面及375px四页签、有数据/空数据、长考试名称；根代理实际查看有数据桌面与移动总览。

## 临时环境与执行边界

本任务自行启动最新 `gk-task3`，使用 `_work/task3.sqlite` 和 `_work/task3-synthetic.key`、只读 data，监听 `127.0.0.1:18083`，工具会话 42574。管理员 `webtester/ui-test-password` 仅在隔离库创建，旧18按原测试改密至 `ui-test-password-next`。全部 UI 验收后本任务发送 Ctrl-C，显示“正在关闭”，退出 0；关闭前两条 context canceled 为浏览器取消请求日志。没有操作其他进程或正式服务。

首次带重定向的旧18在沙箱内因 Chrome setsockopt 权限失败退出 1；升级重跑审批被中止，未得到测试结果，不计作产品失败或通过。根代理改用纯获批脚本前缀重跑并得到45组有效结果。本任务随后只在正常沙箱完成 Go/构建/文档操作，没有权限等待。原根服务 session 11444、Task3旧3833均已失效，不作为本轮启动证据。

## 本地包与文档

新增 [个人中心说明](personal-center.md)，更新 [API](api-design.md) 的八个新合同/CSV例外、[README](../README.md)入口、[部署手册](deployment-2c4g.md)的v12升级/回滚与未测边界、Makefile 发布文档列表。保留历史独立审查原始记录，不以本报告替代终审。

本地 Linux 发布包为 `var/gk-linux-amd64.tar.gz`。前端已单独成功构建，随后 `env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 make -o web release` 复用该产物执行原release目标，不重复安装依赖。包校验18个文件，仅含嵌入前端的 Linux gk、deploy、taxonomy、README、修复记录和发布文档，无数据库/密钥/备份数据/测试fixture，包内文档逐字节与工作区相同；可重复执行的只读审计见 `test-artifacts/personal-center/task4-package-audit.py`。实际成员、每文件SHA、包SHA与检查退出码单独保存在 [task4-package-manifest.json](../test-artifacts/personal-center/task4-package-manifest.json) 和 results JSON。包内报告不包含自身包SHA，避免自引用；最终审查状态另记账本，不需要因此重打包。

Task4 diff 从分派开始刷新后的27文件快照生成，见 [task4-review.diff](../test-artifacts/personal-center/task4-review.diff)；只包含本任务测试/文档/Makefile变化，不把之前未提交修复或Task3 R1修改算入。完整功能宽范围审查包由根代理生成并分派，账本只标“待终审”。

未测正式环境边界：正式数据库/实际密钥升级，线上HTTPS/可信代理配置/压力/长时间运行，异机完整恢复，Windows/macOS原生运行、其他浏览器和真实屏幕阅读器。临时结构迁移、合成历史并发与bundle恢复不等于生产大库/线上性能验收；交叉编译不等于目标系统运行。本次无远程发布。

## 2026-10-06 最终修复集成交付附录

本节是原Task4之后的R1修复交付，原文和task4-results/package-manifest仅代表旧版本，不包含本次修复。最终宽范围审查唯一Important R1是共享Cookie换账号后旧页可写新账号资料；修复与相关身份回调/迟到退出的Ruling、真实红绿、最终新证据见 [最终修复记录](personal-center-final-fix-report.md)。原始最终审查报告保留，完成修复后仍待唯一独立定向复审。

本轮保持ProfileUpdate白名单和Cookie鉴权，引入可选页面预期身份拒绝前提，统一保护本人读取/副作用；旧页面owner固定、跨标签提示保留输入、迟到回调不影响新owner。现代logout撤销会话且无删除Cookie响应，legacy仍删除Cookie。无需新迁移或依赖。

最终新7组定向UI通过（双标签静默/通知2、迟到409/改密/撤销/退出4、只退出通知1），Go全量、最终相关race/vet/前端构建及Linux/Windows/macOS amd64编译均通过。根稳定主修复版原45组均通过；此后仅已知stale早期拒绝/401保留分支调整，以最终7+ordering4验证，普通45不重复、不冒称在狭窄分支调整后全量重跑。新包和独立final-fix-*结果对应最后代码；新发布包补入本最终修复报告，历史旧包在 `_work/pre-final-fix-release/`保留。新包审计与SHA不写进包内报告以避免自引用，见 `final-fix-package-manifest.json`及`final-fix-sealed-manifest.json`。

本地交付不包含正式库/密钥/备份/fixture；目标环境和未测生产边界沿用上节。没有提交、推送或生产部署。
