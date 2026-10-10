# 邮件验证码注册 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 注册必须绑定唯一邮箱并通过 6 位数字邮件验证码验证；SMTP 可在管理后台配置，未配置时开发降级（验证码打印日志）。

**架构：** v13 迁移为 `app_user` 加 `email` 列（部分唯一索引）并新建 `email_code` 表；`internal/mail` 为纯 SMTP 客户端（Config 传入，不碰 DB）；`internal/study` 负责验证码生命周期与注册校验（SQL 只在此层）；`internal/serve` 增加 `POST /api/auth/email-code` 公开端点、修改 register、新增 `POST /api/admin/test-mail`；前端注册表单加邮箱+验证码，管理设置页加邮件配置区块。

**技术栈：** Go 1.26 + chi/v5 + modernc SQLite + 标准库 `net/smtp`/`crypto/tls`（零新依赖）；Vue 3 + Vite。

**规格：** `docs/superpowers/specs/2026-10-10-email-verification-design.md`（执行者必读，本计划的取值依据来自规格）

## 全局约束

- 迁移只追加 v13 到 `internal/store/schema.go` 的 `migrations` 末尾，不改既有条目。
- serve 层禁止 SQL；所有在线写走 `store.WriteTx`；时间一律 `s.clockNow().UTC().Format(time.RFC3339)`。
- 验证码：6 位数字（`crypto/rand`）、5 分钟有效、5 次尝试上限、60 秒重发间隔；SHA-256 哈希入库（复用同包 `tokenHash`），明文不落库。
- 发新码时作废同邮箱旧码（`consumed_at` 置为当前时间）：每邮箱同时至多一个有效码。
- 邮箱归一化：`strings.TrimSpace` + `ToLower`，存储与匹配均用归一化值。
- 错误文案逐字照抄：「发送过于频繁，请稍后再试」「该邮箱已被注册」「验证码错误或已过期」「验证码错误次数过多，请重新获取」「邮箱格式不正确」「验证码发送失败，请稍后重试」「SMTP 未配置」「请先在设置中配置 SMTP」。
- 邮件文案：主题「行测研习注册验证码」；正文「你的验证码是 %s，5 分钟内有效。若非本人操作，请忽略此邮件。」
- 用户名/密码/昵称校验复用 `Register` 现有规则，不改动。
- 后端验证命令：`go test ./...`；前端验证命令：`npm run build`（workdir `web/`）。

## 审查重点

1. 60 秒窗口内连点「发送验证码」：第二次必须 429 且不产生新码记录——任务 4 测试 2 钉住。
2. SMTP 发送失败后立刻重试不应被 60 秒窗口挡（失败记录已删除）——任务 6 测试 2 钉住。
3. 验证码错 5 次后，即使输入正确码也必须拒绝——任务 5 测试 3 钉住。
4. 用户名冲突导致建号事务回滚后，同一验证码必须仍可使用——任务 5 测试 4 钉住。
5. 邮箱大小写/首尾空格不同的写法（`A.User@Example.COM` vs `a.user@example.com`）必须视为同一邮箱：归一化后受同一 60 秒窗口约束、注册匹配同一记录——任务 4 测试 4 钉住。

---

### 任务 1：v13 数据库迁移

**文件：**
- 修改：`internal/store/schema.go`（`migrations` 切片末尾追加）
- 测试：`internal/store/email_migration_test.go`（新建，仿 `personal_migration_test.go` 的截断-恢复模式）

- [ ] **步骤 1：编写失败的测试**

