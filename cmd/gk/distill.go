package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"ai_analyze_guokao/internal/distill"
	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/store"
)

func cmdDistill(args []string) error {
	if len(args) == 0 {
		distillUsage()
		return nil
	}
	switch args[0] {
	case "run":
		return cmdDistillRun(args[1:])
	case "report":
		return cmdDistillReport(args[1:])
	case "prompt":
		return cmdDistillPrompt(args[1:])
	case "-h", "--help", "help":
		distillUsage()
		return nil
	default:
		distillUsage()
		return fmt.Errorf("未知 distill 子命令 %q", args[0])
	}
}

func distillUsage() {
	fmt.Print(`gk distill —— 蒸馏管线（P3）

用法:
  gk distill prompt [--subject 言语理解与表达]   预览将要发给模型的提示词
  gk distill run    [选项]                     执行蒸馏批次
  gk distill report [--run <run_id>]           输出质检指标

run 选项:
  --run string        批次 id（默认 distill-<模块>-<时间>）；重跑同 id 会自动续接
  --module string     只跑某个模块（默认全部）
  --limit int         题目上限；>0 时按模块分层打散抽样
  --seed int          抽样随机种子（默认 20260929，固定即可复现同一样本）
  --concurrency int   并发数（默认取配置）
  --model string      覆盖配置里的模型名
  --taxonomy string   考点规范表路径 (默认 taxonomy/v1/taxonomy.yaml)
  --run-dir string    批次产物目录 (默认 var/runs)
  --dry-run           只展示将要处理多少题与提示词，不调用 API

配置来自管理入口（gk serve 的 /admin 页面）或 gk config set。
`)
}

type distillCommon struct {
	db         *sql.DB
	settings   *setting.Store
	tax        *distill.Taxonomy
	baseURL    string
	apiKey     string
	model      string
	conc       int
	timeout    time.Duration
	pricing    llm.Pricing
	hasPrice   bool
	maxTokens  int
	maxRetries int
}

func loadDistillCommon(dbFile, keyFile, taxPath, modelOverride string) (*distillCommon, error) {
	db, err := store.Open(dbFile)
	if err != nil {
		return nil, err
	}
	settings, err := setting.Open(db, keyFile)
	if err != nil {
		db.Close()
		return nil, err
	}
	tax, err := distill.LoadTaxonomy(taxPath)
	if err != nil {
		db.Close()
		return nil, err
	}

	ctx := context.Background()
	c := &distillCommon{db: db, settings: settings, tax: tax}
	c.baseURL, _ = settings.Get(ctx, setting.KeyBaseURL)
	c.apiKey, _ = settings.Get(ctx, setting.KeyAPIKey)
	c.model, _ = settings.Get(ctx, setting.KeyModel)
	if modelOverride != "" {
		c.model = modelOverride
	}
	concStr, _ := settings.Get(ctx, setting.KeyConcurrency)
	c.conc, _ = strconv.Atoi(concStr)
	if c.conc <= 0 {
		c.conc = 5
	}
	mtStr, _ := settings.Get(ctx, setting.KeyMaxTokens)
	c.maxTokens, _ = strconv.Atoi(mtStr)
	if c.maxTokens <= 0 {
		c.maxTokens = 2500
	}
	mrStr, _ := settings.Get(ctx, setting.KeyMaxRetries)
	c.maxRetries, _ = strconv.Atoi(mrStr)
	if c.maxRetries <= 0 {
		c.maxRetries = 4
	}
	toStr, _ := settings.Get(ctx, setting.KeyTimeoutSec)
	to, _ := strconv.Atoi(toStr)
	if to <= 0 {
		to = 120
	}
	c.timeout = time.Duration(to) * time.Second

	if priceStr, _ := settings.Get(ctx, setting.KeyModelPricing); priceStr != "" {
		if p, ok := llm.ParsePricing(priceStr); ok {
			c.pricing, c.hasPrice = p, true
		}
	}
	return c, nil
}

func (c *distillCommon) Close() { c.db.Close() }

