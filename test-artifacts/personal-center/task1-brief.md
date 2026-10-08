# 完整个人中心实现计划

> 按 subagent-driven-development 顺序实现并逐项审查；用户已要求开始代码开发，生成计划后直接执行。保留现有工作区修复，不提交、推送或部署。

**目标：** 交付真实数据驱动的个人学习工作台。
**技术栈：** Go/Chi/SQLite，Vue 3/SVG/CSS；不引入图表或存储依赖。
**规格：** [个人中心设计](../specs/2026-10-04-personal-center-design.md)。

## 全局约束与接口

- 追加 v12，保留 v1–v11，所有测试写临时库；禁止操作正式 DB/密钥。
- 后端只根据 Cookie 中已验证身份访问个人数据，保持 Origin、草稿 revision、交卷幂等。
- 统计北京时间；分母 0 返回 null；模块/考点少于 5 次显示不足；默认 30 天，可选 7/30/90。
- 8 个预置头像编号 0–7；字号 16/18/20；题量 10/20/50；默认头像 0、字号16、题量20、作答目标20、分钟目标30。
- `Profile` JSON 字段：username,nickname,bio,avatar_id,created_at,updated_at,daily_questions,daily_minutes,exam_name,exam_date,default_limit,default_module,reading_size,revision。整数头像编号，int64 revision。
- `ProfileUpdate` 只有可编辑字段与 revision。保存 POST/PATCH 不新增：使用 PUT `/api/account/profile`。
- `Service.Clock func() time.Time` 为可选测试时钟，`clockNow()` 内部默认真实时间；不改变已有调用的 DB 字段。
- 所有源码/测试随工作区交付；流程审查使用每任务修改文件快照 diff，避免把既有未提交修复算作本轮新增。

## Review Focus

1. 两账号隔离：profile/统计/CSV/会话/复习候选都必须按用户过滤。
2. 午夜/空答案/重复提交：无错日、重复计时和 NaN；固定 Clock 的统计回归覆盖。
3. profile 旧版本和离开页面：保留本地输入，禁止静默覆盖；HTTP CAS + 浏览器延迟保存覆盖。
4. 会话撤销：不得泄漏 token/hash、误撤销当前/他人、伪造 XFF 地址；有真实两 Cookie 验证。
5. CSV/稀疏标签：无公式注入或静默截断、未解锁答案/草稿泄漏；推荐明确样本限制。

## Task 1：迁移、资料和会话安全

**文件：** 修改 `internal/store/schema.go`、`internal/study/auth.go`、`internal/serve/study.go`；新增 `internal/study/profile.go`、`internal/study/sessions.go`、`internal/serve/account.go` 与本任务测试。

- [ ] 写失败的 HTTP 测试：profile 路由必须返回默认资料；有效更新持久化并 CAS；非法参数/他人/未登录/跨站拒绝；profile 元数据和原会话秘密保留。
- [ ] 运行定向测试确认当前路由404与预期失败；记录红灯输出。
- [ ] v12 加 profile 表/回填/注册原子创建，app_session 公开ID与可空元数据、用户索引、统计时间索引。昵称保留 app_user 单一来源。
- [ ] 实现 `Profile(ctx,user) (Profile,error)`、`UpdateProfile(ctx,user,ProfileUpdate) (Profile,error)`，版本冲突用独立 `ErrProfileConflict` →409，写事务复用 store.WriteTx。
- [ ] 保留 `Login(ctx,name,password)`，内部转 `LoginWithMetadata(ctx,name,password,LoginMetadata)`；新元数据创建不能丢失现有密码CAS。设备标签由 HTTP 解析受控浏览器/系统类别；IP 在存储前脱敏。
- [ ] `Sessions(ctx,user,token,page)` → `{items,total,page,size:20}`；每项 public_id/current/device_label/ip_hint/created_at/expires_at。`RevokeSession(ctx,user,token,id)` 拒绝当前/他人；`RevokeOthers` 保留当前。
- [ ] `accountRoutes(chi.Router)` 注册 requireUser 内 profile、sessions GET/DELETE、revoke-others POST；由 userRoutes 调用。保留后续任务扩展同文件的路由槽。
- [ ] 运行本任务定向与 Go 全量测试，写实现报告，独立审查任务。

