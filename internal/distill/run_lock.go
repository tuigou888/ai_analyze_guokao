package distill

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrRunActive = errors.New("相同批次已有执行器运行")

// A kernel lock is shared by processes and is released even after SIGKILL.
// Keep the lock inode: unlinking it while another process opens it breaks exclusion.
func acquireRunLock(ctx context.Context, db *sql.DB, runID string) (func(), error) {
	if runID == "" || runID == "." || runID == ".." || strings.ContainsAny(runID, "/\\") {
		return nil, errors.New("run ID 必须为非空单目录名")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return nil, err
	}
	var path string
	for rows.Next() {
		var seq int
		var name, file string
		if err = rows.Scan(&seq, &name, &file); err != nil {
			rows.Close()
			return nil, err
		}
		if name == "main" {
			path = file
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("蒸馏批次需要持久化 SQLite 数据库")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(filepath.Dir(path), ".gk-run-locks")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(path + "\x00" + runID))
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%x.lock", key)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockRunFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { unlockRunFile(f); _ = f.Close() }, nil
}
