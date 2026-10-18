package serve

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ai_analyze_guokao/internal/admin"
	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/store"
	"ai_analyze_guokao/internal/study"
)

// adminLogin 走 HTTP 登录管理端，返回会话 cookie 与响应体。
func adminLogin(t *testing.T, h http.Handler, username, password string) (*http.Cookie, map[string]any) {
	t.Helper()
	w, v := requestTest(t, h, "POST", "/api/admin/login",
		fmt.Sprintf(`{"username":%q,"password":%q}`, username, password), nil)
	if w.Code != 200 {
		t.Fatalf("admin login %s: HTTP %d %s", username, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c, v
		}
	}
	t.Fatalf("admin login %s: no %s cookie", username, sessionCookie)
	return nil, nil
}

// operator 会话：超管专属接口一律 403；通用管理操作放行；用户接口不认管理端 cookie。
func TestOperatorForbiddenMatrix(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	if e := admin.Create(ctx, db, "opsup", "super-password", admin.RoleSuper); e != nil {
		t.Fatal(e)
	}
	if e := admin.Create(ctx, db, "op1", "operator-password", admin.RoleOperator); e != nil {
		t.Fatal(e)
	}
	h := s.Routes()
	super, sv := adminLogin(t, h, "opsup", "super-password")
	operator, ov := adminLogin(t, h, "op1", "operator-password")

	// 登录响应必须回会话身份（用户名 + 角色）。
	if sv["username"] != "opsup" || sv["role"] != admin.RoleSuper {
		t.Fatalf("super login body: %v", sv)
	}
	if ov["username"] != "op1" || ov["role"] != admin.RoleOperator {
		t.Fatalf("operator login body: %v", ov)
	}

	// 超管专属矩阵：operator 打这些接口必须 403 且说明需要超管。
	for _, c := range []struct {
		method, path, body string
	}{
		{"GET", "/api/admin/settings", ""},
		{"PUT", "/api/admin/settings", fmt.Sprintf(`{%q:"5"}`, setting.KeyDailyFreePapers)},
		{"POST", "/api/admin/test-llm", ""},
		{"POST", "/api/admin/test-mail", `{"to":"a@b.co"}`},
	} {
		w, v := requestTest(t, h, c.method, c.path, c.body, operator)
		if w.Code != 403 {
			t.Fatalf("operator %s %s: HTTP %d %s, want 403", c.method, c.path, w.Code, w.Body.String())
		}
		if msg, _ := v["error"].(string); !strings.Contains(msg, "超级管理员") {
			t.Fatalf("operator %s %s: error %q missing 超级管理员", c.method, c.path, msg)
		}
	}

	// 通用管理操作对 operator 放行：me 回角色、concepts 刷新、改自己的密码。
	w, v := requestTest(t, h, "GET", "/api/admin/me", "", operator)
	if w.Code != 200 || v["role"] != admin.RoleOperator {
		t.Fatalf("operator me: HTTP %d %v, want 200 role=operator", w.Code, v)
	}
	w, v = requestTest(t, h, "POST", "/api/admin/concepts/refresh", "", operator)
	if w.Code != 200 || v["ok"] != true {
		t.Fatalf("operator concepts refresh: HTTP %d %s", w.Code, w.Body.String())
	}
	// 放最后：改密会吊销 operator 的全部会话。
	w, _ = requestTest(t, h, "POST", "/api/admin/password",
		`{"current_password":"operator-password","password":"operator-password2"}`, operator)
	if w.Code != 200 {
		t.Fatalf("operator change password: HTTP %d %s", w.Code, w.Body.String())
	}

	// 管理端会话不是用户会话：用户接口一律 401。
	w, _ = requestTest(t, h, "GET", "/api/questions", "", super)
	if w.Code != 401 {
		t.Fatalf("admin cookie on user endpoint: HTTP %d %s, want 401", w.Code, w.Body.String())
	}
}

// banned 用户登录 403 且给出封禁文案；封禁前签发的旧会话立即失效；额度耗尽
// 429 且 Retry-After 精确指向考试时区次日零点。
func TestBannedLogin403AndQuota429ViaHTTP(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()

	victim := account(t, s, "banned-victim")
	if _, e := db.Exec(`UPDATE app_user SET status='banned' WHERE username='banned-victim'`); e != nil {
		t.Fatal(e)
	}
	w, v := requestTest(t, h, "POST", "/api/auth/login", `{"username":"banned-victim","password":"testpassword"}`, nil)
	if w.Code != 403 {
		t.Fatalf("banned login: HTTP %d %s, want 403", w.Code, w.Body.String())
	}
	if msg, _ := v["error"].(string); !strings.Contains(msg, "账号已封禁") {
		t.Fatalf("banned login error: %q", msg)
	}
	w, _ = requestTest(t, h, "GET", "/api/questions", "", victim)
	if w.Code != 401 {
		t.Fatalf("banned stale session: HTTP %d %s, want 401", w.Code, w.Body.String())
	}

	// 固定 Clock 后额度可精确断言：中午固定，到次日零点恰 43200 秒。
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, study.ExamZone) }
	free := account(t, s, "quota-day")
	create := func() *httptest.ResponseRecorder {
		w, _ := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"paperset","spec":{"paper_id":1}}`, free)
		return w
	}
	for i := 0; i < 3; i++ {
		if w := create(); w.Code != 200 {
			t.Fatalf("paper %d: HTTP %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w = create()
	if w.Code != 429 {
		t.Fatalf("4th paper: HTTP %d %s, want 429", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "43200" {
		t.Fatalf("Retry-After = %q, want 43200", got)
	}
}

// 并发超额只放行限额内的一笔：独立 SQLite 连接池 + HTTP，按状态码计数。
func TestAdminUserQuotaConcurrency429(t *testing.T) {
	s, db := fixture(t)
	fixed := time.Date(2026, 10, 16, 12, 0, 0, 0, study.ExamZone)
	s.Study.Clock = func() time.Time { return fixed }
	h := s.Routes()
	var file string
	if e := db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&file); e != nil {
		t.Fatal(e)
	}
	secondDB, e := store.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	defer secondDB.Close()
	// 两个连接池必须共享同一固定时钟，否则额度按不同日期计数。
	second := New(s.Settings, secondDB, nil)
	second.Study.Clock = func() time.Time { return fixed }
	secondHandler := second.Routes()

	free := account(t, s, "quota-race")
	const n = 4
	start := make(chan struct{})
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			handler := h
			if i%2 == 1 {
				handler = secondHandler
			}
			w, _ := requestTest(t, handler, "POST", "/api/practice/sessions", `{"kind":"paperset","spec":{"paper_id":1}}`, free)
			codes[i] = w.Code
		}(i)
	}
	close(start)
	wg.Wait()

	ok, quota := 0, 0
	for i, c := range codes {
		switch c {
		case 200:
			ok++
		case 429:
			quota++
		default:
			t.Errorf("request %d: HTTP %d, want 200 or 429", i, c)
		}
	}
	if ok != 3 || quota != 1 {
		t.Fatalf("status codes %v: got %d×200 + %d×429, want 3×200 + 1×429", codes, ok, quota)
	}
	var used int
	if e := db.QueryRow(`SELECT papers FROM daily_usage WHERE user_id=(SELECT id FROM app_user WHERE username='quota-race') AND day=?`, study.QuotaDay(fixed)).Scan(&used); e != nil {
		t.Fatal(e)
	}
	if used != 3 {
		t.Fatalf("daily_usage papers = %d, want 3", used)
	}
}
