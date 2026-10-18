package study

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"ai_analyze_guokao/internal/store"
)

func newGuardFixture(t *testing.T) (*Service, func(string, string, string) int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "practice-guard.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	stamp := time.Date(2026, 10, 16, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	s := &Service{DB: db, Clock: func() time.Time { return stamp }}
	if _, err := db.Exec(`INSERT INTO question(id,content_hash,stem,answer,answer_type,option_count) VALUES(1,'guard-1','题1','A','single',2)`); err != nil {
		t.Fatal(err)
	}
	seedUser := func(username, role, status string) int64 {
		t.Helper()
		res, err := db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,role,status) VALUES(?,?,?,?,?,?)`,
			username, "x", username, stamp.UTC().Format(time.RFC3339), role, status)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	return s, seedUser
}

// Catches the paperset+question_ids bypass: one paper slot charged for an
// arbitrary up-to-200-question self-picked session.
func TestCreateSessionPapersetRejectsQuestionIDs(t *testing.T) {
	s, seedUser := newGuardFixture(t)
	ctx := context.Background()
	uid := seedUser("bypass", "free", "active")

	if _, err := s.CreateSession(ctx, uid, "paperset", Spec{QuestionIDs: []int64{1}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("paperset+question_ids: got %v, want ErrInvalid", err)
	}
	var sessions int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM practice_session WHERE user_id=?`, uid).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("paperset+question_ids minted %d sessions (%v), want 0", sessions, err)
	}
	var usageRows int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM daily_usage WHERE user_id=?`, uid).Scan(&usageRows); err != nil || usageRows != 0 {
		t.Fatalf("paperset+question_ids wrote %d daily_usage rows (%v), want 0", usageRows, err)
	}
}

// Catches banned users creating sessions when the in-transaction status read
// only checks role/vip_expires_at.
func TestCreateSessionBannedUserRejected(t *testing.T) {
	s, seedUser := newGuardFixture(t)
	ctx := context.Background()
	uid := seedUser("banned_mid", "free", "banned")

	if _, err := s.CreateSession(ctx, uid, "single", Spec{QuestionIDs: []int64{1}}); !errors.Is(err, ErrCredentials) {
		t.Fatalf("banned CreateSession: got %v, want ErrCredentials", err)
	}
	var sessions int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM practice_session WHERE user_id=?`, uid).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("banned user minted %d sessions (%v), want 0", sessions, err)
	}
	var usageRows int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM daily_usage WHERE user_id=?`, uid).Scan(&usageRows); err != nil || usageRows != 0 {
		t.Fatalf("banned user wrote %d daily_usage rows (%v), want 0", usageRows, err)
	}
}
