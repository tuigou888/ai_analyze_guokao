package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"ai_analyze_guokao/internal/media"
	"ai_analyze_guokao/internal/store"
)

func cmdMedia(args []string) error {
	if len(args) == 0 {
		mediaUsage()
		return nil
	}
	switch args[0] {
	case "index":
		return cmdMediaIndex(args[1:])
	case "ocr-formula":
		return cmdMediaOCR(args[1:], media.KindFormula, media.EngineFormula, media.ScriptFormula)
	case "ocr-text":
		return cmdMediaOCR(args[1:], media.KindQuestion, media.EngineText, media.ScriptText)
	case "backfill":
		return cmdMediaBackfill(args[1:])
	case "report":
		return cmdMediaReport(args[1:])
	case "sample":
		return cmdMediaSample(args[1:])
	case "-h", "--help", "help":
		mediaUsage()
		return nil
	default:
		mediaUsage()
		return fmt.Errorf("未知 media 子命令 %q", args[0])
	}
}

func mediaUsage() {
	fmt.Print(`gk media —— 图片资源处理（P2 公式还原）

用法:
  gk media index         从引用补全 image 表（幂等自愈）+ 扫描磁盘填 sha256 与尺寸
  gk media ocr-formula   公式图 → LaTeX（PP-FormulaNet，tools/ocr_formula.py）
  gk media ocr-text      题目图 → 表格文本（PP-OCRv5，tools/ocr_text.py）
  gk media backfill      占位符 → 文本，写入 explanation/stem/material 三处派生列
  gk media report        查看图片与 OCR 覆盖率、占位符残留率
  gk media sample        分层抽样识别结果，供人工核对质量

通用选项:
  --db string       数据库路径 (默认 "var/db/gk.sqlite")
  --data string     数据目录 (默认 "data")

ocr-* 选项:
  --run string      批次产物目录 (默认 "var/runs")
  --shard int       每个分片的图片数 (默认 5000)
  --batch int       worker 内部批大小 (默认 16)
  --limit int       只处理前 N 张（试跑用，0 = 全部）
  --model string    模型名 (默认 PP-FormulaNet_plus-M)
  --python string   Python 解释器 (默认 python3)
  --script string   worker 脚本 (默认 tools/ocr_formula.py)
  --retry-error     重试此前识别失败的图片
`)
}

type mediaFlags struct {
	db, data string
}

func addCommonMediaFlags(fs *flag.FlagSet) *mediaFlags {
	m := &mediaFlags{}
	fs.StringVar(&m.db, "db", "var/db/gk.sqlite", "数据库路径")
	fs.StringVar(&m.data, "data", "data", "数据目录")
	return m
}

func cmdMediaIndex(args []string) error {
	fs := flag.NewFlagSet("media index", flag.ContinueOnError)
	m := addCommonMediaFlags(fs)
	workers := fs.Int("workers", runtime.NumCPU(), "并发数")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(m.db)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	runID, err := media.NewMediaRun(ctx, db, "index", m.data)
	if err != nil {
		return err
	}

	fmt.Println("扫描图片目录，计算 sha256 与尺寸…")
	st, err := media.Index(ctx, db, m.data, *workers)
	if err != nil {
		return err
	}
	if err := media.FinishMediaRun(ctx, db, runID, st.Indexed, st.Indexed, st.Failed, ""); err != nil {
		return err
	}

	fmt.Printf("\n文件 %d，成功 %d，读取失败 %d\n", st.Files, st.Indexed, st.Failed)
	fmt.Printf("内容重复 %d 张（OCR 只识别一次）\n", st.Duplicate)
	fmt.Printf("孤儿图片 %d 张（磁盘上有、无任何引用）\n", st.Orphans)
	fmt.Printf("引用缺失 %d 张（库里有引用、磁盘上找不到）\n", st.Missing)
	return nil
}

