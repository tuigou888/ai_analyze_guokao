# 权限分级体系实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 为双轨账号体系引入分级权限——管理员分 super/operator、用户分 free/vip 与 active/banned、免费每日额度（管理端可调）、Web 端用户与管理员管理、操作审计。

**架构：** 迁移只追加（v14）。角色/状态判定与额度硬限制在后端：`internal/admin` 管管理员域与审计，`internal/study` 管用户域与额度，`internal/serve` 只做 HTTP 语义与中间件。前端 `web/src` 管理页改标签页，用户侧展示会员/额度反馈。

**技术栈：** Go + chi + modernc SQLite（手写 SQL、`store.WriteTx` 短事务）、bcrypt、Vue 3（无测试框架，验证靠 `npm run build`）。

**规格：** `docs/superpowers/specs/2026-10-16-rbac-permission-design.md`（执行者两份都读；本计划的论证依据来自规格。

## 全局约束

- 迁移只追加；新版本为 v14，单条 SQL（含 `admin_user.role`、`app_user.role/status/vip_expires_at`、`daily_usage`、`admin_audit`）。
- SQL 一律不进 `internal/serve`；业务规则在 `internal/admin`、`internal/study`。
- 会话 token 存 SHA-256；密码 bcrypt（`bcrypt.DefaultCost`）；敏感配置不落明文。
- 角色常量：管理员 `super`/`operator`（`admin.RoleSuper`/`admin.RoleOperator`）；用户 `free`/`vip`，状态 `active`/`banned`。
- 会员判定 SQL 口径：`role='vip' AND (vip_expires_at IS NULL OR vip_expires_at > ?nowTxt)`；`vip_expires_at` 存 `YYYY-MM-DD`（Asia/Shanghai），当日 23:59:59 仍有效。
- 免费额度默认每日 50 题 / 3 卷；setting 键 `user.daily_free_questions`、`user.daily_free_papers`（非敏感，超管可改）。
- 业务日/日期均用固定时区：`study.ExamZone = time.FixedZone("Asia/Shanghai", 8*60*60)`。
- 额度只卡 `CreateSession` 入口；会话内作答不再复查。
- 审计追加写 `admin_audit`，业务成功后由 serve 调 `admin.Audit`；审计失败只记日志不影响业务。
- 封禁即踢线：置 banned 与删除该用户全部 `app_session` 在同一事务。
- 文案（Pin 死）：`「账号已封禁，如有疑问请联系管理员」`、`「该操作需要超级管理员权限」`、`「至少保留一个超级管理员」`、`「不能删除当前登录账号」`、`「用户名已存在」`、`「今日免费额度已用完，明日重置；升级会员不限量」`、`「角色必须是 super 或 operator」`。
- 错误映射：banned 登录 403、banned 会话/越权用户凭据 401、operator 打超管接口 403、额度 429（`Retry-After`=次日 Asia/Shanghai 零点间隔秒数）。

## 审查重点（Review Focus）

1. **封禁用户持旧 token 打任意业务接口**（含 `/media/*`）→ 一律 401，绝不返回数据；登录入口给 403 文案。→ 钉住：任务 4 `Verify` 联查 + 任务 7 HTTP 越权测试。
2. **并发创建会话突破免费额度** → 两个连接池并发 `POST /api/practice/sessions` 第 4 次恰有 1 次 429。→ 钉住：任务 7 `TestAdminUserQuotaConcurrency429`（沿用 `practice_concurrency_test.go` 双连接池模式）。
3. **operator token 读取敏感配置** → `GET /admin/settings` 403 且响应体不含任何 setting 值。→ 钉住：任务 7 operator 403 矩阵测试。
4. **VIP 到期当日边界** → 过期后一切按免费口径（额度、`/auth/me` role）。→ 钉住：任务 5 `TestQuotaVipExpiredFallsBack` + 任务 8 列表有效权益判定。
5. **非法会员到期日注入**（如 `"9999-99-99"`、`"2020-01-01"`）→ 400 校验错误，不写库。→ 钉住：任务 8 `TestAdminSetUserRoleRejectsBadDates`。

---

### 任务 1：v14 迁移（角色/状态/额度/审计表）

**文件：**
- 修改：`internal/store/schema.go`（`migrations` 末尾追加；文件当前以 v13 `email_code` 块结尾，`schema.go:392-405` 附近）
- 测试：`internal/store/v14_migration_test.go`（新建）

