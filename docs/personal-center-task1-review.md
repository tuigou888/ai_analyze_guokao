# 个人中心 Task 1 独立审查

日期：2026-10-04。审查范围：迁移、资料/偏好/目标、普通用户会话元数据及管理。

**规格合规结论：通过。代码质量结论：通过。未发现本任务新增的可复现缺陷或阻断项，可以继续 Task 2。**

## 范围与方法

以 `test-artifacts/personal-center/task1-review.diff` 的任务前后快照为变更边界，逐项对照 `docs/superpowers/plans/2026-10-04-personal-center.md` 的全局约束、Review Focus 和 Task 1，以及 `docs/superpowers/specs/2026-10-04-personal-center-design.md` 第 4、7、8、9 节中属于本任务的合同。阅读实现报告、相关实现与测试，并查看既有鉴权、Origin、clientIP、JSON 解码、迁移与 WriteTx 代码以确认接入方式。

不将此前未提交修复计为本次新增；不要求 Task 1 提前交付 Task 2/3 的统计、导出、复习和前端功能。审查没有修改生产代码、测试代码、真实数据库或密钥，没有分派其他代理。

## 规格核对

| 关注点 | 核对结果与依据 |
| --- | --- |
| v11 追加迁移、数据保留 | `internal/store/schema.go:373` 仅追加 v12；回填默认 profile、随机 public_id 和所需索引，保留原 token、expires_at、密码与草稿列。旧会话的三个元数据列保持 NULL。迁移仍在原事务内执行。v11 临时库迁移回归通过。 |
| 注册与默认资料 | `internal/study/auth.go:64` 将用户/profile 创建放入同一 WriteTx；`internal/study/profile.go:58` 用本人 ID 和 LEFT JOIN/COALESCE 读取，无隐式 GET 写入。注册失败回滚、缺行默认读取及首次保存已有测试。 |
| Cookie 身份与两账号隔离 | `internal/serve/study.go:74` 在 requireUser 内注册账号路由；处理器只使用 currentUser ID。profile 不接收身份字段；会话 SELECT/DELETE 均含 user_id 条件。匿名/管理员 Cookie、客户端 user_id 和两账号隔离回归通过。 |
| 资料 CAS 与昵称原子性 | `internal/study/profile.go:119` 同事务读取原值、比较 revision、执行带 revision 条件的 UPDATE、修改 app_user.nickname 并读取结果；不存在第二份昵称。冲突映射 409，失败回滚；真实并发测试确认一份 200、一份 409。 |
| 输入及考试日期 | `internal/serve/account.go:50` 检查完整可编辑字段、缺失/null/额外字段及大小写别名；业务层检查 Unicode 长度、数值枚举、实际模块和日期。`internal/study/profile.go:91` 使用北京时间；已到/已过日期只能保留同事务读取的本人原值，仍必须通过 revision，改成其他过去日期会拒绝；可清空，新日期限未来至五年。 |
| 登录 CAS 与敏感信息 | `internal/study/auth.go:125` 保留 INSERT SELECT 的 password_hash 条件及 RowsAffected 检查；失败不返回新凭据，last_login_at 同事务提交。会话 DTO 只返回公开 ID 和显示元数据，token 比较只返回布尔值，不返回摘要。公开 ID 独立于登录凭据。 |
| 来源与设备元数据 | HTTP 登录复用既有 clientIP 可信代理规则；`internal/study/sessions.go:42` 再限制设备类别，IP 存储前脱敏；IPv4 mapped IPv6 先 Unmap，IPv6 留 /48；不存原始 UA/IP。伪造 XFF、可信多跳代理、IPv6 及受控标签测试通过。 |
| 会话分页与撤销 | `internal/study/sessions.go:78` 使用 ReadOnly 一致性事务，固定每页 20、过滤已到期行、空集合为 []、历史元数据为 null。`internal/study/sessions.go:108` 拒绝当前会话、他人或不存在 ID 返回 404；`internal/study/sessions.go:126` 在事务内再次验证当前会话并排除其摘要，保留当前 Cookie。两 Cookie 实际鉴权回归通过。 |
| 写请求与兼容 | 沿用全局 Origin，DELETE 无 body 仍检查；PUT 复用 JSON 解码器，revoke-others 检查 application/json。原 Login 包装器、改密、Cookie、草稿和交卷协议保留；独立 ErrProfileConflict 不改变既有错误映射。 |

## 代码质量

HTTP 与业务/SQL 职责清楚，新接口复用现有认证和事务机制，没有增加依赖。资料事务涵盖昵称及 profile，会话读事务检查 rows.Err 并关闭 rows；写失败不会返回有效新 token。测试使用真实 SQLite、HTTP 路由及 Cookie，覆盖用户隔离、并发 CAS、触发器失败回滚、密码 hash CAS、时间边界和迁移，不只断言实现内部细节。

本轮没有发现需要按 Critical/Important/Minor 列出的实际新增缺陷。

## 验证证据与限制

审查者独立执行以下命令，退出码 0；15 个 TestPersonal 测试覆盖三个包：

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve ./internal/store ./internal/study -run TestPersonal -count=1
```

```text
ok  ai_analyze_guokao/internal/serve  2.219s
ok  ai_analyze_guokao/internal/store  0.018s
ok  ai_analyze_guokao/internal/study  0.427s
```

实现者的 `docs/personal-center-task1-report.md` 另记录红灯、Go 全量、相关 race、vet 和登录 CAS 重复测试证据。本次审查没有重复宣称独立执行了这些完整检查。

真实库完整备份恢复、副本大库迁移、浏览器集成及发布包验证仍按计划属于 Task 4；本结论只覆盖 Task 1，不表示整个个人中心或生产发布已验收。
