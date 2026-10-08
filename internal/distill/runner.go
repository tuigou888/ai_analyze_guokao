package distill

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/model"
	"ai_analyze_guokao/internal/store"
)

// Options 配置一次蒸馏运行。
type Options struct {
	RunID        string
	TaxonomyPath string
	RunDir       string
	Module       string // 空 = 全部模块
	Limit        int    // 题目上限；>0 时按模块分层打散抽样
	Seed         int64
	Concurrency  int
	MaxRetries   int
	MaxTokens    int
	Model        string // 本次执行器使用的模型名
	Pricing      llm.Pricing
	HasPricing   bool
	BaseURL      string
}

// Stats 一次运行的统计。
type Stats struct {
	Model                      string // 产出这些标注的模型（多模型并发时每个执行器一份）
	Total, OK, Failed, Skipped int
	TokensIn, TokensOut        int
	CostUSD                    float64
	NovelConcepts              []string
	AnswerMismatch             int
	DoubtRate                  int // 有疑点的题数：安全阀动过的次数
	Duration                   time.Duration
	ModelResponse              string
}

// Runner 执行蒸馏批次。
type Runner struct {
	db       *sql.DB
	client   *llm.Client
	tax      *Taxonomy
	opt      Options
	dir      string
	mu       sync.Mutex
	stats    Stats
	failures []failure

	started  time.Time
	inflight int       // 正在处理（含重试等待）的题数
	lastDone time.Time // 最近一次出结果的时间，用于停滞告警
	abort    context.CancelFunc
	runErr   error
}

type failure struct {
	QuestionID int64  `json:"question_id"`
	Error      string `json:"error"`
	Retryable  bool   `json:"retryable"`
}

// New 构造 Runner。
func New(db *sql.DB, client *llm.Client, tax *Taxonomy, opt Options) *Runner {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 5
	}
	if opt.MaxRetries <= 0 {
		opt.MaxRetries = 2
	}
	return &Runner{
		db: db, client: client, tax: tax, opt: opt,
		dir: filepath.Join(opt.RunDir, opt.RunID),
	}
}

// Run 执行批次。
//
// 断点续跑以 label 表为准（question_id + run_id 唯一）：重跑时已完成的题自动跳过。
// 产物同时落盘（output.jsonl）——盘上是审计凭据，库是查询入口，两者都要有。
func (r *Runner) Run(ctx context.Context) (*Stats, error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return nil, err
	}

	questions, err := store.LoadDistillQuestions(ctx, r.db, r.opt.Module, 0)
	if err != nil {
		return nil, err
	}
	if r.opt.Limit > 0 {
		questions = StratifiedSample(questions, r.opt.Limit, r.opt.Seed)
	}
	if len(questions) == 0 {
		return nil, errors.New("没有可蒸馏的题目（检查 --module 或题库是否已入库）")
	}
	return r.RunOn(ctx, questions, true)
}

