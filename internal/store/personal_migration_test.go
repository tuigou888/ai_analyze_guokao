package store

import (
	"path/filepath"
	"testing"
)

// Catches destructive v12 upgrades and fabricated legacy metadata.
func TestPersonalMigrationFromV11PreservesSecretsAndDraft(t *testing.T) {
	all := migrations
	defer func() { migrations = all }()
	migrations = migrations[:11]
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`INSERT INTO app_user(id,username,password_hash,nickname,created_at) VALUES(1,'old_user','unchanged-hash','旧昵称','2025-01-01T00:00:00Z')`, `INSERT INTO app_session(token,user_id,expires_at) VALUES('unchanged-token-hash',1,'2099-01-01T00:00:00Z')`, `INSERT INTO practice_session(id,user_id,kind,spec,question_ids,started_at,draft,draft_revision) VALUES(1,1,'single','{}','[]','2026-01-01T00:00:00Z','[{"answer":"A"}]',3)`} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	migrations = all
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hash, nick, token, publicID, draft string
	var rev int
	if err = db.QueryRow(`SELECT password_hash,nickname FROM app_user WHERE id=1`).Scan(&hash, &nick); err != nil || hash != "unchanged-hash" || nick != "旧昵称" {
		t.Fatal(hash, nick, err)
	}
	if err = db.QueryRow(`SELECT token,public_id FROM app_session WHERE user_id=1 AND created_at IS NULL AND device_label IS NULL AND ip_hint IS NULL`).Scan(&token, &publicID); err != nil || token != "unchanged-token-hash" || len(publicID) < 32 {
		t.Fatal("session migration", token, publicID, err)
	}
	if err = db.QueryRow(`SELECT draft,draft_revision FROM practice_session WHERE id=1`).Scan(&draft, &rev); err != nil || draft != `[{"answer":"A"}]` || rev != 3 {
		t.Fatal("draft changed", draft, rev, err)
	}
	var avatar, questions, minutes, limit, size int
	if err = db.QueryRow(`SELECT avatar_id,daily_questions,daily_minutes,default_limit,reading_size FROM user_profile WHERE user_id=1`).Scan(&avatar, &questions, &minutes, &limit, &size); err != nil || avatar != 0 || questions != 20 || minutes != 30 || limit != 20 || size != 16 {
		t.Fatal("defaults", err, avatar, questions, minutes, limit, size)
	}
}