// CheckReady 校验调用所需配置齐备，缺什么就明确说缺什么。
func (c *distillCommon) CheckReady() error {
	if c.baseURL == "" {
		return fmt.Errorf("未配置 API 地址。请在管理入口（gk serve 后访问 /admin）填写，或执行：\n" +
			"  gk config set llm.base_url https://new.951357.xyz/v1")
	}
	if c.apiKey == "" {
		return fmt.Errorf("未配置 API key。请在管理入口（gk serve 后访问 /admin）填写。")
	}
	if c.model == "" {
		return fmt.Errorf("未配置模型名。请在管理入口填写，或执行：\n" +
			"  gk config set llm.model deepseek-flash")
	}
	return nil
}

func cmdDistillPrompt(args []string) error {
	fs := flag.NewFlagSet("distill prompt", flag.ContinueOnError)
	taxPath := fs.String("taxonomy", "taxonomy/v1/taxonomy.yaml", "规范表路径")
	subject := fs.String("subject", "言语理解与表达", "预览哪个模块")
	keyFile := fs.String("secret-key-file", secretKeyFile, "配置加密密钥文件")
	dbFile := fs.String("db", "var/db/gk.sqlite", "数据库路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadDistillCommon(*dbFile, *keyFile, *taxPath, "")
	if err != nil {
		return err
	}
	defer c.Close()

	t := c.tax
	if *subject != "" {
		m := t.Find(*subject)
		if m == nil {
			return fmt.Errorf("规范表里没有模块 %q（可选：%v）", *subject, t.Subjects())
		}
		// 只展示该模块，便于人工校对 prompt 是否贴合
		t = &distill.Taxonomy{Version: t.Version, Modules: []distill.Module{*m},
			BoundaryRules: t.BoundaryRules, ExtensionRule: t.ExtensionRule}
	}
	fmt.Println("=========== 系统提示词 ===========")
	fmt.Println(distill.BuildSystemPrompt(t))

	ctx := context.Background()
	qs, err := store.LoadDistillQuestions(ctx, c.db, *subject, 1)
	if err != nil {
		return err
	}
	if len(qs) > 0 {
		q := qs[0]
		in := distill.QuestionInput{
			Module: q.Module, Tag: q.Tag, Stem: q.Stem, Answer: q.Answer,
			Explanation: q.Explanation, HasFigure: q.HasFigure,
		}
		for _, o := range q.Options {
			in.Options = append(in.Options, distill.OptionInput{Label: o.Label, Content: o.Content, Correct: o.Correct})
		}
		fmt.Println("\n=========== 用户提示词示例（题 #" + strconv.FormatInt(q.ID, 10) + "）===========")
		fmt.Println(distill.BuildUserPrompt(in))
	}
	return nil
}

func cmdDistillRun(args []string) error {
	fs := flag.NewFlagSet("distill run", flag.ContinueOnError)
	dbFile := fs.String("db", "var/db/gk.sqlite", "数据库路径")
	keyFile := fs.String("secret-key-file", secretKeyFile, "配置加密密钥文件")
	taxPath := fs.String("taxonomy", "taxonomy/v1/taxonomy.yaml", "规范表路径")
	runDir := fs.String("run-dir", "var/runs", "批次产物目录")
	runID := fs.String("run", "", "批次 id")
	module := fs.String("module", "", "只跑某个模块")
	limit := fs.Int("limit", 0, "题目上限")
	seed := fs.Int64("seed", 20260929, "抽样种子")
	conc := fs.Int("concurrency", 0, "并发数")
	modelFlag := fs.String("model", "", "覆盖模型名")
	modelsFlag := fs.String("models", "", "多模型并发：逗号分隔的模型名，或 auto（自动探测可用模型）")
	probeTimeout := fs.Duration("probe-timeout", 20*time.Second, "auto 模式探测单模型的超时")
	waitAvail := fs.Duration("wait-available", 0, "开工前等待网关可用的最长时间（0 = 不等待）")
	dryRun := fs.Bool("dry-run", false, "只预览不调用")
	if err := fs.Parse(args); err != nil {
		return err
	}

	c, err := loadDistillCommon(*dbFile, *keyFile, *taxPath, *modelFlag)
	if err != nil {
		return err
	}
	defer c.Close()

	if *runID == "" {
		mod := *module
		if mod == "" {
			mod = "all"
		}
		*runID = "distill-" + mod + "-" + time.Now().Format("20060102-150405")
	}
	if *conc > 0 {
		c.conc = *conc
	}

	ctx := context.Background()
	total, err := store.CountDistillCandidates(ctx, c.db, *module)
	if err != nil {
		return err
	}

	fmt.Printf("批次 id:   %s\n", *runID)
	fmt.Printf("API 地址:  %s\n", c.baseURL)
	fmt.Printf("模型:      %s\n", c.model)
	fmt.Printf("规范表:    %s (%s)\n", *taxPath, c.tax.Version)
	fmt.Printf("可蒸馏题:  %d\n", total)
	if *limit > 0 {
		fmt.Printf("本次上限:  %d（按模块分层打散抽样，种子 %d）\n", *limit, *seed)
	}
	if !c.hasPrice {
		fmt.Println("价格未配置：只统计 token 数，不估算花费（避免谎报为 0 成本）")
	}

	if err := c.CheckReady(); err != nil {
		return err
	}

	// 解析本次要用的模型集合
	models, err := resolveModels(ctx, c, *modelsFlag, *probeTimeout, *waitAvail, *dryRun)
	if err != nil {
		return err
	}

	// 取题：多模型时要先拿到完整题集才能切分
	all, err := store.LoadDistillQuestions(ctx, c.db, *module, 0)
	if err != nil {
		return err
	}
	if *limit > 0 {
		all = distill.StratifiedSample(all, *limit, *seed)
	}
	if len(all) == 0 {
		return fmt.Errorf("没有可蒸馏的题目")
	}

	if len(models) > 1 {
		// 动态领取下没有"预先切分好的分片"，能预先给出的是**工作单元清单**。
		// 刻意不显示"假设均分"的结果：那会让人以为分配是事先定好的。
		buckets := distill.BuildBuckets(all)
		distill.SortBucketsBySize(buckets) // 与实际领取顺序一致
		ownerPath := distill.BucketOwnerPath(*runDir, *runID)
		sticky := 0
		if _, err := os.Stat(ownerPath); err == nil {
			sticky = -1 // 存在归属文件，说明是续跑
		}
		fmt.Printf("\n工作单元：%d 个（模块 × 题型，每个都是不可拆的最小单元）\n", len(buckets))
		for _, b := range buckets {
			fmt.Printf("  %s\n", b.Label())
		}
		fmt.Printf("分配方式：桶级动态领取 —— %d 个执行器共享一个队列，谁空谁领下一个。\n", len(models))
		fmt.Println("  不用静态均分：那假设各模型吞吐相同，而实测差异很大（慢模型会成关键路径）；")
		fmt.Println("  也不按题轮流分：那会让同一考点的措辞出自不同模型，污染聚合层。")
		if sticky == -1 {
			fmt.Printf("  本次为续跑：%s 里记录的桶会回到原模型，只有未领取过的桶才进队列。\n", ownerPath)
		}
	}

	if *dryRun {
		fmt.Println("\n--dry-run：未调用 API。用 gk distill prompt 可预览提示词。")
		return nil
	}

	// 前置门：网关全挂时不要开工。
	// 否则每个执行器会各自把重试次数烧在 503 上，几十秒后整批以失败告终——
	// 而"等它恢复"才是正确做法。这里替用户把等待做完。
	if err := waitForGateway(ctx, c, models, *waitAvail); err != nil {
		return err
	}

	opt := distill.Options{
		RunID: *runID, TaxonomyPath: *taxPath, RunDir: *runDir,
		Module: *module, Limit: *limit, Seed: *seed,
		Concurrency: c.conc, Model: c.model, MaxTokens: c.maxTokens, MaxRetries: c.maxRetries,
		Pricing: c.pricing, HasPricing: c.hasPrice, BaseURL: c.baseURL,
	}

	if len(models) == 1 {
		client := llm.New(c.baseURL, c.apiKey, c.timeout)
		runner := distill.New(c.db, client, c.tax, opt)
		fmt.Printf("\n开始蒸馏（模型 %s，并发 %d，单题最多尝试 %d 次，限流自动退避）…\n",
			models[0], c.conc, c.maxRetries+1)
		st, err := runner.RunOn(ctx, all, true)
		if err != nil {
			return err
		}
		printSingleStats(st, c, *runDir, *runID)
		return nil
	}

	engines := make([]distill.Engine, len(models))
	for i, m := range models {
		engines[i] = distill.Engine{
			Model:       m,
			Client:      llm.New(c.baseURL, c.apiKey, c.timeout),
			Concurrency: c.conc,
		}
	}
	fmt.Printf("\n开始蒸馏（%d 个模型并发，每个模型并发 %d，共 %d 路）…\n",
		len(models), c.conc, len(models)*c.conc)
	st, err := distill.RunMulti(ctx, c.db, engines, opt, all, c.tax)
	if err != nil {
		return err
	}

	fmt.Printf("\n完成：成功 %d / 失败 %d / 跳过（已完成）%d，共 %d 题，耗时 %s\n",
		st.OK, st.Failed, st.Skipped, st.Total, st.Duration.Round(time.Second))
	fmt.Printf("token：输入 %d / 输出 %d\n", st.TokensIn, st.TokensOut)
	if c.hasPrice {
		fmt.Printf("花费：约 $%.4f\n", st.CostUSD)
	}
	fmt.Printf("疑点：%d 题   答案与官方不一致：%d 题\n", st.Doubt, st.AnswerMismatch)
	fmt.Println("\n各模型成绩（可横向比较吞吐与质量）:")
	for _, e := range st.PerEngine {
		rate := 0.0
		if e.Questions > 0 {
			rate = float64(e.OK) * 100 / float64(e.Questions)
		}
		fmt.Printf("  %-22s 成功 %d/%d (%.0f%%)  失败 %d  token %d+%d  耗时 %.0fs\n",
			e.Model, e.OK, e.Questions, rate, e.Failed, e.TokensIn, e.TokensOut, e.DurationSec)
	}
	if len(st.Novel) > 0 {
		fmt.Printf("\n规范表外新增的三级考点 %d 个（需人工审查）:\n", len(st.Novel))
		seen := map[string]bool{}
		for _, nc := range st.Novel {
			if !seen[nc] {
				seen[nc] = true
				fmt.Printf("  %s\n", nc)
			}
		}
	}
	fmt.Printf("\n产物: %s/%s/\n质检: gk distill report --run %s\n", *runDir, *runID, *runID)
	return nil
}

// waitForGateway 开工前确认至少有一个模型可用；不可用则按 waitFor 的时长轮询等待。
//
// 只要求"至少一个可用"而不是"全部可用"：多模型的价值恰恰是某个模型挂了别的还能顶上，
// 一开始就要求全部可用会让这套设计失去意义。
func waitForGateway(ctx context.Context, c *distillCommon, models []string, waitFor time.Duration) error {
	tryOnce := func() (string, error) {
		var lastErr error
		for _, m := range models {
			client := llm.New(c.baseURL, c.apiKey, 20*time.Second)
			if _, _, err := client.Ping(ctx, m); err == nil {
				return m, nil
			} else {
				lastErr = err
			}
		}
		return "", lastErr
	}

	if m, err := tryOnce(); err == nil {
		fmt.Printf("前置检查: 网关可用（%s）\n", m)
		return nil
	} else if waitFor <= 0 {
		return fmt.Errorf("网关当前不可用：%w\n"+
			"  这通常是上游渠道故障而非限流。可加 --wait-available 10m 让它在恢复后自动开工。", err)
	}

	fmt.Printf("网关当前不可用，最多等待 %s（每 30 秒重试）…\n", waitFor)
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
		if m, err := tryOnce(); err == nil {
			fmt.Printf("网关已恢复（%s），继续。\n", m)
			return nil
		}
	}
	return fmt.Errorf("等待 %s 后网关仍不可用，已放弃", waitFor)
}

