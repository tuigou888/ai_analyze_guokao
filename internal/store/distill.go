package store

import (
	"context"
	"database/sql"
	"strings"
)

// DistillQuestion 是喂给蒸馏管线的一道题（已尽量还原公式与图片文本）。
type DistillQuestion struct {
	ID          int64
	Module      string
	Tag         string // 数据自带粗标签
	Stem        string
	Answer      string
	AnswerType  string
	Explanation string
	HasFigure   bool
	Options     []DistillOption
	Occurrences int
}

// DistillOption 一个选项。
type DistillOption struct {
	Label   string
	Content string
	Correct bool
}

// LoadDistillQuestions 取待蒸馏的题目。
//
// 三处文本优先用还原后的列：explanation_with_formula / stem_with_text 可能为 NULL
// （题里没有图片占位符时不会写），因此用 COALESCE 回退到原始列。
// 不还原公式就喂给模型，数量关系与资料分析的解析会是一串占位符——
// 这正是数据集作者记录的"资料分析疑点率 28.4%"的根因。
func LoadDistillQuestions(ctx context.Context, db *sql.DB, module string, limit int) ([]DistillQuestion, error) {
	q := `
		SELECT q.id, COALESCE(q.module,''),
		       COALESCE(m.raw_tag,''),
		       COALESCE(NULLIF(q.stem_with_text,''), q.stem, ''),
		       COALESCE(q.answer,''), q.answer_type,
		       COALESCE(NULLIF(q.explanation_with_formula,''), q.explanation, ''),
		       q.has_figure,
		       (SELECT COUNT(*) FROM question_occurrence o WHERE o.question_id = q.id)
		  FROM question q
		  LEFT JOIN question_occurrence m ON m.question_id = q.id
		 WHERE (? = '' OR q.module = ?)
		   AND q.answer_type IN ('single','multi','judge')
		 GROUP BY q.id
		 ORDER BY q.id`
	rows, err := db.QueryContext(ctx, q, module, module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DistillQuestion
	for rows.Next() {
		var d DistillQuestion
		var hasFig int
		if err := rows.Scan(&d.ID, &d.Module, &d.Tag, &d.Stem, &d.Answer, &d.AnswerType,
			&d.Explanation, &hasFig, &d.Occurrences); err != nil {
			return nil, err
		}
		d.HasFigure = hasFig == 1
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 选项按题批量取，避免 N+1 查询。
	byID, err := loadOptions(ctx, db, out)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Options = byID[out[i].ID]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func loadOptions(ctx context.Context, db *sql.DB, qs []DistillQuestion) (map[int64][]DistillOption, error) {
	if len(qs) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(qs))
	args := make([]any, 0, len(qs))
	for _, q := range qs {
		ids = append(ids, "?")
		args = append(args, q.ID)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT question_id, label, COALESCE(content,''), is_correct
		   FROM option WHERE question_id IN (`+strings.Join(ids, ",")+`)
		  ORDER BY question_id, ord`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64][]DistillOption{}
	for rows.Next() {
		var qid int64
		var o DistillOption
		var correct int
		if err := rows.Scan(&qid, &o.Label, &o.Content, &correct); err != nil {
			return nil, err
		}
		o.Correct = correct == 1
		out[qid] = append(out[qid], o)
	}
	return out, rows.Err()
}

// CountDistillCandidates 统计可蒸馏题目数，用于进度显示与批次规模确认。
func CountDistillCandidates(ctx context.Context, db *sql.DB, module string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM question
		  WHERE (? = '' OR module = ?) AND answer_type IN ('single','multi','judge')`,
		module, module).Scan(&n)
	return n, err
}