// RunOn 在一份**指定**的题集上执行。多模型并发时，每个执行器拿到的是分片后的子集。
//
// manageRun 为真时由本方法登记/收尾 label_run（单执行器场景）；
// 多执行器场景下由外部编排器统一登记，避免多个执行器互相覆盖批次状态。
func (r *Runner) RunOn(ctx context.Context, questions []store.DistillQuestion, manageRun bool) (result *Stats, retErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if manageRun {
		release, err := acquireRunLock(ctx, r.db, r.opt.RunID)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if err := ensureRunVersions(ctx, r.db, r.opt.RunID, r.tax); err != nil {
		return nil, err
	}
	if manageRun {
		if err := r.registerRun(ctx, questions); err != nil {
			return nil, err
		}
		defer func() {
			endCtx, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer c()
			var batch *BatchError
			retErr = errors.Join(retErr, finishStoredRun(endCtx, r.db, r.opt.RunID, retErr != nil && !errors.As(retErr, &batch)))
		}()
	}
	// 目录必须在这里建，不能只在 Run 里建：多模型路径直接调 RunOn 而不经过 Run，
	// 少了这一步，每个执行器都会因"manifest.json: no such file or directory"瞬间失败。
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return nil, err
	}
	r.mu.Lock()
	startFailed := r.stats.Failed
	r.abort = cancel
	r.runErr = nil
	r.stats.Model = r.opt.Model
	r.started = time.Now()
	r.lastDone = time.Now()
	r.mu.Unlock()

	// 长跑批次没有进度输出，卡住时完全看不出它在干什么——所以每题一行日志，
	// 外加一个"多久没出结果"的停滞告警（含在途题数，用于判断是卡死还是只是慢）。
	stopTicker := make(chan struct{})
	tickerDone := make(chan struct{})
	defer func() { close(stopTicker); <-tickerDone }()
	go func() { defer close(tickerDone); r.watchProgress(stopTicker, len(questions)) }()

	done, err := r.alreadyDone(ctx)
	if err != nil {
		return nil, err
	}

	manifest := map[string]any{
		"run_id": r.opt.RunID, "module": r.opt.Module, "limit": r.opt.Limit,
		"seed": r.opt.Seed, "concurrency": r.opt.Concurrency,
		"model": r.opt.Model, "base_url": r.opt.BaseURL,
		"prompt_version": PromptVersion, "taxonomy_version": r.tax.Version,
		"total": len(questions), "already_done": len(done),
		"started_at": time.Now().Format(time.RFC3339),
	}
	if err := writeJSON(filepath.Join(r.dir, "manifest.json"), manifest); err != nil {
		return nil, err
	}

	pending := make([]store.DistillQuestion, 0, len(questions))
	r.mu.Lock()
	for _, q := range questions {
		if done[q.ID] {
			r.stats.Skipped++
			continue
		}
		pending = append(pending, q)
	}
	// 用累加而不是覆盖：多引擎路径会对每个桶各调一次 RunOn，
	// 覆盖会让汇总里的 Total 只剩最后一个桶的大小，出现"成功 24/10"这种错乱。
	r.stats.Total += len(questions)
	r.mu.Unlock()

	start := time.Now()
	outFile := filepath.Join(r.dir, "output.jsonl")
	out, err := os.OpenFile(outFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	sysPrompt := BuildSystemPrompt(r.tax)
	jobs := make(chan store.DistillQuestion)
	var wg sync.WaitGroup
	for i := 0; i < r.opt.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := range jobs {
				if ctx.Err() != nil {
					return
				}
				r.mu.Lock()
				r.inflight++
				r.mu.Unlock()
				r.processOne(ctx, q, sysPrompt, out)
			}
		}()
	}
feeding:
	for _, q := range pending {
		select {
		case <-ctx.Done():
			break feeding
		case jobs <- q:
		}
	}
	close(jobs)
	wg.Wait()

	r.stats.Duration = time.Since(start)

	// 失败清单：区分可重试与不可重试，便于决定是重跑还是改配置（§3.5）。
	if len(r.failures) > 0 {
		if err := writeJSONL(filepath.Join(r.dir, "failures.jsonl"), r.failures); err != nil {
			r.infrastructureFailure(err)
		}
	}
	sort.Strings(r.stats.NovelConcepts)
	if r.runErr != nil {
		return &r.stats, r.runErr
	}
	if ctx.Err() != nil {
		return &r.stats, ctx.Err()
	}
	if r.stats.Failed > startFailed {
		return &r.stats, &BatchError{Failed: r.stats.Failed - startFailed}
	}
	return &r.stats, nil
}

