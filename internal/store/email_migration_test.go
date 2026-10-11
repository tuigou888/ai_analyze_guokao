package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Catches a destructive v13 upgrade or a unique index that rejects legacy
// empty-email accounts.
func TestEmailMigrationFromV12(t *testing.T) {
	all := migrations
	defer func() { migrations = all }()
	migrations = migrations[:12]
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO app_user(id,username,password_hash,nickname,created_at) VALUES(1,'old_user','h','旧昵称','2025-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	migrations = all
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var email string
	if err = db.QueryRow(`SELECT email FROM app_user WHERE id=1`).Scan(&email); err != nil || email != "" {
		t.Fatal(email, err)
	}
	if _, err = db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES('u2','h','','2025-01-02T00:00:00Z','')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES('u3','h','','2025-01-03T00:00:00Z','a@b.co')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES('u4','h','','2025-01-04T00:00:00Z','a@b.co')`); err == nil {
		t.Fatal("duplicate email accepted")
	}
	res, err := db.Exec(`INSERT INTO email_code(email,code_hash,expires_at,created_at) VALUES('a@b.co','x','2026-10-10T00:05:00Z','2026-10-10T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	var attempts int
	var consumed sql.NullString
	if err = db.QueryRow(`SELECT attempts,consumed_at FROM email_code WHERE email='a@b.co'`).Scan(&attempts, &consumed); err != nil || attempts != 0 || consumed.Valid {
		t.Fatal(attempts, consumed, err)
	}
	_ = res
}
