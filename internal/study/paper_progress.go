package study

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// PaperProgress describes only the current user's selected attempt, never its answers.
// The newest unfinished attempt wins; otherwise use the newest submitted attempt.
type PaperProgress struct {
	PaperID     int64  `json:"paper_id"`
	SessionID   int64  `json:"session_id"`
	Status      string `json:"status"`
	Answered    int    `json:"answered"`
	Total       int    `json:"total"`
	Correct     *int   `json:"correct,omitempty"`
	StartedAt   string `json:"started_at"`
	SubmittedAt string `json:"submitted_at,omitempty"`
}

func (s *Service) PaperProgress(ctx context.Context, user int64, paperIDs []int64) ([]PaperProgress, error) {
	out := []PaperProgress{}
	if user <= 0 {
		return nil, ErrCredentials
	}
	if len(paperIDs) == 0 || len(paperIDs) > 100 {
		return nil, ErrInvalid
	}
	args := []any{user}
	for _, id := range paperIDs {
		if id <= 0 {
			return nil, ErrInvalid
		}
		args = append(args, id)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(paperIDs)), ",")
	// All association data already lives in the frozen spec. No migration or new
	// session is needed, and checking a card never consumes practice quota.
	paperExpr := `CASE WHEN json_valid(spec) THEN CAST(json_extract(spec,'$.paper_id') AS INTEGER) END`
	rows, err := s.DB.QueryContext(ctx, `WITH ranked AS (
  SELECT id,`+paperExpr+` AS paper_id,question_ids,total,correct,started_at,
   COALESCE(submitted_at,'') AS submitted_at,draft,
   ROW_NUMBER() OVER (PARTITION BY `+paperExpr+` ORDER BY
    CASE WHEN COALESCE(submitted_at,'')='' THEN 0 ELSE 1 END,
    julianday(submitted_at) DESC,id DESC) AS priority
  FROM practice_session WHERE user_id=? AND kind='paperset' AND `+paperExpr+` IN (`+placeholders+`)
 ) SELECT id,paper_id,question_ids,total,correct,started_at,submitted_at,draft,
  (SELECT COUNT(*) FROM practice_answer a WHERE a.session_id=ranked.id AND trim(a.user_answer)<>'')
 FROM ranked WHERE priority=1 ORDER BY paper_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p PaperProgress
		var questionIDs, draft string
		var correct, submittedAnswers int
		if err = rows.Scan(&p.SessionID, &p.PaperID, &questionIDs, &p.Total, &correct, &p.StartedAt, &p.SubmittedAt, &draft, &submittedAnswers); err != nil {
			return nil, err
		}
		if p.SubmittedAt != "" {
			p.Status = "completed"
			p.Correct = &correct
			p.Answered = submittedAnswers
		} else {
			p.Status = "in_progress"
			var ids []int64
			var answers []Answer
			if err = json.Unmarshal([]byte(questionIDs), &ids); err != nil {
				return nil, fmt.Errorf("read paper progress question ids: %w", err)
			}
			if err = json.Unmarshal([]byte(draft), &answers); err != nil {
				return nil, fmt.Errorf("read paper progress draft: %w", err)
			}
			allowed := make(map[int64]bool, len(ids))
			for _, id := range ids {
				allowed[id] = true
			}
			seen := make(map[int64]bool, len(answers))
			for _, answer := range answers {
				if allowed[answer.QuestionID] && strings.TrimSpace(answer.Answer) != "" {
					seen[answer.QuestionID] = true
				}
			}
			p.Answered = len(seen)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
