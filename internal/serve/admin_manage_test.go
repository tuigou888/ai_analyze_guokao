package serve

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"ai_analyze_guokao/internal/admin"
)

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertAuditRow(t *testing.T, s *Server, action, wantAdmin, wantTarget, wantDetail string) {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM admin_audit WHERE action=? AND admin=? AND target=? AND detail=?`,
		action, wantAdmin, wantTarget, wantDetail).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		var got []string
		rows, err := s.DB.Query(`SELECT admin,target,detail FROM admin_audit WHERE action=?`, action)
		if err == nil {
			for rows.Next() {
				var a, tg, d string
				if err := rows.Scan(&a, &tg, &d); err != nil {
					t.Fatal(err)
				}
				got = append(got, fmt.Sprintf("admin=%s target=%s detail=%q", a, tg, d))
			}
			rows.Close()
		}
		t.Fatalf("audit %s: got %d rows matching, want exactly 1 (rows: %v)", action, n, got)
	}
}

func TestAdminUsersHTTP(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	if err := admin.Create(ctx, db, "op1", "operator-password", admin.RoleOperator); err != nil {
		t.Fatal(err)
	}
	h := s.Routes()
	operator, _ := adminLogin(t, h, "op1", "operator-password")

	userCookie1 := account(t, s, "target1")
	account(t, s, "target2")
	var uid int64
	var nickname, created, lastLogin string
	if err := db.QueryRow(`SELECT id,nickname,COALESCE(created_at,''),COALESCE(last_login_at,'') FROM app_user WHERE username='target1'`).Scan(&uid, &nickname, &created, &lastLogin); err != nil {
		t.Fatal(err)
	}
	if created == "" || lastLogin == "" {
		t.Fatalf("fixture timestamps: created=%q lastLogin=%q", created, lastLogin)
	}

	// 列表：顶层键、每行键集与值逐项断言（id 升序 → target1 在前）。
	w, v := requestTest(t, h, "GET", "/api/admin/users", "", operator)
	if w.Code != 200 {
		t.Fatalf("list: HTTP %d %s", w.Code, w.Body.String())
	}
	if got := sortedKeys(v); !reflect.DeepEqual(got, []string{"total", "users"}) {
		t.Fatalf("list top keys: %v", got)
	}
	if v["total"].(float64) != 2 {
		t.Fatalf("total = %v, want 2", v["total"])
	}
	users, ok := v["users"].([]any)
	if !ok || len(users) != 2 {
		t.Fatalf("users = %#v", v["users"])
	}
	first := users[0].(map[string]any)
	wantKeys := []string{"created_at", "id", "last_login_at", "nickname", "role", "status", "username", "vip_expires_at"}
	if got := sortedKeys(first); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("user keys: %v", got)
	}
	if first["id"].(float64) != float64(uid) || first["username"] != "target1" || first["nickname"] != nickname ||
		first["role"] != "free" || first["status"] != "active" || first["vip_expires_at"] != "" ||
		first["created_at"] != created || first["last_login_at"] != lastLogin {
		t.Fatalf("user values: %#v", first)
	}

	// 空结果 users 必须是 [] 而不是 null；无效筛选 400。
	if _, v = requestTest(t, h, "GET", "/api/admin/users?q=no-such-user", "", operator); v["users"] == nil {
		t.Fatal("empty users must be [] not null")
	}
	if w, _ = requestTest(t, h, "GET", "/api/admin/users?status=frozen", "", operator); w.Code != 400 {
		t.Fatalf("bad status filter: HTTP %d, want 400", w.Code)
	}
	if w, _ = requestTest(t, h, "GET", "/api/admin/users?role=manager", "", operator); w.Code != 400 {
		t.Fatalf("bad role filter: HTTP %d, want 400", w.Code)
	}

	// 越权：用户 cookie 与未登录访问任意管理端用户接口均 401。
	target := fmt.Sprintf("user:%d", uid)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/admin/users", ""},
		{"PUT", fmt.Sprintf("/api/admin/users/%d/status", uid), `{"status":"banned"}`},
		{"PUT", fmt.Sprintf("/api/admin/users/%d/role", uid), `{"role":"vip","expires":""}`},
	} {
		if w, _ := requestTest(t, h, c.method, c.path, c.body, userCookie1); w.Code != 401 {
			t.Fatalf("user cookie %s %s: HTTP %d, want 401", c.method, c.path, w.Code)
		}
		if w, _ := requestTest(t, h, c.method, c.path, c.body, nil); w.Code != 401 {
			t.Fatalf("anonymous %s %s: HTTP %d, want 401", c.method, c.path, w.Code)
		}
	}

	// user_ban：200 + {"ok":true} + 审计精确落行；即封即踢（旧 cookie 401）。
	w, v = requestTest(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/status", uid), `{"status":"banned"}`, operator)
	if w.Code != 200 || v["ok"] != true {
		t.Fatalf("ban: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "user_ban", "op1", target, `{"status":"banned"}`)
	if w, _ = requestTest(t, h, "GET", "/api/auth/me", "", userCookie1); w.Code != 401 {
		t.Fatalf("banned user stale session: HTTP %d, want 401", w.Code)
	}

	// user_unban。
	w, v = requestTest(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/status", uid), `{"status":"active"}`, operator)
	if w.Code != 200 || v["ok"] != true {
		t.Fatalf("unban: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "user_unban", "op1", target, `{"status":"active"}`)

	// 重新登录拿新 cookie 供后面断言重置密码吊销会话用。
	w, _ = requestTest(t, h, "POST", "/api/auth/login", `{"username":"target1","password":"testpassword"}`, nil)
	if w.Code != 200 {
		t.Fatalf("re-login: HTTP %d %s", w.Code, w.Body.String())
	}
	fresh := w.Result().Cookies()[0]

	// vip_set（带到期日）/ vip_set（长期）/ vip_clear。
	w, v = requestTest(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/role", uid), `{"role":"vip","expires":"2027-10-16"}`, operator)
	if w.Code != 200 || v["ok"] != true {
		t.Fatalf("vip set: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "vip_set", "op1", target, `{"expires":"2027-10-16"}`)
	if _, v = requestTest(t, h, "GET", "/api/admin/users?q=target1", "", operator); v["users"].([]any)[0].(map[string]any)["vip_expires_at"] != "2027-10-16" {
		t.Fatalf("vip_expires_at not reflected: %v", v["users"])
	}
	w, v = requestTest(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/role", uid), `{"role":"vip","expires":""}`, operator)
	if w.Code != 200 || v["ok"] != true {
		t.Fatalf("long-term vip set: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "vip_set", "op1", target, "长期")
	w, v = requestTest(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/role", uid), `{"role":"free"}`, operator)
	if w.Code != 200 || v["ok"] != true {
		t.Fatalf("vip clear: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "vip_clear", "op1", target, `{"expires":""}`)
	if _, v = requestTest(t, h, "GET", "/api/admin/users?q=target1", "", operator); v["users"].([]any)[0].(map[string]any)["role"] != "free" {
		t.Fatalf("role not cleared: %v", v["users"])
	}

	// user_password_reset：200 + 审计 detail 为空串；旧会话吊销、新密码可登录。
	w, v = requestTest(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/password", uid), `{"password":"resetpassword"}`, operator)
	if w.Code != 200 || v["ok"] != true {
		t.Fatalf("password reset: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "user_password_reset", "op1", target, "")
	if w, _ = requestTest(t, h, "GET", "/api/auth/me", "", fresh); w.Code != 401 {
		t.Fatalf("stale session after reset: HTTP %d, want 401", w.Code)
	}
	if w, _ = requestTest(t, h, "POST", "/api/auth/login", `{"username":"target1","password":"resetpassword"}`, nil); w.Code != 200 {
		t.Fatalf("login with reset password: HTTP %d %s", w.Code, w.Body.String())
	}

	// 目标不存在 → 404「用户不存在」（写操作四种路径 + 非法 id 文案统一）。
	for _, c := range []struct{ path, body string }{
		{"/api/admin/users/99999/status", `{"status":"banned"}`},
		{"/api/admin/users/99999/role", `{"role":"vip","expires":""}`},
		{"/api/admin/users/ab/role", `{"role":"vip","expires":""}`},
		{"/api/admin/users/99999/password", `{"password":"newpassword"}`},
	} {
		w, v := requestTest(t, h, "PUT", c.path, c.body, operator)
		if w.Code != 404 || v["error"] != "用户不存在" {
			t.Fatalf("missing target %s: HTTP %d %v, want 404 用户不存在", c.path, w.Code, v)
		}
	}
}
