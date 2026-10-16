# 权限分级体系设计规格

日期：2026-10-16。状态：用户已批准完整分级方案及全部设计章节，按本文进入实现计划编写。

## 1. 目标与已确认范围

为系统的双轨账号体系引入角色与状态分级：

- **管理员分两种角色**：`super`（超级管理员）与 `operator`（运营管理员）。角色固定，不做可自定义的权限点矩阵。
- **用户分角色与状态**：`role` = `free`/`vip`（会员），`status` = `active`/`banned`（封禁）。
- **会员权益 = 限量版**：免费用户每天最多创建 50 道题、3 套试卷的练习会话（管理端可调）；会员无限制。其余内容（题目、解析、考点卡）全部用户可见。
- **会员开通 = 管理端手动**：管理员在用户管理页设置/取消，支持到期时间，不接支付、无兑换码。
- **新增 Web 管理端用户管理**：用户列表/筛选、封禁/解封、设/取消会员、重置用户密码。
- **新增管理端管理员账号管理（超管专属）**：列出/创建/改口令/删除管理员。
- **审计**：用户管理与管理员管理操作以追加写方式记录到 `admin_audit`。

成功标准：

1. 运营管理员访问超管接口一律 403；用户凭据访问任何管理接口一律 401；管理员凭据访问用户业务接口一律 401。
2. 封禁立即生效：登录拒绝、既有会话删除、接口访问 401。
3. 免费额度后端硬限制生效，会员与到期降级判定正确，并发创建会话不超限。
4. 旧库向后迁移：现有管理员自动成为 super，现有用户自动成为 free/active，行为不变。

明确排除（本期不做）：

- 注册开关（开放/关闭注册）——如需另开任务。
- 解析分层、题库分层等其他会员权益形态。
- 兑换码、在线支付、对照解封、限时冻结。
- 权限点矩阵与自定义角色组。
- 管理员用户名修改；用户自助注销。

## 2. 现有实现与约束

- 后端 Go + Chi + modernc SQLite；迁移只追加，当前最新 v13（`internal/store/schema.go:392`）。`migrations` 每版单事务应用（`schema.go:432`）。
- 双轨账号体系完全分离：
  - 管理员：`admin_user` + `admin_session`（v5，`schema.go:202-215`），CLI `gk admin create/passwd`（`cmd/gk/serve.go:113`），包 `internal/admin`。
  - 用户：`app_user` + `app_session`（v7/v12），邮箱验证码自助注册（v13），包 `internal/study`。
- `requireAdmin`（`internal/serve/server.go:95`）从 Bearer 头或 `gk_admin` cookie 校验，用户名放 `X-Admin-User` 头；`requireUser`（`internal/serve/study.go:28`）校验 `gk_user` cookie，`study.User` 放 context，并带页面身份前置 `X-GK-Expected-User`（`internal/serve/identity.go`）。
- `admin.Verify(ctx, db, token) (name, error)` 是当前会话校验点；`app_user` 无 role/status 字段。
- `study.User`（`internal/study/auth.go:40-44`）仅 id/username/nickname，`/auth/me` 原样返回。
- `study.CreateSession`（`internal/study/practice.go:73`）：先按 spec 选出 `ids`，kind=`paperset` 时 `spec.Limit=200`（整卷），随后建会话；这是额度卡点。
- 时区约定：业务日使用固定 `Asia/Shanghai`（`internal/study/profile.go:95-98`）。
- 配置存 `setting` 表（`internal/setting`），敏感值 AES-256-GCM；serve 层 `handlePutSettings` 支持掩码保持原值。`study` 包不 import `setting`，分层上配置读取须经 serve 注入。
- `admin_session.token`、`app_session.token` 均存 SHA-256 哈希；密码 bcrypt。
- 前端 `web/src`：路由守卫按 `meta.admin` 调 `/api/admin/me`（`web/src/router/index.js:24`）；管理页单页 `web/src/views/Admin.vue`。

## 3. 数据模型（v14 迁移）

在 `internal/store/schema.go` 的 `migrations` 末尾追加 v14：