```go
func TestEmailMigrationFromV12(t *testing.T) {
	all := migrations
	defer func() { migrations = all }()
	migrations = migrations[:12]
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := Open(path)
	if err != nil { t.Fatal(err) }
	if _, err = db.Exec(`INSERT INTO app_user(id,username,password_hash,nickname,created_at) VALUES(1,'old_user','h','旧昵称','2025-01-01T00:00:00Z')`); err != nil { t.Fatal(err) }
	db.Close()
	migrations = all
	db, err = Open(path)
	if err != nil { t.Fatal(err) }
	defer db.Close()
	// 存量账号 email 为空串。
	var email string
	if err = db.QueryRow(`SELECT email FROM app_user WHERE id=1`).Scan(&email); err != nil || email != "" {
		t.Fatal(email, err)
	}
	// 空串不参与唯一约束：第二个空 email 用户可插入。
	if _, err = db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES('u2','h','','2025-01-02T00:00:00Z','')`); err != nil {
		t.Fatal(err)
	}
	// 同一非空邮箱第二次插入必须失败。
	if _, err = db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES('u3','h','','2025-01-03T00:00:00Z','a@b.co')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES('u4','h','','2025-01-04T00:00:00Z','a@b.co')`); err == nil {
		t.Fatal("duplicate email accepted")
	}
	// email_code 表可写入可查询，attempts 默认 0、consumed_at 为 NULL。
	res, err := db.Exec(`INSERT INTO email_code(email,code_hash,expires_at,created_at) VALUES('a@b.co','x','2026-10-10T00:05:00Z','2026-10-10T00:00:00Z')`)
	if err != nil { t.Fatal(err) }
	var attempts int
	var consumed sql.NullString
	if err = db.QueryRow(`SELECT attempts,consumed_at FROM email_code WHERE email='a@b.co'`).Scan(&attempts, &consumed); err != nil || attempts != 0 || consumed.Valid {
		t.Fatal(attempts, consumed, err)
	}
	_ = res
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/store/ -run TestEmailMigration -v`
预期：FAIL，报错 `no such column: email` 或 `no such table: email_code`

- [ ] **步骤 3：在 `internal/store/schema.go` 的 `migrations` 末尾追加 v13**

SQL 逐字取自规格 §3（`app_user.email` 列 + 部分唯一索引 `idx_app_user_email` + `email_code` 表 + `idx_email_code_email` 索引），带 `// v13：注册邮箱绑定与邮件验证码。旧账号 email 为空串，不参与唯一约束。` 注释。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/store/ -v`
预期：全部 PASS（含既有 `TestPersonalMigrationFromV11PreservesSecretsAndDrafts`）

- [ ] **步骤 5：Commit**

```bash
git add internal/store/schema.go internal/store/email_migration_test.go
git commit -m "feat: v13 迁移增加用户邮箱与验证码表"
```

### 任务 2：setting 邮件配置键

**文件：**
- 修改：`internal/setting/setting.go`（键常量区 `setting.go:28-43`、`Defaults`、`isSecretKey` `setting.go:241-243`）
- 测试：`internal/setting/mail_setting_test.go`（新建）

- [ ] **步骤 1：编写失败的测试**

用 `Open(db, filepath.Join(t.TempDir(), "key"))` 构造 Store（`store.Open` 建库）。断言：
1. `Set(ctx, KeySMTPPassword, "secret", "test")` 后 `Get` 返回 `"secret"`（加密往返）。
2. `GetMasked(ctx, KeySMTPPassword)` 返回 `(Mask, true, nil)`。
3. `All(ctx)` 包含键 `mail.smtp_host` 且值为 `""`（Defaults 兜底）。
4. `Set` + `Get` `mail.smtp_host` 明文往返；`GetMasked` 不掩码非敏感键。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/setting/ -v`
预期：FAIL，编译错误 `KeySMTPHost undefined`

- [ ] **步骤 3：实现配置键**

在 `setting.go` 增加：`KeySMTPHost = "mail.smtp_host"`、`KeySMTPPort = "mail.smtp_port"`、`KeySMTPUser = "mail.smtp_user"`、`KeySMTPPassword = "mail.smtp_password"`、`KeyMailFrom = "mail.from"`；`Defaults` 加 4 个非敏感键（值 `""`，不加 Password）；`isSecretKey` 改为 `key == KeyAPIKey || key == KeySMTPPassword`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/setting/ -v`
预期：PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/setting/setting.go internal/setting/mail_setting_test.go
git commit -m "feat: setting 增加邮件服务配置键"
```

### 任务 3：internal/mail 纯 SMTP 客户端

**文件：**
- 创建：`internal/mail/mail.go`
- 测试：`internal/mail/mail_test.go`

**接口（后续任务依赖，逐字使用）：**

```go
package mail

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

func (c Config) Configured() bool // Host、Port、From 均非空

var ErrNotConfigured = errors.New("SMTP 未配置")

func Send(ctx context.Context, cfg Config, to, subject, body string) error
```

- [ ] **步骤 1：编写失败的测试**

三个测试，共用的假 SMTP 服务器用 `net.Listen` 起在 `127.0.0.1:0`，goroutine 内 `bufio` 按行读命令，把收到的行推入 channel 供断言；按命令回 `220`/`250`/`354`/`221`：

