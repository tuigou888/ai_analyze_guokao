package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"ai_analyze_guokao/internal/setting"
)

type backupManifest struct {
	Format         string `json:"format"`
	CreatedAt      string `json:"created_at"`
	DatabaseSHA256 string `json:"database_sha256"`
	KeySHA256      string `json:"key_sha256"`
}

func createBackupBundle(source, target, keyFile string) (err error) {
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	// Reserve an exclusive, private directory. A crash before complete.json leaves
	// an incomplete bundle; verification and restore must reject it.
	if err = os.Mkdir(target, 0700); err != nil {
		return fmt.Errorf("备份目录必须不存在: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(target)
		}
	}()
	snapshot := filepath.Join(target, "gk.sqlite")
	if err = createSnapshot(source, snapshot); err != nil {
		return err
	}
	db, err := openBackupSnapshot(snapshot)
	if err != nil {
		return err
	}
	defer db.Close()
	key, err := setting.ExistingKeyForBackup(db, keyFile)
	if err != nil {
		return fmt.Errorf("无法备份与快照匹配的有效密钥: %w", err)
	}
	bundledKey := filepath.Join(target, "secret.key")
	if err = writePrivateFile(bundledKey, []byte(hex.EncodeToString(key))); err != nil {
		return err
	}
	if err = checkBackupDatabase(db, bundledKey); err != nil {
		return err
	}
	dbHash, err := backupHash(snapshot)
	if err != nil {
		return err
	}
	keyHash, err := backupHash(bundledKey)
	if err != nil {
		return err
	}
	manifest, err := json.Marshal(backupManifest{"gk-backup-v1", time.Now().UTC().Format(time.RFC3339), dbHash, keyHash})
	if err != nil {
		return err
	}
	if err = writePrivateFile(filepath.Join(target, "complete.json"), manifest); err != nil {
		return err
	}
	complete = true
	fmt.Printf("完整数据库与密钥备份已生成并校验: %s\n图片目录需另行备份；请将完整目录安全复制到异机。\n", target)
	return nil
}

func openBackupSnapshot(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro&_pragma=busy_timeout(10000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func checkBackupDatabase(db *sql.DB, keyFile string) error {
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("备份数据库完整性检查失败")
	}
	return setting.ValidateBackupKey(db, keyFile)
}

func verifyBackupBundle(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "complete.json"))
	if err != nil {
		return err
	}
	var manifest backupManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if manifest.Format != "gk-backup-v1" {
		return errors.New("未知备份格式或备份未完成")
	}
	snapshot, keyFile := filepath.Join(dir, "gk.sqlite"), filepath.Join(dir, "secret.key")
	for _, entry := range []struct{ path, expected string }{{snapshot, manifest.DatabaseSHA256}, {keyFile, manifest.KeySHA256}} {
		actual, e := backupHash(entry.path)
		if e != nil {
			return e
		}
		if actual != entry.expected {
			return errors.New("备份文件校验值不匹配")
		}
	}
	db, err := openBackupSnapshot(snapshot)
	if err != nil {
		return err
	}
	defer db.Close()
	return checkBackupDatabase(db, keyFile)
}

func backupHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writePrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
