package distill

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/store"
)

// Engine 一个可用的执行器：一个模型 + 一个客户端 + 它的并发度。
type Engine struct {
	Model       string
	Client      *llm.Client
	Concurrency int
}

// MultiStats 多模型并发的汇总。
type MultiStats struct {
	RunID          string
	Total          int
	OK             int
	Failed         int
	Skipped        int
	TokensIn       int
	TokensOut      int
	CostUSD        float64
	Duration       time.Duration
	PerEngine      []EngineStats
	Novel          []string
	AnswerMismatch int
	Doubt          int
}

// EngineStats 单个执行器的成绩，用于横向比较模型质量与吞吐。
type EngineStats struct {
	Model       string   `json:"model"`
	Questions   int      `json:"questions"`
	OK          int      `json:"ok"`
	Failed      int      `json:"failed"`
	Skipped     int      `json:"skipped"`
	TokensIn    int      `json:"tokens_in"`
	TokensOut   int      `json:"tokens_out"`
	CostUSD     float64  `json:"cost_usd"`
	DurationSec float64  `json:"duration_sec"`
	Buckets     []string `json:"buckets"`
}

// RunMulti 用多个模型并发执行同一批次，**桶级动态领取**。
//
// 为什么不是静态均分：静态按题数分假设各模型吞吐相同，实测不是——verify-300 里
// deepseek-v4-flash 只完成 48/102 时，另两个已跑完 96/99 和 90/99，慢的那个成了
// 关键路径。而配额状况随时间变化，事先测不准。改成共享队列后，快模型自然做得多。
//
// 为什么不是"按题轮流"：那会让同一考点的措辞出自不同模型，1:1 率变差、聚合层被污染。
// 桶（模块 × 题型）是**不可拆的最小单元**，这是本方案的核心不变量，有测试守着。
//
// 归属粘滞（schedule.go）：领取时把「桶 → 模型」落盘，续跑时已归属的桶回到原模型，
// 只有从未被领取过的桶才进共享队列。否则中断续跑会让桶被两个模型各做一半。
func RunMulti(ctx context.Context, db *sql.DB, engines []Engine, opt Options,
	questions []store.DistillQuestion, tax *Taxonomy) (result *MultiStats, retErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	release, err := acquireRunLock(ctx, db, opt.RunID)
	if err != nil {
		return nil, err
	}
	defer release()

	if len(engines) == 0 {
		return nil, fmt.Errorf("没有可用的执行器")
	}
	seen := map[string]bool{}
	dirs := map[string]bool{}
	for _, e := range engines {
		d := safeName(e.Model)
		if e.Model == "" || d == "." || d == ".." || seen[e.Model] || dirs[d] {
			return nil, fmt.Errorf("重复或无效模型目录: %q", e.Model)
		}
		seen[e.Model] = true
		dirs[d] = true
	}
	if err := ensureRunVersions(ctx, db, opt.RunID, tax); err != nil {
		return nil, err
	}

	buckets := BuildBuckets(questions)
	ownerPath := BucketOwnerPath(opt.RunDir, opt.RunID)
	if err := os.MkdirAll(filepath.Dir(ownerPath), 0o755); err != nil {
		return nil, err
	}
	owners := loadBucketOwners(ownerPath)
	if owners.loadErr != nil {
		return nil, owners.loadErr
	}
	owned, queue, reassigned := buildWorkPlan(buckets, engines, owners)

	models := make([]string, len(engines))
	for i, e := range engines {
		models[i] = e.Model
	}
	scope, _ := json.Marshal(map[string]any{
		"module": opt.Module, "limit": opt.Limit, "seed": opt.Seed,
		"models": models, "buckets": len(buckets), "schedule": "dynamic-bucket",
		"sticky_reassigned": reassigned,
	})
	registration := opt
	registration.Model = strings.Join(models, ",")
	if err := prepareStoredRun(ctx, db, registration, tax, questions, string(scope)); err != nil {
		return nil, err
	}
	defer func() {
		endCtx, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer c()
		var batch *BatchError
		retErr = errors.Join(retErr, finishStoredRun(endCtx, db, opt.RunID, ctx.Err() != nil || retErr != nil && !errors.As(retErr, &batch)))
	}()

	if reassigned > 0 {
		fmt.Fprintf(os.Stderr,
			"[warn] %d 个桶的原归属模型不在本次执行器列表里，已重新分配；"+
				"这些桶内可能出现两种模型的措辞（label.model 有记录，可事后过滤）\n", reassigned)
	}
	if n := len(owned); n > 0 {
		for i := range owned {
			if len(owned[i]) > 0 {
				fmt.Fprintf(os.Stderr, "[%s] 续跑归属：%s\n", engines[i].Model, summarizeBuckets(owned[i], 8))
			}
		}
	}

	start := time.Now()
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		perEng      = make([]EngineStats, len(engines))
		novel       []string
		agg         MultiStats
		batchErrors []error
	)
	recordError := func(err error) {
		mu.Lock()
		batchErrors = append(batchErrors, err)
		mu.Unlock()
		var batch *BatchError
		if !errors.As(err, &batch) {
			cancel()
		}
	}

	// 共享队列：大桶在前，谁空谁领。无缓冲 channel 天然形成工作窃取：
	// 主协程阻塞在发送上，直到某个执行器处理完手上的桶回来领取。
	work := make(chan *Bucket)

	for i, eng := range engines {
		wg.Add(1)
		go func(idx int, eng Engine, pre []*Bucket) {
			defer wg.Done()
			es := EngineStats{Model: eng.Model}

			sub := opt
			sub.Model = eng.Model
			sub.Concurrency = eng.Concurrency
			sub.RunDir = filepath.Join(opt.RunDir, opt.RunID, "engines")
			runner := New(db, eng.Client, tax, sub)
			runner.dir = filepath.Join(sub.RunDir, safeName(eng.Model))

			process := func(b *Bucket) {
				es.Buckets = append(es.Buckets, b.displayKey())
				if _, err := runner.RunOn(ctx, b.Items, false); err != nil {
					fmt.Fprintf(os.Stderr, "[%s] 桶 %s 处理异常: %v\n", eng.Model, b.displayKey(), err)
					recordError(err)
				}
			}
			// 先做续跑归属的桶，再从共享队列领取
			for _, b := range pre {
				if ctx.Err() != nil {
					break
				}
				process(b)
			}
			for b := range work {
				if ctx.Err() != nil {
					break
				}
				if err := owners.claim(b.Key(), eng.Model); err != nil {
					recordError(err)
					break
				}
				process(b)
			}

			// 汇总本执行器的累计成绩
			mu.Lock()
			es.Questions = runner.stats.Total
			es.OK = runner.stats.OK
			es.Failed = runner.stats.Failed
			es.Skipped = runner.stats.Skipped
			es.TokensIn = runner.stats.TokensIn
			es.TokensOut = runner.stats.TokensOut
			es.CostUSD = runner.stats.CostUSD
			es.DurationSec = time.Since(start).Seconds()
			perEng[idx] = es
			novel = append(novel, runner.stats.NovelConcepts...)
			agg.AnswerMismatch += runner.stats.AnswerMismatch
			agg.Doubt += runner.stats.DoubtRate
			mu.Unlock()
		}(i, eng, owned[i])
	}

