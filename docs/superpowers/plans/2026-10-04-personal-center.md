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

- [x] 写失败的 HTTP 测试：profile 路由必须返回默认资料；有效更新持久化并 CAS；非法参数/他人/未登录/跨站拒绝；profile 元数据和原会话秘密保留。
- [x] 运行定向测试确认当前路由404与预期失败；记录红灯输出。
- [x] v12 加 profile 表/回填/注册原子创建，app_session 公开ID与可空元数据、用户索引、统计时间索引。昵称保留 app_user 单一来源。
- [x] 实现 `Profile(ctx,user) (Profile,error)`、`UpdateProfile(ctx,user,ProfileUpdate) (Profile,error)`，版本冲突用独立 `ErrProfileConflict` →409，写事务复用 store.WriteTx。
- [x] 保留 `Login(ctx,name,password)`，内部转 `LoginWithMetadata(ctx,name,password,LoginMetadata)`；新元数据创建不能丢失现有密码CAS。设备标签由 HTTP 解析受控浏览器/系统类别；IP 在存储前脱敏。
- [x] `Sessions(ctx,user,token,page)` → `{items,total,page,size:20}`；每项 public_id/current/device_label/ip_hint/created_at/expires_at。`RevokeSession(ctx,user,token,id)` 拒绝当前/他人；`RevokeOthers` 保留当前。
- [x] `accountRoutes(chi.Router)` 注册 requireUser 内 profile、sessions GET/DELETE、revoke-others POST；由 userRoutes 调用。保留后续任务扩展同文件的路由槽。
- [x] 运行本任务定向与 Go 全量测试，写实现报告，独立审查任务。

## Task 2：学习分析、成就、复习与导出

**文件：** 新增 `internal/study/dashboard.go`、`internal/study/export.go`、`internal/study/review.go` 及测试；扩展 `internal/serve/account.go` 的看板/CSV/复习处理器。

- [x] 先写 HTTP 固定日期测试：today/period/lifetime 的正确率、计时不重计、7/30/90范围、午夜、空数据、标签覆盖、streak/徽章、续做与两用户隔离，当前dashboard404应失败。
- [x] 实现 `Dashboard(ctx,user,days)` 只读一致性事务，JSON 合同至少：timezone,start_date,end_date,days,profile,lifetime,period,previous,today,trend,modules,activity,streak,achievements,review。
- [x] 计数组统一 `answered,correct,accuracy,duration_ms,sessions`，accuracy 为0–100数值/null；每日点加 date；模块点加 module/sufficient；热力图固定90日；streak加current,longest,practiced_today。
- [x] `review` 含 unfinished（最多3）、weak_modules/weak_concepts（最多3，至少5次）、labeled_answered/label_coverage、wrong/favorites、recent wrong candidates；最新标签不使用陈旧缓存数。
- [x] 成就实时计算首答、100/1000次、7/30天、六模块各5次，含id/name/current/target/unlocked。
- [x] 优先错题创建最多20个可练有效题，按 wrong_count/last_wrong_at排序，返回原Session。无题明确400，不拷贝判分代码。
- [x] CSV `days=7/30/90/all`，最多5000行；超限400先于发送文件；有BOM、转义与公式注入防护，只导出本人已提交答案和题号/模块/耗时。
- [x] 运行固定 Clock 统计/安全/CSV/推荐与全量测试，测合成历史多请求，写报告并独立审查。

## Task 3：个人中心页面与可视化

**文件：** 替换 `web/src/views/Account.vue` 为页面组合；新增 `web/src/components/account/` 下 Overview/Review/ProfileForm/Security 和 charts 独立组件；修改 api/auth/router/App/QuestionList/Practice、增加独立 account CSS。仅修改本任务前端文件及前端验收脚本。

- [x] 先写真实浏览器验收脚本（临时服务），当前页面不存在总览和页签/图表，应失败；不得用假图表数据冒充功能。
- [x] API 客户端统一 Profile/Session/Dashboard 真实 DTO，导出用同源 Cookie、检查失败，不在URL放凭据；复用已有401处理。
- [x] `/account` 四本地页签：学习总览、复习助手、资料与偏好、账户安全；顶部/侧栏入口。保持原改密标签和统计重算功能可访问。
- [x] 四类SVG/CSS图形：双趋势、六模块画像、90日热力图、目标环；有键盘/鼠标具体值、文字摘要和数据表；0样本为空而非0分。
- [x] 资料/目标/偏好表单、8头像、日期、字段错误、保存版本、409输入保留与确认重载；离开/切页签保护；保存中防重复。
- [x] 成就、倒计时、续做、优先错题、薄弱模块/考点、收藏与记录入口接入有效练习；无数据/不足样本有说明。
- [x] 安全页会话分页/撤销/退出其他确认，当前会话保留；改密原逻辑与重算统计保持兼容。
- [x] 保存后同步顶部昵称/头像；偏好缓存按用户ID隔离，退出清理；显式题库URL筛选优先，练习字号按保存值应用。
- [x] 375px/桌面/键盘/无障碍/空状态/失败重试/范围切换/多标签冲突/用户切换回归与生产构建；写报告，独立审查。

## Task 4：集成验收与交付

**文件：** 新增 `test-artifacts/personal_center_regression.py` 和个人中心证据目录、`docs/personal-center.md`；更新 API/部署/README/Makefile 发布文档，完成实现进度文件。

- [x] 核对规格逐条覆盖，追加跨任务缺失测试；既有24项浏览器回归须通过。
- [x] 运行 Go 全量、核心包race/vet、前端构建、Linux与Windows/macOS编译；v11真实结构副本v12升级与完整备份恢复验收。
- [x] 仅在临时DB/合成密钥环境启动浏览器验收服务，记录截图/JSON/命令与结果，结束关闭本任务服务。
- [x] 最终独立宽范围代码审查，修复有效严重发现并复测相关用例。
- [x] 更新源码/文档与本地Linux发布包，校验内容不含数据库、密钥、备份；记录SHA与未测生产边界。不自动提交或远程部署。

## 进度账本

详见 `docs/personal-center-progress.md`；任务完成只在测试与审查证据齐备后标记。
