package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
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

func TestAdminManagementAPI(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	for _, a := range []struct{ name, password, role string }{
		{"boss", "super-password", admin.RoleSuper},
		{"op1", "operator-password", admin.RoleOperator},
	} {
		if err := admin.Create(ctx, db, a.name, a.password, a.role); err != nil {
			t.Fatal(err)
		}
	}
	h := s.Routes()
	super, _ := adminLogin(t, h, "boss", "super-password")
	operator, _ := adminLogin(t, h, "op1", "operator-password")

	// —— operator 越权矩阵：4 条管理员管理路由全 403，且不产生任何审计或数据变更。
	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admin_audit`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/admin/admins", ""},
		{"POST", "/api/admin/admins", `{"username":"sneaky","password":"sneaky-password","role":"super"}`},
		{"PUT", "/api/admin/admins/99999/password", `{"password":"resetpassword"}`},
		{"DELETE", "/api/admin/admins/99999", ""},
	} {
		w, v := requestTest(t, h, c.method, c.path, c.body, operator)
		if w.Code != 403 {
			t.Fatalf("operator %s %s: HTTP %d %s, want 403", c.method, c.path, w.Code, w.Body.String())
		}
		if msg, _ := v["error"].(string); !strings.Contains(msg, "超级管理员") {
			t.Fatalf("operator %s %s: error %q missing 超级管理员", c.method, c.path, msg)
		}
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admin_audit`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("operator 越权产生审计: %d -> %d", before, after)
	}
	var sneaky int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admin_user WHERE username='sneaky'`).Scan(&sneaky); err != nil {
		t.Fatal(err)
	}
	if sneaky != 0 {
		t.Fatal("operator 越权创建了管理员")
	}

	// —— POST 建 operator：200 + {"ok":true}；审计 admin_create target=新 id、detail={"role":"operator"}。
	w, v := requestTest(t, h, "POST", "/api/admin/admins", `{"username":"op2","password":"op2-password","role":"operator"}`, super)
	if w.Code != 200 || v["ok"] != true {
		t.Fatalf("create op2: HTTP %d %s", w.Code, w.Body.String())
	}
	var op2ID int64
	var op2Created string
	if err := db.QueryRow(`SELECT id, created_at FROM admin_user WHERE username='op2'`).Scan(&op2ID, &op2Created); err != nil {
		t.Fatal(err)
	}
	assertAuditRow(t, s, "admin_create", "boss", fmt.Sprintf("admin:%d", op2ID), `{"role":"operator"}`)

	// 重复名 → 409；密码 <8 或 >72 → 400（>72 必须前置拦，否则 bcrypt 报 500）；非法用户名/角色 → 400。
	if w, v = requestTest(t, h, "POST", "/api/admin/admins", `{"username":"op2","password":"op2-password","role":"operator"}`, super); w.Code != 409 || v["error"] != "用户名已存在" {
		t.Fatalf("duplicate: HTTP %d %v, want 409 用户名已存在", w.Code, v)
	}
	for _, c := range []struct{ body, wantErr string }{
		{`{"username":"op3","password":"short","role":"operator"}`, "密码需为 8-72 位"},
		{fmt.Sprintf(`{"username":"op3","password":%q,"role":"operator"}`, strings.Repeat("x", 73)), "密码需为 8-72 位"},
		{`{"username":"ab","password":"op3-password","role":"operator"}`, "用户名需为 3-32 位字母、数字、下划线或短横线"},
		{`{"username":"bad name!","password":"op3-password","role":"operator"}`, "用户名需为 3-32 位字母、数字、下划线或短横线"},
		{`{"username":"op3","password":"op3-password","role":"root"}`, "角色必须是 super 或 operator"},
	} {
		if w, v = requestTest(t, h, "POST", "/api/admin/admins", c.body, super); w.Code != 400 || v["error"] != c.wantErr {
			t.Fatalf("create %s: HTTP %d %v, want 400 %q", c.body, w.Code, v, c.wantErr)
		}
	}

	// —— GET 列表：顶层键、行键集与值逐项断言（id 升序 → boss/op1/op2）。
	w, v = requestTest(t, h, "GET", "/api/admin/admins", "", super)
	if w.Code != 200 {
		t.Fatalf("list admins: HTTP %d %s", w.Code, w.Body.String())
	}
	if got := sortedKeys(v); !reflect.DeepEqual(got, []string{"admins"}) {
		t.Fatalf("list top keys: %v", got)
	}
	admins, ok := v["admins"].([]any)
	if !ok || len(admins) != 3 {
		t.Fatalf("admins = %#v, want 3 rows", v["admins"])
	}
	wantKeys := []string{"created_at", "id", "last_login_at", "role", "username"}
	byName := map[string]map[string]any{}
	for _, a := range admins {
		row := a.(map[string]any)
		if got := sortedKeys(row); !reflect.DeepEqual(got, wantKeys) {
			t.Fatalf("admin row keys: %v", got)
		}
		byName[row["username"].(string)] = row
	}
	op2Row := byName["op2"]
	if op2Row == nil || op2Row["id"].(float64) != float64(op2ID) || op2Row["role"] != "operator" ||
		op2Row["created_at"] != op2Created || op2Row["last_login_at"] != "" {
		t.Fatalf("op2 row: %#v", op2Row)
	}
	if bossRow := byName["boss"]; bossRow == nil || bossRow["role"] != "super" || bossRow["last_login_at"] == "" {
		t.Fatalf("boss row: %#v", byName["boss"])
	}

	// 空列表必须是 [] 而非 null：无管理员的服务实例直接打 handler（HTTP 路由打不进去——超管专属）。
	empty, _ := fixture(t)
	rec := httptest.NewRecorder()
	empty.handleAdminListAdmins(rec, httptest.NewRequest("GET", "/api/admin/admins", nil))
	var emptyBody map[string][]any
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyBody); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || emptyBody["admins"] == nil || len(emptyBody["admins"]) != 0 {
		t.Fatalf("empty list: HTTP %d %q, want {\"admins\":[]}", rec.Code, rec.Body.String())
	}

	// —— PUT 改密：op2 先 HTTP 登录拿 token；super 重置后旧 token 三段断言失效；新密码可登录。
	op2Cookie, _ := adminLogin(t, h, "op2", "op2-password")
	if name, role, err := admin.Verify(ctx, db, op2Cookie.Value); err != nil || name != "op2" || role != admin.RoleOperator {
		t.Fatalf("op2 会话预检: name=%q role=%q err=%v", name, role, err)
	}
	if w, v = requestTest(t, h, "PUT", fmt.Sprintf("/api/admin/admins/%d/password", op2ID), `{"password":"op2-password2"}`, super); w.Code != 200 || v["ok"] != true {
		t.Fatalf("reset op2 password: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "admin_password_reset", "boss", fmt.Sprintf("admin:%d", op2ID), "")
	if name, role, err := admin.Verify(ctx, db, op2Cookie.Value); err == nil || name != "" || role != "" {
		t.Fatalf("旧 token 未吊销: name=%q role=%q err=%v", name, role, err)
	}
	adminLogin(t, h, "op2", "op2-password2")
	if w, v = requestTest(t, h, "PUT", fmt.Sprintf("/api/admin/admins/%d/password", op2ID), `{"password":"short"}`, super); w.Code != 400 || v["error"] != "密码需为 8-72 位" {
		t.Fatalf("short password: HTTP %d %v, want 400 密码需为 8-72 位", w.Code, v)
	}
	if w, v = requestTest(t, h, "PUT", "/api/admin/admins/99999/password", `{"password":"valid-password"}`, super); w.Code != 404 || v["error"] != "目标管理员不存在" {
		t.Fatalf("missing target: HTTP %d %v, want 404 目标管理员不存在", w.Code, v)
	}

	// —— DELETE：删自己 → 400；建第二个 super 后删它成立 + 审计 + 会话吊销；再删同 id → 404。
	var bossID int64
	if err := db.QueryRow(`SELECT id FROM admin_user WHERE username='boss'`).Scan(&bossID); err != nil {
		t.Fatal(err)
	}
	if w, v = requestTest(t, h, "DELETE", fmt.Sprintf("/api/admin/admins/%d", bossID), "", super); w.Code != 400 || v["error"] != "不能删除当前登录账号" {
		t.Fatalf("self delete: HTTP %d %v, want 400 不能删除当前登录账号", w.Code, v)
	}
	if w, v = requestTest(t, h, "POST", "/api/admin/admins", `{"username":"sup2","password":"sup2-password","role":"super"}`, super); w.Code != 200 || v["ok"] != true {
		t.Fatalf("create sup2: HTTP %d %s", w.Code, w.Body.String())
	}
	var sup2ID int64
	if err := db.QueryRow(`SELECT id FROM admin_user WHERE username='sup2'`).Scan(&sup2ID); err != nil {
		t.Fatal(err)
	}
	assertAuditRow(t, s, "admin_create", "boss", fmt.Sprintf("admin:%d", sup2ID), `{"role":"super"}`)
	sup2Cookie, _ := adminLogin(t, h, "sup2", "sup2-password")
	if w, v = requestTest(t, h, "DELETE", fmt.Sprintf("/api/admin/admins/%d", sup2ID), "", super); w.Code != 200 || v["ok"] != true {
		t.Fatalf("delete sup2: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "admin_delete", "boss", fmt.Sprintf("admin:%d", sup2ID), "")
	if name, role, err := admin.Verify(ctx, db, sup2Cookie.Value); err == nil || name != "" || role != "" {
		t.Fatalf("被删 sup2 会话未吊销: name=%q role=%q err=%v", name, role, err)
	}
	if w, v = requestTest(t, h, "DELETE", fmt.Sprintf("/api/admin/admins/%d", sup2ID), "", super); w.Code != 404 || v["error"] != "目标管理员不存在" {
		t.Fatalf("re-delete: HTTP %d %v, want 404 目标管理员不存在", w.Code, v)
	}

	// 删 operator 同样成立且审计落行。
	if w, v = requestTest(t, h, "DELETE", fmt.Sprintf("/api/admin/admins/%d", op2ID), "", super); w.Code != 200 || v["ok"] != true {
		t.Fatalf("delete op2: HTTP %d %s", w.Code, w.Body.String())
	}
	assertAuditRow(t, s, "admin_delete", "boss", fmt.Sprintf("admin:%d", op2ID), "")

	// ErrLastSuper 到不了 HTTP 路由：4 条路由超管专属 → 当前会话恒为库中 super；
	// 删非本人的 super 时库中 super 数恒 ≥2（进不去 ≤1 分支），删自己则先命中
	// ErrSelfDelete。故对映射函数直接断言 400 文案透传（守卫行为由 internal/admin 单测钉住）。
	rec = httptest.NewRecorder()
	s.adminManageError(rec, admin.ErrLastSuper)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 400 || body["error"] != "至少保留一个超级管理员" {
		t.Fatalf("ErrLastSuper mapping: HTTP %d %s (err=%v), want 400 至少保留一个超级管理员", rec.Code, rec.Body.String(), err)
	}
}
