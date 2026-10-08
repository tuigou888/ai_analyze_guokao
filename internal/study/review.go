package study

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

type UnfinishedPractice struct {
	SessionID     int64  `json:"session_id"`
	Kind          string `json:"kind"`
	Total         int    `json:"total"`
	SavedAnswers  int    `json:"saved_answers"`
	StartedAt     string `json:"started_at"`
	DraftRevision int64  `json:"draft_revision"`
}
type WeakConcept struct {
	ConceptID  int64    `json:"concept_id"`
	Name       string   `json:"name"`
	Module     string   `json:"module"`
	Answered   int      `json:"answered"`
	Correct    int      `json:"correct"`
	Accuracy   *float64 `json:"accuracy"`
	Sufficient bool     `json:"sufficient"`
}
type WrongCandidate struct {
	QuestionID  int64  `json:"question_id"`
	Module      string `json:"module"`
	WrongCount  int    `json:"wrong_count"`
	LastWrongAt string `json:"last_wrong_at"`
}
type ReviewSuggestions struct {
	Unfinished      []UnfinishedPractice `json:"unfinished"`
	WeakModules     []StudyModule        `json:"weak_modules"`
	WeakConcepts    []WeakConcept        `json:"weak_concepts"`
	LabeledAnswered int                  `json:"labeled_answered"`
	// Labels without a stable concept mapping count toward coverage, but never
	// become recommendations that the existing concept practice cannot start.
	UnmappedLabeledAnswered int              `json:"unmapped_labeled_answered"`
	LabelCoverage           *float64         `json:"label_coverage"`
	Wrong                   int              `json:"wrong"`
	Favorites               int              `json:"favorites"`
	WrongCandidates         []WrongCandidate `json:"wrong_candidates"`
}
type reviewReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func prioritizedWrongs(ctx context.Context, db reviewReader, user int64) ([]WrongCandidate, error) {
	rows, err := db.QueryContext(ctx, `SELECT q.id,COALESCE(q.module,''),w.wrong_count,w.last_wrong_at FROM wrongbook w JOIN question q ON q.id=w.question_id WHERE w.user_id=? AND w.source='auto' AND w.resolved=0 AND q.answer_type IN ('single','multi','judge') ORDER BY w.wrong_count DESC,julianday(w.last_wrong_at) DESC,q.id LIMIT 20`, user)
	out := []WrongCandidate{}
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var c WrongCandidate
		if err = rows.Scan(&c.QuestionID, &c.Module, &c.WrongCount, &c.LastWrongAt); err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PrioritizedWrongbook selects only this user's pending automatic wrongbook
// questions. The established session service owns validation, drafts and grading.
func (s *Service) PrioritizedWrongbook(ctx context.Context, user int64) (Session, error) {
	candidates, err := prioritizedWrongs(ctx, s.DB, user)
	if err != nil {
		return Session{}, err
	}
	if len(candidates) == 0 {
		return Session{}, errors.Join(ErrInvalid, errors.New("暂无待订正且可练习的错题"))
	}
	ids := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.QuestionID)
	}
	return s.CreateSession(ctx, user, "wrongbook", Spec{QuestionIDs: ids, Limit: 20})
}

func dashboardReview(ctx context.Context, tx *sql.Tx, user int64, start, end time.Time, period StudyCounts, modules []StudyModule) (ReviewSuggestions, error) {
	out := ReviewSuggestions{Unfinished: []UnfinishedPractice{}, WeakModules: []StudyModule{}, WeakConcepts: []WeakConcept{}, WrongCandidates: []WrongCandidate{}}
	rows, err := tx.QueryContext(ctx, `SELECT id,kind,total,started_at,draft_revision,draft FROM practice_session WHERE user_id=? AND submitted_at IS NULL ORDER BY started_at DESC,id DESC LIMIT 3`, user)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p UnfinishedPractice
		var raw string
		if err = rows.Scan(&p.SessionID, &p.Kind, &p.Total, &p.StartedAt, &p.DraftRevision, &raw); err != nil {
			rows.Close()
			return out, err
		}
		var draft []Answer
		if err = json.Unmarshal([]byte(raw), &draft); err != nil {
			rows.Close()
			return out, err
		}
		for _, a := range draft {
			if strings.TrimSpace(a.Answer) != "" {
				p.SavedAnswers++
			}
		}
		out.Unfinished = append(out.Unfinished, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, m := range modules {
		if m.Sufficient && m.Module != "" {
			out.WeakModules = append(out.WeakModules, m)
		}
	}
	sort.SliceStable(out.WeakModules, func(i, j int) bool { return *out.WeakModules[i].Accuracy < *out.WeakModules[j].Accuracy })
	if len(out.WeakModules) > 3 {
		out.WeakModules = out.WeakModules[:3]
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN c.id IS NULL THEN 1 ELSE 0 END),0)
 FROM practice_answer a JOIN practice_session p ON p.id=a.session_id JOIN latest_label l ON l.question_id=a.question_id LEFT JOIN concept c ON c.module=l.subject AND c.name=l.tertiary
 WHERE p.user_id=? AND p.submitted_at IS NOT NULL AND julianday(p.submitted_at)>=julianday(?) AND julianday(p.submitted_at)<julianday(?) AND trim(a.user_answer)<>''`, user, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)).Scan(&out.LabeledAnswered, &out.UnmappedLabeledAnswered); err != nil {
		return out, err
	}
	out.LabelCoverage = accuracy(period.Answered, out.LabeledAnswered)
	// Current label mapping and original answers deliberately bypass cached
	// user_concept_stat, which can be stale after relabeling.
	rows, err = tx.QueryContext(ctx, `SELECT c.id,l.tertiary,l.subject,COUNT(*),SUM(CASE WHEN a.is_correct=1 THEN 1 ELSE 0 END)
 FROM practice_answer a JOIN practice_session p ON p.id=a.session_id JOIN latest_label l ON l.question_id=a.question_id JOIN concept c ON c.module=l.subject AND c.name=l.tertiary
 WHERE p.user_id=? AND p.submitted_at IS NOT NULL AND julianday(p.submitted_at)>=julianday(?) AND julianday(p.submitted_at)<julianday(?) AND trim(a.user_answer)<>'' AND COALESCE(l.tertiary,'')<>''
 GROUP BY c.id,l.tertiary,l.subject HAVING COUNT(*)>=5 ORDER BY (1.0*SUM(CASE WHEN a.is_correct=1 THEN 1 ELSE 0 END)/COUNT(*)),l.subject,l.tertiary LIMIT 3`, user, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c WeakConcept
		if err = rows.Scan(&c.ConceptID, &c.Name, &c.Module, &c.Answered, &c.Correct); err != nil {
			rows.Close()
			return out, err
		}
		c.Accuracy = accuracy(c.Answered, c.Correct)
		c.Sufficient = true
		out.WeakConcepts = append(out.WeakConcepts, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wrongbook WHERE user_id=? AND source='auto' AND resolved=0`, user).Scan(&out.Wrong); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM favorite WHERE user_id=?`, user).Scan(&out.Favorites); err != nil {
		return out, err
	}
	out.WrongCandidates, err = prioritizedWrongs(ctx, tx, user)
	return out, err
}
