// 命令 gk 是本项目唯一的入口，子命令覆盖数据管线与后续的服务层。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"ai_analyze_guokao/internal/ingest"
	"ai_analyze_guokao/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "ingest":
		err = cmdIngest(os.Args[2:])
	case "backup":
		err = cmdBackup(os.Args[2:])
	case "stats":
		err = cmdStats(os.Args[2:])
	case "media":
		err = cmdMedia(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "admin":
		err = cmdAdmin(os.Args[2:])
	case "config":
		err = cmdConfig(os.Args[2:])
	case "distill":
		err = cmdDistill(os.Args[2:])
	case "llm":
		err = cmdLLM(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`gk —— 国考/公考真题数据管线

用法:
  gk ingest [选项]    解析 data/ 下的 Markdown 真题并入库
  gk backup --bundle 目录  生成并校验数据库与有效密钥的完整备份（不覆盖）
  gk backup --verify-bundle 目录  只读验证完整备份的校验值与解密
  gk backup --out 文件  仅生成一致性数据库快照（图片、密钥另行备份）
  gk stats  [选项]    输出统计与解析契约校验
  gk media  <子命令>  图片索引 / 公式图 OCR / 解析回填（P2）
  gk serve  [选项]    启动 HTTP 服务与管理入口
  gk admin  <子命令>  管理入口账号（create / passwd）
  gk config [子命令]  查看与修改配置（list / set）
  gk distill <子命令> 蒸馏管线（prompt / run / report）
  gk llm    <子命令> 接口自检（models / ping）

ingest 选项:
  --data string     数据目录 (默认 "data")
  --db string       数据库路径 (默认 "var/db/gk.sqlite")
  --reset           新目标路径建库（拒绝覆盖已有数据库）
  --workers int     解析并发数 (默认 CPU 核数)

stats 选项:
  --db string       数据库路径 (默认 "var/db/gk.sqlite")
  --dup int         额外列出重复收录最多的 N 道题 (默认 0)
`)
}

func cmdIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	dataDir := fs.String("data", "data", "数据目录")
	dbFile := fs.String("db", "var/db/gk.sqlite", "数据库路径")
	reset := fs.Bool("reset", false, "新目标路径建库，拒绝覆盖已有数据库")
	workers := fs.Int("workers", runtime.NumCPU(), "解析并发数")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *workers < 1 {
		return fmt.Errorf("--workers 必须大于 0")
	}
	if *reset {
		// 不自动替换现有库：其中包含无法由题库重建的用户记录，且可能有在线连接。
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if _, err := os.Lstat(*dbFile + suffix); err == nil {
				return fmt.Errorf("拒绝覆盖已有数据库或附属文件 %s；请使用新的 --db 路径建库", *dbFile+suffix)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(*dbFile), 0o755); err != nil {
		return err
	}

	files, err := ingest.Discover(*dataDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("在 %s 下没有找到任何笔记", *dataDir)
	}
	fmt.Printf("发现 %d 篇笔记，开始解析（并发 %d）…\n", len(files), *workers)

	db, err := store.Open(*dbFile)
	if err != nil {
		return err
	}
	defer db.Close()

	ing, err := store.NewIngester(db, fmt.Sprintf("data=%s files=%d", *dataDir, len(files)))
	if err != nil {
		return err
	}
	defer ing.Close()

	type parsed struct {
		file *ingest.File
		err  error
	}

	// 解析并行、写入串行：SQLite 只有一个写者，把并发留给解析才有效。
	jobs := make(chan string, *workers)
	results := make(chan parsed, *workers)

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range jobs {
				f, err := ingest.ParseFile(filepath.Join(*dataDir, rel), rel)
				results <- parsed{file: f, err: err}
			}
		}()
	}
	go func() {
		for _, rel := range files {
			jobs <- rel
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var (
		start                                             = time.Now()
		done, nQuestions, nOccurrences, nWarnings, failed int
	)
	for r := range results {
		if r.err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "解析失败: %v\n", r.err)
			continue
		}
		newEntities, occ, err := ing.File(r.file)
		if err != nil {
			return fmt.Errorf("写入 %s: %w", r.file.Paper.SourcePath, err)
		}
		done++
		nQuestions += newEntities
		nOccurrences += occ
		nWarnings += len(r.file.Warnings)
		if done%200 == 0 {
			fmt.Fprintf(os.Stderr, "\r已入库 %d/%d 篇，新增题目实体 %d…", done, len(files), nQuestions)
		}
	}
	fmt.Fprintf(os.Stderr, "\r已入库 %d/%d 篇                          \n", done, len(files))

	if err := ing.Finish(done, nQuestions, nOccurrences, nWarnings); err != nil {
		return err
	}
	elapsed := time.Since(start)
	fmt.Printf("\n完成：%d 篇 / 题块 %d / 新增题目实体 %d / 解析告警 %d，耗时 %s\n",
		done, nOccurrences, nQuestions, nWarnings, elapsed.Round(time.Second))
	if failed > 0 {
		return fmt.Errorf("有 %d 篇解析失败，导入未完整完成；已完成文件可保留并重跑", failed)
	}
	fmt.Println("\n下一步: gk stats")
	return nil
}

