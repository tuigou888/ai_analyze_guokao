package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"ai_analyze_guokao/internal/store"
)

func TestResetNeverDeletesExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO app_user(username,password_hash,created_at) VALUES('saved','hash','now')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = cmdIngest([]string{"--db", path, "--data", filepath.Join(dir, "missing"), "--reset"}); err == nil {
		t.Fatal("reset must refuse existing database")
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM app_user WHERE username='saved'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("saved user lost: count=%d err=%v", n, err)
	}
}

func TestCleanPreservesPersistentData(t *testing.T) {
	makefile, err := filepath.Abs("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"db/gk.sqlite", "secret.key", "backups/snapshot.sqlite", "runs/output.jsonl", "gk", "release/gk", "gk-linux-amd64.tar.gz"} {
		path := filepath.Join(dir, "var", name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := exec.Command("make", "-f", makefile, "clean")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("clean: %v %s", err, out)
	}
	for _, name := range []string{"db/gk.sqlite", "secret.key", "backups/snapshot.sqlite", "runs/output.jsonl"} {
		b, err := os.ReadFile(filepath.Join(dir, "var", name))
		if err != nil || string(b) != "keep" {
			t.Fatalf("clean removed %s: %v", name, err)
		}
	}
	for _, name := range []string{"gk", "release/gk", "gk-linux-amd64.tar.gz"} {
		if _, err := os.Stat(filepath.Join(dir, "var", name)); !os.IsNotExist(err) {
			t.Fatalf("build output remains: %s", name)
		}
	}
}
