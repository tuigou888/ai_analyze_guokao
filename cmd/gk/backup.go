package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// cmdBackup uses SQLite's consistent VACUUM INTO snapshot, including committed
// WAL content. It reads the source without migrations and refuses overwrite.
func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dbFile := fs.String("db", "var/db/gk.sqlite", "源数据库路径")
	bundle := fs.String("bundle", "", "完整备份新目录（快照 + 有效密钥 + 完成标记）")
	verify := fs.String("verify-bundle", "", "只读校验完整备份目录")
	keyFile := fs.String("secret-key-file", secretKeyFile, "源配置密钥文件；GK_SECRET_KEY 优先")
	out := fs.String("out", "", "新快照文件路径（必须不存在）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *verify != "" {
		if *out != "" || *bundle != "" {
			return errors.New("校验模式不能同时生成备份")
		}
		if err := verifyBackupBundle(*verify); err != nil {
			return err
		}
		fmt.Println("完整备份校验通过")
		return nil
	}
	if *bundle != "" {
		if *out != "" {
			return errors.New("--out 与 --bundle 只能选择一个")
		}
		return createBackupBundle(*dbFile, *bundle, *keyFile)
	}
	if *out == "" {
		return errors.New("用法: gk backup --db <源库> --bundle <新目录> 或 --out <快照文件>")
	}
	if err := createSnapshot(*dbFile, *out); err != nil {
		return err
	}
	fmt.Printf("一致性快照已生成: %s\n这是仅数据库快照；完整备份请使用 --bundle，图片目录另行备份。\n", *out)
	return nil
}

func createSnapshot(dbFile, out string) error {
	source, err := filepath.Abs(dbFile)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if source == target {
		return errors.New("快照路径不能等于源数据库")
	}
	if _, err = os.Stat(target); err == nil {
		return errors.New("快照文件已存在，请使用新的文件名")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	dsn := (&url.URL{Scheme: "file", Path: source, RawQuery: "mode=ro&_pragma=busy_timeout(10000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	stage, err := os.MkdirTemp(filepath.Dir(target), ".gk-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, "snapshot.sqlite")
	if _, err = db.Exec(`VACUUM INTO '` + strings.ReplaceAll(staged, `'`, `''`) + `'`); err != nil {
		return fmt.Errorf("生成快照失败: %w", err)
	}
	if err = os.Chmod(staged, 0600); err != nil {
		return err
	}
	snapshot, err := os.OpenFile(staged, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = snapshot.Sync()
	closeErr := snapshot.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(staged, target); err != nil {
		return fmt.Errorf("创建快照目标失败（文件不能已存在）: %w", err)
	}
	return nil
}