// processOne 处理一道题：调用 → 解析 → 校验 → 不合格重试 → 落库落盘。
func (r *Runner) processOne(ctx context.Context, q store.DistillQuestion, sysPrompt string, out *os.File) {
	defer func() { r.mu.Lock(); r.inflight--; r.lastDone = time.Now(); r.mu.Unlock() }()
	in := QuestionInput{
		Module: q.Module, Tag: q.Tag, Stem: q.Stem, Material: q.Material,
		Answer: q.Answer, AnswerType: model.AnswerType(q.AnswerType),
		Explanation: q.Explanation, HasFigure: q.HasFigure,
	}
	for _, o := range q.Options {
		in.Options = append(in.Options, OptionInput{Label: o.Label, Content: o.Content, Correct: o.Correct})
	}

	var (
		lastErr   error
		lastClass bool // 可重试？
	)
	attempts := r.opt.MaxRetries + 1
	maxTokens := r.opt.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2500
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		userPrompt := BuildUserPrompt(in)
		if attempt > 1 && lastErr != nil {
			// 把上一次的错误回灌给模型，让它有机会修正格式而不是重复犯错。
			userPrompt += fmt.Sprintf("\n\n【上一次输出不合格，请修正后重新输出】\n%s\n"+
				"请只输出一个合法的 JSON 对象。\n", truncate(lastErr.Error(), 300))
		}

		resp, err := r.client.Chat(ctx, llm.Request{
			Model:       r.opt.Model,
			Messages:    []llm.Message{{Role: "system", Content: sysPrompt}, {Role: "user", Content: userPrompt}},
			Temperature: 0.2,
			MaxTokens:   maxTokens,
			JSONMode:    true,
		})
		var callID int64
		if resp != nil {
			var usageErr error
			callID, usageErr = r.recordUsage(ctx, q, resp)
			if usageErr != nil {
				r.mu.Lock()
				r.stats.Failed++
				r.failures = append(r.failures, failure{QuestionID: q.ID, Error: usageErr.Error()})
				r.mu.Unlock()
				r.infrastructureFailure(usageErr)
				return
			}
		}
		if err != nil {
			if callID != 0 {
				if markErr := r.setCallStatus(ctx, callID, "invalid"); markErr != nil {
					r.infrastructureFailure(markErr)
				}
			}
			if resp == nil {
				unknownCtx, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_, markErr := r.db.ExecContext(unknownCtx, `UPDATE label_run SET usage_complete=0 WHERE id=?`, r.opt.RunID)
				c()
				if markErr != nil {
					r.infrastructureFailure(markErr)
				}
			}
			lastErr, lastClass = err, isRetryable(err)
			if ctx.Err() != nil {
				break
			}
			if !lastClass {
				break
			}
			// 退避后再试。不退避会立刻重打，把 429 越撞越死——
			// 限流下的裸重试比不重试更糟。
			r.backoff(ctx, err, attempt)
			continue
		}

		label, perr := ParseLabel(resp.Content)
		if perr != nil {
			if err := r.setCallStatus(ctx, callID, "invalid"); err != nil {
				r.infrastructureFailure(err)
				return
			}
			lastErr, lastClass = perr, true
			// JSON 不合格不是限流问题，固定短延迟即可，还带上了错误反馈供模型修正。
			r.sleep(ctx, time.Second)
			continue
		}

		v := &Validation{}
		v.Validate(label, r.tax, q.Module, q.Answer)
		if v.FatalCount() > 0 {
			if err := r.setCallStatus(ctx, callID, "invalid"); err != nil {
				r.infrastructureFailure(err)
				return
			}
			lastErr = fmt.Errorf("字段校验失败: %s", strings.Join(v.FatalMessages(), "; "))
			lastClass = true
			r.sleep(ctx, time.Second)
			continue
		}

		status := "accepted"
		if err := r.record(ctx, q, label, v, resp, out); err != nil {
			status = "save_error"
			r.infrastructureFailure(err)
		}
		if err := r.setCallStatus(ctx, callID, status); err != nil {
			r.infrastructureFailure(err)
		}
		return
	}

	// 全部尝试失败：记入失败清单。**写盘失败不重试**（重试等于重复付费），
	// 所以这里的失败是"模型或校验没过"，不是"写盘没过"。
	r.mu.Lock()
	r.stats.Failed++
	r.lastDone = time.Now()
	done, total := r.stats.OK+r.stats.Failed+r.stats.Skipped, r.stats.Total
	r.failures = append(r.failures, failure{
		QuestionID: q.ID, Error: truncate(errString(lastErr), 400), Retryable: lastClass,
	})
	r.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[%s] ✗ 题 %d（%s）尝试 %d 次后失败: %s  进度 %d/%d\n",
		r.opt.Model, q.ID, q.Module, attempts, truncate(errString(lastErr), 160), done, total)
}

