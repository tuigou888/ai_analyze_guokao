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

// Catches banned-login session minting, banned sessions surviving Verify,
// VIP-expiry fallback regressions and NULL vip_expires_at scan failures.
func TestBannedAndEffectiveRole(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "identity.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := time.Date(2026, 10, 16, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	s := &Service{DB: db, Clock: func() time.Time { return stamp }}
	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte("testpassword"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	seedUser := func(username, role, expires, status string) int64 {
		t.Helper()
		var expiresArg any
		if expires != "" {
			expiresArg = expires
		}
		res, err := db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,email,role,status,vip_expires_at) VALUES(?,?,?,?,?,?,?,?)`,
			username, string(hash), username, stamp.UTC().Format(time.RFC3339), username+"@example.com", role, status, expiresArg)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	seedSession := func(userID int64, token string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO app_session(token,user_id,expires_at) VALUES(?,?,?)`,
			tokenHash(token), userID, stamp.Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}

	// 1. 封禁用户登录：正确密码 ErrBanned，不产生会话；错误密码仍是 ErrCredentials（bcrypt 先于 banned）。
	bannedID := seedUser("banned_login", "free", "", "banned")
	if _, _, err := s.Login(ctx, "banned_login", "testpassword"); !errors.Is(err, ErrBanned) {
		t.Fatalf("banned login: got %v, want ErrBanned", err)
	}
	if _, _, err := s.Login(ctx, "banned_login", "wrongpassword"); !errors.Is(err, ErrCredentials) {
		t.Fatalf("banned login with wrong password: got %v, want ErrCredentials", err)
	}
	var sessions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_session WHERE user_id=?`, bannedID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("banned login minted %d sessions (%v)", sessions, err)
	}

	// 2. 封禁用户持旧 token：ErrCredentials。
	bannedToken := "banned-old-token-0123456789abcdef"
	seedSession(bannedID, bannedToken)
	if _, err := s.Verify(ctx, bannedToken); !errors.Is(err, ErrCredentials) {
		t.Fatalf("banned session verify: got %v, want ErrCredentials", err)
	}

	// 3. 会员已过期：Role 回落 free，到期日原样保留；前一天到期同样回落。
	expiredID := seedUser("vip_expired", "vip", "2026-10-10", "active")
	expiredToken := "vip-expired-token-0123456789abcd"
	seedSession(expiredID, expiredToken)
	u, err := s.Verify(ctx, expiredToken)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "free" || u.VipExpiresAt != "2026-10-10" {
		t.Fatalf("expired vip verify: %+v", u)
	}
	yesterdayID := seedUser("vip_yesterday", "vip", "2026-10-15", "active")
	yesterdayToken := "vip-yesterday-token-0123456789ab"
	seedSession(yesterdayID, yesterdayToken)
	u, err = s.Verify(ctx, yesterdayToken)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "free" || u.VipExpiresAt != "2026-10-15" {
		t.Fatalf("yesterday-expiry vip verify: %+v", u)
	}

	// 4. 未到期 vip、到期日当天 vip 与长期 vip（NULL 到期）。
	activeID := seedUser("vip_active", "vip", "2026-10-20", "active")
	activeToken := "vip-active-token-0123456789abcde"
	seedSession(activeID, activeToken)
	u, err = s.Verify(ctx, activeToken)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "vip" || u.VipExpiresAt != "2026-10-20" {
		t.Fatalf("active vip verify: %+v", u)
	}
	todayID := seedUser("vip_today", "vip", "2026-10-16", "active")
	todayToken := "vip-today-token-0123456789abcde"
	seedSession(todayID, todayToken)
	u, err = s.Verify(ctx, todayToken)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "vip" || u.VipExpiresAt != "2026-10-16" {
		t.Fatalf("same-day expiry vip verify: %+v", u)
	}
	foreverID := seedUser("vip_forever", "vip", "", "active")
	foreverToken := "vip-forever-token-0123456789abcd"
	seedSession(foreverID, foreverToken)
	u, err = s.Verify(ctx, foreverToken)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "vip" || u.VipExpiresAt != "" {
		t.Fatalf("forever vip verify: %+v", u)
	}

	// 5. 普通用户：Role=="free"。
	freeID := seedUser("plain_free", "free", "", "active")
	freeToken := "plain-free-token-0123456789abcde"
	seedSession(freeID, freeToken)
	u, err = s.Verify(ctx, freeToken)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "free" || u.Username != "plain_free" {
		t.Fatalf("plain free verify: %+v", u)
	}
}