feeding:
	for _, b := range queue {
		select {
		case <-ctx.Done():
			break feeding
		case work <- b:
		}
	}
	close(work)
	wg.Wait()

	agg.RunID = opt.RunID
	agg.Total = len(questions)
	agg.PerEngine = perEng
	agg.Novel = novel
	agg.Duration = time.Since(start)
	for i := range perEng {
		agg.OK += perEng[i].OK
		agg.Failed += perEng[i].Failed
		agg.Skipped += perEng[i].Skipped
		agg.TokensIn += perEng[i].TokensIn
		agg.TokensOut += perEng[i].TokensOut
		agg.CostUSD += perEng[i].CostUSD
	}
	if gap := agg.Total - agg.OK - agg.Skipped - agg.Failed; gap > 0 {
		agg.Failed += gap
	}

	enginesJSON, _ := json.Marshal(perEng)
	saveCtx, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer c()
	if _, err := db.ExecContext(saveCtx, `UPDATE label_run SET engines=? WHERE id=?`, string(enginesJSON), opt.RunID); err != nil {
		return nil, err
	}
	sort.Strings(agg.Novel)
	return &agg, errors.Join(append(batchErrors, ctx.Err())...)
}

// safeName 把模型名变成安全的目录名。
func safeName(s string) string {
	r := strings.NewReplacer("/", "_", ":", "_", "\\", "_", " ", "_")
	return r.Replace(s)
}
