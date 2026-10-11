package serve

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai_analyze_guokao/internal/setting"
)

func TestEmailCodeEndpoint(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	h := s.Routes()

	// 1. 开发降级：未配置 SMTP 时返回 200，email_code 表新增 1 行。
	w, v := requestTest(t, h, "POST", "/api/auth/email-code", `{"email":"dev@test.local"}`, nil)
	if w.Code != 200 || v["ok"] != true {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_code WHERE email='dev@test.local'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}

	// 2. 60 秒窗口：立即再发同请求 → 429。
	w, _ = requestTest(t, h, "POST", "/api/auth/email-code", `{"email":"dev@test.local"}`, nil)
	if w.Code != 429 {
		t.Fatal("expected 429", w.Code, w.Body.String())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_code WHERE email='dev@test.local'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("new code created within window", n)
	}

	// 3. 发送失败清理：配置一个立即关闭的 SMTP 地址 → 502；库里 0 行；立即再发 → 200。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	host, port, _ := net.SplitHostPort(addr)
	if err := s.Settings.Set(ctx, setting.KeySMTPHost, host, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.Settings.Set(ctx, setting.KeySMTPPort, port, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.Settings.Set(ctx, setting.KeyMailFrom, "noreply@test.local", "test"); err != nil {
		t.Fatal(err)
	}
	w, _ = requestTest(t, h, "POST", "/api/auth/email-code", `{"email":"fail@test.local"}`, nil)
	if w.Code != 502 {
		t.Fatal("expected 502", w.Code, w.Body.String())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_code WHERE email='fail@test.local'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed code not cleaned up", n)
	}
	s.Settings.Set(ctx, setting.KeySMTPHost, "", "test")
	s.Settings.Set(ctx, setting.KeySMTPPort, "", "test")
	s.Settings.Set(ctx, setting.KeyMailFrom, "", "test")
	w, _ = requestTest(t, h, "POST", "/api/auth/email-code", `{"email":"fail@test.local"}`, nil)
	if w.Code != 200 {
		t.Fatal("expected 200 after cleanup", w.Code, w.Body.String())
	}

	// 4. 端到端注册：CreateEmailCode 拿码 → register 带 email+code → 201；同码再注册 → 400。
	_, code, err := s.Study.CreateEmailCode(ctx, "e2e@test.local")
	if err != nil {
		t.Fatal(err)
	}
	w, _ = requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"e2e_user","password":"testpassword","nickname":"","email":"e2e@test.local","code":%q}`, code), nil)
	if w.Code != 201 {
		t.Fatal("register failed", w.Code, w.Body.String())
	}
	w, _ = requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"e2e_user2","password":"testpassword","nickname":"","email":"e2e@test.local","code":%q}`, code), nil)
	if w.Code != 400 {
		t.Fatal("expected 400 for reused code", w.Code, w.Body.String())
	}

	// 5. register 缺验证码 → 400。
	w, _ = requestTest(t, h, "POST", "/api/auth/register", `{"username":"nocode_user","password":"testpassword","nickname":"","email":"nocode@test.local","code":""}`, nil)
	if w.Code != 400 {
		t.Fatal("expected 400 for missing code", w.Code, w.Body.String())
	}

	// 6. 邮箱枚举防护：已注册邮箱再发码 → 200（与未注册邮箱响应一致）。
	w, _ = requestTest(t, h, "POST", "/api/auth/email-code", `{"email":"existing@test.local"}`, nil)
	if w.Code != 200 {
		t.Fatal("expected 200 for existing email", w.Code, w.Body.String())
	}
}

func TestEmailCodeRateLimit(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	for i := 1; i <= 30; i++ {
		email := fmt.Sprintf("u%d@test.local", i)
		w, _ := requestTest(t, h, "POST", "/api/auth/email-code", fmt.Sprintf(`{"email":%q}`, email), nil)
		if w.Code != 200 {
			t.Fatalf("email %d: expected 200, got %d", i, w.Code)
		}
	}
	w, _ := requestTest(t, h, "POST", "/api/auth/email-code", `{"email":"u31@test.local"}`, nil)
	if w.Code != 429 {
		t.Fatal("expected 429 at 31st request", w.Code, w.Body.String())
	}
}

