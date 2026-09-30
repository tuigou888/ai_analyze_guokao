package main

import (
	"ai_analyze_guokao/internal/store"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupIncludesCommittedWALAndRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.sqlite")
	dst := filepath.Join(dir, "quote's snapshot.sqlite")
	db, err := store.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO question(content_hash,stem,answer_type) VALUES('backup-fixture','中文题目','single')`); err != nil {
		t.Fatal(err)
	}
	if err = cmdBackup([]string{"--db", src, "--out", dst}); err != nil {
		t.Fatal(err)
	}
	snap, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	var n int
	if err = snap.QueryRow(`SELECT COUNT(*) FROM question WHERE content_hash='backup-fixture'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("snapshot permissions", err)
	}
	if err = cmdBackup([]string{"--db", src, "--out", dst}); err == nil {
		t.Fatal("overwrites snapshot")
	}
	if err = cmdBackup([]string{"--db", src, "--out", src}); err == nil {
		t.Fatal("overwrites source")
	}
}