1. `TestSendNotConfigured`：空 Config 调 `Send` → `errors.Is(err, ErrNotConfigured)`。
2. `TestSendPlain`：`Config{Host:"127.0.0.1", Port:端口, From:"noreply@example.com"}`（无 Username）发送到 `to@example.com`，主题「行测研习注册验证码」，正文「你的验证码是 123456，5 分钟内有效。」。断言：命令流含 `MAIL FROM:<noreply@example.com>`、`RCPT TO:<to@example.com>`；DATA 内容含 `Subject: =?UTF-8?B?` 前缀（RFC 2047 编码）与正文原文；`Content-Type: text/plain; charset=UTF-8`。
3. `TestSendRefusesAuthWithoutTLS`：`Config{... Username:"u", Password:"p"}`，假服务器 EHLO 不广告 `STARTTLS` → `Send` 返回含「SMTP 服务器不支持加密连接」的错误。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/mail/ -v`
预期：FAIL，`no Go files` 或 `undefined: mail.Send`

- [ ] **步骤 3：实现 `internal/mail/mail.go`**

- `!cfg.Configured() → ErrNotConfigured`。
- 端口 `465`：`tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host})`；其余端口 `net.Dial` + `smtp.NewClient`。
- 客户端支持 `STARTTLS` 扩展时 `client.StartTLS(&tls.Config{ServerName: cfg.Host})`。
- `cfg.Username != ""`：当前连接非 TLS 且服务器不支持 STARTTLS → 返回错误「SMTP 服务器不支持加密连接」；否则 `client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host))`。
- `client.Mail(cfg.From)`、`client.Rcpt(to)`、`Data()` 写头（`From`、`To`、`Subject` RFC 2047 B 编码、`MIME-Version: 1.0`、`Content-Type: text/plain; charset=UTF-8`、`Date` RFC 1123Z）+ 空行 + 正文；`client.Quit()`。
- 连接与命令全程受 `ctx` 取消影响（dial 用 `DialContext`，其余操作失败即返回）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/mail/ -v`
预期：3 个测试 PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/mail/
git commit -m "feat: 新增纯 SMTP 邮件客户端包"
```

### 任务 4：study 验证码生成与生命周期

**文件：**
- 创建：`internal/study/email_code.go`
- 测试：`internal/study/email_code_test.go`（新建）

**接口（后续任务依赖，逐字使用）：**

```go
var ErrTooFrequent = errors.New("发送过于频繁，请稍后再试")

func normalizeEmail(email string) string // TrimSpace + ToLower
func validEmail(email string) bool       // 总长 ≤254、恰一个 @、本地部分与域名非空、域名含点

func (s *Service) CreateEmailCode(ctx context.Context, email string) (id int64, code string, err error)
func (s *Service) DeleteEmailCode(ctx context.Context, id int64) error
```

- [ ] **步骤 1：编写失败的测试**

测试构造仿 `personal_auth_test.go`：`store.Open(t.TempDir())` + `&Service{DB: db, Clock: ...}`（Clock 返回可变 `stamp` 变量）：

1. 创建成功：返回值 `code` 匹配 `^[0-9]{6}$`；库里 `code_hash == tokenHash(code)` 且 `!= code`；`expires_at` == `stamp+5min`；`attempts` 0；`consumed_at` NULL。
2. 60 秒窗口：同邮箱立即再发 → `ErrTooFrequent`；库里仍只有 1 条记录。
3. `stamp` 推进 61 秒再发：成功，且旧记录 `consumed_at` 非空（作废）、新记录 `consumed_at` NULL。
4. 大小写归一：先对 `A.User@Example.COM` 发码成功，立即对 `  a.user@example.com `（带空格小写）再发 → `ErrTooFrequent`。
5. 非法邮箱：`""`、`"no-at"`、`"a@b"`、`"a b@c.co"` → `ErrInvalid`（`errors.Is`）。
6. `DeleteEmailCode` 后立即同邮箱再发：成功（60 秒窗口不再拦截，删的是刚建记录）。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/study/ -run EmailCode -v`
预期：FAIL，`undefined: (*Service).CreateEmailCode`

- [ ] **步骤 3：实现 `internal/study/email_code.go`**

