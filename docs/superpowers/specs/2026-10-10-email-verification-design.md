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
- `consumed_at` 非空表示已使用，防止验证码复用；发送新验证码时，同一邮箱此前所有未使用记录也标记为 `consumed_at`（作废），保证**每邮箱同时至多一个有效验证码**，注册时无匹配歧义。
- `attempts` 记录尝试次数，上限 5 次。

## 4. API 设计

### 4.1 新增 `POST /api/auth/email-code`（公开）

请求：`{"email": "user@example.com"}`
响应：`200 {"ok": true}`

逻辑（`study.CreateEmailCode` 写库 + serve 层发送编排）：
1. 校验邮箱格式：总长 ≤ 254、含单个 `@`、本地部分与域名非空、域名含点号（RFC 5322 简化校验）。
2. `store.WriteTx` 内查该邮箱最近一条记录，若 `created_at` 在 60 秒内 → 429「发送过于频繁，请稍后再试」；否则作废旧码并插入新记录：生成 6 位数字验证码（`crypto/rand`），SHA-256 哈希入库（`expires_at` = now + 5min），返回 `(id, 明文验证码)`。明文仅存在于内存与邮件正文，不落库。
3. serve 层加载邮件配置并同步调用 `mail.Send`：
   - 返回 `ErrNotConfigured`（未配置 SMTP，开发降级）→ 服务端日志打印验证码，返回 200 `{"ok": true}`，保证开发环境流程可走通。
   - 发送失败 → 删除刚插入的记录（`study.DeleteEmailCode(id)`，避免未送达的码占用 60 秒重发窗口），返回 502「验证码发送失败，请稍后重试」。
   - 发送成功 → 200 `{"ok": true}`。

### 4.2 修改 `POST /api/auth/register`

请求增加字段：`{"username", "password", "nickname", "email", "code"}`
响应不变：`201 {"id", "username", "nickname"}`

逻辑（`study.Register` 扩展）：
1. 校验邮箱格式（同 4.1）。
2. 取该邮箱当前有效记录（`consumed_at IS NULL` 且最新一条；由 4.1 的作废规则，至多一条）：不存在或已过期 → 400「验证码错误或已过期」。
3. 验证码比对（SHA-256 哈希 + `subtle.ConstantTimeCompare`）：不匹配 → 单独短事务 `attempts++` 后返回 400「验证码错误或已过期」；`attempts` 达 5 → 400「验证码错误次数过多，请重新获取」。尝试计数必须提交，即使注册未成功。
4. 校验用户名/密码（复用现有规则）。
5. 验证通过后进入建号事务（`store.WriteTx`）：插入 `app_user`（含 email）+ `user_profile` + 标记 `email_code.consumed_at`；任何一步失败（如用户名冲突）整个事务回滚，`consumed_at` 保持 NULL，验证码仍可用。
6. 邮箱 UNIQUE 冲突 → 409「该邮箱已被注册」。

错误映射：`studyError` 新增 `ErrTooFrequent` → 429；邮件相关 502/开发降级在 email-code handler 内处理，不进 `studyError`。

### 4.3 限流

`/api/auth/email-code` 加入 `authLimiter` 路径列表（`auth_limit.go:70`），复用 30 次/分钟/IP+账号限流。

## 5. 邮件子系统（`internal/mail` 包）

新建 `internal/mail/mail.go`，纯 SMTP 客户端，不依赖数据库——配置由调用方（serve 层，已持有 `setting.Store`）加载后传入，便于独立测试：

```go
package mail

type Config struct {
    Host     string // mail.smtp_host
    Port     string // mail.smtp_port
    Username string // mail.smtp_user
    Password string // mail.smtp_password（敏感）
    From     string // mail.from
}

// Configured 判断是否已配置（host/port/from 非空即视为已配置）。
func (c Config) Configured() bool

var ErrNotConfigured = errors.New("SMTP 未配置")

// Send 同步发送纯文本邮件。未配置返回 ErrNotConfigured。
func Send(ctx context.Context, cfg Config, to, subject, body string) error
```

- `Send` 用标准库 `net/smtp`：端口 465 用 `tls.Dial`（隐式 TLS）；其余端口 `smtp.Dial` + 服务器支持时升级 STARTTLS。`Username` 非空时执行 `smtp.PlainAuth` 认证，拒绝在无 TLS 连接上发送认证。
- 邮件内容 `text/plain; charset=UTF-8`，主题用 RFC 2047 编码（中文主题）。
- serve 层加载配置（`s.Settings.Get`），未配置时捕获 `ErrNotConfigured` 打印日志（开发降级）；真实发送失败返回 502。

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
- 4 个非敏感键以空字符串加入 `Defaults`，保证管理后台设置接口（`All()`）始终返回邮件键。
- 管理后台设置页（`web/src/views/Settings.vue`）新增「邮件服务」区块，含 SMTP 主机/端口/账号/密码/发件人。
- 新增 `POST /api/admin/test-mail`（复用 `handleTestLLM` 模式，server.go:205）：管理后台「测试」按钮向指定地址发送测试邮件，验证配置连通性。
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
- `internal/mail/mail_test.go`：`net.Listen` 起本地假 SMTP 服务器验证 `Send`（含 STARTTLS 不可用降级）；未配置时返回 `ErrNotConfigured`。
- 前端：注册表单交互（倒计时、错误提示），复用 Playwright E2E 模式。
- 运行：`make test`（`go test ./...`）。

## 10. 影响文件清单

| 文件 | 改动 |
|---|---|
| `internal/store/schema.go` | 追加 v13 迁移 |
| `internal/setting/setting.go` | 新增 5 个配置键 + isSecretKey |
| `internal/mail/mail.go` | 新建纯 SMTP 客户端包（Config + Send） |
| `internal/study/auth.go` | 修改 Register 签名（增加 email、code 参数与校验） |
| `internal/study/email_code.go` | 新建验证码业务（CreateEmailCode / DeleteEmailCode / 校验逻辑） |
| `internal/serve/study.go` | 新增 email-code 端点；修改 register 端点；studyError 增加 ErrTooFrequent→429 |
| `internal/serve/auth_limit.go` | 限流路径加入 email-code |
| `internal/serve/server.go` | 新增 `POST /api/admin/test-mail` 端点 |
| `web/src/views/Login.vue` | 注册表单加邮箱+验证码 |
| `web/src/api.js` | 新增 sendEmailCode |
| `web/src/views/Settings.vue` | 邮件服务配置区块 + 测试按钮 |
