package serve

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Removing the Cookie-verified identity precondition would disclose B's data
// and mutate B's profile/password/sessions via the stale A page.
func TestPersonalExpectedIdentityRejectsBeforeEffects(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	account(t, h, "identity_a")
	bob := account(t, h, "identity_b")
	wLogin, _ := requestTest(t, h, "POST", "/api/auth/login", `{"username":"identity_b","password":"testpassword"}`, nil)
	if wLogin.Code != 200 {
		t.Fatal(wLogin.Body.String())
	}
	_, created := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"single","spec":{"question_ids":[1]}}`, bob)
	sid := int(created["session_id"].(float64))
	wSubmit, _ := requestTest(t, h, "POST", fmt.Sprintf("/api/practice/sessions/%d/submit", sid), `{"draft_revision":0,"answers":[{"question_id":1,"answer":"H","duration_ms":1000}]}`, bob)
	if wSubmit.Code != 200 {
		t.Fatal(wSubmit.Body.String())
	}
	if _, err := db.Exec(`DELETE FROM user_concept_stat WHERE user_id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_concept_stat(user_id,concept_id,answered,correct,mastery,updated_at) SELECT 2,id,7,3,42,'synthetic-marker' FROM concept LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	_, sessions := requestTest(t, h, "GET", "/api/account/sessions", "", bob)
	id := sessions["items"].([]any)[0].(map[string]any)["public_id"].(string)
	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM app_user WHERE username='identity_b'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/account/profile", profileBody(t, map[string]any{"nickname": "old A input"})},
		{"GET", "/api/account/profile", ""}, {"GET", "/api/account/dashboard", ""},
		{"GET", "/api/account/export?days=all", ""}, {"GET", "/api/account/sessions", ""},
		{"POST", "/api/auth/password", `{"current_password":"testpassword","password":"new-test-password"}`},
		{"POST", "/api/account/sessions/revoke-others", `{}`},
		{"DELETE", "/api/account/sessions/" + id, ""},
		{"POST", "/api/practice/stats/rebuild", `{}`},
		{"POST", "/api/account/review/wrongbook", `{}`},
		{"POST", "/api/auth/logout", `{}`},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Origin", "http://example.com")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-GK-Expected-User", "1")
			r.AddCookie(bob)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"account_changed"`) {
				t.Fatalf("expected refusal, got %d %s", w.Code, w.Body.String())
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("refusal replaced valid B Cookie")
			}
		})
	}
	w, profile := requestTest(t, h, "GET", "/api/account/profile", "", bob)
	if w.Code != 200 || profile["nickname"] != "" || profile["revision"] != float64(0) {
		t.Fatal("B profile changed", w.Code, profile)
	}
	var stat int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_concept_stat WHERE user_id=2 AND answered=7 AND updated_at='synthetic-marker'`).Scan(&stat); err != nil || stat != 1 {
		t.Fatal("B statistics changed", stat, err)
	}
	var after string
	if err := db.QueryRow(`SELECT password_hash FROM app_user WHERE username='identity_b'`).Scan(&after); err != nil || after != hash {
		t.Fatal("B password changed", err)
	}
	w, remaining := requestTest(t, h, "GET", "/api/account/sessions", "", bob)
	if w.Code != 200 || remaining["total"] != sessions["total"] {
		t.Fatal("B sessions changed", w.Code, remaining)
	}
	// No precondition remains compatible; matching B precondition only permits B.
	w, _ = requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, nil), bob)
	if w.Code != 200 {
		t.Fatal("legacy API broken", w.Code, w.Body.String())
	}
	for _, expected := range []string{"2", "bad", "", "01", "1,2"} {
		r := httptest.NewRequest(http.MethodGet, "/api/account/profile", nil)
		r.AddCookie(bob)
		r.Header["X-Gk-Expected-User"] = []string{expected}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 400
		if expected == "2" {
			want = 200
		}
		if expected == "2" && !strings.Contains(w.Body.String(), `"username":"identity_b"`) {
			t.Fatal("precondition selected a different principal", w.Body.String())
		}
		if w.Code != want {
			t.Fatalf("precondition %q: %d %s", expected, w.Code, w.Body.String())
		}
	}
	w, _ = requestTest(t, h, "POST", "/api/auth/logout", `{}`, bob)
	if w.Code != 200 {
		t.Fatal("legacy logout broken", w.Code)
	}
}

// Preconditions must not replace authentication, Origin or administrator boundaries.
func TestPersonalExpectedIdentityAuthenticationAndOrigin(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "precondition_origin")
	for _, tc := range []struct {
		path, origin, expected string
		authenticated          bool
		want                   int
	}{
		{"/api/account/profile", "http://example.com", "999", false, 401},
		{"/api/auth/logout", "http://example.com", "999", false, 401},
		{"/api/account/profile", "https://evil.example", "999", true, 403},
		{"/api/auth/logout", "https://evil.example", "999", true, 403},
		{"/api/admin/logout", "http://example.com", "bad", false, 200},
	} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(`{}`))
		if tc.path == "/api/account/profile" {
			r.Method = "PUT"
		}
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-GK-Expected-User", tc.expected)
		if tc.authenticated {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
}

// A guarded logout must revoke its own token without a delayed Set-Cookie
// deleting a later login's Cookie. Legacy clients retain Cookie deletion.
func TestPersonalGuardedLogoutDoesNotDeleteLaterCookie(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "guarded_logout")
	r := httptest.NewRequest("POST", "/api/auth/logout", strings.NewReader(`{}`))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-GK-Expected-User", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("guarded logout could delete later Cookie: %d %v", w.Code, w.Result().Cookies())
	}
	w, _ = requestTest(t, h, "GET", "/api/auth/me", "", cookie)
	if w.Code != 401 {
		t.Fatal("guarded logout token still authenticates", w.Code)
	}
	legacy := account(t, h, "legacy_logout")
	w, _ = requestTest(t, h, "POST", "/api/auth/logout", `{}`, legacy)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("legacy logout no longer deletes Cookie", w.Code, w.Result().Cookies())
	}
}
