# 个人中心 Task 1 实现与验证记录

日期：2026-10-04。范围：v12 迁移、资料/偏好/目标、普通用户登录元数据与活跃会话管理。保留此前未提交修复，未提交、推送、部署，未操作真实 `var/` 数据库或密钥；测试全部使用 `t.TempDir()` 临时库、临时合成密钥和合成账号。未安装依赖。

## 实现与接口

修改文件：

- `internal/store/schema.go`：只追加 v12 与 ReadOnly 事务说明，保留 v1–v11。
- `internal/study/auth.go`：保留 `Service.DB`，新增可选 `Clock func() time.Time`；注册与 profile 原子创建；兼容 Login 包装器与有元数据的登录；保留密码 hash 校验和插入时 hash CAS、会话摘要存储和 Cookie 合同。
- 新增 `internal/study/profile.go`、`internal/study/sessions.go`：资料校验/CAS与会话查询/撤销。
- `internal/serve/study.go`：requireUser 中调用 accountRoutes，映射 ErrProfileConflict→409，登录使用已有 clientIP 可信代理算法。
- 新增 `internal/serve/account.go`：资料与会话 HTTP 路由；留 dashboard/export/review 的同鉴权边界扩展入口。
- 新增测试 `internal/serve/personal_profile_test.go`、`internal/store/personal_migration_test.go`、`internal/study/personal_auth_test.go`。
- 新增本报告；未修改计划、其他模块或现有测试文件。

Service 接口（所有 user 参数都是 Cookie 鉴权得到的 `int64` 用户 ID）：

```go
Profile(ctx context.Context, user int64) (Profile, error)
UpdateProfile(ctx context.Context, user int64, in ProfileUpdate) (Profile, error)
Login(ctx context.Context, username, password string) (string, User, error)
LoginWithMetadata(ctx context.Context, username, password string, metadata LoginMetadata) (string, User, error)
Sessions(ctx context.Context, user int64, token string, page int) (SessionPage, error)
RevokeSession(ctx context.Context, user int64, token, id string) error
RevokeOthers(ctx context.Context, user int64, token string) (int64, error)
```

DTO：

- `Profile` 完整 JSON：username,nickname,bio,avatar_id,created_at,updated_at,daily_questions,daily_minutes,exam_name,exam_date,default_limit,default_module,reading_size,revision。头像 `int` 0–7，revision `int64`，日期/时间为字符串。
- `ProfileUpdate`：nickname,bio,avatar_id,daily_questions,daily_minutes,exam_name,exam_date,default_limit,default_module,reading_size,revision。HTTP PUT 要求每个字段存在且不为 null；拒绝额外字段、只读身份字段及大小写别名。未知字段不会影响本人身份。
- `LoginMetadata`：DeviceLabel、IP，只供服务内部使用。HTTP 只传受控浏览器/系统类别和可信来源地址，服务再次约束设备类别并在存储之前脱敏。
- `SessionPage`：items,total,page,size（size 固定 20）。`LoginSession`：public_id,current,device_label,ip_hint,created_at,expires_at；device_label/ip_hint/created_at 是 `*string`，历史信息为空时 JSON null，前端显示“历史会话/信息未知”。绝不返回 token/hash。

路由：GET/PUT `/api/account/profile`，GET `/api/account/sessions?page=1`，DELETE `/api/account/sessions/{public_id}`，POST `/api/account/sessions/revoke-others`。全部在 requireUser 中，复用全局 Origin 校验。PUT 与 POST 要求 application/json；撤销动作允许空 body 或 `{}`，无 body DELETE 仍校验 Origin。DELETE 当前会话 400、他人或不存在的 ID 404。revoke-others 返回 `{revoked:n}` 并保留当前 Cookie。

## 设计决定

1. `user_profile` 不复制昵称，昵称始终以 `app_user.nickname` 为单一来源。回填旧用户默认资料，新用户注册事务中创建 profile。缺失 profile 的兼容 GET 用 LEFT JOIN/COALESCE 返回默认值，不隐式写入；首次显式 PUT 才创建缺行。
2. 资料先读取本人原值与 revision，再在同一 `store.WriteTx` 事务内校验原考试日期、CAS 更新 profile、更新昵称、读取结果。旧 revision 返回独立 ErrProfileConflict/409，不覆盖任何资料字段。
3. 用户确认的到期日期规则：新设置/修改考试日期必须是北京时间未来日期且最多五年后；本人已存日期已到/已过时，可以原样保留并修改其他字段，也可以清空。例外只使用同事务中本人原值，不借用他人日期或绕开 CAS。GET 不自动删除过期日期。
4. v12 仅向 app_session 追加 public_id 与 nullable 元数据；旧行通过随机 16 字节公开 ID 回填，不改变 token 摘要、过期时间或草稿。新公开 ID 使用 crypto/rand，独立于登录凭据；建立唯一索引和用户/过期索引。
5. IPv4 保留前三段（示例 `203.0.113.*`）；IPv6 仅保留 /48 前缀（示例 `2001:db8:abcd::/48`）；IPv4 mapped IPv6 先 Unmap。无效地址保存 null，不保存原始地址或 UA，不做外部地理查询。
6. 会话列表只读一致性事务明确 `ReadOnly:true`，现代 SQLite 驱动将其以普通 BEGIN 开始；写事务使用 WriteTx 的 IMMEDIATE/重试/取消语义。列表固定 20 条，非法页号拒绝、空列表返回 []。
7. 保留原登录密码比较与 session INSERT SELECT 的 `password_hash=?` CAS。新增元数据与 last_login_at 在同一写事务；登录失败或写失败不会返回 token。改密旧逻辑与原 Cookie 的 HttpOnly/Secure/SameSite 保持兼容。

