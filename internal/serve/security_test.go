package serve

import (
	"ai_analyze_guokao/internal/admin"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestAuthLimiterIgnoresSpoofedForwardedHeaders(t *testing.T) {
	h := newAuthLimiter().middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
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
	h := newAuthLimiter().middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for i := 0; i < 31; i++ {
		r := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"username":"admin"}`))
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i == 30 && w.Code != 429 {
			t.Fatal("distributed account guesses accepted")
		}
	}
}

func TestAuthLimiterDoesNotRetainLargeUsernames(t *testing.T) {
	l := newAuthLimiter()
	h := l.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for i := 0; i < 4; i++ {
		body := fmt.Sprintf(`{"username":"%s%d"}`, strings.Repeat("a", 512*1024), i)
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
		r.RemoteAddr = "192.0.2.1:1234"
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	retained := 0
	for key := range l.entries {
		retained += len(key)
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
	if e := admin.Create(ctx, db, "admin", "originalpw"); e != nil {
		t.Fatal(e)
	}
	token, e := admin.Login(ctx, db, "admin", "originalpw")
	if e != nil {
		t.Fatal(e)
	}
	c := &http.Cookie{Name: sessionCookie, Value: token}
	h := s.Routes()
	w, _ := requestTest(t, h, "POST", "/api/admin/password", `{"password":"replacementpw"}`, c)
	if w.Code == 200 {
		t.Fatal("password changed without current password")
	}
	if _, e = admin.Login(ctx, db, "admin", "originalpw"); e != nil {
		t.Fatal(e)
	}
	w, _ = requestTest(t, h, "POST", "/api/admin/password", `{"current_password":"originalpw","password":"replacementpw"}`, c)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, e = admin.Verify(ctx, db, token); e == nil {
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
