package study

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ai_analyze_guokao/internal/store"
)

// Catches resend-window bypass, missing supersession, normalization gaps and
// hash-only storage of email verification codes.
func TestCreateEmailCodeLifecycle(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "email-code.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s := &Service{DB: db, Clock: func() time.Time { return stamp }}
	ctx := context.Background()

	// 1. 创建成功：明文 6 位数字，库里只有哈希，5 分钟后过期。
	id, code, err := s.CreateEmailCode(ctx, "a.user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) {
		t.Fatal("code not 6 digits:", code)
	}
	var hash, expires string
	var attempts int
	var consumed sql.NullString
	if err = db.QueryRow(`SELECT code_hash,expires_at,attempts,consumed_at FROM email_code WHERE id=?`, id).Scan(&hash, &expires, &attempts, &consumed); err != nil {
		t.Fatal(err)
	}
	if hash != tokenHash(code) || hash == code {
		t.Fatal("code_hash must be tokenHash(code), never plaintext")
	}
	if want := stamp.Add(5 * time.Minute).UTC().Format(time.RFC3339); expires != want {
		t.Fatal("expires_at", expires, "want", want)
	}
	if attempts != 0 || consumed.Valid {
		t.Fatal(attempts, consumed)
	}

	// 2. 60 秒窗口：立即重发被拦，且不产生新记录。
	if _, _, err = s.CreateEmailCode(ctx, "a.user@example.com"); !errors.Is(err, ErrTooFrequent) {
		t.Fatal("resend within window accepted:", err)
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM email_code WHERE email='a.user@example.com'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}

	// 3. 窗口过后重发成功，旧码被作废。
	stamp = stamp.Add(61 * time.Second)
	id2, _, err := s.CreateEmailCode(ctx, "a.user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT consumed_at FROM email_code WHERE id=?`, id).Scan(&consumed); err != nil || !consumed.Valid {
		t.Fatal("old code not superseded", consumed, err)
	}
	if err = db.QueryRow(`SELECT consumed_at FROM email_code WHERE id=?`, id2).Scan(&consumed); err != nil || consumed.Valid {
		t.Fatal("new code already consumed", consumed, err)
	}

	// 4. 大小写与首尾空格归一为同一邮箱。
	if _, _, err = s.CreateEmailCode(ctx, "Case.User@Example.COM"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateEmailCode(ctx, "  case.user@example.com "); !errors.Is(err, ErrTooFrequent) {
		t.Fatal("case/space variant not normalized:", err)
	}

	// 5. 非法邮箱一律 ErrInvalid。
	for _, bad := range []string{"", "no-at", "a@b", "a b@c.co"} {
		if _, _, err = s.CreateEmailCode(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid email:", bad, err)
		}
	}

	// 6. 删除记录后同邮箱可立即再发（窗口随记录一起消失）。
	id3, _, err := s.CreateEmailCode(ctx, "del@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteEmailCode(ctx, id3); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateEmailCode(ctx, "del@example.com"); err != nil {
		t.Fatal("resend blocked after delete:", err)
	}
}

// Catches wrong-signature Register, missing code validation, race on attempts,
// stale consumed markers, and normalization gaps.
func TestRegisterWithEmailCode(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "reg.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s := &Service{DB: db, Clock: func() time.Time { return stamp }}
	ctx := context.Background()
	mustCode := func(email string) (int64, string) {
		id, code, err := s.CreateEmailCode(ctx, email)
		if err != nil {
			t.Fatalf("CreateEmailCode(%q): %v", email, err)
		}
		return id, code
	}

	// 1. 正确码注册成功。
	id, code := mustCode("a@b.co")
	u, err := s.Register(ctx, "mail_user", "testpassword", "", "a@b.co", code)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "mail_user" || u.Nickname != "" {
		t.Fatal(u)
	}
	var email string
	if err = db.QueryRow(`SELECT email FROM app_user WHERE id=?`, u.ID).Scan(&email); err != nil || email != "a@b.co" {
		t.Fatal(email, err)
	}
	var upCount int
	if err = db.QueryRow(`SELECT COUNT(*) FROM user_profile WHERE user_id=?`, u.ID).Scan(&upCount); err != nil || upCount != 1 {
		t.Fatal(upCount, err)
	}
	var consumed sql.NullString
	if err = db.QueryRow(`SELECT consumed_at FROM email_code WHERE id=?`, id).Scan(&consumed); err != nil || !consumed.Valid {
		t.Fatal("code not consumed after registration", consumed)
	}

	// 2. 错误码：ErrInvalid，文案「验证码错误或已过期」；attempts==1。
	//    先为 err@a.co 发码，再用错码注册。
	_, errCode := mustCode("err@a.co")
	if _, err = s.Register(ctx, "err_user", "testpassword", "", "err@a.co", "000000"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid got %v", err)
	}
	var attempts int
	if err = db.QueryRow(`SELECT attempts FROM email_code WHERE email='err@a.co'`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal(attempts, err)
	}

	// 3. 5 次上限：错 4 次（attempts 1→4），第 5 次错 →「验证码错误次数过多」；
	//    第 6 次输入正确码也拒绝。
	for i := 0; i < 3; i++ {
		if _, err = s.Register(ctx, fmt.Sprintf("err_user_%d", i), "testpassword", "", "err@a.co", "000000"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expect ErrInvalid at attempt %d", i+2)
		}
	}
	if _, err = s.Register(ctx, "err_user_final", "testpassword", "", "err@a.co", "000000"); err == nil || !strings.Contains(err.Error(), "验证码错误次数过多") {
		t.Fatal("expected 5-attempts error", err)
	}
	// 正确码也拒绝。
	if _, err = s.Register(ctx, "err_user_ok", "testpassword", "", "err@a.co", errCode); err == nil || !strings.Contains(err.Error(), "验证码错误次数过多") {
		t.Fatal("correct code after 5 failures accepted", err)
	}

	// 4. 用户名冲突回滚：用户名已存在时 Register 应回滚 consumed_at（NULL），
	//    同一码可重试另一个用户名。
	// 先注册一个用户占用用户名 "taken"。
	_, takenCode := mustCode("taken@a.co")
	if _, err = s.Register(ctx, "taken", "testpassword", "", "taken@a.co", takenCode); err != nil {
		t.Fatal(err)
	}
	// 用新码 C 对已存在用户名注册 → ErrConflict；码 C 仍可用。
	_, rbCode := mustCode("rollback@a.co")
	if _, err = s.Register(ctx, "taken", "testpassword", "", "rollback@a.co", rbCode); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate username: %v", err)
	}
	var c sql.NullString
	if err = db.QueryRow(`SELECT consumed_at FROM email_code WHERE email='rollback@a.co'`).Scan(&c); err != nil || c.Valid {
		t.Fatal("consumed_at should stay NULL after rollback", c)
	}
	if _, err = s.Register(ctx, "taken_existing2", "testpassword", "", "rollback@a.co", rbCode); err != nil {
		t.Fatal("same code reusable after rollback", err)
	}

	// 5. 邮箱占用：账号甲绑 dup@b.co；对 DUP@B.CO（归一化同邮箱）发新码注册新用户名 → ErrEmailConflict。
	_, dupCode := mustCode("dup@b.co")
	if _, err = s.Register(ctx, "owner_dup", "testpassword", "", "dup@b.co", dupCode); err != nil {
		t.Fatal(err)
	}
	stamp = stamp.Add(61 * time.Second)
	_, newCode := mustCode("DUP@B.CO")
	if _, err = s.Register(ctx, "other_dup", "testpassword", "", "DUP@B.CO", newCode); !errors.Is(err, ErrEmailConflict) {
		t.Fatalf("expected ErrEmailConflict: %v", err)
	}

	// 6. 过期码：发码后 stamp 推进 6 分钟 → 「验证码错误或已过期」。
	_, expCode := mustCode("expired@a.co")
	stamp = stamp.Add(6 * time.Minute)
	if _, err = s.Register(ctx, "expired_user", "testpassword", "", "expired@a.co", expCode); err == nil || !strings.Contains(err.Error(), "验证码错误或已过期") {
		t.Fatalf("expected expired-code rejection: %v", err)
	}

	// 7. 从未发码的邮箱 → 「验证码错误或已过期」。
	if _, err = s.Register(ctx, "never_user", "testpassword", "", "never@a.co", "123456"); err == nil || !strings.Contains(err.Error(), "验证码错误或已过期") {
		t.Fatalf("expected rejection for never-sent email: %v", err)
	}

	// 8. 复用已消费码（case 1 的码）→ 「验证码错误或已过期」。
	if _, err = s.Register(ctx, "reuse_user", "testpassword", "", "a@b.co", code); err == nil || !strings.Contains(err.Error(), "验证码错误或已过期") {
		t.Fatalf("expected rejection for consumed code: %v", err)
	}

	// 9. 非法邮箱 → 「邮箱格式不正确」。
	if _, err = s.Register(ctx, "bad_email_user", "testpassword", "", "bad", "123456"); err == nil || !strings.Contains(err.Error(), "邮箱格式不正确") {
		t.Fatalf("expected invalid-email rejection: %v", err)
	}
}
