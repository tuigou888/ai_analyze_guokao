package study

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
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