`CreateEmailCode` 流程：`normalizeEmail` → `!validEmail` 返回 `errors.Join(ErrInvalid, errors.New("邮箱格式不正确"))` → `store.WriteTx` 内：`SELECT created_at FROM email_code WHERE email=? ORDER BY id DESC LIMIT 1`，若 > `now-60s` 返回 `ErrTooFrequent`；`UPDATE email_code SET consumed_at=? WHERE email=? AND consumed_at IS NULL` 作废旧码；`crypto/rand` 生成 `[0,1000000)` 整数 `fmt.Sprintf("%06d", ...)`；`INSERT INTO email_code(email,code_hash,expires_at,created_at) VALUES(?,?,?,?)`（hash 用同包 `tokenHash`，`expires_at` = now+5min）→ 返回 `(LastInsertId, 明文码)`。`DeleteEmailCode`：`store.WriteTx` 内 `DELETE FROM email_code WHERE id=?`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/study/ -v`
预期：新测试与既有测试全部 PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/study/email_code.go internal/study/email_code_test.go
git commit -m "feat: study 层邮件验证码生成与生命周期"
```

### 任务 5：Register 扩展与全部调用点更新

**文件：**
- 修改：`internal/study/auth.go`（`Register` 签名与流程）
- 修改（调用点）：`internal/serve/study.go:197-212`（register handler）、`internal/study/personal_auth_test.go:24,88`、`cmd/gk/backup_restore_test.go:40,112`、`internal/serve/personal_profile_test.go:310`、`internal/serve/practice_concurrency_test.go:40`、`internal/serve/study_test.go:74-85`（`account` helper）及其全部约 50 处 `account(t, ...)` 调用
- 测试：`internal/study/email_code_test.go`（追加）

**接口（后续任务依赖，逐字使用）：**

```go
var ErrEmailConflict = errors.New("该邮箱已被注册")

func (s *Service) Register(ctx context.Context, username, password, nickname, email, code string) (User, error)
```

- [ ] **步骤 1：编写失败的测试**

追加到 `internal/study/email_code_test.go`，辅助函数 `mustCode(t, s, email) (id int64, code string)` 内调 `CreateEmailCode` 并 `t.Fatal` on error：

