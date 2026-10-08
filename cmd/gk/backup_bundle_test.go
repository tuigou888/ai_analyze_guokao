package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/store"
)

func TestBackupBundleRestoresEffectiveKey(t *testing.T) {
	for _, mode := range []string{"file", "environment_only", "environment_overrides_stale_file"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("GK_SECRET_KEY", "")
			dir := t.TempDir()
			source := filepath.Join(dir, "source.sqlite")
			key := filepath.Join(dir, "source.key")
			bundle := filepath.Join(dir, "backup")
			db, e := store.Open(source)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			if mode != "file" {
				t.Setenv("GK_SECRET_KEY", "synthetic-environment-backup-passphrase")
			}
			if mode == "environment_overrides_stale_file" {
				if e = os.WriteFile(key, []byte(strings.Repeat("0", 64)), 0600); e != nil {
					t.Fatal(e)
				}
			}
			settings, e := setting.Open(db, key)
			if e != nil {
				t.Fatal(e)
			}
			if e = settings.Set(context.Background(), setting.KeyAPIKey, "synthetic-restored-api-key", "test"); e != nil {
				t.Fatal(e)
			}
			if e = cmdBackup([]string{"--db", source, "--bundle", bundle, "--secret-key-file", key}); e != nil {
				t.Fatal(e)
			}
			t.Setenv("GK_SECRET_KEY", "synthetic-wrong-environment")
			if e = cmdBackup([]string{"--verify-bundle", bundle}); e != nil {
				t.Fatal("verification depended on environment", e)
			}
			t.Setenv("GK_SECRET_KEY", "") // Restore must not depend on the source machine's environment.
			snapshotData, e := os.ReadFile(filepath.Join(bundle, "gk.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			restoredPath := filepath.Join(dir, "restored.sqlite")
			if e = os.WriteFile(restoredPath, snapshotData, 0600); e != nil {
				t.Fatal(e)
			}
			restored, e := store.Open(restoredPath)
			if e != nil {
				t.Fatal(e)
			}
			defer restored.Close()
			rs, e := setting.Open(restored, filepath.Join(bundle, "secret.key"))
			if e != nil {
				t.Fatal("bundle cannot decrypt", e)
			}
			if value, e := rs.Get(context.Background(), setting.KeyAPIKey); e != nil || value != "synthetic-restored-api-key" {
				t.Fatal("setting not restored", e)
			}
			for _, file := range []string{"gk.sqlite", "secret.key", "complete.json"} {
				info, e := os.Stat(filepath.Join(bundle, file))
				if e != nil {
					t.Fatal(e)
				}
				if info.Mode().Perm() != 0600 {
					t.Fatal("public backup permissions", file, info.Mode())
				}
			}
			marker, e := os.ReadFile(filepath.Join(bundle, "complete.json"))
			if e != nil {
				t.Fatal(e)
			}
			var m map[string]any
			if e = json.Unmarshal(marker, &m); e != nil {
				t.Fatal(e)
			}
			if m["format"] != "gk-backup-v1" {
				t.Fatal("no completion marker", m)
			}
			if mode == "environment_only" {
				if _, e = os.Stat(key); !os.IsNotExist(e) {
					t.Fatal("backup generated source key", e)
				}
			}
			if e = cmdBackup([]string{"--db", source, "--bundle", bundle, "--secret-key-file", key}); e == nil {
				t.Fatal("overwrote existing backup")
			}
			if _, e = os.Stat(filepath.Join(bundle, "complete.json")); e != nil {
				t.Fatal("existing backup deleted on error", e)
			}
			// Detect an accidental modification even if SQLite can still read it.
			data, e := os.ReadFile(filepath.Join(bundle, "gk.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(bundle, "gk.sqlite"), append(data, 1), 0600); e != nil {
				t.Fatal(e)
			}
			if e = cmdBackup([]string{"--verify-bundle", bundle}); e == nil {
				t.Fatal("corrupted bundle accepted")
			}
			if e = os.Remove(filepath.Join(bundle, "complete.json")); e != nil {
				t.Fatal(e)
			}
			if e = cmdBackup([]string{"--verify-bundle", bundle}); e == nil {
				t.Fatal("incomplete bundle accepted")
			}
		})
	}
}

func TestBackupBundleRejectsMissingOrMismatchedKey(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_%t", missing), func(t *testing.T) {
			t.Setenv("GK_SECRET_KEY", "")
			dir := t.TempDir()
			source := filepath.Join(dir, "source.sqlite")
			key := filepath.Join(dir, "source.key")
			bundle := filepath.Join(dir, "backup")
			db, e := store.Open(source)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			settings, e := setting.Open(db, key)
			if e != nil {
				t.Fatal(e)
			}
			if e = settings.Set(context.Background(), setting.KeyAPIKey, "synthetic-private-key", "test"); e != nil {
				t.Fatal(e)
			}
			if missing {
				e = os.Remove(key)
			} else {
				e = os.WriteFile(key, []byte(strings.Repeat("0", 64)), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = cmdBackup([]string{"--db", source, "--bundle", bundle, "--secret-key-file", key}); e == nil {
				t.Fatal("unrestorable backup reported success")
			}
			if _, e = os.Stat(bundle); !os.IsNotExist(e) {
				t.Fatal("failed bundle left a complete-looking directory", e)
			}
		})
	}
}
