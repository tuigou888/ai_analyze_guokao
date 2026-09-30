// Package media 处理数据集的图片资源：索引、公式图 OCR、解析回填。
//
// 这一层存在的理由：解析里的数学公式在源数据里是**图片**而不是文本
// （33,309 张公式图，被引用 115,349 次）。不还原成文本，数量关系与资料分析的解析
// 对模型就是一串占位符——这正是数据集作者记录的"资料分析疑点率 28.4%"的根因。
package media

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ImageMeta 一张图片的磁盘元数据。
type ImageMeta struct {
	URL    string // 归一化相对路径：公式图/x.png
	SHA256 string
	Bytes  int64
	Width  int
	Height int
}

// IndexStats 索引结果。
type IndexStats struct {
	Files     int // 磁盘上的图片文件数
	Indexed   int // 成功读取元数据的
	Failed    int
	Orphans   int // 磁盘上有、但没有任何引用（不在 image 表里）
	Missing   int // image 表里有引用、但磁盘上找不到
	Duplicate int // 内容重复（同 sha256 但对不同文件名）

	Added     int // 本次从引用补出来的 image 行（应为 0，非 0 说明 ingest 漏登记）
	Refreshed int // 刷新引用计数的行数
}

// Index 遍历图片目录，把 sha256 / 尺寸 / 字节数回填到 image 表。
//
// 需要 sha256 的原因有两个：一是内容去重（公式图有 167 张内容重复，识别一次即可），
// 二是让"识别结果与图片内容绑定"而不是与文件名绑定——文件名是数据集的内部细节。
func Index(ctx context.Context, db *sql.DB, dataDir string, workers int) (*IndexStats, error) {
	// 先按引用补全 image 行，再扫磁盘填元数据。
	// 顺序不能反：从引用补出来的新行只有先存在，后面的 sha256 回填才能落到它们身上。
	added, refreshed, err := Reconcile(ctx, db)
	if err != nil {
		return nil, err
	}

	root := filepath.Join(dataDir, imageDirName)
	kinds := []string{KindFormula, KindQuestion}

	// 取库里已知的 url 集合，用于识别孤儿图片。
	known := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT url FROM image`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return nil, err
		}
		known[u] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type job struct{ url, path string }
	jobs := make(chan job, workers*2)
	metas := make(chan ImageMeta, workers*2)
	var failures int64

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				m, err := readMeta(j.url, j.path)
				if err != nil {
					atomic.AddInt64(&failures, 1)
					continue
				}
				metas <- m
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, kind := range kinds {
			dir := filepath.Join(root, kind)
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				jobs <- job{url: kind + "/" + e.Name(), path: filepath.Join(dir, e.Name())}
			}
		}
	}()
	go func() { wg.Wait(); close(metas) }()

	st := &IndexStats{Added: added, Refreshed: refreshed}
	seenHash := map[string]bool{}
	onDisk := map[string]bool{}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	upd, err := tx.PrepareContext(ctx, `UPDATE image SET sha256=?, bytes=?, width=?, height=? WHERE url=?`)
	if err != nil {
		return nil, err
	}
	defer upd.Close()

	for m := range metas {
		st.Files++
		onDisk[m.URL] = true
		if !known[m.URL] {
			st.Orphans++
		}
		if seenHash[m.SHA256] {
			st.Duplicate++
		}
		seenHash[m.SHA256] = true
		if _, err := upd.ExecContext(ctx, m.SHA256, m.Bytes, m.Width, m.Height, m.URL); err != nil {
			return nil, err
		}
		st.Indexed++
	}
	st.Failed = int(failures)
	for u := range known {
		if !onDisk[u] {
			st.Missing++
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return st, nil
}

func readMeta(url, path string) (ImageMeta, error) {
	m := ImageMeta{URL: url}
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return m, err
	}
	m.Bytes = n
	m.SHA256 = hex.EncodeToString(h.Sum(nil))

	// 只解头部拿尺寸，不做完整解码——33309 张小图全解码是浪费。
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return m, err
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		// 尺寸读不到不算失败：sha256 仍然是有效的，OCR 也不依赖尺寸。
		return m, nil
	}
	m.Width, m.Height = cfg.Width, cfg.Height
	return m, nil
}

// NewMediaRun 登记一次媒体处理批次。
func NewMediaRun(ctx context.Context, db *sql.DB, kind, note string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO media_run(kind, started_at, note) VALUES (?,?,?)`,
		kind, time.Now().Format(time.RFC3339), note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishMediaRun 收尾媒体批次。
func FinishMediaRun(ctx context.Context, db *sql.DB, id int64, images, ok, failed int, model string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE media_run SET finished_at=?, images=?, ok=?, failed=?, model=? WHERE id=?`,
		time.Now().Format(time.RFC3339), images, ok, failed, model, id)
	return err
}
