package main

import (
	"path/filepath"
	"strings"
	"testing"

	"ai_analyze_guokao/internal/admin"
	"ai_analyze_guokao/internal/store"
)

// TestCmdAdminCreateWithRoleFlag 钉住 gk admin create 的 --role 行为：
// 显式 operator 落库、缺省 super、非法角色报错且不落库。
func TestCmdAdminCreateWithRoleFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gk.sqlite")

	if err := cmdAdmin([]string{"create", "--db", dbPath, "--user", "op", "--password", "longenough", "--role", "operator"}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	if err := db.QueryRow(`SELECT role FROM admin_user WHERE username='op'`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != admin.RoleOperator {
		t.Fatal("--role operator 落库为", role)
	}

	if err := cmdAdmin([]string{"create", "--db", dbPath, "--user", "admin2", "--password", "longenough"}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT role FROM admin_user WHERE username='admin2'`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != admin.RoleSuper {
		t.Fatal("缺省 role 应为 super，实际", role)
	}

	err = cmdAdmin([]string{"create", "--db", dbPath, "--user", "root", "--password", "longenough", "--role", "root"})
	if err == nil {
		t.Fatal("非法 --role root 未报错")
	}
	if !strings.Contains(err.Error(), "角色必须是 super 或 operator") {
		t.Fatal("错误串 =", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admin_user WHERE username='root'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("非法 role 的账号被落库")
	}
}
