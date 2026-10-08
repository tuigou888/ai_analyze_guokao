package admin

import (
	"ai_analyze_guokao/internal/store"
	"context"
	"path/filepath"
	"testing"
)

func TestPasswordAndRevocationRollbackTogether(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	if e = Create(ctx, db, "admin", "originalpw"); e != nil {
		t.Fatal(e)
	}
	token, e := Login(ctx, db, "admin", "originalpw")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TRIGGER fail_revocation BEFORE DELETE ON admin_session BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); e != nil {
		t.Fatal(e)
	}
	if e = SetPassword(ctx, db, "admin", "replacementpw"); e == nil {
		t.Fatal("injected failure missing")
	}
	if _, e = Login(ctx, db, "admin", "replacementpw"); e == nil {
		t.Fatal("password committed despite revocation failure")
	}
	if _, e = Login(ctx, db, "admin", "originalpw"); e != nil {
		t.Fatal("old password not restored", e)
	}
	if _, e = Verify(ctx, db, token); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`DROP TRIGGER fail_revocation`); e != nil {
		t.Fatal(e)
	}
	if e = SetPassword(ctx, db, "admin", "replacementpw"); e != nil {
		t.Fatal(e)
	}
	if _, e = Verify(ctx, db, token); e == nil {
		t.Fatal("old token survived successful change")
	}
}
