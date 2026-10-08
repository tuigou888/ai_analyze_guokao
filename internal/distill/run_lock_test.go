package distill

import (
	"ai_analyze_guokao/internal/store"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunLockAcrossProcessesAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	release, e := acquireRunLock(context.Background(), db, "run")
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	run := func(want string) {
		t.Helper()
		c := exec.Command(bin, "-test.run=^TestRunLockChild$")
		c.Env = append(os.Environ(), "GK_LOCK_CHILD="+path, "GK_LOCK_EXPECT="+want)
		if out, e := c.CombinedOutput(); e != nil {
			t.Fatalf("child: %v %s", e, out)
		}
	}
	run("locked")
	release()
	run("free")
}
func TestRunLockChild(t *testing.T) {
	path := os.Getenv("GK_LOCK_CHILD")
	if path == "" {
		t.Skip("subprocess helper")
	}
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	release, e := acquireRunLock(context.Background(), db, "run")
	if os.Getenv("GK_LOCK_EXPECT") == "locked" {
		if !errors.Is(e, ErrRunActive) {
			if release != nil {
				release()
			}
			t.Fatal("lock not held by parent", e)
		}
	} else {
		if e != nil {
			t.Fatal(e)
		}
		release()
	}
}