## 红灯证据

首轮先写真实 HTTP 与 v11 结构迁移测试再实现。命令：

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve ./internal/store -run 'TestPersonal' -count=1
```

退出码 1，预期缺功能失败：

```text
FAIL TestPersonalProfileDefaultsUpdateCAS: default profile: 404 {"error":"接口不存在"}
FAIL TestPersonalProfileRejectsInvalidAndUnauthorized: avatar_id=8: 404
FAIL TestPersonalSessionsIsolationAndRevocation: sessions: 404
FAIL TestPersonalLoginMetadata: 404
FAIL TestPersonalMigrationFromV11PreservesSecretsAndDraft:
  session migration SQL logic error: no such column: public_id (1)
```

随后补充边界测试并确认真实失败，再修复对应校验：

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve -run TestPersonalProfileRequiresComplete -count=1
```

退出码 1：遗漏 avatar_id 原先返回 200（默认 0）；补 null/缺失检查后，大小写额外字段 `Avatar_ID` 仍原先返回 200；最终完整白名单修复后二者都返回 400。

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve -run TestPersonalSessionWriteRequestSecurity -count=1
```

退出码 1：POST revoke-others 的 text/plain 原先返回 200；修复后 415。跨站 POST/无 body DELETE 为 403。

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve -run TestPersonalProfilePreservesOwnExpiredExamDate -count=1
```

退出码 1：冻结 Clock 到本人原考试日期当天，修改昵称被 400“考试日期需为未来日期”阻塞；绑定原值与 CAS 的同事务例外修复后通过。

## 绿灯与完整验证

最终资料/会话测试总计 15 个，使用真实 SQLite、路由、Cookie 和两个账号；补充测试保护既有登录 hash CAS、枚举/Unicode/date 边界、资料及注册事务失败回滚、真实并发 HTTP CAS、分页/到期过滤/历史元数据，以及可信代理 IPv6 脱敏。

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/serve ./internal/store ./internal/study -run TestPersonal -count=1
```

退出码 0：

```text
ok ai_analyze_guokao/internal/serve 2.222s
ok ai_analyze_guokao/internal/store 0.020s
ok ai_analyze_guokao/internal/study 0.430s
```

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./... -count=1
```

退出码 0，全部 Go 包通过，无未报告失败或不相关未完成代码造成的阻塞：

```text
ok ai_analyze_guokao/cmd/gk 0.303s
ok ai_analyze_guokao/internal/admin 0.345s
ok ai_analyze_guokao/internal/distill 3.239s
ok ai_analyze_guokao/internal/ingest 10.484s
ok ai_analyze_guokao/internal/llm 0.003s
ok ai_analyze_guokao/internal/media 0.351s
?  ai_analyze_guokao/internal/model [no test files]
ok ai_analyze_guokao/internal/serve 7.115s
ok ai_analyze_guokao/internal/setting 0.048s
ok ai_analyze_guokao/internal/store 0.776s
ok ai_analyze_guokao/internal/study 0.432s
```

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test -race ./internal/serve ./internal/study ./internal/store -run TestPersonal -count=1
```

退出码 0，相关包 race 通过：

```text
ok ai_analyze_guokao/internal/serve 33.129s
ok ai_analyze_guokao/internal/study 7.447s
ok ai_analyze_guokao/internal/store 1.402s
```

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go vet ./internal/serve ./internal/study ./internal/store
git diff --check -- internal/store/schema.go internal/study/auth.go internal/serve/study.go
```

均退出码 0，无输出。新增 Go 文件经过 gofmt。

最后仅调整登录 CAS 测试的等待方式，避免把短暂的初始 SELECT 连接占用误认为写锁等待。针对该测试验证稳定性：

```sh
env GOCACHE=/tmp/gk-fixes-cache GOMAXPROCS=2 go test ./internal/study -run TestPersonalLoginPreservesHashCAS -count=5
```

退出码 0：`ok ai_analyze_guokao/internal/study 1.311s`。该调整只涉及测试同步，不修改生产代码。

## 自审与风险

- 根据测试覆盖做变异检查：去掉 user_id 过滤会打破双账号隔离；去掉 profile revision 条件会打破真实并发 CAS；昵称先提交会打破事务失败回滚；返回 token/hash 会打破秘密检测；去掉 trusted proxy 判断或脱敏会打破存储与响应断言；删除当前/所有会话会打破两个 Cookie 的鉴权验证；造旧元数据会打破历史 null 断言。
- v11 升级测试确认旧密码、昵称、token 摘要、草稿文本及 draft_revision 不变，并确认默认资料和随机 public_id。这里只验证临时结构与合成数据；真实数据库完整备份/恢复、副本大库迁移、发布包和浏览器集成属于后续 Task 4，尚未以生产数据验证。
- profile 校验覆盖 HTTP 字段白名单、缺失/null、错误枚举、超限 Unicode、日期上界与北京时间午夜；管理员 Cookie 不可成为普通用户身份，跨站写入与无 Origin DELETE 拒绝。
- 会话设备类别来自 UA，作为显示提示，不是设备身份验证。旧会话未知资料返回 null，前端必须提供明确未知文案。公开 ID 随机碰撞概率极低，唯一索引会阻止覆盖。
- 登录 hash CAS 测试使用真实 SQLite 写锁暂停插入，变更密码 hash 后确认旧密码不能产生 session；无专用生产测试 hook。测试等待写连接稳定占用，并进行五次定向稳定性验证。
- 上层 Task 2/3 需要使用上述 DTO 与账号路由；本任务没有实现看板、导出、复习推荐或页面。独立规格/质量审查由主代理安排，本报告不把未执行的独立审查标为通过。
