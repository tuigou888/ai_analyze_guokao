package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ai_analyze_guokao/internal/store"
)

func TestAdminUserOps(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "admin_users.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	s := &Service{DB: db, Clock: func() time.Time { return stamp }}
	ctx := context.Background()

	seed := func(username, nickname, role, status string, expires any) int64 {
		t.Helper()
		res, err := db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,role,status,vip_expires_at) VALUES(?,?,?,?,?,?,?)`,
			username, "x", nickname, stamp.UTC().Format(time.RFC3339), role, status, expires)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	alice := seed("alice", "爱丽丝", "free", "active", nil)
	bob := seed("bob", "鲍勃", "vip", "banned", "2020-01-01")
	carol := seed("carol", "卡罗尔", "vip", "active", nil)

	list := func(page int, status, role, q string) AdminUserPage {
		t.Helper()
		p, err := s.AdminListUsers(ctx, page, status, role, q)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	names := func(p AdminUserPage) []string {
		t.Helper()
		out := make([]string, len(p.Users))
		for i, u := range p.Users {
			out[i] = u.Username
		}
		return out
	}

	// 无筛选：id 升序全量；过期 vip 按有效权益显示 free 且 vip_expires_at 原样。
	p := list(0, "", "", "")
	if p.Total != 3 || len(p.Users) != 3 {
		t.Fatalf("unfiltered: total=%d users=%v", p.Total, names(p))
	}
	if got := names(p); got[0] != "alice" || got[1] != "bob" || got[2] != "carol" {
		t.Fatalf("order: %v", got)
	}
	if p.Users[0].ID != alice || p.Users[1].ID != bob || p.Users[2].ID != carol {
		t.Fatalf("ids: %d %d %d", p.Users[0].ID, p.Users[1].ID, p.Users[2].ID)
	}
	if p.Users[1].Role != "free" || p.Users[1].VipExpiresAt != "2020-01-01" {
		t.Fatalf("expired vip: %+v", p.Users[1])
	}
	if p.Users[2].Role != "vip" || p.Users[2].VipExpiresAt != "" {
		t.Fatalf("long-term vip: %+v", p.Users[2])
	}
	if p.Users[0].CreatedAt == "" || p.Users[0].Nickname != "爱丽丝" {
		t.Fatalf("display fields: %+v", p.Users[0])
	}

	// q：username 与 nickname 均可模糊匹配；LIKE 通配符被转义。
	if p := list(1, "", "", "ali"); p.Total != 1 || p.Users[0].Username != "alice" {
		t.Fatalf("q=ali: %d %v", p.Total, names(p))
	}
	if p := list(1, "", "", "卡"); p.Total != 1 || p.Users[0].Username != "carol" {
		t.Fatalf("q=卡: %d %v", p.Total, names(p))
	}
	if p := list(1, "", "", "%"); p.Total != 0 {
		t.Fatalf("q=%% must not match all: %v", names(p))
	}
	if p := list(1, "", "", "_"); p.Total != 0 {
		t.Fatalf("q=_ must not match all: %v", names(p))
	}

	// status / role 过滤：role=vip 只含有效权益（过期 vip 落 free）。
	if p := list(1, "active", "", ""); p.Total != 2 {
		t.Fatalf("status=active: %d %v", p.Total, names(p))
	}
	if p := list(1, "banned", "", ""); p.Total != 1 || p.Users[0].Username != "bob" {
		t.Fatalf("status=banned: %d %v", p.Total, names(p))
	}
	if p := list(1, "", "vip", ""); p.Total != 1 || p.Users[0].Username != "carol" {
		t.Fatalf("role=vip: %d %v", p.Total, names(p))
	}
	if p := list(1, "", "free", ""); p.Total != 2 {
		t.Fatalf("role=free: %d %v", p.Total, names(p))
	}

	// page=2 空且 total 正确；空 users 必须是非 nil 空切片。
	p = list(2, "", "", "")
	if p.Total != 3 || p.Users == nil || len(p.Users) != 0 {
		t.Fatalf("page=2: total=%d users=%#v", p.Total, p.Users)
	}

	// 无效筛选值 → ErrInvalid。
	if _, err := s.AdminListUsers(ctx, 1, "frozen", "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad status filter: %v", err)
	}
	if _, err := s.AdminListUsers(ctx, 1, "", "manager", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad role filter: %v", err)
	}

	// SetStatus：未知 id → ErrNotFound；同值幂等；封禁删会话（即封即踢）。
	if err := s.AdminSetUserStatus(ctx, 9999, "banned"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ban: %v", err)
	}
	if err := s.AdminSetUserStatus(ctx, bob, "banned"); err != nil {
		t.Fatalf("re-ban idempotent: %v", err)
	}
	if err := s.AdminSetUserStatus(ctx, alice, "frozen"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad status: %v", err)
	}
	token := "alice-token"
	if _, err := db.Exec(`INSERT INTO app_session(token,user_id,expires_at) VALUES(?,?,?)`,
		tokenHash(token), alice, stamp.Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, token); err != nil {
		t.Fatalf("session before ban: %v", err)
	}
	if err := s.AdminSetUserStatus(ctx, alice, "banned"); err != nil {
		t.Fatal(err)
	}
	var sessions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_session WHERE user_id=?`, alice).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("ban must delete sessions, have %d", sessions)
	}
	if _, err := s.Verify(ctx, token); !errors.Is(err, ErrCredentials) {
		t.Fatalf("banned session verify: %v", err)
	}
	if err := s.AdminSetUserStatus(ctx, alice, "active"); err != nil {
		t.Fatalf("unban: %v", err)
	}

	// SetUserRole：非法 role / 非法日期（含非零填充）→ ErrInvalid。
	if err := s.AdminSetUserRole(ctx, alice, "manager", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad role: %v", err)
	}
	for _, bad := range []string{"2020-01-01", "9999-99-99", "2026-9-5"} {
		if err := s.AdminSetUserRole(ctx, alice, "vip", bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad expires %q: %v", bad, err)
		}
	}
	// 合法未来日期 → vip + 原样展示。
	if err := s.AdminSetUserRole(ctx, alice, "vip", "2027-10-16"); err != nil {
		t.Fatal(err)
	}
	p = list(1, "", "vip", "alice")
	if p.Total != 1 || p.Users[0].Role != "vip" || p.Users[0].VipExpiresAt != "2027-10-16" {
		t.Fatalf("vip set: %+v", p)
	}
	// 长期 vip：空 expires → NULL，展示空串。
	if err := s.AdminSetUserRole(ctx, alice, "vip", ""); err != nil {
		t.Fatal(err)
	}
	if p = list(1, "", "vip", "alice"); p.Users[0].VipExpiresAt != "" {
		t.Fatalf("long-term vip expires: %+v", p.Users[0])
	}
	// free → 清空 vip_expires_at。
	if err := s.AdminSetUserRole(ctx, alice, "free", ""); err != nil {
		t.Fatal(err)
	}
	if p = list(1, "", "free", "alice"); p.Users[0].Role != "free" || p.Users[0].VipExpiresAt != "" {
		t.Fatalf("vip clear: %+v", p.Users[0])
	}
	if err := s.AdminSetUserRole(ctx, 9999, "vip", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown role target: %v", err)
	}

	// ResetUserPassword：短密码 → ErrInvalid；换密后旧 token 失效；未知 id → ErrNotFound。
	if err := s.AdminResetUserPassword(ctx, alice, "short"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short password: %v", err)
	}
	token2 := "alice-token-2"
	if _, err := db.Exec(`INSERT INTO app_session(token,user_id,expires_at) VALUES(?,?,?)`,
		tokenHash(token2), alice, stamp.Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, token2); err != nil {
		t.Fatalf("session before reset: %v", err)
	}
	if err := s.AdminResetUserPassword(ctx, alice, "newpassword"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, token2); !errors.Is(err, ErrCredentials) {
		t.Fatalf("session after reset: %v", err)
	}
	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM app_user WHERE id=?`, alice).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("newpassword")) != nil {
		t.Fatal("password hash not updated")
	}
	if err := s.AdminResetUserPassword(ctx, 9999, "newpassword"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown reset target: %v", err)
	}
}
