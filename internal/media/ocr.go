package media

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Work 是一张待识别的图片。
type Work struct {
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
	Path   string `json:"path"`
}

// Result 是 worker 返回的一条识别结果。
type Result struct {
	SHA256 string `json:"sha256"`
	Status string `json:"status"` // ok / error
	TeX    string `json:"tex"`
	Error  string `json:"error"`
	Model  string `json:"model"`
}

// OCRStats 一次 OCR 运行的统计。
type OCRStats struct {
	Candidates int // 待识别的图片行数
	Unique     int // 按 sha256 去重后的实际识别张数
	Done       int // 本次新增识别成功
	Failed     int // 本次识别失败
	AlreadyOK  int // 此前已识别，跳过
	Shards     int
	Missing    int // 未返回识别记录的图片数（必须为 0）
	Duration   time.Duration
	Model      string
}

// OCROptions 配置一次 OCR 运行。
type OCROptions struct {
	Kind       string // 默认 公式图
	Engine     string // formula | text，决定回填时怎么用结果；默认 formula
	Model      string // 传给 worker 的模型名；留空用 worker 默认
	Python     string // python 解释器，默认 python3
	Script     string // worker 脚本路径，默认 tools/ocr_formula.py
	DataDir    string
	RunDir     string // 批次产物目录
	ShardSize  int    // 每个 shard 的图片数
	Batch      int    // worker 内部批大小
	Limit      int    // 只处理前 N 张（试跑用）
	RetryError bool   // 是否重试此前失败的图片
}

// BuildWorklist 构造待识别清单：按 sha256 去重，跳过已成功的。
//
// 按 sha256 而不是文件名来排活，有两个好处：内容重复的图片只识别一次；
// 识别结果与内容绑定，源数据集若重命名文件也不会让结果失配。
func BuildWorklist(ctx context.Context, db *sql.DB, dataDir string, kind string, retryError bool, limit int) ([]Work, int, error) {
	where := `kind = ? AND ocr_status IS NULL`
	if retryError {
		where = `kind = ? AND (ocr_status IS NULL OR ocr_status = 'error')`
	}
	// 已被同一 sha256 的其它行成功识别过的，本轮无需再排。
	q := `SELECT url, COALESCE(sha256,'') FROM image WHERE ` + where + ` ORDER BY url`
	rows, err := db.QueryContext(ctx, q, kind)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var (
		candidates int
		seen       = map[string]bool{}
		out        []Work
	)
	for rows.Next() {
		var url, hash string
		if err := rows.Scan(&url, &hash); err != nil {
			return nil, 0, err
		}
		candidates++
		if hash == "" {
			continue // 还没跑过 index，拿不到 sha256
		}
		if seen[hash] {
			continue
		}
		seen[hash] = true
		out = append(out, Work{
			SHA256: hash,
			URL:    url,
			Path:   filepath.Join(dataDir, imageDirName, url),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SHA256 < out[j].SHA256 }) // 顺序可复现
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, candidates, nil
}

// RunOCR 分片执行公式识别。
//
// 分片的意义在容错：一个 shard 崩了只影响它自己，其余结果已经在盘上；
// 由于 worker 支持按 sha256 断点续跑，重跑该 shard 只补没做的部分。
func RunOCR(ctx context.Context, db *sql.DB, opt OCROptions) (*OCRStats, error) {
	if opt.Kind == "" {
		opt.Kind = KindFormula
	}
	if opt.Engine == "" {
		opt.Engine = EngineFormula
	}
	if opt.Python == "" {
		opt.Python = "python3"
	}
	if opt.Script == "" {
		opt.Script = filepath.Join("tools", "ocr_formula.py")
	}
	if opt.ShardSize <= 0 {
		opt.ShardSize = 5000
	}
	if opt.Batch <= 0 {
		opt.Batch = 16
	}

	work, candidates, err := BuildWorklist(ctx, db, opt.DataDir, opt.Kind, opt.RetryError, opt.Limit)
	if err != nil {
		return nil, err
	}
	st := &OCRStats{Candidates: candidates, Unique: len(work)}
	start := time.Now()
	defer func() { st.Duration = time.Since(start); st.Missing = max(st.Unique-st.Done-st.Failed, 0) }()
	if len(work) == 0 {
		return st, nil
	}

	runDir := filepath.Join(opt.RunDir, time.Now().Format("ocr-20060102-150405"))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return st, err
	}
	var runErr error

	for i := 0; i < len(work); i += opt.ShardSize {
		if ctx.Err() != nil {
			return st, errors.Join(runErr, ctx.Err())
		}
		end := i + opt.ShardSize
		if end > len(work) {
			end = len(work)
		}
		shard := work[i:end]
		st.Shards++

		manifest := filepath.Join(runDir, fmt.Sprintf("manifest-%03d.jsonl", st.Shards))
		outFile := filepath.Join(runDir, fmt.Sprintf("result-%03d.jsonl", st.Shards))
		if err := writeManifest(manifest, shard); err != nil {
			return st, errors.Join(runErr, err)
		}

		fmt.Fprintf(os.Stderr, "[%d/%d] shard %d：%d 张 → %s\n",
			end, len(work), st.Shards, len(shard), filepath.Base(outFile))
		if err := runWorker(ctx, opt, manifest, outFile); err != nil {
			// 单个 shard 失败不中止整轮：已完成的结果已在盘上，其余 shard 继续。
			fmt.Fprintf(os.Stderr, "  shard %d 失败（其余继续）: %v\n", st.Shards, err)
			runErr = errors.Join(runErr, fmt.Errorf("shard %d worker 失败: %w", st.Shards, err))
		}
		res, err := readResults(outFile)
		if err != nil {
			return st, errors.Join(runErr, err)
		}
		expected := make(map[string]bool, len(shard))
		for _, w := range shard {
			expected[w.SHA256] = true
		}
		seen := map[string]bool{}
		valid := true
		for _, r := range res {
			if !expected[r.SHA256] || seen[r.SHA256] || (r.Status != "ok" && r.Status != "error") {
				valid = false
				break
			}
			seen[r.SHA256] = true
		}
		if !valid {
			st.Missing += len(shard)
			runErr = errors.Join(runErr, fmt.Errorf("shard %d 结果包含重复、未知 sha256 或无效状态，拒绝回填", st.Shards))
			continue
		}
		// 防线：worker 必须为每一张图返回一条记录。
		// predict 会静默跳过不支持的格式（如 GIF），一旦 worker 用 zip 配对，
		// 后续图片的文本就会挂到别人的 sha256 上——静默写入错误数据。
		// worker 侧已改为按索引配对并逐张重跑，这里再兜一道：数量对不上就明确报出来。
		if len(res) != len(shard) {
			fmt.Fprintf(os.Stderr,
				"  ⚠ shard %d：期望 %d 条识别记录，实际 %d 条，差额 %d 张未完成（重跑可补齐）\n",
				st.Shards, len(shard), len(res), len(shard)-len(res))
			st.Missing += len(shard) - len(res)
			runErr = errors.Join(runErr, fmt.Errorf("shard %d 识别记录不完整：期望 %d，实际 %d", st.Shards, len(shard), len(res)))
		}
		ok, failed, model, err := applyResults(ctx, db, res, opt.Engine)
		if err != nil {
			return st, errors.Join(runErr, err)
		}
		st.Done += ok
		st.Failed += failed
		if model != "" {
			st.Model = model
		}
	}

	st.Duration = time.Since(start)
	if st.Failed > 0 {
		runErr = errors.Join(runErr, fmt.Errorf("%d 张图片识别失败", st.Failed))
	}
	return st, runErr
}

