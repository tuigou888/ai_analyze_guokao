package serve

import (
	"ai_analyze_guokao/internal/admin"
	"ai_analyze_guokao/internal/setting"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// failHandler 模拟认证失败（401），用于驱动"只对失败计数"的限速。
func failHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
}

func TestAuthLimiterIgnoresSpoofedForwardedHeaders(t *testing.T) {
	h := newAuthLimiter().middleware(failHandler())
	blocked := 0
	for i := 0; i < 35; i++ {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(fmt.Sprintf(`{"username":"user%d"}`, i)))
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == 429 {
			blocked++
		}
	}
	// IP 桶预算 30：第 31 次起拒绝。
	if blocked != 5 {
		t.Fatalf("spoof bypass: expected 5 blocked, got %d", blocked)
	}
}

func TestTrustedProxyUsesRightmostUntrustedHop(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.99, 192.0.2.5")
	if got := clientIP(r, []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}); got != "192.0.2.5" {
		t.Fatal(got)
	}
	if got := clientIP(r, nil); got != "127.0.0.1" {
		t.Fatal(got)
	}
	r.Header.Set("X-Forwarded-For", "invalid")
	if got := clientIP(r, []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}); got != "127.0.0.1" {
		t.Fatal(got)
	}
}

func TestAuthLimiterBoundsOneAccountAcrossIPs(t *testing.T) {
	h := newAuthLimiter().middleware(failHandler())
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"username":"admin"}`))
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i == 10 && w.Code != 429 {
			t.Fatal("distributed account guesses accepted")
		}
	}
}

// H-1：打满一个接口不得影响其余认证接口。
func TestAuthLimiterBucketsArePerEndpoint(t *testing.T) {
	h := newAuthLimiter().middleware(failHandler())
	for i := 0; i < 31; i++ {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(fmt.Sprintf(`{"username":"u%d"}`, i)))
		r.RemoteAddr = "192.0.2.9:1234"
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	for _, path := range []string{"/api/admin/login", "/api/auth/register", "/api/auth/email-code"} {
		body := `{"username":"someone"}`
		if path == "/api/auth/email-code" {
			body = `{"email":"a@b.co"}`
		}
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.RemoteAddr = "192.0.2.9:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == 429 {
			t.Fatalf("%s collateral-blocked by /api/auth/login", path)
		}
	}
}

// H-1：成功登录清零账号桶，受害者被恶意锁定时可自助恢复。
func TestAuthLimiterSuccessResetsAccountBucket(t *testing.T) {
	var status = 401
	h := newAuthLimiter().middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	attempt := func() int {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"victim"}`))
		r.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 10; i++ {
		if c := attempt(); c != 401 {
			t.Fatalf("attempt %d: %d", i, c)
		}
	}
	if c := attempt(); c != 429 {
		t.Fatalf("account bucket not full: %d", c)
	}
	// 账号被锁期间正确口令同样 429——这是账号维度的表现。
	status = 200
	if c := attempt(); c != 429 {
		t.Fatalf("locked account accepted: %d", c)
	}
}

