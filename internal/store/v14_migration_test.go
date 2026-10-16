package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// Catches v14 upgrades losing legacy identities or daily-usage/audit invariants.
func TestV14MigrationAddsRolesStatusAndAudit(t *testing.T) {
	all := migrations
	defer func() { migrations = all }()
	migrations = migrations[:13]
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO admin_user(username,password_hash,created_at) VALUES('root','hash','2025-01-01T00:00:00Z')`,
		`INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES('u1','hash','','2025-01-01T00:00:00Z','')`,
	} {
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
	var adminRole string
	if err = db.QueryRow(`SELECT role FROM admin_user WHERE username='root'`).Scan(&adminRole); err != nil || adminRole != "super" {
		t.Fatal("admin role", adminRole, err)
	}
	var role, status string
	var vipExpiresAt any
	if err = db.QueryRow(`SELECT role,status,vip_expires_at FROM app_user WHERE username='u1'`).Scan(&role, &status, &vipExpiresAt); err != nil || role != "free" || status != "active" || vipExpiresAt != nil {
		t.Fatal("user defaults", role, status, vipExpiresAt, err)
	}
	if _, err = db.Exec(`INSERT INTO daily_usage(user_id,day,questions,papers) VALUES(1,'2026-10-16',5,0)`); err != nil {
		t.Fatal("daily_usage insert", err)
	}
	if _, err = db.Exec(`INSERT INTO daily_usage(user_id,day,questions,papers) VALUES(1,'2026-10-16',1,1)`); err == nil {
		t.Fatal("daily_usage duplicate (user_id,day) accepted")
	} else if !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("daily_usage want UNIQUE conflict, got %v", err)
	}
	if _, err = db.Exec(`INSERT INTO admin_audit(admin,action,target,detail,created_at) VALUES('root','user_ban','user:7','{}','2026-10-16T00:00:00Z')`); err != nil {
		t.Fatal("admin_audit insert", err)
	}
	var admin, action, target, detail, createdAt string
	if err = db.QueryRow(`SELECT admin,action,target,detail,created_at FROM admin_audit WHERE admin='root'`).Scan(&admin, &action, &target, &detail, &createdAt); err != nil || admin != "root" || action != "user_ban" || target != "user:7" || detail != "{}" || createdAt == "" {
		t.Fatal("admin_audit roundtrip", admin, action, target, detail, createdAt, err)
	}
}