func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	dbFile := fs.String("db", "var/db/gk.sqlite", "数据库路径")
	dup := fs.Int("dup", 0, "列出重复收录最多的 N 道题")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbFile)
	if err != nil {
		return err
	}
	defer db.Close()

	s, err := store.Collect(db)
	if err != nil {
		return err
	}
	c, err := store.CheckContract(db, store.WantOccurrences)
	if err != nil {
		return err
	}

	o := s.Overview
	fmt.Println("=== 全库概览 ===")
	fmt.Printf("卷（卷×模块）     %6d\n", o.Papers)
	fmt.Printf("题块（题目出现）  %6d\n", o.Occurrences)
	fmt.Printf("题目实体（去重）  %6d\n", o.Questions)
	fmt.Printf("去重率            %6.1f%%\n", pct(o.Occurrences-o.Questions, o.Occurrences))
	fmt.Printf("选项              %6d\n", o.Options)
	fmt.Printf("材料段            %6d\n", o.Materials)
	fmt.Printf("图片（唯一）      %6d\n", o.Images)
	fmt.Printf("解析告警          %6d\n", o.Warnings)

	fmt.Println("\n=== 按模块 ===")
	fmt.Printf("%-18s%10s%10s%10s%10s\n", "模块", "题块", "唯一题", "去重率", "含题图")
	for _, m := range s.Modules {
		fmt.Printf("%-18s%10d%10d%10s%10d\n", m.Module, m.Occurrences, m.Entities,
			fmt.Sprintf("%.1f%%", pct(m.Occurrences-m.Entities, m.Occurrences)), m.WithFigure)
	}

	fmt.Println("\n=== 作答形态 ===")
	for _, b := range s.Answer {
		fmt.Printf("%-10s %6d\n", b.Key, b.Count)
	}

	fmt.Println("\n=== 考试类型（按卷×模块） ===")
	for _, b := range s.ExamType {
		fmt.Printf("%-10s %6d\n", b.Key, b.Count)
	}

	fmt.Println("\n=== 卷型 ===")
	for _, b := range s.Variants {
		fmt.Printf("%-12s %6d\n", b.Key, b.Count)
	}

	if len(s.Years) > 0 {
		fmt.Println("\n=== 年份（按卷×模块） ===")
		var sb []string
		for _, b := range s.Years {
			sb = append(sb, fmt.Sprintf("%s:%d", b.Key, b.Count))
		}
		fmt.Println(join(sb, "  "))
	}

	fmt.Println("\n=== 解析告警分布 ===")
	if len(s.Warnings) == 0 {
		fmt.Println("（无）")
	}
	for _, b := range s.Warnings {
		fmt.Printf("%-24s %6d\n", b.Key, b.Count)
	}

	fmt.Println("\n=== 解析契约 ===")
	ok, problems := c.ContractOK()
	fmt.Printf("题块数            %6d  (期望 %d)\n", c.Occurrences, c.WantedTotal)
	fmt.Printf("题目实体          %6d  (期望 %d)\n", c.Entities, store.WantUniqueEntities)
	fmt.Printf("✅ 与答案冲突      %6d  (期望 %d)\n", c.MarkMismatch, store.WantMarkMismatch)
	fmt.Printf("判断题            %6d  (期望 %d)\n", c.JudgeQuestions, store.WantJudgeQuestions)
	fmt.Printf("答案缺失          %6d  (期望 %d)\n", c.AnswerMissing, store.WantAnswerMissing)
	fmt.Printf("被拆分的 qid      %6d  (期望 %d)\n", c.QIDSplit, store.WantQIDSplit)
	fmt.Printf("合并裁决队列      %6d  (期望 %d)\n", c.QIDMerged, store.WantQIDMerged)
	if ok {
		fmt.Println("结果: 通过")
	} else {
		fmt.Println("结果: 未通过")
		for _, p := range problems {
			fmt.Println("  -", p)
		}
	}

	// 合并裁决队列只有几十条，值得逐条列出来给人看，而不是只报个数字。
	if conflicts, err := store.ListQIDConflicts(db); err != nil {
		return err
	} else if len(conflicts) > 0 {
		fmt.Printf("\n=== 合并裁决队列（%d 条：同一指纹覆盖多个 qid）===\n", len(conflicts))
		for _, cf := range conflicts {
			fmt.Printf("  出现 %2d 次  %-10s qid=%-24s %s\n", cf.Occurs, cf.Module, cf.QIDs, cf.StemBrief)
		}
	}

	if *dup > 0 {
		rows, err := store.TopDuplicates(db, *dup)
		if err != nil {
			return err
		}
		fmt.Printf("\n=== 重复收录最多的 %d 道题 ===\n", len(rows))
		for _, d := range rows {
			fmt.Printf("%3d 次  %-10s qid=%-10s %s\n", d.Count, d.Module, d.QID, d.StemBrief)
		}
	}
	return nil
}

func pct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
