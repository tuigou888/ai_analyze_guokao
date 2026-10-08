package distill

import (
	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/store"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

func ensureRunVersions(ctx context.Context, db *sql.DB, id string, tax *Taxonomy) error {
	var prompt, taxVersion sql.NullString
	err := db.QueryRowContext(ctx, `SELECT prompt_version,taxonomy_version FROM label_run WHERE id=?`, id).Scan(&prompt, &taxVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if prompt.String != PromptVersion || taxVersion.String != tax.Version {
		return fmt.Errorf("批次 %s 的提示词/规范表版本不同，请使用新 --run（旧 %s/%s，新 %s/%s）", id, prompt.String, taxVersion.String, PromptVersion, tax.Version)
	}
	return nil
}

func prepareStoredRun(ctx context.Context, db *sql.DB, opt Options, tax *Taxonomy, questions []store.DistillQuestion, scope string) error {
	if err := ensureRunVersions(ctx, db, opt.RunID, tax); err != nil {
		return err
	}
	items := append([]store.DistillQuestion(nil), questions...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	raw, err := json.Marshal(struct {
		Questions []store.DistillQuestion
		Taxonomy  *Taxonomy
	}{items, tax})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	var oldHash, model, base sql.NullString
	err = db.QueryRowContext(ctx, `SELECT input_hash,model_config,base_url FROM label_run WHERE id=?`, opt.RunID).Scan(&oldHash, &model, &base)
	if err == nil {
		if oldHash.Valid && oldHash.String != hash {
			return fmt.Errorf("批次 %s 的题集或输入内容变化，请使用新 --run", opt.RunID)
		}
		if model.String != opt.Model || base.String != opt.BaseURL {
			return fmt.Errorf("批次 %s 的模型或端点变化，请使用新 --run", opt.RunID)
		}
		_, err = db.ExecContext(ctx, `UPDATE label_run SET legacy_tokens_in=CASE WHEN input_hash IS NULL THEN MAX(tokens_in,legacy_tokens_in) ELSE legacy_tokens_in END,
		 legacy_tokens_out=CASE WHEN input_hash IS NULL THEN MAX(tokens_out,legacy_tokens_out) ELSE legacy_tokens_out END,
		 legacy_cost_usd=CASE WHEN input_hash IS NULL THEN MAX(cost_usd,legacy_cost_usd) ELSE legacy_cost_usd END,
		 usage_complete=CASE WHEN input_hash IS NULL THEN 0 ELSE usage_complete END,
		 input_hash=?,status='running',finished_at=NULL WHERE id=?`, hash, opt.RunID)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO label_run(id,scope,prompt_version,taxonomy_version,model_config,base_url,status,started_at,total,input_hash) VALUES(?,?,?,?,?,?,'running',?,?,?)`, opt.RunID, scope, PromptVersion, tax.Version, opt.Model, opt.BaseURL, time.Now().UTC().Format(time.RFC3339), len(questions), hash)
	return err
}

func (r *Runner) recordUsage(ctx context.Context, q store.DistillQuestion, resp *llm.Response) (int64, error) {
	var price any
	cost := 0.0
	if r.opt.HasPricing && resp.UsageKnown {
		cost = r.opt.Pricing.Cost(resp.TokensIn, resp.TokensOut)
		price = cost
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	res, err := r.db.ExecContext(saveCtx, `INSERT INTO label_call(run_id,question_id,model_config,model_response,tokens_in,tokens_out,cost_usd,usage_known,status,created_at) VALUES(?,?,?,?,?,?,?,?,'received',?)`, r.opt.RunID, q.ID, r.opt.Model, resp.Model, resp.TokensIn, resp.TokensOut, price, resp.UsageKnown, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("模型用量保存失败: %w", err)
	}
	r.mu.Lock()
	r.stats.TokensIn += resp.TokensIn
	r.stats.TokensOut += resp.TokensOut
	r.stats.CostUSD += cost
	if resp.Model != "" {
		r.stats.ModelResponse = resp.Model
	}
	r.mu.Unlock()
	return res.LastInsertId()
}

func finishStoredRun(ctx context.Context, db *sql.DB, id string, forceFailed bool) error {
	// Queries are authoritative: resumed labels and every paid response are retained.
	_, err := db.ExecContext(ctx, `UPDATE label_run SET
	 usage_complete=CASE WHEN EXISTS(SELECT 1 FROM label_call WHERE run_id=label_run.id AND usage_known=0) THEN 0 ELSE usage_complete END,
	 ok=(SELECT COUNT(*) FROM label WHERE run_id=label_run.id),
	 failed=MAX(total-(SELECT COUNT(*) FROM label WHERE run_id=label_run.id),0),
	 tokens_in=legacy_tokens_in+(SELECT COALESCE(SUM(tokens_in),0) FROM label_call WHERE run_id=label_run.id),
	 tokens_out=legacy_tokens_out+(SELECT COALESCE(SUM(tokens_out),0) FROM label_call WHERE run_id=label_run.id),
	 cost_usd=legacy_cost_usd+(SELECT COALESCE(SUM(cost_usd),0) FROM label_call WHERE run_id=label_run.id),
	 cost_known=CASE WHEN usage_complete=0 OR EXISTS(SELECT 1 FROM label_call WHERE run_id=label_run.id AND cost_usd IS NULL) THEN 0 ELSE 1 END,
	 model_response=COALESCE((SELECT model_response FROM label_call WHERE run_id=label_run.id AND model_response<>'' ORDER BY id DESC LIMIT 1),model_response),
	 status=CASE WHEN ? THEN 'failed' WHEN total=(SELECT COUNT(*) FROM label WHERE run_id=label_run.id) THEN 'finished'
	 WHEN EXISTS(SELECT 1 FROM label WHERE run_id=label_run.id) THEN 'partial' ELSE 'failed' END,
	 finished_at=? WHERE id=?`, forceFailed, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

type BatchError struct{ Failed int }

func (e *BatchError) Error() string {
	return fmt.Sprintf("批次有 %d 道题未完成，可使用相同 --run 续跑", e.Failed)
}
func (r *Runner) infrastructureFailure(err error) {
	r.mu.Lock()
	r.runErr = errors.Join(r.runErr, err)
	abort := r.abort
	r.mu.Unlock()
	if abort != nil {
		abort()
	}
}
func (r *Runner) setCallStatus(ctx context.Context, id int64, status string) error {
	saveCtx, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer c()
	_, err := r.db.ExecContext(saveCtx, `UPDATE label_call SET status=? WHERE id=?`, status, id)
	return err
}
