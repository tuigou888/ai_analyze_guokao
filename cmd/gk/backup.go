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
	out := fs.String("out", "", "新快照文件路径（必须不存在）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("用法: gk backup --db <源数据库> --out <新快照文件>")
	}
	source, err := filepath.Abs(*dbFile)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(*out)
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
	if err = os.Link(staged, target); err != nil {
		return fmt.Errorf("创建快照目标失败（文件不能已存在）: %w", err)
	}
	fmt.Printf("一致性快照已生成: %s\n请同时备份配置加密密钥与图片目录。\n", target)
	return nil
}