```sql
-- 管理员角色：迁移时存量管理员落 DEFAULT 'super'，等价于"现有管理员自动成为超管"。
ALTER TABLE admin_user ADD COLUMN role TEXT NOT NULL DEFAULT 'super';

-- 用户角色与状态：存量用户落 DEFAULT，行为不变。
ALTER TABLE app_user ADD COLUMN role           TEXT NOT NULL DEFAULT 'free';   -- free | vip
ALTER TABLE app_user ADD COLUMN status         TEXT NOT NULL DEFAULT 'active'; -- active | banned
ALTER TABLE app_user ADD COLUMN vip_expires_at TEXT;                            -- NULL=长期有效

-- 每日额度计数（free 用户用量；会员不计数也不读它）。
CREATE TABLE daily_usage (
  user_id   INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  day       TEXT NOT NULL,                 -- Asia/Shanghai 自然日 YYYY-MM-DD
  questions INTEGER NOT NULL DEFAULT 0,
  papers    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(user_id, day)
);

-- 管理操作审计（只追加，不更新不改）。
CREATE TABLE admin_audit (
  id         INTEGER PRIMARY KEY,
  admin      TEXT NOT NULL,                -- 操作人用户名
  action     TEXT NOT NULL,                -- user_ban 等（见 §4.4）
  target     TEXT NOT NULL,                -- 目标标识（如 user:7 / admin:3）
  detail     TEXT,                         -- 变更摘要（JSON 或短文本）
  created_at TEXT NOT NULL
);
CREATE INDEX idx_admin_audit_created ON admin_audit(created_at DESC);
```

要点：

- **会员判定 SQL（统一口径）**：`role='vip' AND (vip_expires_at IS NULL OR vip_expires_at > ?nowTxt)`。惰性降级——过期 downfall 到免费，不靠定时任务。
- 会员到期格式 `vip_expires_at` 用 `YYYY-MM-DD`（Asia/Shanghai 日期），比较用同日历的 `YYYY-MM-DD HH:MM:SS` 展开（当日 23:59:59 仍有效）。
- `admin_user.role` 的合法值只有 `super`/`operator`，写入前在代码内校验（不建 CHECK 约束，SQLite ALTER 成本考量）。

## 4. 管理端角色与 API

### 4.1 会话校验返回角色（internal/admin）

- `Verify` 签名改为 `Verify(ctx, db, token) (name, role string, err error)`，单查询同时取 `u.role`；更新全部调用点（`internal/serve/server.go` requireAdmin 与相关测试）。
- `admin.Login` 改为返回 `(token, name, role, error)`：现有登录查询一次取 `id, password_hash, username, role`，serve 登录响应体为 `{"username":..., "role":...}`。
- `Create(ctx, db, username, password, role)`：增加 role 参数，角色值白名单校验；CLI（`cmd/gk/serve.go`）`gk admin create --role super|operator`，缺省 `super`。`gk admin passwd` 不变。
- 新增 `CountByRole(ctx, db, role)` 供"最后一个超管不可删"校验。

### 4.2 serve 中间件与路由分组（internal/serve/server.go）

- `requireAdmin`：校验后把 name 放 `X-Admin-User`、role 放 request context（`adminKey{}`）。
- 新增 `requireRole(role ...string)` 中间件：context 中 role 不在列表 → 403「该操作需要超级管理员权限」。
- 路由重组（保持既有路径语义不变）：

| 组 | 中间件 | 路由 |
|---|---|---|
| 通用管理 | requireAdmin | `/admin/me`、`/admin/concepts/refresh`、`/admin/password`、全部新增 `/admin/users*` |
| 超管专属 | requireAdmin → requireRole("super") | `/admin/settings` GET/PUT、`test-llm`、`test-mail`、`/admin/admins` GET/POST、`/admin/admins/{id}/password` PUT、`/admin/admins/{id}` DELETE |

- 运营管理员对 `/admin/settings` 全部 403（读也不可见，避免暴露 SMTP 与 LLM 配置细节）。
- 新增用户管理 handler 与 SQL 编排见 §4.4；SQL 在 `internal/study`（用户域归属），serve 只做 HTTP 语义；审计写 `internal/admin`。

### 4.3 用户侧角色信息贯通

- `study.User` 增加 `Role string`、`VipExpiresAt string`（`"YYYY-MM-DD"` 或空串）；`Verify`、`LoginWithMetadata` 联查返回。
- `/auth/me` 因此向本人暴露是否会员与到期日，前端练习页展示"会员至 2027-10-16"或额度提示。

### 4.4 用户管理 API（internal/study + serve）

所有接口位于 `requireAdmin` 组；响应统一 `{"ok":true}`（列表除外）；非法目标 id → 404。