1. 正确码注册成功：`Register(ctx, "mail_user", "testpassword", "", "a@b.co", code)` → `app_user.email == "a@b.co"`、`user_profile` 有行、该 `email_code` 行 `consumed_at` 非空。
2. 错误码：`Register(..., "000000")` → `ErrInvalid`（`errors.Is`）文案「验证码错误或已过期」；该记录 `attempts` == 1。
3. 5 次上限：连续错 4 次（attempts 1→4，每次 `ErrInvalid`），第 5 次错 → 文案「验证码错误次数过多，请重新获取」；第 6 次输入**正确码** → 仍「验证码错误次数过多，请重新获取」。
4. 用户名冲突回滚：用码 C 对用户名 `taken`（已存在）注册 → `ErrConflict`；验证码行 `consumed_at` 仍 NULL；换用户名用**同一码 C** 注册 → 成功（审查重点 4）。
5. 邮箱占用：账号甲绑 `dup@b.co` 后，发新码给 `DUP@B.CO`（归一化同邮箱）注册新用户名 → `ErrEmailConflict`（同时钉住归一化唯一约束）。
6. 过期码：`stamp` 推进 6 分钟 → 「验证码错误或已过期」。
7. 无记录：从未发码的邮箱 → 「验证码错误或已过期」。
8. 复用已消费码：注册成功后用同码再注册 → 「验证码错误或已过期」。
9. 非法邮箱：`Register(..., "bad", code)` → 「邮箱格式不正确」。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/study/ -run EmailCode -v`
预期：FAIL（新用例因当前 `Register` 不校验码而通过不了，或编译错）

- [ ] **步骤 3：实现 `Register` 新签名与流程**

保留现有用户名/昵称/密码校验与 bcrypt。追加：`email = normalizeEmail(email)`；`!validEmail(email)` → `errors.Join(ErrInvalid, errors.New("邮箱格式不正确"))`。验证码路径：
1. `SELECT id,code_hash,attempts FROM email_code WHERE email=? AND consumed_at IS NULL ORDER BY id DESC LIMIT 1`：无行或 `expires_at <= now` → `errors.Join(ErrInvalid, errors.New("验证码错误或已过期"))`。
2. `attempts >= 5` → `errors.Join(ErrInvalid, errors.New("验证码错误次数过多，请重新获取"))`。
3. `subtle.ConstantTimeCompare([]byte(tokenHash(code)), []byte(code_hash)) != 1`：单独 `store.WriteTx` 执行 `UPDATE email_code SET attempts=attempts+1 WHERE id=?`（必须提交），递增后 `attempts >= 5` 返回「验证码错误次数过多，请重新获取」否则「验证码错误或已过期」。
4. 建号 `store.WriteTx`：`SELECT COUNT(*) FROM app_user WHERE email=?` > 0 → `ErrEmailConflict`；`INSERT INTO app_user(username,password_hash,nickname,created_at,email)`（UNIQUE 错误按错误串含 `email` 归为 `ErrEmailConflict`，否则维持现有 `ErrConflict` 判定）；`INSERT user_profile`；`UPDATE email_code SET consumed_at=? WHERE id=?`。

- [ ] **步骤 4：更新全部调用点（保持编译与测试全绿）**

- `internal/serve/study.go` register handler：decode struct 加 `Email string \`json:"email"\``、`Code string \`json:"code"\``，调用改为 `s.Study.Register(r.Context(), in.Username, in.Password, in.Nickname, in.Email, in.Code)`。
- `internal/serve/study_test.go` `account` helper 改为 `func account(t *testing.T, s *Server, name string) *http.Cookie`：内部 `h := s.Routes()`；`s.Study.CreateEmailCode(ctx, name+"@test.local")` 拿码 → `POST /api/auth/register` 带 `email`+`code`（仍断言 201）→ `POST /api/auth/login` 返回 cookie。
- 机械替换两种调用模式：`account(t, h, "x")` → `account(t, s, "x")`；`account(t, s.Routes(), "x")` → `account(t, s, "x")`（可用 `grep -rl 'account(t,' internal/serve/` 配合 sed；替换后逐文件确认变量 `s` 在作用域内）。
- `internal/study/personal_auth_test.go`、`cmd/gk/backup_restore_test.go`、`internal/serve/personal_profile_test.go`、`internal/serve/practice_concurrency_test.go` 的直接调用：先 `CreateEmailCode` 拿码再传 5 参（如 `id, code, _ := s.CreateEmailCode(ctx, "cas_user@t.co"); s.Register(ctx, "cas_user", "old-password", "", "cas_user@t.co", code)`）。

- [ ] **步骤 5：运行全量测试验证通过**

运行：`go test ./...`
预期：全部 PASS（既有测试无行为回归）

- [ ] **步骤 6：Commit**

```bash
git add -A
git commit -m "feat: 注册强制邮箱验证码并更新全部调用点"
```

### 任务 6：serve email-code 端点、限流与错误映射

**文件：**
- 创建：`internal/serve/email_code.go`（`emailCode` handler + `mailConfig` helper）
- 修改：`internal/serve/study.go`（`userRoutes` 挂 `r.Post("/auth/email-code", s.emailCode)`；`studyError` 加两个分支）
- 修改：`internal/serve/auth_limit.go`（`auth_limit.go:70` 路径列表 + body 解析增加 email 键）
- 测试：`internal/serve/email_code_test.go`（新建，复用 `fixture`/`requestTest`）

- [ ] **步骤 1：编写失败的测试**

fixture 模式（`fixture(t)` 返回 `(s, db)`），测试内可注入 `s.Study.Clock`：