func TestEmailCodeInvalidFormat(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	for _, bad := range []string{"", "no-at", "a@b", "a b@c.co"} {
		body := fmt.Sprintf(`{"email":%q}`, bad)
		w, _ := requestTest(t, h, "POST", "/api/auth/email-code", body, nil)
		if w.Code != 400 {
			t.Fatalf("email %q: expected 400, got %d", bad, w.Code)
		}
	}
}

func TestEmailCodeMethodNotAllowed(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	w, _ := requestTest(t, h, "GET", "/api/auth/email-code", "", nil)
	if w.Code != 405 {
		t.Fatal("expected 405", w.Code)
	}
}

func TestEmailCodeOriginCheck(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	r := httptest.NewRequest("POST", "/api/auth/email-code", strings.NewReader(`{"email":"a@b.co"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://evil.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("expected 403 for cross-origin", w.Code)
	}
}

func TestEmailCodeRegisterEmailConflict(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	h := s.Routes()
	_, code, _ := s.Study.CreateEmailCode(ctx, "conflict@test.local")
	requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"conflict_user","password":"testpassword","nickname":"","email":"conflict@test.local","code":%q}`, code), nil)
	// 推进时钟 61 秒绕过 60 秒重发窗口，再发同邮箱的码。
	origClock := s.Study.Clock
	s.Study.Clock = func() time.Time { return time.Now().Add(61 * time.Second) }
	_, code2, _ := s.Study.CreateEmailCode(ctx, "conflict@test.local")
	s.Study.Clock = origClock
	w, _ := requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"conflict_user2","password":"testpassword","nickname":"","email":"conflict@test.local","code":%q}`, code2), nil)
	if w.Code != 409 {
		t.Fatal("expected 409 for email conflict", w.Code, w.Body.String())
	}
}

func TestEmailCodeRegisterInvalidCode(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	w, _ := requestTest(t, h, "POST", "/api/auth/register", `{"username":"bad_code_user","password":"testpassword","nickname":"","email":"badcode@test.local","code":"000000"}`, nil)
	if w.Code != 400 {
		t.Fatal("expected 400 for invalid code", w.Code, w.Body.String())
	}
}

func TestEmailCodeRegisterExpiredCode(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	h := s.Routes()
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC) }
	_, code, _ := s.Study.CreateEmailCode(ctx, "expired@test.local")
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 10, 12, 6, 0, 0, time.UTC) }
	w, _ := requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"expired_user","password":"testpassword","nickname":"","email":"expired@test.local","code":%q}`, code), nil)
	if w.Code != 400 {
		t.Fatal("expected 400 for expired code", w.Code, w.Body.String())
	}
}

func TestEmailCodeRegisterAttemptsLimit(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	h := s.Routes()
	_, code, _ := s.Study.CreateEmailCode(ctx, "attempts@test.local")
	for i := 0; i < 5; i++ {
		w, _ := requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"attempts_user_%d","password":"testpassword","nickname":"","email":"attempts@test.local","code":"000000"}`, i), nil)
		if w.Code != 400 {
			t.Fatalf("attempt %d: expected 400, got %d", i+1, w.Code)
		}
	}
	w, _ := requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"attempts_user_final","password":"testpassword","nickname":"","email":"attempts@test.local","code":%q}`, code), nil)
	if w.Code != 400 {
		t.Fatal("expected 400 after 5 attempts", w.Code, w.Body.String())
	}
}

func TestEmailCodeRegisterUsernameConflict(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	h := s.Routes()
	_, code, _ := s.Study.CreateEmailCode(ctx, "conflict3@test.local")
	requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"conflict3_user","password":"testpassword","nickname":"","email":"conflict3@test.local","code":%q}`, code), nil)
	origClock := s.Study.Clock
	s.Study.Clock = func() time.Time { return time.Now().Add(61 * time.Second) }
	_, code2, _ := s.Study.CreateEmailCode(ctx, "conflict3@test.local")
	s.Study.Clock = origClock
	w, _ := requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":"conflict3_user","password":"testpassword","nickname":"","email":"conflict3@test.local","code":%q}`, code2), nil)
	if w.Code != 409 {
		t.Fatal("expected 409 for username conflict", w.Code, w.Body.String())
	}
}