```
GET /api/admin/users?page=1&status=&role=&q=
  → 200 {"users":[{id,username,nickname,role,status,vip_expires_at,created_at,last_login_at}],"total":N}
  q 对 username/nickname 做 LIKE 模糊匹配；status∈{active,banned}；role∈{free,vip}；page 每页 20。
  列表与筛选均按有效权益判定（命中 §3 过期口径视为 free，此时筛选 role=free 含它）；`vip_expires_at` 原样展示供管理员核对。
PUT /api/admin/users/{id}/status    {"status":"banned"|"active"}
  banned：事务内 UPDATE status + DELETE app_session WHERE user_id=?（即封即踢）。
  重复设置同值幂等成功。审计 action=user_ban / user_unban。
PUT /api/admin/users/{id}/role      {"role":"vip"|"free","expires":"2027-10-16"|""}
  role=vip：expires 空 → vip_expires_at=NULL（长期）；非空 → 校验 YYYY-MM-DD 且晚于当前 Asia/Shanghai 日期。
  role=free：一并清空 vip_expires_at。审计 action=vip_set / vip_clear。
PUT /api/admin/users/{id}/password  {"password":"..."}
  8–72 字节校验 → bcrypt 更新 + 删除该用户全部 app_session。审计 action=user_password_reset（不记密码本身）。
```

- 目标用户不存在 → 404「用户不存在」。
- 所有写操作在单事务（`store.WriteTx`）内完成，成功后由 serve 调 `admin.Audit` 记审计；审计失败只记服务日志，不影响业务结果。
- `admin.Audit(ctx, db, adminName, action, target, detail)` 插入 `admin_audit`，`created_at` UTC RFC3339。

### 4.5 管理员管理 API（internal/admin + serve，超管专属）

```
GET    /api/admin/admins → 200 {"admins":[{id,username,role,created_at,last_login_at}]}
POST   /api/admin/admins {"username","password","role":"super"|"operator"}
       校验用户名 3–32 可见字母数字下划线短横、密码 8–72；UNIQUE 冲突 → 409「用户名已存在」。
       审计 action=admin_create。
PUT    /api/admin/admins/{id}/password {"password":...}
       bcrypt 更新 + DELETE admin_session（复用现有吊销策略）。审计 action=admin_password_reset。
DELETE /api/admin/admins/{id}
       拒绝删除自己（400「不能删除当前登录账号」）；拒绝删除最后一个 super（400「至少保留一个超级管理员」）。
       级联删除 admin_session（FK ON DELETE CASCADE 已保证）。审计 action=admin_delete。
```

- 目标管理员不存在 → 404。
- 超管创建另一个 super 允许（受上一条"最后一人"保护约束）。

## 5. 用户状态与额度（internal/study）

### 5.1 封禁语义

- 登录（`LoginWithMetadata`）：取数联查 `status`；命中 banned → 返回 403 语义错误 `ErrBanned`「账号已封禁，如有疑问请联系管理员」（serve 层 `studyError` 映射 403）；banned 账号不进入建会话事务。
- 会话校验（`Verify`）：联查 `status`，banned → `ErrCredentials` 导致 401「请先登录」（鉴权面统一 401，不在接口上区分封禁细节；登录入口已给明确提示）。
- 解封后账号即可正常登录，历史会话在封禁时已清空。

### 5.2 额度（免费 user 每日 50 题 / 3 卷，管理端可调）

- **配置**：新增非敏感 setting 键 `user.daily_free_questions`（默认 `50`）、`user.daily_free_papers`（默认 `3`）。`internal/study` 不直接读 setting 表——新增 `Service.Quota func(ctx) (questions, papers int)` 注入点，serve 构造 `New` 时用 `setting.Store` 实现闭包；`Quota == nil` 时回退默认 50/3（避免测试与 CLI 场景耦合）。
- **卡点**：`CreateSession` 选定 `ids` 的事务内：
  - `kind=paperset` → 消耗 `papers` +1；
  - 其他 kind → 消耗 `questions` + `len(ids)`（1–200）。
  - 单事务内读取会员身份与当日 `daily_usage`：免费用户若 `当日已用 + 本次消耗 > 配额` → `ErrQuotaExceeded`，不建会话；否则 UPSERT 累加并建会话。
  - VIP（§3 判定，含不过期与未到期）跳过全部额度检查，不写 `daily_usage`。
- **错误映射**：`ErrQuotaExceeded` → 429 + `Retry-After`（次日 Asia/Shanghai 零点距现在的秒数）+ 文案「今日免费额度已用完，明日重置；升级会员不限量」。
- **只卡入口**：会话创建成功后，会话内继续作答（draft/submit）不再复查额度，避免练习中途被冻结。
- 会话并发：两个并发 `CreateSession` 在同一 `WriteTx` 序列化下不会突破上限（复用现有 `practice_concurrency_test.go` 的写法验证）。