func printSingleStats(st *distill.Stats, c *distillCommon, runDir, runID string) {
	if st.ModelResponse != "" && st.ModelResponse != c.model {
		fmt.Printf("⚠ 回执模型 %q 与配置 %q 不一致\n", st.ModelResponse, c.model)
	}
}

// resolveModels 决定本次用哪些模型。
//
// 显式列表优先；`auto` 时去问网关有哪些模型、逐个 ping，用能通的那个集合。
// 之所以要"探"而不是"猜"：配额是按模型记的，哪个模型还有额度只有端点知道，
// 从错误信息反推不可靠（docs/架构方案.md §6.4 的模型自证原则）。
func resolveModels(ctx context.Context, c *distillCommon, spec string,
	timeout, waitFor time.Duration, dryRun bool) ([]string, error) {

	spec = strings.TrimSpace(spec)
	if spec == "" {
		return []string{c.model}, nil
	}
	if spec != "auto" {
		var out []string
		for _, m := range strings.Split(spec, ",") {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("--models 里没有有效的模型名")
		}
		return out, nil
	}

	candidates, err := llm.ListModels(ctx, c.baseURL, c.apiKey, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("自动探测失败（可改用 --models a,b,c 显式指定）: %w", err)
	}

	// 网关整体不可用时（典型报文 "No available channel"），等它恢复而不是直接放弃。
	// 探测本身也要花钱花时间，所以只在确实需要时才进入等待。
	deadline := time.Now().Add(waitFor)
	for round := 1; ; round++ {
		fmt.Printf("自动探测（第 %d 轮）：网关可见 %d 个模型，逐个试探可用性…\n", round, len(candidates))
		ok := probeModels(ctx, c, candidates, timeout)
		if len(ok) > 0 {
			if dryRun {
				fmt.Println("（dry-run：探测本身会真实调用端点）")
			}
			return ok, nil
		}
		if waitFor <= 0 {
			return nil, fmt.Errorf("没有模型可用。检查网关状态与配额，" +
				"或加 --wait-available 10m 让它在恢复后自动开工；也可用 --models a,b,c 显式指定")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待 %s 后仍无可用模型，已放弃", waitFor)
		}
		fmt.Println("  无可用模型，30 秒后重试…")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
}

// probeModels 逐个试探模型可用性，返回可用的那些。
func probeModels(ctx context.Context, c *distillCommon, candidates []string, timeout time.Duration) []string {
	var ok []string
	for _, m := range candidates {
		client := llm.New(c.baseURL, c.apiKey, timeout)
		// 每个模型试两次：限流是随机的，一次失败不代表不能用。
		var lastErr error
		for attempt := 0; attempt < 2; attempt++ {
			if _, _, err := client.Ping(ctx, m); err == nil {
				lastErr = nil
				break
			} else {
				lastErr = err
				time.Sleep(time.Second)
			}
		}
		if lastErr == nil {
			fmt.Printf("  ✅ %s\n", m)
			ok = append(ok, m)
		} else {
			fmt.Printf("  ✗ %s  (%s)\n", m, firstLine(lastErr.Error()))
		}
	}
	return ok
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 70 {
		return string(r[:70]) + "…"
	}
	return s
}

func cmdDistillReport(args []string) error {
	fs := flag.NewFlagSet("distill report", flag.ContinueOnError)
	dbFile := fs.String("db", "var/db/gk.sqlite", "数据库路径")
	runID := fs.String("run", "", "批次 id（默认取最近一次）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := store.Open(*dbFile)
	if err != nil {
		return err
	}
	defer db.Close()

	var id string
	if *runID == "" {
		if err := db.QueryRow(`SELECT id FROM label_run ORDER BY started_at DESC LIMIT 1`).Scan(&id); err != nil {
			return fmt.Errorf("没有找到任何蒸馏批次: %w", err)
		}
	} else {
		id = *runID
	}

	if err := printRunSummary(db, id); err != nil {
		return err
	}
	return printQualityMetrics(db, id)
}

func printRunSummary(db *sql.DB, id string) error {
	var (
		status, started, finished, modelCfg, modelResp, pv, tv sql.NullString
		total, ok, failed                                      int
		tin, tout                                              int
		cost                                                   float64
	)
	err := db.QueryRow(`SELECT status, started_at, finished_at, model_config, model_response,
		prompt_version, taxonomy_version, total, ok, failed, tokens_in, tokens_out, cost_usd
		FROM label_run WHERE id=?`, id).Scan(&status, &started, &finished, &modelCfg, &modelResp,
		&pv, &tv, &total, &ok, &failed, &tin, &tout, &cost)
	if err != nil {
		return err
	}
	fmt.Printf("=== 批次 %s ===\n", id)
	fmt.Printf("状态        %s\n", status.String)
	fmt.Printf("题目        %d（成功 %d / 失败 %d）\n", total, ok, failed)
	fmt.Printf("模型        配置 %s / 回执 %s\n", modelCfg.String, orDash(modelResp.String))
	fmt.Printf("版本        prompt %s / 规范表 %s\n", pv.String, tv.String)
	fmt.Printf("token       输入 %d / 输出 %d\n", tin, tout)
	fmt.Printf("花费        $%.4f\n", cost)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
