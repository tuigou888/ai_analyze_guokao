# 邮件验证码注册设计规格

日期：2026-10-10。状态：用户已批准方案 A 及全部设计章节，按本文进入实现计划编写。

## 1. 目标与已确认范围

为普通用户注册流程增加邮箱验证：注册时必须绑定一个邮箱，并通过该邮箱接收 6 位数字验证码，验证通过后方可完成注册。用户名仍为登录标识，邮箱唯一绑定到账号。

用户已确认包含：邮箱唯一绑定、单表单一步提交注册、6 位数字验证码（5 分钟有效、5 次尝试上限、60 秒重发间隔）、SMTP 配置存管理后台 setting 表、未配置 SMTP 时开发降级（打印日志）、验证码存数据库。

成功标准：新用户注册必须通过邮箱验证码验证；未配置 SMTP 时可开发调试；配置 SMTP 后生产环境正常发送；既有账号与登录流程不受影响。

明确排除：存量账号邮箱补绑、邮箱登录（登录仍用用户名）、找回密码、异步发送、图形验证码、第三方登录。

## 2. 现有实现与约束

- 后端 Go + Chi + modernc SQLite，业务在 `internal/study`，HTTP 在 `internal/serve`；迁移只追加，当前最新 v12。
- `app_user`（v7）已有 username/password_hash/nickname/created_at/last_login_at；`app_session`（v12）有 public_id/device_label/ip_hint。
- `Register`（`internal/study/auth.go:50-86`）在单事务内插入 app_user + user_profile，无邮箱字段。
- `internal/store/schema.go` 的 `migrations` 按版本序排列，每版单事务应用。
- `internal/setting/setting.go` 管理敏感配置（AES-256-GCM 加密），`isSecretKey` 判定敏感键；`handlePutSettings`（`internal/serve/server.go:179`）支持掩码保持原值。
- `authLimiter`（`internal/serve/auth_limit.go:70`）对 `/api/auth/login|register` 限流 30 次/分钟/IP+账号。
- 前端 `web/src/views/Login.vue` 注册表单提交 `api.register({username, password, nickname})`。
- 项目无任何邮件/验证码基础设施；无 Redis；无 ORM；手写网络协议先例（`internal/llm/client.go`）。
- 普通用户与管理员认证体系完全分离，本轮只改普通用户体系。

## 3. 数据模型（v13 迁移）

在 `internal/store/schema.go` 的 `migrations` 末尾追加：

```sql
ALTER TABLE app_user ADD COLUMN email TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_app_user_email ON app_user(email) WHERE email != '';
CREATE TABLE email_code (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL,
  code_hash TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  consumed_at TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX idx_email_code_email ON email_code(email, created_at DESC);
```

- `app_user.email` 默认空串 + 部分唯一索引（`WHERE email != ''`）：存量账号不受影响，新注册邮箱唯一。
- 验证码存 SHA-256 哈希（与 `app_session.token` 存储策略一致）。
- `consumed_at` 非空表示已使用，防止验证码复用。
- `attempts` 记录尝试次数，上限 5 次。

## 4. API 设计

### 4.1 新增 `POST /api/auth/email-code`（公开）

请求：`{"email": "user@example.com"}`
响应：`200 {"ok": true}`

逻辑：
1. 校验邮箱格式（RFC 5322 简化校验）。
2. 查 `email_code` 表该邮箱最近一条记录，若 `created_at` 在 60 秒内 → 429「发送过于频繁，请稍后再试」。
3. 生成 6 位数字验证码（`crypto/rand`），SHA-256 哈希后写入 `email_code`（`expires_at` = now + 5min）。
4. 同步调用 `mail.Send`；未配置 SMTP 时打印日志（开发模式）；发送失败 → 502「验证码发送失败，请稍后重试」。

### 4.2 修改 `POST /api/auth/register`

请求增加字段：`{"username", "password", "nickname", "email", "code"}`
响应不变：`201 {"id", "username", "nickname"}`

逻辑（单事务 `store.WriteTx`）：
1. 校验邮箱格式。
2. 校验验证码：`email` 匹配、`consumed_at IS NULL`、`expires_at > now`。
3. 验证码比对（SHA-256 哈希 + `subtle.ConstantTimeCompare`）；失败则 `attempts++`，达 5 次 → 400「验证码错误次数过多，请重新获取」。
4. 校验用户名/密码（复用现有规则）。
5. 插入 `app_user`（含 email）+ `user_profile` + 更新 `email_code.consumed_at`。
6. 邮箱 UNIQUE 冲突 → 409「该邮箱已被注册」。

### 4.3 限流

`/api/auth/email-code` 加入 `authLimiter` 路径列表（`auth_limit.go:70`），复用 30 次/分钟/IP+账号限流。

## 5. 邮件子系统（`internal/mail` 包）

新建 `internal/mail/mail.go`：