## 6. 前端（web/src）

- `Admin.vue` 改造为标签页容器：
  - **系统配置**（role=super 可见；operator 隐藏该页签）：原 settings + test-llm/test-mail 内容。
  - **用户管理**（两种角色可见）：列表/筛选（状态、角色、关键字、分页）；行内操作：封禁/解封、设会员（弹窗输入到期日，可留空=长期）、取消会员、重置密码（二次确认）。封禁/解封与设会员均显示成功提示。
  - **管理员账号**（super 可见）：列表 + 新建（用户名/密码/角色）、重置口令、删除（二次确认，后端兜底"最后一个超管/删自己"）。
- `/api/admin/me` 响应带 `role`，据此渲染页签。
- 登录页：捕获 403（banned）展示后端文案。
- 练习页：创建会话返回 429 时展示提示文案与 `Retry-After` 换算的"明日重置"；顶栏会员标识读 `/auth/me` 的 role 与到期日。

## 7. 测试

按仓库既有风格（`internal/serve/security_test.go` 等，httptest + 临时库）：

1. **管理员角色边界**：operator 请求 `GET/PUT /admin/settings`、`test-llm`、`test-mail`、`GET/POST /admin/admins`、`PUT /admin/admins/{id}/password`、`DELETE /admin/admins/{id}` → 403；`/admin/me`、`concepts/refresh`、`/admin/users*` → 200；super 全部 200。
2. **用户/管理员凭据互斥**：user cookie 打 `/api/admin/*` → 401；admin cookie 打 `/api/questions` 等用户接口 → 401（扩展 `personal_profile_test.go` 既有断言）。
3. **CLI 角色**：`gk admin create --role operator` 建出 operator；缺省 super；非法 role 值报参数错误。
4. **删除保护**：删自己 → 400；删最后一个 super → 400；保留两个 super 时互删 → 200。
5. **封禁**：banned 登录 → 403 且不签发会话；既有会话在封禁事务中被删；封禁后持旧 token 请求 → 401；解封 → 可登录。
6. **额度**：free 建满 3 卷后再建第 4 卷 → 429 且 `Retry-After`=次日零点间隔；free 累计题数触及 50 后再建 → 429；设置 vip → 放开；vip 带 `vip_expires_at` 未到期放开、过期回落 free；`Quota` 闭包读到 setting 默认 50/3 与改后值。
7. **并发**：并行 `CreateSession`（试卷）第 4 个并发请求恰有 1 个 429（沿用 `practice_concurrency_test.go` 模式）。
8. **迁移**：v13 老库升 v14：现有 admin → super，用户 → free/active，`vip_expires_at` NULL，`daily_usage`/`admin_audit` 空表可用。
9. **审计**：封禁/设会员/重置密码/管理员增删改口令各写一条 `admin_audit`，包含操作人、action、target。
10. **回归**：现有 `internal/serve/*_test.go`、`internal/admin/admin_test.go` 随 `Verify` 签名更新后全绿。

## 8. 实现落点（文件级）

| 文件 | 变更 |
|---|---|
| `internal/store/schema.go` | 追加 v14 迁移 |
| `internal/admin/admin.go` | role 贯穿：Create(带 role)、Verify 返回 role、CountByRole、Audit |
| `cmd/gk/serve.go` | `gk admin create --role` 参数与用法文案 |
| `internal/serve/server.go` | requireAdmin 暴露 role、requireRole、settings/test-* 挪入超管组、/admin/admins handler、/admin/users handler、Quota 闭包接线 |
| `internal/study/auth.go` | User 增加 Role/VipExpiresAt、登录/Verify 联查 status、ErrBanned |
| `internal/study/practice.go` | CreateSession 额度事务（会员判定+daily_usage UPSERT+ErrQuotaExceeded） |
| `internal/study/admin_users.go`（新） | 用户管理 SQL（列表/封禁/设会员/重置密码） |
| `internal/setting/setting.go` | 新增 `user.daily_free_questions`、`user.daily_free_papers` 默认键 |
| `web/src/views/Admin.vue`(+拆分组件) | 标签页改造、用户管理、管理员管理 |
| `web/src/views/Login.vue`、`Practice.vue`、`api.js` | 403 提示、429 引导、role/me 字段 |
| 对应 `*_test.go` | §7 全部场景 |
