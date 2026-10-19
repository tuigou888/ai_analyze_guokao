package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai_analyze_guokao/internal/admin"
)

// bothCookiesRequest 构造同时携带 gk_admin 与 gk_user 的请求——复现"先登管理端、
// 再登普通用户"的浏览器里两个 cookie 共存的形态。
func bothCookiesRequest(t *testing.T, h http.Handler, method, path, body string, adminC, userC *http.Cookie) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(adminC)
	r.AddCookie(userC)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return w, v
}

// 用户身份与管理端权限互斥：只持用户 cookie → 管理接口一律 401；用户 cookie 与
// 有效 admin cookie 共存 → 仍然 401（管理端权限不得在持有用户会话的浏览器上行使）。
func TestUserIdentityExcludesAdminAPIs(t *testing.T) {
	s, db := fixture(t)
	if e := admin.Create(t.Context(), db, "root", "root-password", admin.RoleSuper); e != nil {
		t.Fatal(e)
	}
	h := s.Routes()
	user := account(t, s, "plain-user")
	adminC, _ := adminLogin(t, h, "root", "root-password")

	// 基线：user cookie 单独打管理接口 → 401。
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/admin/me"},
		{"GET", "/api/admin/users"},
		{"GET", "/api/admin/settings"},
		{"GET", "/api/admin/admins"},
	} {
		w, _ := requestTest(t, h, c.method, c.path, "", user)
		if w.Code != 401 {
			t.Fatalf("user-only %s %s: HTTP %d, want 401", c.method, c.path, w.Code)
		}
	}

	// 共存：即使 admin cookie 有效，管理接口也必须 401。
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/admin/me", ""},
		{"GET", "/api/admin/users", ""},
		{"GET", "/api/admin/settings", ""},
		{"PUT", "/api/admin/settings", fmt.Sprintf(`{%q:"5"}`, "user.daily_free_papers")},
		{"GET", "/api/admin/admins", ""},
		{"PUT", "/api/admin/users/1/status", `{"status":"banned"}`},
	} {
		w, _ := bothCookiesRequest(t, h, c.method, c.path, c.body, adminC, user)
		if w.Code != 401 {
			t.Fatalf("both-cookies %s %s: HTTP %d %s, want 401", c.method, c.path, w.Code, w.Body.String())
		}
	}

	// 基线不回归：admin cookie 单独使用仍然 200。
	w, v := requestTest(t, h, "GET", "/api/admin/me", "", adminC)
	if w.Code != 200 || v["username"] != "root" {
		t.Fatalf("admin-only me: HTTP %d %v, want 200 root", w.Code, v)
	}
}

// 管理员登录完成身份切换：请求里带着有效 gk_user 时，签发 admin 会话须同时吊销
// 该用户会话并清除 cookie——否则互斥检查会把浏览器锁在管理端登录页死循环里。
func TestAdminLoginSwitchesIdentity(t *testing.T) {
	s, db := fixture(t)
	if e := admin.Create(t.Context(), db, "root", "root-password", admin.RoleSuper); e != nil {
		t.Fatal(e)
	}
	h := s.Routes()
	user := account(t, s, "switch-user")

	// 以用户浏览器身份登管理端。
	w, _ := requestTest(t, h, "POST", "/api/admin/login", `{"username":"root","password":"root-password"}`, user)
	if w.Code != 200 {
		t.Fatalf("admin login with user cookie: HTTP %d %s", w.Code, w.Body.String())
	}
	var adminC *http.Cookie
	userCleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			adminC = c
		}
		if c.Name == userCookie && c.MaxAge < 0 {
			userCleared = true
		}
	}
	if adminC == nil || !userCleared {
		t.Fatalf("admin login cookies: admin=%v user_cleared=%v", adminC != nil, userCleared)
	}

	// 旧用户会话服务端已吊销。
	w, _ = requestTest(t, h, "GET", "/api/auth/me", "", user)
	if w.Code != 401 {
		t.Fatalf("old user session after admin login: HTTP %d, want 401", w.Code)
	}
	// 新 admin 会话（无用户 cookie）可用。
	w, v := requestTest(t, h, "GET", "/api/admin/me", "", adminC)
	if w.Code != 200 || v["username"] != "root" {
		t.Fatalf("admin me after switch: HTTP %d %v", w.Code, v)
	}
}

// 对称方向：用户登录吊销请求里携带的 admin 会话，避免浏览器同时持有双身份。
func TestUserLoginRevokesAdminSession(t *testing.T) {
	s, db := fixture(t)
	if e := admin.Create(t.Context(), db, "root", "root-password", admin.RoleSuper); e != nil {
		t.Fatal(e)
	}
	h := s.Routes()
	adminC, _ := adminLogin(t, h, "root", "root-password")
	account(t, s, "later-user") // 注册好用户；下面模拟"持 admin cookie 的浏览器"再登用户端

	w, _ := requestTest(t, h, "POST", "/api/auth/login", `{"username":"later-user","password":"testpassword"}`, adminC)
	if w.Code != 200 {
		t.Fatalf("user login with admin cookie: HTTP %d %s", w.Code, w.Body.String())
	}
	var userC *http.Cookie
	adminCleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == userCookie {
			userC = c
		}
		if c.Name == sessionCookie && c.MaxAge < 0 {
			adminCleared = true
		}
	}
	if userC == nil || !adminCleared {
		t.Fatalf("user login cookies: user=%v admin_cleared=%v", userC != nil, adminCleared)
	}

	// 旧 admin 会话服务端已吊销。
	w, _ = requestTest(t, h, "GET", "/api/admin/me", "", adminC)
	if w.Code != 401 {
		t.Fatalf("old admin session after user login: HTTP %d, want 401", w.Code)
	}
	// 新用户会话可用。
	w, _ = requestTest(t, h, "GET", "/api/auth/me", "", userC)
	if w.Code != 401 && w.Code != 200 {
		t.Fatalf("user me after switch: HTTP %d", w.Code)
	}
}
