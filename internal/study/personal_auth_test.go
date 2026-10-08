package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"ai_analyze_guokao/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// Catches removing the password-hash CAS while adding login metadata. The actual
// SQLite write lock pauses session insertion after the old hash was verified.
func TestPersonalLoginPreservesHashCAS(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "cas.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Service{DB: db}
	ctx := context.Background()
	u, err := s.Register(ctx, "cas_user", "old-password", "")
	if err != nil {
		t.Fatal(err)
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result := make(chan error, 1)
	go func() {
		_, _, e := s.LoginWithMetadata(ctx, "cas_user", "old-password", LoginMetadata{DeviceLabel: "Chrome / Windows", IP: "203.0.113.42"})
		result <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	stable := 0
	for stable < 3 {
		if time.Now().After(deadline) {
			t.Fatal("login did not reach the locked write transaction")
		}
		if db.Stats().InUse >= 2 {
			stable++
		} else {
			stable = 0
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE app_user SET password_hash=? WHERE id=?`, string(newHash), u.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, ErrCredentials) {
			t.Fatal("stale password created session", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login did not resume")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM app_session WHERE user_id=?`, u.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("stale session persisted", count, err)
	}
	if _, _, err = s.Login(ctx, "cas_user", "new-password"); err != nil {
		t.Fatal("compatible Login wrapper", err)
	}
}

// Catches retaining a token at its exact expiry or returning arbitrary/raw metadata.
func TestPersonalSessionClockAndControlledMetadata(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "clock.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	s := &Service{DB: db, Clock: func() time.Time { return stamp }}
	ctx := context.Background()
	u, err := s.Register(ctx, "clock_user", "testpassword", "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := s.LoginWithMetadata(ctx, "clock_user", "testpassword", LoginMetadata{DeviceLabel: "<script>raw UA</script>", IP: "::ffff:203.0.113.42"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Sessions(ctx, u.ID, token, 1)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	row := page.Items[0]
	if row.DeviceLabel == nil || *row.DeviceLabel != "信息未知" || row.IPHint == nil || *row.IPHint != "203.0.113.*" || row.CreatedAt == nil || *row.CreatedAt != "2026-10-04T00:00:00Z" || row.ExpiresAt != "2026-10-11T00:00:00Z" {
		t.Fatal(row)
	}
	stamp = stamp.Add(SessionTTL)
	if _, err = s.Verify(ctx, token); !errors.Is(err, ErrCredentials) {
		t.Fatal("expired token valid", err)
	}
	page, err = s.Sessions(ctx, u.ID, token, 1)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("expired session listed", page, err)
	}
	if _, err = s.RevokeOthers(ctx, u.ID, token); !errors.Is(err, ErrCredentials) {
		t.Fatal("expired token allowed revoke", err)
	}
}
