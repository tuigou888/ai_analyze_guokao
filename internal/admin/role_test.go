package admin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"ai_analyze_guokao/internal/store"
)

// TestRoleLifecycle 按计划断言清单走一遍角色全链路。
func TestRoleLifecycle(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()

	// Create("op","password8",RoleOperator) → Login → (token=="非空", name=="op", role=="operator")
	if e = Create(ctx, db, "op", "password8", RoleOperator); e != nil {
		t.Fatal(e)
	}
	if !ValidRole(RoleSuper) || !ValidRole(RoleOperator) || ValidRole("root") || ValidRole("") {
		t.Fatal("ValidRole 边界")
	}
	token, name, role, e := Login(ctx, db, "op", "password8")
	if e != nil {
		t.Fatal(e)
	}
	if token == "" || name != "op" || role != RoleOperator {
		t.Fatal("Login →", token, name, role)
	}

	// Create 非法 role "root" → ErrRoleInvalid；重复用户名 → ErrDuplicate
	if e = Create(ctx, db, "root", "password8", "root"); !errors.Is(e, ErrRoleInvalid) {
		t.Fatal("want ErrRoleInvalid, got", e)
	}
	if e = Create(ctx, db, "op", "password8", RoleSuper); !errors.Is(e, ErrDuplicate) {
		t.Fatal("want ErrDuplicate, got", e)
	}

	// Create("admin2","password8",RoleSuper)；Verify(token) role=="operator"
	if e = Create(ctx, db, "admin2", "password8", RoleSuper); e != nil {
		t.Fatal(e)
	}
	if vn, vr, e := Verify(ctx, db, token); e != nil || vn != "op" || vr != RoleOperator {
		t.Fatal("Verify →", vn, vr, e)
	}

	// 计划要求 CountByRole(RoleSuper)==2 且「先删掉另一个 super」，
	// 因此在 op/operator 之外补第二、第三个账号：admin2/super、root/super。
	if e = Create(ctx, db, "root", "password8", RoleSuper); e != nil {
		t.Fatal(e)
	}

	// CountByRole(RoleSuper)==2
	if n, e := CountByRole(ctx, db, RoleSuper); e != nil || n != 2 {
		t.Fatal("CountByRole(super) →", n, e)
	}
	// ListAdmins 行按 id 升序且字段齐全。
	rows, e := ListAdmins(ctx, db)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 3 {
		t.Fatal("ListAdmins 行数", len(rows))
	}
	if rows[0].ID != 1 || rows[0].Username != "op" || rows[0].Role != RoleOperator ||
		rows[0].CreatedAt == "" || rows[0].LastLoginAt == "" {
		t.Fatal("op 行", rows[0])
	}
	if rows[1].ID != 2 || rows[1].Username != "admin2" || rows[1].Role != RoleSuper || rows[1].LastLoginAt != "" {
		t.Fatal("admin2 行", rows[1])
	}
	if rows[2].ID != 3 || rows[2].Username != "root" || rows[2].Role != RoleSuper {
		t.Fatal("root 行", rows[2])
	}
	var opID, admin2ID, rootID int64
	for _, r := range rows {
		switch r.Username {
		case "op":
			opID = r.ID
		case "admin2":
			admin2ID = r.ID
		case "root":
			rootID = r.ID
		}
	}
	if opID == 0 || admin2ID == 0 || rootID == 0 {
		t.Fatal("账号 id 缺失", opID, admin2ID, rootID)
	}

	// Audit("root","user_ban","user:7","{}") 逐字回读；created_at 长度>0
	if e = Audit(ctx, db, "root", "user_ban", "user:7", "{}"); e != nil {
		t.Fatal(e)
	}
	var aAdmin, aAction, aTarget, aDetail, aCreated string
	if e = db.QueryRowContext(ctx, `SELECT admin, action, target, detail, created_at FROM admin_audit WHERE admin='root'`).
		Scan(&aAdmin, &aAction, &aTarget, &aDetail, &aCreated); e != nil {
		t.Fatal(e)
	}
	if aAdmin != "root" || aAction != "user_ban" || aTarget != "user:7" || aDetail != "{}" || aCreated == "" {
		t.Fatal("审计回读", aAdmin, aAction, aTarget, aDetail, aCreated)
	}

	// SetPasswordByID(opID,"newpass99") 后旧 token Verify 失败
	if e = SetPasswordByID(ctx, db, opID, "newpass99"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = Verify(ctx, db, token); e == nil {
		t.Fatal("旧 token 未被吊销")
	}
	if e = SetPasswordByID(ctx, db, opID, "shortpw"); e == nil {
		t.Fatal("8 位以下口令被接受")
	}
	if e = SetPasswordByID(ctx, db, 9999, "newpass99"); !errors.Is(e, ErrNoTarget) {
		t.Fatal("want ErrNoTarget, got", e)
	}
	token2, _, role, e := Login(ctx, db, "op", "newpass99")
	if e != nil || token2 == "" || role != RoleOperator {
		t.Fatal("改密后登录 →", token2, role, e)
	}

	// Delete(selfID,"op") → ErrSelfDelete
	if e = Delete(ctx, db, opID, "op"); !errors.Is(e, ErrSelfDelete) {
		t.Fatal("want ErrSelfDelete, got", e)
	}
	// 删最后一个 super → ErrLastSuper（先删掉另一个 super）
	if e = Delete(ctx, db, admin2ID, "op"); e != nil {
		t.Fatal(e)
	}
	if e = Delete(ctx, db, rootID, "op"); !errors.Is(e, ErrLastSuper) {
		t.Fatal("want ErrLastSuper, got", e)
	}
	if e = Delete(ctx, db, 9999, "op"); !errors.Is(e, ErrNoTarget) {
		t.Fatal("want ErrNoTarget, got", e)
	}
	// 删除 operator 成功后再 Verify 旧 token 失败
	if e = Delete(ctx, db, opID, "root"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = Verify(ctx, db, token2); e == nil {
		t.Fatal("旧 token 未随账号删除失效")
	}
	if n, e := CountByRole(ctx, db, RoleSuper); e != nil || n != 1 {
		t.Fatal("剩余 super →", n, e)
	}
	if n, e := CountByRole(ctx, db, RoleOperator); e != nil || n != 0 {
		t.Fatal("剩余 operator →", n, e)
	}
}

// TestEnsureCreatesSuper 最小验证 Ensure 签名不变且以 RoleSuper 引导。
func TestEnsureCreatesSuper(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	created, e := Ensure(ctx, db, "boss", "password8")
	if e != nil || !created {
		t.Fatal("首建 →", created, e)
	}
	if created, e = Ensure(ctx, db, "boss", "password8"); e != nil || created {
		t.Fatal("幂等 →", created, e)
	}
	if _, _, role, e := Login(ctx, db, "boss", "password8"); e != nil || role != RoleSuper {
		t.Fatal("role →", role, e)
	}
}

// TestSetPasswordByIDRevocationRollback 是 TestPasswordAndRevocationRollbackTogether
// 的 by-ID 等价用例：吊销会话被注入失败时，改密必须整体回滚。
func TestSetPasswordByIDRevocationRollback(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	if e = Create(ctx, db, "admin", "originalpw", RoleSuper); e != nil {
		t.Fatal(e)
	}
	rows, e := ListAdmins(ctx, db)
	if e != nil || len(rows) != 1 {
		t.Fatal("ListAdmins", rows, e)
	}
	id := rows[0].ID
	token, _, _, e := Login(ctx, db, "admin", "originalpw")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TRIGGER fail_revocation BEFORE DELETE ON admin_session BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); e != nil {
		t.Fatal(e)
	}
	if e = SetPasswordByID(ctx, db, id, "replacementpw"); e == nil {
		t.Fatal("injected failure missing")
	}
	if _, _, _, e = Login(ctx, db, "admin", "replacementpw"); e == nil {
		t.Fatal("password committed despite revocation failure")
	}
	if _, _, _, e = Login(ctx, db, "admin", "originalpw"); e != nil {
		t.Fatal("old password not restored", e)
	}
	if _, _, e = Verify(ctx, db, token); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`DROP TRIGGER fail_revocation`); e != nil {
		t.Fatal(e)
	}
	if e = SetPasswordByID(ctx, db, id, "replacementpw"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = Verify(ctx, db, token); e == nil {
		t.Fatal("old token survived successful change")
	}
}

// TestChangePasswordRevokesSessions 钉住并发保护谓词不得带进吊销语句：
// ChangePassword 的 expectedHash 非空，UPDATE 后行上已是新哈希；若吊销复用
// 同一谓词会匹配不到行、静默漏吊销。旧 token 必须失效。
func TestChangePasswordRevokesSessions(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	if e = Create(ctx, db, "admin", "originalpw", RoleSuper); e != nil {
		t.Fatal(e)
	}
	token, _, _, e := Login(ctx, db, "admin", "originalpw")
	if e != nil {
		t.Fatal(e)
	}
	if e = ChangePassword(ctx, db, "admin", "originalpw", "replacementpw"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = Verify(ctx, db, token); e == nil {
		t.Fatal("改密后旧 token 应失效")
	}
	if _, _, _, e = Login(ctx, db, "admin", "replacementpw"); e != nil {
		t.Fatal("新口令应可登录", e)
	}
}