```go
type Config struct {
    Host     string // mail.smtp_host
    Port     string // mail.smtp_port
    Username string // mail.smtp_user
    Password string // mail.smtp_password（敏感）
    From     string // mail.from
}

var ErrNotConfigured = errors.New("SMTP 未配置")

type Sender struct { db *sql.DB }

func New(db *sql.DB) *Sender
func (s *Sender) LoadConfig(ctx context.Context) (Config, error)
func (s *Sender) Send(ctx context.Context, to, subject, body string) error
```

- `LoadConfig` 从 setting 表读取 5 个配置键。
- `Send` 用标准库 `net/smtp`：Dial → 端口 465 用 TLS 包裹 / 端口 587 STARTTLS → `smtp.PlainAuth` → `Mail`/`Rcpt`/`Data`。
- 邮件内容 `text/plain; charset=UTF-8`，主题用 RFC 2047 编码（中文主题）。
- 未配置 SMTP 返回 `ErrNotConfigured`；serve 层捕获后打印日志（开发模式），生产环境发送失败返回 502。

## 6. 配置设计

`internal/setting/setting.go` 新增键：

| 键 | 说明 | 敏感 |
|---|---|---|
| `mail.smtp_host` | SMTP 主机 | 否 |
| `mail.smtp_port` | SMTP 端口 | 否 |
| `mail.smtp_user` | SMTP 认证用户名 | 否 |
| `mail.smtp_password` | SMTP 认证密码 | 是（AES-256-GCM 加密） |
| `mail.from` | 发件人地址 | 否 |

- `KeySMTPPassword` 加入 `isSecretKey`（`setting.go:241-243`）。
- 管理后台设置页新增「邮件服务」区块。
- 管理后台测试按钮复用 `handleTestLLM` 模式（`server.go:205`），发送测试邮件。
- 默认值全部空字符串（未配置 = 开发模式）。

## 7. 前端设计

### 7.1 `web/src/views/Login.vue` 注册表单

新增：
- 邮箱输入框（`type="email"`，`required`）。
- 验证码输入框（6 位数字）+「发送验证码」按钮（点击后 60 秒倒计时禁用）。
- 注册提交时携带 `email` + `code`。

### 7.2 `web/src/api.js`

新增：
```js
export async function sendEmailCode(email) {
  return api.post('/auth/email-code', { email })
}
```

### 7.3 交互流程

1. 用户填邮箱 → 点「发送验证码」→ `POST /api/auth/email-code`。
2. 填用户名/密码/验证码 → 点「注册并登录」→ `POST /api/auth/register`（带 email+code）。
3. 成功后走现有登录逻辑（`setUser` + 路由跳转）。
4. 错误复用现有 `error` ref 展示。

## 8. 安全设计

| 威胁 | 防护 |
|---|---|
| 验证码明文泄露 | SHA-256 哈希存库 |
| 暴力破解 | 5 次尝试上限 |
| 重放攻击 | `consumed_at` 标记已使用 |
| 长期有效 | 5 分钟过期 |
| 邮件轰炸 | 60 秒重发间隔 + authLimiter 限流 |
| 邮箱枚举 | 发送接口不区分邮箱是否已注册 |
| 时序侧信道 | `subtle.ConstantTimeCompare` 比对 |
| 邮箱格式注入 | 服务端格式校验 + 参数化查询 |

注册接口返回 409「该邮箱已被注册」是注册场景的必要信息，可接受。

## 9. 测试策略

- `internal/study/email_code_test.go`：验证码生成/校验（过期、尝试次数、consumed_at 防重放）、注册事务（邮箱冲突 409、验证码错误 400、成功创建）。
- `internal/serve/email_code_test.go`：发送接口限流（60 秒重发、429）、端点路由。
- `internal/mail/mail_test.go`：`httptest` mock SMTP 服务器验证 `Send`；未配置时返回 `ErrNotConfigured`。
- 前端：注册表单交互（倒计时、错误提示），复用 Playwright E2E 模式。
- 运行：`make test`（`go test ./...`）。

## 10. 影响文件清单

| 文件 | 改动 |
|---|---|
| `internal/store/schema.go` | 追加 v13 迁移 |
| `internal/setting/setting.go` | 新增 5 个配置键 + isSecretKey |
| `internal/mail/mail.go` | 新建邮件发送包 |
| `internal/study/auth.go` | 修改 Register（邮箱+验证码校验）；新增 SendEmailCode |
| `internal/study/email_code.go` | 新建验证码业务逻辑 |
| `internal/serve/study.go` | 新增 email-code 端点；修改 register 端点 |
| `internal/serve/auth_limit.go` | 限流路径加入 email-code |
| `internal/serve/server.go` | 测试邮件按钮（可选） |
| `web/src/views/Login.vue` | 注册表单加邮箱+验证码 |
| `web/src/api.js` | 新增 sendEmailCode |
| `web/src/views/AdminSettings.vue` | 邮件配置区块 |
