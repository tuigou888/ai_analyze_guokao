package study

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// RefreshConcepts only aggregates existing labels. It never calls an LLM.
// The stable mapping is additive; counts use one latest label per question.
func (s *Service) RefreshConcepts(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO concept(name,module,secondary)
 SELECT tertiary,subject,secondary FROM latest_label WHERE COALESCE(tertiary,'')<>'' GROUP BY subject,tertiary
 ON CONFLICT(module,name) DO UPDATE SET secondary=excluded.secondary`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE concept SET question_count=(SELECT COUNT(*) FROM latest_label l WHERE l.tertiary=concept.name AND l.subject=concept.module)`); err != nil {
		return err
	}
	return tx.Commit()
}

type Concept struct {
	ID            int64            `json:"id"`
	Name          string           `json:"name"`
	Module        string           `json:"module"`
	Secondary     string           `json:"secondary"`
	QuestionCount int              `json:"question_count"`
	Answered      int              `json:"answered"`
	Correct       int              `json:"correct"`
	Mastery       float64          `json:"mastery"`
	Card          map[string]any   `json:"card,omitempty"`
	Related       []map[string]any `json:"related,omitempty"`
	Samples       []Brief          `json:"sample_questions,omitempty"`
}

func (s *Service) Concepts(ctx context.Context, user int64, module, secondary string) ([]Concept, error) {
	out := []Concept{}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,c.name,c.module,c.secondary,c.question_count,COALESCE(s.answered,0),COALESCE(s.correct,0),COALESCE(s.mastery,0)
 FROM concept c LEFT JOIN user_concept_stat s ON s.concept_id=c.id AND s.user_id=?
 WHERE (?='' OR c.module=?) AND (?='' OR c.secondary=?) AND c.question_count>0 ORDER BY c.module,c.secondary,c.name`, user, module, module, secondary, secondary)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Concept
		if err = rows.Scan(&c.ID, &c.Name, &c.Module, &c.Secondary, &c.QuestionCount, &c.Answered, &c.Correct, &c.Mastery); err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Service) Concept(ctx context.Context, user, id int64) (Concept, error) {
	var c Concept
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,module,secondary,question_count FROM concept WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.Module, &c.Secondary, &c.QuestionCount)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	// Cards contain general concept guidance only. Question-specific solution steps
	// and conclusions are available through the guarded question reveal endpoint.
	cols := []string{"question_model", "template", "key_features", "boundary", "confusable", "typical_ask", "model", "run_id"}
	selects := []string{}
	vals := make([]string, len(cols))
	dest := make([]any, len(cols))
	for i, key := range cols {
		selects = append(selects, "COALESCE("+key+",'')")
		dest[i] = &vals[i]
	}
	err = s.DB.QueryRowContext(ctx, `SELECT `+strings.Join(selects, ",")+` FROM latest_label WHERE subject=? AND tertiary=? ORDER BY id DESC LIMIT 1`, c.Module, c.Name).Scan(dest...)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	c.Card = map[string]any{}
	c.Related = []map[string]any{}
	for i, key := range cols {
		if key == "key_features" || key == "confusable" || key == "typical_ask" {
			var v any
			if json.Unmarshal([]byte(vals[i]), &v) == nil {
				c.Card[key] = v
			} else {
				c.Card[key] = []any{}
			}
		} else {
			c.Card[key] = vals[i]
		}
	}
	// Resolve exact concept names; unmapped names remain visible as guidance.
	if vs, ok := c.Card["confusable"].([]any); ok {
		for _, v := range vs {
			if m, ok := v.(map[string]any); ok {
				name, _ := m["考点"].(string)
				signal, _ := m["区分信号"].(string)
				var relatedID int64
				err := s.DB.QueryRowContext(ctx, `SELECT id FROM concept WHERE name=? AND module=?`, name, c.Module).Scan(&relatedID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return c, err
				}
				c.Related = append(c.Related, map[string]any{"concept_id": relatedID, "name": name, "kind": "confusable", "signal": signal})
			}
		}
	}
	page, err := s.Questions(ctx, Filter{ConceptID: id, Size: 5})
	c.Samples = page.Items
	return c, err
}
func (s *Service) Search(ctx context.Context, f Filter, mode string) ([]map[string]any, error) {
	if mode != "" && mode != "keyword" && mode != "concept" {
		return nil, errors.Join(ErrInvalid, errors.New("语义搜索将在考点数据准备完成后开放"))
	}
	if strings.TrimSpace(f.Q) == "" || len(f.Q) > 500 {
		return nil, ErrInvalid
	}
	f.Page = 1
	f.SearchMode = mode
	items, err := s.Questions(ctx, f)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, b := range items.Items {
		l, err := s.label(ctx, b.ID, false)
		if err != nil {
			return nil, err
		}
		ter := ""
		if l != nil {
			ter, _ = l["tertiary"].(string)
		}
		out = append(out, map[string]any{"question_id": b.ID, "brief": b.Stem, "module": b.Module, "tertiary": ter, "score": 1, "highlights": []string{f.Q}})
	}
	return out, nil
}