func writeManifest(path string, work []Work) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, it := range work {
		if err := enc.Encode(it); err != nil {
			return err
		}
	}
	return w.Flush()
}

func runWorker(ctx context.Context, opt OCROptions, manifest, outFile string) error {
	args := []string{opt.Script, "--manifest", manifest, "--out", outFile,
		"--batch", fmt.Sprint(opt.Batch)}
	if opt.Model != "" {
		args = append(args, "--model", opt.Model)
	}
	cmd := exec.CommandContext(ctx, opt.Python, args...)
	cmd.Stdout = os.Stderr // worker 的结果写文件，stdout 只可能出现在异常路径
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func readResults(path string) ([]Result, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // shard 没产出任何结果
		}
		return nil, err
	}
	defer f.Close()

	var out []Result
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // LaTeX 结果可能很长
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Result
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			// 崩溃时会留下截断的末行，跳过——那个 sha256 会重新识别。
			continue
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// applyResults 把识别结果写回库。按 sha256 更新，因此内容重复的所有图片行都会拿到结果。
func applyResults(ctx context.Context, db *sql.DB, results []Result, engine string) (ok, failed int, model string, err error) {
	if len(results) == 0 {
		return 0, 0, "", nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", err
	}
	defer tx.Rollback()

	updOK, err := tx.PrepareContext(ctx,
		`UPDATE image SET ocr_tex=?, ocr_status='ok', ocr_engine=?, ocr_model=?, ocr_at=?, ocr_error=NULL
		 WHERE sha256=?`)
	if err != nil {
		return 0, 0, "", err
	}
	defer updOK.Close()
	updErr, err := tx.PrepareContext(ctx,
		`UPDATE image SET ocr_status='error', ocr_model=?, ocr_at=?, ocr_error=? WHERE sha256=?`)
	if err != nil {
		return 0, 0, "", err
	}
	defer updErr.Close()

	now := time.Now().Format(time.RFC3339)
	for _, r := range results {
		rec := r.Model
		if rec == "" {
			rec = model
		} else {
			model = rec
		}
		if r.Status == "ok" {
			// 注意：tex 为空也算成功——空白小图上模型确实会输出空串，
			// 把它记成失败会导致这些图每次运行都重试一遍。
			if _, err := updOK.ExecContext(ctx, r.TeX, engine, rec, now, r.SHA256); err != nil {
				return ok, failed, model, err
			}
			ok++
			continue
		}
		if _, err := updErr.ExecContext(ctx, rec, now, r.Error, r.SHA256); err != nil {
			return ok, failed, model, err
		}
		failed++
	}
	return ok, failed, model, tx.Commit()
}