// watchProgress 每 30 秒检查一次是否还在出结果；停滞时把在途题数一并报出来，
// 便于区分"卡死"与"只是慢"。
func (r *Runner) watchProgress(stop <-chan struct{}, total int) {
	if total < 5 {
		return
	}
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			r.mu.Lock()
			idle := time.Since(r.lastDone)
			done := r.stats.OK + r.stats.Failed + r.stats.Skipped
			inflight := r.inflight
			r.mu.Unlock()
			if idle > 90*time.Second {
				fmt.Fprintf(os.Stderr,
					"[%s] ⏳ %s 无新结果（在途 %d 题，进度 %d/%d）——通常在重试退避中\n",
					r.opt.Model, idle.Round(time.Second), inflight, done, total)
			} else {
				fmt.Fprintf(os.Stderr, "[%s] … 进度 %d/%d（在途 %d）\n",
					r.opt.Model, done, total, inflight)
			}
		}
	}
}

// record 保存一次合格的产出。
func (r *Runner) record(ctx context.Context, q store.DistillQuestion, l *Label,
	v *Validation, resp *llm.Response, out *os.File) error {

	tokensIn, tokensOut := resp.TokensIn, resp.TokensOut

	row := labelRow(l)
	_, err := r.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO label(question_id, run_id, subject, secondary, tertiary, detail,
			question_model, reasoning_chain, fastest_solution, pitfalls, template, key_features,
			boundary, confusable, typical_ask, doubt, tokens_in, tokens_out, latency_ms, created_at, model)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		q.ID, r.opt.RunID, l.Subject, l.Secondary, l.Tertiary, l.Detail,
		l.QuestionModel, row.reasoning, l.FastestSolution, row.pitfalls, l.Template, row.features,
		l.Boundary, row.confusable, row.typicalAsk, l.Doubt,
		tokensIn, tokensOut, resp.Latency.Milliseconds(), time.Now().Format(time.RFC3339), r.opt.Model)

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.stats.Failed++
		r.failures = append(r.failures, failure{QuestionID: q.ID,
			Error: "写库失败（不可重试）: " + err.Error(), Retryable: false})
		return fmt.Errorf("保存标注失败: %w", err)
	}

	r.stats.OK++
	r.lastDone = time.Now()
	done := r.stats.OK + r.stats.Failed + r.stats.Skipped
	fmt.Fprintf(os.Stderr, "[%s] ✓ 题 %d（%s）%s/%s  %d+%d token  %.1fs  进度 %d/%d\n",
		r.opt.Model, q.ID, q.Module, l.Subject, l.Tertiary,
		tokensIn, tokensOut, resp.Latency.Seconds(), done, r.stats.Total)
	r.stats.NovelConcepts = append(r.stats.NovelConcepts, v.NovelConcepts...)
	if strings.TrimSpace(l.Doubt) != "" {
		r.stats.DoubtRate++
	}
	if hasAnswerMismatch(v) {
		r.stats.AnswerMismatch++
	}
	if resp.Model != "" {
		r.stats.ModelResponse = resp.Model
	}

	// 盘上留一份审计凭据。写盘失败只降级告警，绝不上抛（§3.5：catch 里再抛会杀死整批）。
	rec := map[string]any{
		"question_id": q.ID, "module": q.Module, "run_id": r.opt.RunID, "occurrence_id": q.OccurrenceID, "material_id": q.MaterialID,
		"model_config": r.opt.Model, "model_response": resp.Model,
		"prompt_version": PromptVersion, "taxonomy_version": r.tax.Version,
		"tokens_in": tokensIn, "tokens_out": tokensOut,
		"latency_ms":     resp.Latency.Milliseconds(),
		"novel_concepts": v.NovelConcepts,
		"label":          l,
	}
	b, _ := json.Marshal(rec)
	if _, werr := out.Write(append(b, '\n')); werr != nil {
		return fmt.Errorf("审计输出写入失败（标注已保存）: %w", werr)
	}
	return nil
}

type labelJSON struct {
	reasoning  string
	pitfalls   string
	features   string
	confusable string
	typicalAsk string
}

func labelRow(l *Label) labelJSON {
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return "[]"
		}
		return string(b)
	}
	return labelJSON{
		reasoning:  enc(orEmpty(l.ReasoningChain)),
		pitfalls:   enc(orEmpty(l.Pitfalls)),
		features:   enc(orEmpty(l.KeyFeatures)),
		confusable: enc(orEmpty(l.Confusable)),
		typicalAsk: enc(orEmpty(l.TypicalAsk)),
	}
}