1. 开发降级：`POST /api/auth/email-code {"email":"dev@test.local"}` → 200 `{"ok":true}`；`email_code` 表新增 1 行。
2. 60 秒窗口：立即再发同请求 → 429 + `Retry-After: 60`；库里仍 1 行。
3. 发送失败清理（审查重点 2）：`s.Settings.Set` 把 `mail.smtp_host/port/from` 指向一个立即 `Close` 的 `net.Listen` 地址 → POST → 502「验证码发送失败，请稍后重试」；库里该邮箱 0 行（已删）；**立即再发**（把 SMTP 配置改回空串模拟降级）→ 200 不被窗口拦。
4. 端到端注册：`s.Study.CreateEmailCode(ctx, "e2e@test.local")` 拿明文码 → `POST /api/auth/register` 带 `username/password/email/code` → 201；同码再注册另一用户名 → 400。
5. register 缺验证码：`POST /api/auth/register` 不带 code → 400。
6. 邮箱枚举防护：先注册账号绑定 `probed@test.local`，再对同一邮箱 POST email-code → 200 `{"ok":true}`（与未注册邮箱响应完全一致，不泄露注册状态）。
7. authLimiter：对 31 个**互不相同**的邮箱（`u1@test.local`…`u31@test.local`，避免与 60 秒窗口的 429 混淆）逐个 POST email-code → 第 31 次 429（30 次/分钟/IP 上限）。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/serve/ -run EmailCode -v`
预期：FAIL，404（路由不存在）

- [ ] **步骤 3：实现**

- `internal/serve/email_code.go`：
  - `func (s *Server) mailConfig(ctx context.Context) (mail.Config, error)`：`s.Settings.Get` 读 5 键组装。
  - `func (s *Server) emailCode(w, r)`：decode `{"email"}` → `s.Study.CreateEmailCode` 拿 `(id, code)`（错误走 `s.studyError`）→ `mailConfig` → `!cfg.Configured()`：`s.Log.Warn("开发降级：邮件验证码仅打印日志", "email", 归一化邮箱, "code", code)`，`writeJSON(w, 200, {"ok": true})` → `mail.Send(ctx, cfg, email, "行测研习注册验证码", fmt.Sprintf("你的验证码是 %s，5 分钟内有效。若非本人操作，请忽略此邮件。", code))` 失败：`s.Study.DeleteEmailCode(r.Context(), id)`（忽略删除错误）+ `writeErr(w, 502, "验证码发送失败，请稍后重试")`；成功 200。
- `internal/serve/study.go` `studyError` 加：`errors.Is(err, study.ErrTooFrequent)` → `Retry-After: 60` + 429；`errors.Is(err, study.ErrEmailConflict)` → 409。
- `internal/serve/auth_limit.go`：路径条件加 `p != "/api/auth/email-code"`；body 解析 struct 加 `Email string \`json:"email"\``，email 非空时追加键 `"account:"+p+":"+hex(sha256(email))`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/serve/ -v`
预期：新测试与既有测试全部 PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/serve/
git commit -m "feat: 邮箱验证码发送端点与限流接入"
```

### 任务 7：admin 测试邮件端点

**文件：**
- 修改：`internal/serve/server.go`（admin 组挂 `r.Post("/admin/test-mail", s.handleTestMail)`，`server.go:70-80`）
- 修改：`internal/serve/email_code.go`（追加 `handleTestMail`）
- 测试：`internal/serve/email_code_test.go`（追加）

- [ ] **步骤 1：编写失败的测试**

管理员准备：`admin.Create(ctx, db, "admin", "admin-password")` + `admin.Login` 拿 token，请求带 `Authorization: Bearer <token>` 头：