- [ ] **步骤 1：编写失败的测试**

沿用 `personal_migration_test.go` 的 `migrations[:13]` 截断模式：先以 13 版本建库、插入一行 `admin_user`（不带 role 列）与一行 `app_user`，关闭后以全量迁移重开，断言：

```go
func TestV14MigrationAddsRolesStatusAndAudit(t *testing.T) {
    // v13 建库 + INSERT INTO admin_user(username,password_hash,created_at) VALUES('root','hash','...')
    //       + INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES('u1','hash','','2025-01-01T00:00:00Z','')
    // 重开后断言：
    //  - SELECT role FROM admin_user WHERE username='root' == "super"
    //  - SELECT role,status,vip_expires_at FROM app_user == "free","active",NULL
    //  - daily_usage 可插入 (1,'2026-10-16',...,0) 且主键 (user_id,day) 冲突报 UNIQUE
    //  - admin_audit 可插入并回读 admin/action/target/detail/created_at
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/store/ -run TestV14MigrationAddsRolesStatusAndAudit -v`
预期：FAIL（无 role 列等 SQLITE 报错）

- [ ] **步骤 3：在 `internal/store/schema.go` 追加 v14**

SQL 以规格 §3 的迁移块为准逐字照抄（`ALTER TABLE admin_user ADD COLUMN role TEXT NOT NULL DEFAULT 'super';` 起，到 `CREATE INDEX idx_admin_audit_created ...` 止），并写与相邻版本一致的单行注释说明。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/store/ -v`
预期：PASS（含既有迁移测试）

- [ ] **步骤 5：Commit**

```bash
git add internal/store/schema.go internal/store/v14_migration_test.go
git commit -m "feat: v14 迁移-管理员角色/用户角色与状态/每日额度计数/管理审计表"
```

### 任务 2：internal/admin 角色贯穿与审计底座

**文件：**
- 修改：`internal/admin/admin.go`（`Create`、`Ensure`、`Login`、`Verify` 均在此）
- 修改：`internal/admin/admin_test.go`（`Create` 调用点加 role 参数；`Verify` 三返回值适配）
- 测试：`internal/admin/role_test.go`（新建）

**Interfaces（后续任务引用）：**

```go
const (RoleSuper = "super"; RoleOperator = "operator")
var (
    ErrDuplicate   = errors.New("用户名已存在")
    ErrRoleInvalid = errors.New("角色必须是 super 或 operator")
    ErrSelfDelete  = errors.New("不能删除当前登录账号")
    ErrLastSuper   = errors.New("至少保留一个超级管理员")
)
func ValidRole(r string) bool
func Create(ctx, db, username, password, role string) error          // UNIQUE 冲突→ErrDuplicate；非法 role→ErrRoleInvalid
func Ensure(ctx, db, username, password string) (bool, error)        // 内部以 RoleSuper 调 Create（签名不变）
func Login(ctx, db, username, password string) (token, name, role string, err error)
func Verify(ctx, db, token string) (name, role string, err error)
func CountByRole(ctx, db, role string) (int, error)
func Audit(ctx, db, admin, action, target, detail string) error      // INSERT admin_audit，created_at=UTC RFC3339
type AdminRow struct{ ID int64; Username, Role, CreatedAt, LastLoginAt string }
func ListAdmins(ctx, db) ([]AdminRow, error)                          // ORDER BY id
func SetPasswordByID(ctx, db, id int64, password string) error        // 校验≥8位；bcrypt 更新；DELETE 该 admin_session；0 行→ErrNoAdmin 语义错误（新 var ErrNoTarget = errors.New("目标管理员不存在")）
func Delete(ctx, db, id int64, currentName string) error              // 事务内：自删（按 username==currentName 取 id 判断）→ErrSelfDelete；role==super 且 CountByRole==1→ErrLastSuper；0 行→ErrNoTarget；成功级联清会话
```

- [ ] **步骤 1：编写失败的测试（`internal/admin/role_test.go`）**

```go
func TestRoleLifecycle(t *testing.T) {
    // Create("op","password8",RoleOperator) → Login → (token=="非空", name=="op", role=="operator")
    // Create 非法 role "root" → ErrRoleInvalid；重复用户名 → ErrDuplicate
    // Create("admin2","password8",RoleSuper)；Verify(token) role=="operator"
    // CountByRole(RoleSuper)==2；ListAdmins 两条且字段齐全
    // Audit("root","user_ban","user:7","{}") 逐字回读；created_at 长度>0
    // SetPasswordByID(opID,"newpass99") 后旧 token Verify 失败
    // Delete(selfID,"op") → ErrSelfDelete；删最后一个 super → ErrLastSuper（先删掉另一个 super）；
    //   删除 operator 成功后再 Verify 旧 token 失败
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/admin/ -v`
预期：FAIL（未定义符号/签名不符导致编译失败即视为红）

- [ ] **步骤 3：按 Interfaces 实现**

`Create` 判 UNIQUE 冲突用与 `internal/study/auth.go:124` 相同的 `strings.Contains(err.Error(), "UNIQUE")` 手法。`Delete` 的自删判定需先 `SELECT username FROM admin_user WHERE id=?`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/admin/ -v`
预期：PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/admin/admin.go internal/admin/admin_test.go internal/admin/role_test.go
git commit -m "feat: 管理员角色贯穿-双角色/按ID管理管理员/审计底座"
```

### 任务 3：CLI `gk admin create --role`

**文件：**
- 修改：`cmd/gk/serve.go`（`cmdAdmin`，`serve.go:111-164`）
- 测试：`cmd/gk/admin_role_test.go`（新建）

- [ ] **步骤 1：编写失败的测试**

```go
func TestCmdAdminCreateWithRoleFlag(t *testing.T) {
    // dbPath := filepath.Join(t.TempDir(),"gk.sqlite")
    // cmdAdmin([]string{"create","--db",dbPath,"--user","op","--password","longenough","--role","operator"})
    // store.Open(dbPath) 后 SELECT role FROM admin_user WHERE username='op' == "operator"
    // 再跑一次不带 --role 的 create（用户名 admin2）→ role=="super"
    // --role root 报错且错误串含 "角色必须是 super 或 operator"
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./cmd/gk/ -run TestCmdAdminCreateWithRoleFlag -v`
预期：FAIL（无 --role flag）

- [ ] **步骤 3：实现 flag 与用法文案**

加 `role := fs.String("role", "super", "角色 super|operator")`，`create` 分支传给 `admin.Create(ctx, db, *user, pw, *role)`；help 文案 `create` 行改为：

```
  gk admin create --user <用户名> [--password <密码>] [--role super|operator]   创建管理员（缺省 super）
```

`passwd` 行不变。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./cmd/gk/ -run TestCmdAdminCreateWithRoleFlag -v`
预期：PASS

- [ ] **步骤 5：Commit**

```bash
git add cmd/gk/serve.go cmd/gk/admin_role_test.go
git commit -m "feat: gk admin create 支持 --role super|operator"
```

### 任务 4：study 用户身份（role/status/banned 贯通）

**文件：**
- 修改：`internal/study/auth.go`
- 测试：`internal/study/user_identity_test.go`（新建）

**Interfaces：**

```go
type User struct {                      // 追加字段，JSON 命名 Pin 死
    ID       int64  `json:"id"`
    Username string `json:"username"`
    Nickname string `json:"nickname"`
    Role     string `json:"role"`
    VipExpiresAt string `json:"vip_expires_at,omitempty"`
}
var ErrBanned = errors.New("账号已封禁，如有疑问请联系管理员")
// LoginWithMetadata：SELECT 追加 status、role、vip_expires_at；bcrypt 通过后 status=="banned" → ErrBanned（不建会话）
// Verify：JOIN 追加 status/role/vip_expires_at；banned → ErrCredentials；User 归一化：role=="vip" 且 expires 非空且 expires <= 当日(ExamZone, YYYY-MM-DD) → Role="free"
// 说明：过期回落判定写成 helper `effectiveRole(role, expires, today string) string` 供 Verify 与任务 8 列表复用
// daily_quota 物理 day 格式 "2006-01-02"；同文件导出：
var ExamZone = time.FixedZone("Asia/Shanghai", 8*60*60)   // 放 practice.go（见任务 5）
```

- [ ] **步骤 1：编写失败的测试**

```go
func TestBannedAndEffectiveRole(t *testing.T) {
    // service := &Service{DB: db, Clock: 固定 2026-10-16 Asia/Shanghai 10:00}
    // seed: 直接 SQL 插入 app_user(role='vip',vip_expires_at='2026-10-10',status='active') 与 banned 用户
    // 1) banned 用户 Login → ErrBanned；且 app_session 无新行
    // 2) banned 用户持预插 token Verify → ErrCredentials
    // 3) vip(2026-10-10) 用户 Verify → Role=="free"、VipExpiresAt=="2026-10-10"
    // 4) vip(2026-10-20) → Role=="vip"；role='vip',expires NULL → "vip"
    // 5) 正常用户 Verify → Role=="free"
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/study/ -run TestBannedAndEffectiveRole -v`
预期：FAIL

- [ ] **步骤 3：实现**

`effectiveRole` 在 `auth.go`；`Verify` 把 SELECT 从三列扩到六列（id,username,nickname,status,role,vip_expires_at）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/study/ -v`
预期：PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/study/auth.go internal/study/user_identity_test.go
git commit -m "feat: 用户身份-封禁校验/会员有效判定/角色贯通到会话校验"
```

### 任务 5：study 每日额度（CreateSession 事务内硬限制）

**文件：**
- 修改：`internal/study/practice.go`（`CreateSession` `practice.go:73-155`；`ExamZone` 加入此文件）
- 测试：`internal/study/quota_test.go`（新建）

**Interfaces：**

```go
const (DefaultFreeQuestions = 50; DefaultFreePapers = 3)
var ErrQuotaExceeded = errors.New("今日免费额度已用完，明日重置；升级会员不限量")
// Service 追加字段：Quota func(ctx) (questions, papers int)   // nil→(50,3)
// helper：func QuotaDay(t time.Time) string { return t.In(ExamZone).Format("2006-01-02") }
```

`CreateSession` 重构：把 `INSERT INTO practice_session`（`practice.go:146`）与回读前的整段落进 `store.WriteTx`，事务内顺序：

1. `SELECT status,role,vip_expires_at FROM app_user WHERE id=?`；命中规格 §3 会员口径（expires 为 NULL 或 > 当日 `QuotaDay`，按 `YYYY-MM-DD` 字符串比较）→ 跳过计数；
2. 非 VIP：读 `SELECT COALESCE(questions,0),COALESCE(papers,0) FROM daily_usage WHERE user_id=? AND day=?`；消耗量 = `paperset→papers+1`，其他 kinds→`questions+len(ids)`；`已有+消耗 > 此类限额` → `ErrQuotaExceeded`；
3. 通过则 `INSERT INTO daily_usage(...) ON CONFLICT(user_id,day) DO UPDATE SET questions=questions+?/papers=papers+?`（按 kind 只累加对应列），再插 practice_session（原 SQL 不变）。

- [ ] **步骤 1：编写失败的测试**

```go
func TestDailyQuotaHardLimit(t *testing.T) {
    // Clock 固定 2026-10-16；Quota 返回 (50,3)
    // free 用户 paperset×3 成功 → 第 4 卷 ErrQuotaExceeded；daily_usage.papers==3
    // free 用户题数：Quota questions=2 时 len(ids)==2 会话成功、第 2 次（又 2 题）→ 拒
    // vip（未到期）→ 无限建卷、daily_usage 无行；vip(expired) → 与 free 同限
    // 次日 Clock 前移一天 → free 可再建
}
func TestQuotaDay(t *testing.T) { // QuotaDay(UTC 2026-10-16T18:00)=="2026-10-17"（移交 ExamZone 边界）
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/study/ -run 'TestDailyQuotaHardLimit|TestQuotaDay' -v`
预期：FAIL

- [ ] **步骤 3：实现事务重构与判定**

保持对 `spec.Limit=200`（paperset，`practice.go:111`）等既有行为不变；`ids` 选择逻辑保持在事务外。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/study/ -v`
预期：PASS（含 `practice_concurrency_test.go` 等既有测试）

- [ ] **步骤 5：Commit**

```bash
git add internal/study/practice.go internal/study/quota_test.go
git commit -m "feat: 免费用户每日额度硬限制-vip 豁免与到期回落"
```

### 任务 6：setting 额度键与 serve 接线

**文件：**
- 修改：`internal/setting/setting.go`（键常量与 `Defaults`）
- 修改：`internal/serve/server.go`（`New` 闭包，`server.go:46-48`）
- 测试：`internal/serve/quota_wiring_test.go`（新建）

- [ ] **步骤 1：编写失败的测试**

```go
func TestQuotaFromSettings(t *testing.T) {
    // fixture(t)（既有 serve fixture，settings store 已挂）生成 free 用户
    // POST /api/practice/sessions kind=paperset（有效 paper_id）连打到第 4 次 → 429，Retry-After 数字在 (0,86400]
    // settings.Put "user.daily_free_papers"="5" → 再建第 4、5 卷成功，第 6 次 429
    // PUT 数值改为 "0" → 视作未配置回落默认 3（第 4 次拒绝）……改为改回 "3" 断言行为即可
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/serve/ -run TestQuotaFromSettings -v`
预期：FAIL（429 语义来自配置需接线后才生效，当前默认注入不存在）

- [ ] **步骤 3：实现键与闭包**

`setting.go` 增加：

```go
KeyDailyFreeQuestions = "user.daily_free_questions"
KeyDailyFreePapers    = "user.daily_free_papers"
// Defaults：50 与 3
```

`server.go` `New` 里给 `Study.Quota` 赋闭包：逐键 `settings.Get` → `strconv.Atoi`，解析失败/≤0 → 回落 `study.DefaultFreeQuestions`/`DefaultFreePapers`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/serve/ -run TestQuotaFromSettings -v`
预期：PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/setting/setting.go internal/serve/server.go internal/serve/quota_wiring_test.go
git commit -m "feat: 免费额度可配置-user.daily_free_* 配置键与 serve 接线"
```

### 任务 7：serve 权限中间件、路由重排与错误映射

**文件：**
- 修改：`internal/serve/server.go`（路由表 `server.go:64-89`、`studyError` 在 `internal/serve/study.go:45-68`）
- 修改：既有测试的 `admin.Verify` 调用点：`internal/serve/security_test.go`、`internal/serve/email_code_test.go`、`internal/serve/personal_identity_test.go`
- 测试：`internal/serve/admin_role_guard_test.go`（新建）

**路由终态（Pin 死）：**

```go
r.Route("/api", func(r chi.Router) {
    s.userRoutes(r)
    r.NotFound(  /* 不变 */ )
    r.Post("/admin/login", s.handleLogin)    // 响应改 {"ok":true,"username":…,"role":…}
    r.Post("/admin/logout", s.handleLogout)
    r.Group(func(r chi.Router) {             // 通用管理：requireAdmin
        r.Use(s.requireAdmin)
        r.Get("/admin/me", s.handleMe)       // 响应改 {"username":…,"role":…}
        r.Post("/admin/concepts/refresh", …) // 不变
        r.Post("/admin/password", s.handleChangePassword)
    })
    r.Group(func(r chi.Router) {             // 超管专属：requireAdmin → requireRole("super")
        r.Use(s.requireAdmin); r.Use(s.requireRole)
        r.Get("/admin/settings", …); r.Put("/admin/settings", …)
        r.Post("/admin/test-llm", …); r.Post("/admin/test-mail", …)
    })
})
```

**Interfaces：** context key `adminRoleKey{}` + `currentAdminRole(r *http.Request) string`；`requireRole` 不匹配 → `writeErr(w, 403, "该操作需要超级管理员权限")`；`studyError` 新增两 case：`study.ErrBanned`→403、`study.ErrQuotaExceeded`→429 + `w.Header().Set("Retry-After", strconv.FormatInt(secondsUntilTomorrow(now), 10))`，`secondsUntilTomorrow` 用 `study.ExamZone` 计算到次日零点。

- [ ] **步骤 1：编写失败的测试**

```go
func TestOperatorForbiddenMatrix(t *testing.T) {
    // super（默认迁移即 super）与 operator（直接改 admin_user.role='operator'）两类会话
    // operator 依次打：GET/PUT /api/admin/settings、POST test-llm、POST test-mail → 全 403 且 body 含 "超级管理员"
    // operator 打 GET /api/admin/me、POST /api/admin/concepts/refresh、POST /api/admin/password → 200/常规业务响应
    // 超管 cookie 打用户接口 /api/questions → 401（补竞断言）
}
func TestBannedLogin403AndQuota429ViaHTTP(t *testing.T) {
    // banned 用户 POST /api/auth/login → 403 body 含 "账号已封禁"
    // quota 用任务 6 fixture 思路：free 用户打满后 POST /api/practice/sessions → 429 + Retry-After == 到次日 00:00 秒差（Clock 固定后可精确断言）
    // banned 用户持旧 token GET /api/questions → 401
}
func TestAdminUserQuotaConcurrency429(t *testing.T) {
    // 沿用 practice_concurrency_test.go 双连接池模式：fixture + store.Open(file) 第二池
    // free 用户（额度默认 3 卷）并发发 4 个 POST /api/practice/sessions（paperset）
    // 断言：恰 3 个 201、1 个 429（god 并发序不固定，按状态码计数）
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/serve/ -run 'TestOperatorForbiddenMatrix|TestBannedLogin403AndQuota429ViaHTTP' -v`
预期：FAIL

- [ ] **步骤 3：实现中间件、路由重排、``verify``/``login`` 响应改造与错误映射**

`handleLogin` 改用 `admin.Login` 新返回值拼响应；`handleMe` 用 `x-admin-role`（从 context 取，Pin：通过 context，不新增响应头）。旧 cookie `gk_admin`、token 逻辑不动。

- [ ] **步骤 4：全量 serve 测试通过**

运行：`go test ./internal/serve/ -count=1`
预期：PASS（三者既有测试的 `Verify` 调用点已适配三返回值）

- [ ] **步骤 5：Commit**

```bash
git add internal/serve/server.go internal/serve/study.go internal/serve/admin_role_guard_test.go internal/serve/security_test.go internal/serve/email_code_test.go internal/serve/personal_identity_test.go
git commit -m "feat: 管理端权限中间件-超管专属/双错误映射/operator 矩阵"
```

### 任务 8：管理端用户管理（study SQL + serve handlers + 审计）

**文件：**
- 新建：`internal/study/admin_users.go`
- 测试：`internal/study/admin_users_test.go`（study 层）；`internal/serve/admin_manage_test.go`（HTTP 层）
- 修改：`internal/serve/` 新建 `admin_manage.go`（users 四个 handler）并在 `server.go` 通用管理组注册 4 条路由

**Interfaces（Pin）：**

```go
type AdminUser struct{ ID int64; Username, Nickname, Role, Status, VipExpiresAt, CreatedAt, LastLoginAt string }
type AdminUserPage struct{ Total int; Users []AdminUser }
func (s *Service) AdminListUsers(ctx, page int, status, role, q string) (AdminUserPage, error)
//   page≥1 每页 20 OFFSET；status∈{active,banned}、role∈{free,vip}（有效权益口径=任务4 effectiveRole）；q LIKE username/nickname；无筛选则全量
func (s *Service) AdminSetUserStatus(ctx, id int64, status string) error
//   status 必须二选一否则 ErrInvalid（附加错误 "状态必须是 active 或 banned"）
//   同值幂等成功；置 banned 事务内 DELETE app_session WHERE user_id=?；目标不存在→ErrNotFound
func (s *Service) AdminSetUserRole(ctx, id int64, role, expires string) error
//   role∈{vip,free} 否则 ErrInvalid("角色必须是 free 或 vip")；vip：expires 空串→NULL（长期），非空须 YYYY-MM-DD 且 > 今日(ExamZone) 否则 ErrInvalid("到期日期格式应为 YYYY-MM-DD 且晚于今天")；free→清 vip_expires_at；0 行→ErrNotFound
func (s *Service) AdminResetUserPassword(ctx, id int64, password string) error
//   8–72 字节；bcrypt 更新 + DELETE 该用户会话；0 行→ErrNotFound
```

**HTTP 路由与动作映射（Pin）：** `GET /api/admin/users?page&status&role&q` → `{"users":[…],"total":n}`，`vip_expires_at` 原样字符串、空为 `""`；`PUT /api/admin/users/{id}/status` 动作 `user_ban`/`user_unban`；`PUT …/role` 动作 `vip_set`（detail `{"expires":"2027-10-16"}` 或 `"长期"`）与 `vip_clear`；`PUT …/password` 动作 `user_password_reset`（detail `""`）。`target` 一律 `"user:<id>"`。serve 在业务成功后 `admin.Audit(…)`，失败只 `s.Log`。

- [ ] **步骤 1：编写失败的测试（study 层）**

```go
func TestAdminUserOps(t *testing.T) {
    // seed 3 用户（active free / banned vip+过期 / active vip 长期）
    // ListUsers：q/nick 匹配、status/role 过滤、page=2 空、total 正确；过期 vip 有效权益显示 free
    // SetStatus(unknown_id) → ErrNotFound；重复 banned 幂等 nil；banned 会话删除（预先插 token）
    // SetUserRole：非法 role/日期 "2020-01-01"/"9999-99-99"/未来 OK；free 清空
    // ResetUserPassword："short" → ErrInvalid；换密后旧 token Verify 失败
}
```

- [ ] **步骤 2：运行验证失败** → `go test ./internal/study/ -run TestAdminUserOps -v` 预期 FAIL。

- [ ] **步骤 3：实现 `admin_users.go`**（换密逻辑独立实现，不与用户自助 `Password` 混用）。

- [ ] **步骤 4：运行验证通过** → PASS。

- [ ] **步骤 5：编写 HTTP 失败测试（serve 层）+ 实现 handlers + 注册路由**

```go
func TestAdminUsersHTTP(t *testing.T) {
    // operator 会话：GET /api/admin/users → 200；四种写操作 → 200 且 admin_audit 各落一行（admin/operator, action 精确）
    // 越权：user cookie → 401；未登录 → 401
    // {"total"} 与响应字段逐项断言（AdminUser JSON 键与 §4.4 一致）
}
```

- [ ] **步骤 6：运行验证通过** → `go test ./internal/serve/ -run TestAdminUsersHTTP -v` PASS；`go test ./...` 无回归。

- [ ] **步骤 7：Commit**

```bash
git add internal/study/admin_users.go internal/study/admin_users_test.go internal/serve/admin_manage.go internal/serve/admin_manage_test.go internal/serve/server.go
git commit -m "feat: 管理端用户管理-列表/封禁/设会员/重置密码+审计"
```

注：任务 8 的「步骤 5（HTTP 测试+实现）」与「步骤 6（验证通过）」是完整 TDD 循环，实现者先写失败测试再完成 handler。

### 任务 9：管理端管理员管理 API（超管）

**文件：**
- 修改：`internal/serve/admin_manage.go`（补 4 个 handler）、`internal/serve/admin_manage_test.go`、`internal/serve/server.go`（在任务 7 的超管组内注册路由）

**路由与语义（Pin）：**

```
GET    /api/admin/admins                      → {"admins":[{id,username,role,created_at,last_login_at}]}
POST   /api/admin/admins                      {"username","password","role"} 409=ErrDuplicate；400=参数/角色非法
PUT    /api/admin/admins/{id}/password        {"password"} → admin.SetPasswordByID
DELETE /api/admin/admins/{id}                 → admin.Delete(id, 当前名) 400=ErrSelfDelete/ErrLastSuper
```

- 错误映射：`admin.ErrNoTarget`→404、`ErrDuplicate`→409、`ErrSelfDelete`/`ErrLastSuper`/参数→400「原因透传」。
- 审计 action：`admin_create`（detail `{"role":…}`）、`admin_password_reset`、`admin_delete`；target `"admin:<id>"`。

- [ ] **步骤 1：编写失败的测试**

```go
func TestAdminManagementAPI(t *testing.T) {
    // operator 打 4 路由 → 403 且不产生审计
    // super：POST 建 operator → 200 落审计；重复名 → 409；密码 <8 → 400
    // GET 列表含新管理员；PUT password 重置 → 该管理员旧 token 失效（admin.Verify 三返回值断言）
    // DELETE：删自己 → 400 文案；仅剩超管 → 400 文案；互删成立 → 404 后续
}
```

- [ ] **步骤 2：运行验证失败** → `go test ./internal/serve/ -run TestAdminManagementAPI -v` FAIL。

- [ ] **步骤 3：实现 handlers + 注册**（复用任务 2 的 `admin` 函数，serve 不写 SQL）。

- [ ] **步骤 4：运行验证通过** → PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/serve/admin_manage.go internal/serve/admin_manage_test.go internal/serve/server.go
git commit -m "feat: 管理端管理员管理-超管专属增删改与最后超管保护"
```

### 任务 10：前端管理端（标签页 + 用户管理 + 管理员管理）

**文件：**
- 修改：`web/src/views/Admin.vue`、`web/src/views/Settings.vue`、`web/src/api.js`
- 新建：`web/src/views/admin/Users.vue`、`web/src/views/admin/Admins.vue`

**api.js 增补（Pin 命名）：** `adminUsers(f)`、`adminSetUserStatus(id, status)`、`adminSetUserRole(id, role, expires)`、`adminResetUserPassword(id, password)`、`adminList()`、`adminCreate(username, password, role)`、`adminResetAdminPassword(id, password)`、`adminDelete(id)`。

**行为（Pin）：**
- `Admin.vue`：加载后用 `api.me()`（现返回 `{username, role}`）按 `role` 渲染页签——`系统配置`（仅超管，含原 `Settings` + test 卡片 + 额度配置）、`用户管理`、`管理员账号`（仅超管）。
- `Settings.vue`：`form` 增加 `'user.daily_free_questions'` 与 `'user.daily_free_papers'` 两键（默认 '50'/'3'，普通输入框，归入「用户额度」小节）。
- `Users.vue`：筛选（状态下拉/角色下拉/关键字/分页 20 条，调 `Pagination.vue` 风格一致）；行内操作——封禁/解封（确认后调 status）、设为会员（弹窗：到期日期 input `type=date` 必填项可留空=长期，传空串）、取消会员、重置密码（确认框内输新密码 ≥8）；成功后刷新列表并显示成功提示；banned 行加视觉标识。
- `Admins.vue`：列表 + 新建表单（username/password/角色 select）；重置口令与删除均二次确认；删除展示后端 400 文案。

- [ ] **步骤 1：api.js 增补方法**（无单测；`npm run build` 兜底编译）。
- [ ] **步骤 2：Admin.vue 改造标签页 + role 驱动可见性**。
- [ ] **步骤 3：Settings.vue 额度两键**。
- [ ] **步骤 4：Users.vue 与 Admins.vue 落地**。
- [ ] **步骤 5：验证**

运行：`cd web && npm run build`
预期：构建成功无报错。

- [ ] **步骤 6：Commit**

```bash
git add web/src/views/Admin.vue web/src/views/Settings.vue web/src/views/admin web/src/api.js
git commit -m "feat: 管理端标签页-用户管理与超管专属管理员管理"
```

### 任务 11：前端用户侧（封禁提示、额度引导、会员标识）

**文件：**
- 修改：`web/src/views/Login.vue`（登录失败 403 时展示后端文案，命中「账号已封禁」）
- 修改：`web/src/views/Practice.vue`（会话创建失败 HTTP 429 → 展示「今日免费额度已用完…」并提示明日重置）
- 修改：`web/src/App.vue` 与 `web/src/auth.js`（`/auth/me` 响应新增 role、vip_expires_at；顶栏 role==='vip' 显示「会员」徽标，`vip_expires_at` 非空显示「会员至 YYYY-MM-DD」）
- 修改：`web/src/router/index.js`（如守卫使用 user 对象需同步字段，仅适配不重构）

- [ ] **步骤 1：auth.js 保留 me 响应新字段**（`userMe` 响应直接存对象，不筛选字段）。
- [ ] **步骤 2：Login.vue / Practice.vue 错误路径**（沿用既有 notice/err 展示模式，不新增样式体系）。
- [ ] **步骤 3：App.vue 会员徽标**。
- [ ] **步骤 4：验证**

运行：`cd web && npm run build`
预期：构建成功。

- [ ] **步骤 5：手动冒烟（记录进 PR 描述，不进测试）：** 注册新用户被 429 拦截 → 管理端设为会员 → 无限练习；封禁后被踢出且登录见 403 文案。

- [ ] **步骤 6：Commit**

```bash
git add web/src/views/Login.vue web/src/views/Practice.vue web/src/App.vue web/src/auth.js web/src/router/index.js
git commit -m "feat: 用户侧-封禁提示/额度耗尽引导/会员标识"
```

### 任务 12：全量回归与收尾

**文件：** 无新增（只跑验证与可能的零星适配）

- [ ] **步骤 1：全量后端验证**

运行：`go vet ./... && go test ./... -count=1`
预期：全绿。

- [ ] **步骤 2：前端构建验证**

运行：`cd web && npm run build`
预期：成功。

- [ ] **步骤 3：二进制冒烟（embed 路线）**

```bash
go run ./cmd/gk serve --db /tmp/opencode/smoke.sqlite --port 0 2>/dev/null & sleep 2; curl -s 127.0.0.1:$PORT/healthz; kill %1
```
（端口取动态随机即可；重点确认新二进制能起：管理员未建时提示语不变。）

- [ ] **步骤 4：如有遗漏文件补 commit**

```bash
git status --short && git add -A && git commit -m "chore: 权限分级体系收尾回归"  # 仅当有未提交变更
```