// 成功路径会清账号桶：失败未满 → 成功 → 再失败未满，不应被锁。
func TestAuthLimiterSuccessClearsAccountFailures(t *testing.T) {
	status := 401
	l := newAuthLimiter()
	h := l.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	attempt := func(ip string) int {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"victim"}`))
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 9; i++ {
		if c := attempt("192.0.2.1"); c != 401 {
			t.Fatalf("failure %d: %d", i, c)
		}
	}
	status = 200
	if c := attempt("192.0.2.2"); c != 200 {
		t.Fatalf("login before reset: %d", c)
	}
	status = 401
	for i := 0; i < 9; i++ {
		if c := attempt("192.0.2.1"); c != 401 {
			t.Fatalf("bucket not reset by success at failure %d: %d", i, c)
		}
	}
}

func TestAuthLimiterDoesNotRetainLargeUsernames(t *testing.T) {
	l := newAuthLimiter()
	h := l.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	for i := 0; i < 4; i++ {
		body := fmt.Sprintf(`{"username":"%s%d"}`, strings.Repeat("a", 512*1024), i)
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
		r.RemoteAddr = "192.0.2.1:1234"
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	retained := 0
	for _, b := range l.account {
		for key := range b.entries {
			retained += len(key)
		}
	}
	if retained > 8192 {
		t.Fatalf("retained body-sized limit keys: %d bytes", retained)
	}
}

func TestSameOriginRejectsCrossPortAndScheme(t *testing.T) {
	h := sameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, origin := range []string{"https://practice.example.com:8443", "http://practice.example.com", "https://evil.example", ""} {
		r := httptest.NewRequest("POST", "https://practice.example.com/api/admin/password", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("accepted origin %q: %d", origin, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "https://practice.example.com/api/admin/password", nil)
	r.Header.Set("Origin", "https://practice.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("same origin rejected", w.Code)
	}
}

// M-3：所有响应都带 CSP / Permissions-Policy，HTTPS 时额外带 HSTS。
func TestSecurityHeadersAlwaysEmitted(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	r := httptest.NewRequest("GET", "https://practice.example.com/", nil)
	r.TLS = &tls.ConnectionState{}
	r.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	(&Server{}).sameOrigin(next).ServeHTTP(w, r)
	for _, h := range []string{"Content-Security-Policy", "Permissions-Policy", "Strict-Transport-Security"} {
		if w.Header().Get(h) == "" {
			t.Fatalf("missing %s", h)
		}
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") || !strings.Contains(got, "script-src 'self'") {
		t.Fatal(got)
	}

	// 明文直连不下发 HSTS——浏览器会把它当成对该主机的承诺，反而把纯 HTTP
	// 的内网部署锁死。
	r = httptest.NewRequest("GET", "http://practice.example.com/", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	w = httptest.NewRecorder()
	(&Server{}).sameOrigin(next).ServeHTTP(w, r)
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS emitted over plain HTTP")
	}
}

func TestDecodeRejectsTextForm(t *testing.T) {
	r := httptest.NewRequest("POST", "https://practice.example.com/api/admin/password", strings.NewReader(`{"password":"attackerpw","pad":"="}`))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	var in map[string]string
	if decode(w, r, &in) {
		t.Fatal("text form accepted")
	}
}

func TestAdminHTTPPasswordRequiresCurrentPassword(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	if e := admin.Create(ctx, db, "admin", "originalpw", "super"); e != nil {
		t.Fatal(e)
	}
	token, _, _, e := admin.Login(ctx, db, "admin", "originalpw")
	if e != nil {
		t.Fatal(e)
	}
	c := &http.Cookie{Name: sessionCookie, Value: token}
	h := s.Routes()
	w, _ := requestTest(t, h, "POST", "/api/admin/password", `{"password":"replacementpw"}`, c)
	if w.Code == 200 {
		t.Fatal("password changed without current password")
	}
	if _, _, _, e = admin.Login(ctx, db, "admin", "originalpw"); e != nil {
		t.Fatal(e)
	}
	w, _ = requestTest(t, h, "POST", "/api/admin/password", `{"current_password":"originalpw","password":"replacementpw"}`, c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, _, e = admin.Verify(ctx, db, token); e == nil {
		t.Fatal("token survives")
	}
}

func TestOriginOnlyTrustsConfiguredProxyProtocol(t *testing.T) {
	r := httptest.NewRequest("POST", "http://practice.example.com/api/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "https://practice.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for _, v := range []struct {
		trusted bool
		want    int
	}{{false, 403}, {true, 204}} {
		s := &Server{}
		if v.trusted {
			s.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
		}
		w := httptest.NewRecorder()
		s.sameOrigin(next).ServeHTTP(w, r)
		if w.Code != v.want {
			t.Fatal(v, w.Code)
		}
	}
}

func TestAuthLimiterSlotsOnlyGatePasswordEndpoints(t *testing.T) {
	l := newAuthLimiter()
	l.slots["/api/auth/login"] <- struct{}{}
	l.slots["/api/auth/login"] <- struct{}{}
	// 灌满登录接口的全部并发槽。
	for i := 0; i < cap(l.slots["/api/auth/login"])-2; i++ {
		l.slots["/api/auth/login"] <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(l.slots["/api/auth/login"]); i++ {
			<-l.slots["/api/auth/login"]
		}
	}()
	h := l.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))

	r := httptest.NewRequest("POST", "/api/auth/email-code", strings.NewReader(`{"email":"a@b.co"}`))
	r.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("email-code blocked by password slots: %d", w.Code)
	}
	r = httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"u"}`))
	r.RemoteAddr = "192.0.2.2:1234"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatalf("login should wait for a slot: %d", w.Code)
	}
}

func TestSettingsKeepSMTPPasswordWhenEmptyOrMasked(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	if e := admin.Create(ctx, db, "admin", "originalpw", "super"); e != nil {
		t.Fatal(e)
	}
	token, _, _, e := admin.Login(ctx, db, "admin", "originalpw")
	if e != nil {
		t.Fatal(e)
	}
	if e := s.Settings.Set(ctx, setting.KeySMTPPassword, "smtp-secret", "test"); e != nil {
		t.Fatal(e)
	}
	c := &http.Cookie{Name: sessionCookie, Value: token}
	h := s.Routes()

	// 空串和掩码都表示「未修改」，不得清掉已存的 SMTP 密码。
	for _, body := range []string{
		`{"mail.smtp_password":""}`,
		fmt.Sprintf(`{"mail.smtp_password":%q}`, setting.Mask),
	} {
		w, _ := requestTest(t, h, "PUT", "/api/admin/settings", body, c)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		got, err := s.Settings.Get(ctx, setting.KeySMTPPassword)
		if err != nil || got != "smtp-secret" {
			t.Fatalf("password wiped by %s: %q %v", body, got, err)
		}
	}
}

// M-1：未知配置键整批拒绝，不得污染配置表。
func TestSettingsRejectUnknownKeys(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	if e := admin.Create(ctx, db, "admin", "originalpw", "super"); e != nil {
		t.Fatal(e)
	}
	token, _, _, e := admin.Login(ctx, db, "admin", "originalpw")
	if e != nil {
		t.Fatal(e)
	}
	c := &http.Cookie{Name: sessionCookie, Value: token}
	h := s.Routes()
	w, _ := requestTest(t, h, "PUT", "/api/admin/settings", `{"totally.bogus.key":"injected","llm.model":"ok-model"}`, c)
	if w.Code != 400 {
		t.Fatalf("unknown key accepted: %d %s", w.Code, w.Body.String())
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM setting WHERE key='totally.bogus.key'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("bogus key persisted: %d %v", n, err)
	}
	// 整批拒绝：合法键也不得部分写入。
	if v, _ := s.Settings.Get(ctx, setting.KeyModel); v == "ok-model" {
		t.Fatal("partial write despite rejected batch")
	}
	// SDK 层同样拒绝。
	if err := s.Settings.Set(ctx, "another.bogus", "x", "test"); !errors.Is(err, setting.ErrUnknownKey) {
		t.Fatalf("Store.Set accepted unknown key: %v", err)
	}
}