1. 未配置：POST `/api/admin/test-mail {"to":"a@b.co"}` → 400「请先在设置中配置 SMTP」。
2. 已配置：`net.Listen` 起假 SMTP（复用任务 3 的假服务器写法，导出为两个测试共用的小 helper 或在本包内再写一个精简版），`s.Settings.Set` 三个非空键指向它 → POST → 200 `{"ok":true,"latency_ms":≥0}`。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/serve/ -run TestMail -v`
预期：FAIL，404

- [ ] **步骤 3：实现 `handleTestMail`**

decode `{"to"}` → `mailConfig` → `!cfg.Configured()` → `writeErr(w, 400, "请先在设置中配置 SMTP")` → `time.Now()` 计时调 `mail.Send(ctx, cfg, in.To, "行测研习测试邮件", "这是一封测试邮件，收到即表示邮件服务配置正常。")` 失败 `writeErr(w, http.StatusBadGateway, err.Error())`；成功 `writeJSON(w, 200, {"ok":true, "latency_ms": time.Since(start).Milliseconds()})`。路由挂入 `Routes()` 的 requireAdmin 组。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/serve/ -v`
预期：PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/serve/
git commit -m "feat: 管理后台测试邮件端点"
```

### 任务 8：前端注册表单（api.js + Login.vue）

**文件：**
- 修改：`web/src/api.js`（`api.js:5` 排除列表；API 对象加 `sendEmailCode`）
- 修改：`web/src/views/Login.vue`

- [ ] **步骤 1：修改 `web/src/api.js`**

- 第 5 行排除数组加入 `'/api/auth/email-code'`（公开端点不带身份头）。
- API 对象加：`sendEmailCode: email => post('/api/auth/email-code', { email })`（`register: data => post(...)` 已是对象形式，无需改）。

- [ ] **步骤 2：修改 `web/src/views/Login.vue`**

- 新 state：`email = ref('')`、`code = ref('')`、`countdown = ref(0)`、`sending = ref(false)`。
- `sendCode()`：`email` 为空或不含 `@` → `error.value = '请先填写邮箱'`；否则 `sending=true` 调 `api.sendEmailCode(email.value)`；成功 → `countdown=60`，`setInterval` 每秒递减至 0 时 `clearInterval`；失败 → `error.value = e.message`。
- `submit()` 注册分支：`api.register({ username: username.value, password: password.value, nickname: nickname.value, email: email.value, code: code.value })`。
- 模板（`register && !admin` 时显示）：邮箱 `<label>邮箱<input v-model="email" type="email" required maxlength="254" autocomplete="email" /></label>`；验证码行 = `<input v-model="code" required maxlength="6" inputmode="numeric" pattern="[0-9]{6}" placeholder="6 位数字" />` + 发送按钮 `:disabled="sending || countdown > 0"`，文案 `countdown > 0 ? countdown + 's 后可重发' : sending ? '发送中…' : '发送验证码'`。样式复用现有 `login-card` 内 label/按钮类，不新增 CSS 文件。
- 切换登录/注册（`register=!register`）时清空 `email`/`code`/`countdown`（clearInterval）。

- [ ] **步骤 3：构建验证**

运行：`npm run build`（workdir `web/`）
预期：构建成功，产物输出到 `cmd/gk/web_dist`

- [ ] **步骤 4：Commit**

```bash
git add web/src/api.js web/src/views/Login.vue
git commit -m "feat: 注册表单支持邮箱验证码"
```

### 任务 9：管理设置页邮件区块

**文件：**
- 修改：`web/src/api.js`（API 对象加 `testMail`）
- 修改：`web/src/views/Settings.vue`

- [ ] **步骤 1：修改 `web/src/api.js`**

API 对象加：`testMail: to => post('/api/admin/test-mail', { to })`。

- [ ] **步骤 2：修改 `web/src/views/Settings.vue`**

- `form` 加 5 键初值 `''`：`mail.smtp_host`、`mail.smtp_port`、`mail.smtp_user`、`mail.smtp_password`、`mail.from`。
- 仿 `keyIsSet` 模式加 `passwordIsSet`：`load()` 中 `mail.smtp_password === MASK` 时置位并回填 MASK；`save()` 中该键等于 MASK 时从 payload 删除（后端保持原值）。
- 新增 `<section class="card">`「邮件服务」（放在「LLM 接口」卡片后）：字段 SMTP 服务器（placeholder `smtp.example.com`）、端口（placeholder `465` 或 `587`）、账号、密码（`type="password"` + `keyPlaceholder` 同款提示「已保存（留空或保持不变即可）」）、发件人（placeholder `noreply@example.com`）；「测试收件地址」输入 + `mailTestResult` ref；按钮 `testMail()` 调 `api.testMail(to)`，结果区仿 `testResult` 结构展示 `{ok, latency_ms}` 或错误文本。保存按钮复用现有 `save()`（同一 form 提交）。
- 顶部说明文案：`<p class="muted small">未配置时注册验证码仅打印到服务端日志（开发降级）。</p>`

- [ ] **步骤 3：构建验证**

运行：`npm run build`（workdir `web/`），随后 `make build` 确认嵌入编译通过。
预期：均成功

- [ ] **步骤 4：Commit**

```bash
git add web/src/api.js web/src/views/Settings.vue
git commit -m "feat: 管理设置页增加邮件服务配置"
```

### 任务 10：全量回归

- [ ] **步骤 1：后端全量**

运行：`go vet ./... && go test ./...`
预期：无告警、全部 PASS

- [ ] **步骤 2：前端构建**

运行：`npm run build`（workdir `web/`）+ `make build`
预期：成功

- [ ] **步骤 3：手动/Playwright 抽查（可选）**

启动 `make serve`（`:8081`），走一遍注册流：填邮箱→发送（观察服务端日志中的验证码）→填码注册→登录成功；管理后台配置假 SMTP→测试邮件 502 提示正常。若需要留存回归脚本，仿 `test-artifacts/personal_center_regression.py` 模式（非必须）。