func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func hasAnswerMismatch(v *Validation) bool {
	for _, p := range v.Problems {
		if p.Field == "conclusion" {
			return true
		}
	}
	return false
}

// backoff 计算并执行退避等待。
//
// 三种情况分开处理，因为它们的"正确等待时间"差了三个数量级：
//   - 上游渠道故障：等分钟级。这是 outage，不是我们请求太快。
//   - 网关给了 Retry-After：照它说的等。
//   - 其他可重试错误：指数退避。
func (r *Runner) backoff(ctx context.Context, err error, attempt int) {
	var e *llm.Error
	if errors.As(err, &e) {
		if e.Unavailable {
			r.sleep(ctx, 60*time.Second)
			return
		}
		if e.RetryAfter > 0 {
			r.sleep(ctx, e.RetryAfter)
			return
		}
		if e.RateLimited {
			// 配额窗口通常按分钟计，至少等到下一个窗口：20s、40s、60s… 封顶 60s。
			// 429 不消耗 token，所以这样等是零成本的。
			d := time.Duration(20*(attempt)) * time.Second
			if d > 60*time.Second {
				d = 60 * time.Second
			}
			r.sleep(ctx, d)
			return
		}
	}
	base := 2 * time.Second
	d := base << (attempt - 1) // 2s, 4s, 8s…
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	r.sleep(ctx, d)
}

func (r *Runner) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func isRetryable(err error) bool {
	var e *llm.Error
	if errors.As(err, &e) {
		return e.Retryable
	}
	return true // 未知错误按可重试处理，最多耗尽重试次数
}

func errString(err error) string {
	if err == nil {
		return "未知错误"
	}
	return err.Error()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// alreadyDone 取本批次已完成的题号。
func (r *Runner) alreadyDone(ctx context.Context) (map[int64]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT question_id FROM label WHERE run_id = ?`, r.opt.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	done := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		done[id] = true
	}
	return done, rows.Err()
}

func (r *Runner) registerRun(ctx context.Context, questions []store.DistillQuestion) error {
	scope, _ := json.Marshal(map[string]any{"module": r.opt.Module, "limit": r.opt.Limit, "seed": r.opt.Seed})
	return prepareStoredRun(ctx, r.db, r.opt, r.tax, questions, string(scope))
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func writeJSONL[T any](path string, items []T) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, it := range items {
		b, err := json.Marshal(it)
		if err != nil {
			continue
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// StratifiedSample 按模块分层抽样，并在层内打散。
//
// 为什么要打散而不是"按类目顺序取前 N 条"：参考文档 §3.2 记录了一个真实事故——
// 抽样配额是均衡的，但脚本按类目整段排列，而蒸馏脚本严格按序消费，
// 于是跑到 41% 时抽检的样本 95% 都落在最规整的那一类题型上，得出的质量结论过于乐观。
// 固定种子保证同一批抽样可复现。
func StratifiedSample(qs []store.DistillQuestion, n int, seed int64) []store.DistillQuestion {
	if n <= 0 || n >= len(qs) {
		return qs
	}
	rng := rand.New(rand.NewSource(seed))

	byModule := map[string][]store.DistillQuestion{}
	var order []string
	for _, q := range qs {
		if _, ok := byModule[q.Module]; !ok {
			order = append(order, q.Module)
		}
		byModule[q.Module] = append(byModule[q.Module], q)
	}
	sort.Strings(order)
	// 层内打散
	for _, m := range order {
		g := byModule[m]
		rng.Shuffle(len(g), func(i, j int) { g[i], g[j] = g[j], g[i] })
	}

	// 轮转取，保证各模块都被覆盖；某层取空后自动跳过。
	out := make([]store.DistillQuestion, 0, n)
	for round := 0; len(out) < n; round++ {
		progressed := false
		for _, m := range order {
			g := byModule[m]
			if round < len(g) {
				out = append(out, g[round])
				progressed = true
				if len(out) >= n {
					break
				}
			}
		}
		if !progressed {
			break
		}
	}
	return out
}