func cmdMediaOCR(args []string, kind, engine, defaultScript string) error {
	fs := flag.NewFlagSet("media ocr", flag.ContinueOnError)
	m := addCommonMediaFlags(fs)
	runDir := fs.String("run", "var/runs", "批次产物目录")
	shard := fs.Int("shard", 5000, "每个分片的图片数")
	batch := fs.Int("batch", 16, "worker 内部批大小")
	limit := fs.Int("limit", 0, "只处理前 N 张")
	model := fs.String("model", "", "模型名")
	python := fs.String("python", "python3", "Python 解释器")
	script := fs.String("script", defaultScript, "worker 脚本路径")
	retry := fs.Bool("retry-error", false, "重试此前失败的图片")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(m.db)
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := os.Stat(*script); err != nil {
		return fmt.Errorf("找不到 worker 脚本 %s: %w", *script, err)
	}

	ctx := context.Background()
	runID, err := media.NewMediaRun(ctx, db, "ocr",
		fmt.Sprintf("kind=%s engine=%s script=%s model=%s", kind, engine, *script, *model))
	if err != nil {
		return err
	}

	st, err := media.RunOCR(ctx, db, media.OCROptions{
		Kind:       kind,
		Engine:     engine,
		Model:      *model,
		Python:     *python,
		Script:     *script,
		DataDir:    m.data,
		RunDir:     *runDir,
		ShardSize:  *shard,
		Batch:      *batch,
		Limit:      *limit,
		RetryError: *retry,
	})
	if err != nil {
		return err
	}
	if err := media.FinishMediaRun(ctx, db, runID, st.Unique, st.Done, st.Failed, st.Model); err != nil {
		return err
	}

	fmt.Printf("\n待识别图片行 %d，按内容去重后 %d 张，分 %d 片\n",
		st.Candidates, st.Unique, st.Shards)
	fmt.Printf("识别成功 %d，失败 %d，模型 %s，耗时 %s\n",
		st.Done, st.Failed, st.Model, st.Duration.Round(1e9))
	if st.Failed > 0 {
		fmt.Println("失败项可用 --retry-error 重试（detail 见 image.ocr_error）")
	}
	return nil
}

func cmdMediaBackfill(args []string) error {
	fs := flag.NewFlagSet("media backfill", flag.ContinueOnError)
	m := addCommonMediaFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(m.db)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	runID, err := media.NewMediaRun(ctx, db, "backfill", "")
	if err != nil {
		return err
	}

	st, err := media.Backfill(ctx, db)
	if err != nil {
		return err
	}
	if err := media.FinishMediaRun(ctx, db, runID, st.Questions, st.Replaced, st.Unresolved, ""); err != nil {
		return err
	}

	fmt.Printf("解析：替换公式占位符 %d 处，无法解析 %d 处（残留题 %d 道，含公式题 %d 道）\n",
		st.Replaced, st.Unresolved, st.StaleRemain, st.WithFormula)
	fmt.Printf("题面/材料：替换题面图 %d 处；无文字的图 %d 处（纯图形，正常）；真正的缺口 %d 处\n",
		st.FiguresReplaced, st.FiguresNoText, st.FiguresMissing)
	fmt.Printf("写出 stem_with_text 的题 %d 道，写出 body_with_text 的材料 %d 条\n",
		st.StemsFilled, st.MaterialsFilled)
	return nil
}

func cmdMediaSample(args []string) error {
	fs := flag.NewFlagSet("media sample", flag.ContinueOnError)
	m := addCommonMediaFlags(fs)
	n := fs.Int("n", 30, "抽样数量")
	seed := fs.Int64("seed", 20260929, "随机种子（固定即可复现同一样本）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(m.db)
	if err != nil {
		return err
	}
	defer db.Close()

	samples, err := media.CollectSamples(context.Background(), db, m.data, *n, *seed)
	if err != nil {
		return err
	}
	media.PrintSamples(os.Stdout, samples)
	return nil
}

func cmdMediaReport(args []string) error {
	fs := flag.NewFlagSet("media report", flag.ContinueOnError)
	m := addCommonMediaFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(m.db)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	rep, err := media.CollectReport(ctx, db)
	if err != nil {
		return err
	}
	rep.Print(os.Stdout)
	return nil
}
