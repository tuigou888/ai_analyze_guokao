package study

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"ai_analyze_guokao/internal/store"
)

type Spec struct {
	Filter
	QuestionIDs []int64 `json:"question_ids,omitempty"`
	Limit       int     `json:"limit,omitempty"`
}
type Session struct {
	DraftRevision int64      `json:"draft_revision"`
	ID            int64      `json:"session_id"`
	Kind          string     `json:"kind"`
	Spec          Spec       `json:"spec"`
	QuestionIDs   []int64    `json:"question_ids"`
	StartedAt     string     `json:"started_at"`
	SubmittedAt   string     `json:"submitted_at"`
	Total         int        `json:"total"`
	Correct       int        `json:"correct"`
	Duration      int64      `json:"duration_ms"`
	Questions     []Question `json:"questions,omitempty"`
	Answers       []Answer   `json:"answers"`
	Results       []Result   `json:"results,omitempty"`
}
type Answer struct {
	QuestionID int64  `json:"question_id"`
	Answer     string `json:"answer"`
	Duration   int64  `json:"duration_ms"`
}
type Result struct {
	QuestionID    int64          `json:"question_id"`
	Answer        string         `json:"user_answer"`
	IsCorrect     bool           `json:"is_correct"`
	CorrectAnswer string         `json:"correct_answer,omitempty"`
	Explanation   string         `json:"explanation,omitempty"`
	Label         map[string]any `json:"label,omitempty"`
}

func normalized(a string) (string, error) {
	seen := map[rune]bool{}
	for _, r := range strings.ToUpper(strings.TrimSpace(a)) {
		if unicode.IsSpace(r) || strings.ContainsRune(",，、;；", r) {
			continue
		}
		if r < 'A' || r > 'Z' {
			return "", ErrInvalid
		}
		seen[r] = true
	}
	var b strings.Builder
	for r := 'A'; r <= 'Z'; r++ {
		if seen[r] {
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}
func Grade(kind, official, answer string) bool {
	a, e := normalized(answer)
	b, e2 := normalized(official)
	return e == nil && e2 == nil && a != "" && a == b && (kind == "multi" || len(a) == 1)
}
func (s *Service) CreateSession(ctx context.Context, user int64, kind string, spec Spec) (Session, error) {
	if kind != "single" && kind != "paperset" && kind != "concept" && kind != "wrongbook" && kind != "favorite" {
		return Session{}, ErrInvalid
	}
	if spec.Limit <= 0 {
		spec.Limit = 20
	}
	if spec.Limit > 200 || len(spec.QuestionIDs) > 200 {
		return Session{}, ErrInvalid
	}
	ids := []int64{}
	if len(spec.QuestionIDs) > 0 {
		seen := map[int64]bool{}
		for _, id := range spec.QuestionIDs {
			if seen[id] || id <= 0 {
				return Session{}, ErrInvalid
			}
			seen[id] = true
			var typ string
			if err := s.DB.QueryRowContext(ctx, `SELECT answer_type FROM question WHERE id=?`, id).Scan(&typ); err != nil {
				return Session{}, ErrNotFound
			}
			if typ == "other" {
				return Session{}, errors.Join(ErrInvalid, errors.New("该题暂无标准答案，暂不支持练习"))
			}
			ids = append(ids, id)
		}
	} else {
		where, args := spec.Filter.where()
		where += " AND q.answer_type IN ('single','multi','judge')"
		order := "random()"
		switch kind {
		case "paperset":
			if spec.PaperID <= 0 {
				return Session{}, ErrInvalid
			}
			order = `(SELECT number FROM question_occurrence o WHERE o.question_id=q.id AND o.paper_id=?)`
			args = append(args, spec.PaperID)
			spec.Limit = 200
		case "concept":
			if spec.ConceptID <= 0 {
				return Session{}, ErrInvalid
			}
		case "wrongbook":
			where += ` AND EXISTS(SELECT 1 FROM wrongbook w WHERE w.question_id=q.id AND w.user_id=? AND w.source='auto' AND w.resolved=0)`
			args = append(args, user)
		case "favorite":
			where += ` AND EXISTS(SELECT 1 FROM favorite f WHERE f.question_id=q.id AND f.user_id=?)`
			args = append(args, user)
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT q.id FROM question q WHERE `+where+` ORDER BY `+order+` LIMIT ?`, append(args, spec.Limit)...)
		if err != nil {
			return Session{}, err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return Session{}, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Session{}, err
		}
	}
	if len(ids) == 0 {
		return Session{}, errors.Join(ErrInvalid, errors.New("所选条件下没有可练习的题目"))
	}
	raw, _ := json.Marshal(spec)
	qi, _ := json.Marshal(ids)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO practice_session(user_id,kind,spec,question_ids,started_at,total) VALUES(?,?,?,?,?,?)`, user, kind, string(raw), string(qi), now(), len(ids))
	if err != nil {
		return Session{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Session{}, err
	}
	return s.Session(ctx, user, id)
}
func loadSession(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, user, id int64) (Session, error) {
	p := Session{Answers: []Answer{}}
	var spec, ids, draft string
	err := q.QueryRowContext(ctx, `SELECT id,kind,spec,question_ids,started_at,COALESCE(submitted_at,''),total,correct,duration_ms,draft,draft_revision FROM practice_session WHERE id=? AND user_id=?`, id, user).Scan(&p.ID, &p.Kind, &spec, &ids, &p.StartedAt, &p.SubmittedAt, &p.Total, &p.Correct, &p.Duration, &draft, &p.DraftRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(spec), &p.Spec); err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(ids), &p.QuestionIDs); err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(draft), &p.Answers)
	return p, err
}
func (s *Service) Session(ctx context.Context, user, id int64) (Session, error) {
	p, err := loadSession(ctx, s.DB, user, id)
	if err != nil {
		return p, err
	}
	if p.SubmittedAt != "" {
		p.Results, err = s.results(ctx, user, id)
		if err != nil {
			return p, err
		}
		p.Answers = []Answer{}
		for _, v := range p.Results {
			p.Answers = append(p.Answers, Answer{QuestionID: v.QuestionID, Answer: v.Answer})
		}
	}
	answered := map[int64]bool{}
	for _, a := range p.Answers {
		answered[a.QuestionID] = a.Answer != ""
	}
	for _, qid := range p.QuestionIDs {
		q, err := s.Question(ctx, qid, p.SubmittedAt != "" && answered[qid], p.Spec.PaperID)
		if err != nil {
			return p, err
		}
		p.Questions = append(p.Questions, q)
	}
	return p, nil
}
func (s *Service) Draft(ctx context.Context, user, id int64, answers []Answer, revision int64) (int64, error) {
	if revision < 0 {
		return 0, ErrInvalid
	}
	p, err := loadSession(ctx, s.DB, user, id)
	if err != nil {
		return 0, err
	}
	if p.SubmittedAt != "" {
		return 0, ErrDraftConflict
	}
	if p.DraftRevision != revision {
		return 0, ErrDraftConflict
	}
	if _, err = s.validateAnswers(ctx, p, answers); err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(answers)
	res, err := s.DB.ExecContext(ctx, `UPDATE practice_session SET draft=?,draft_revision=draft_revision+1 WHERE id=? AND user_id=? AND submitted_at IS NULL AND draft_revision=?`, string(raw), id, user, revision)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrDraftConflict
	}
	return revision + 1, nil
}
func (s *Service) validateAnswers(ctx context.Context, p Session, answers []Answer) (map[int64]Answer, error) {
	allowed := map[int64]bool{}
	for _, id := range p.QuestionIDs {
		allowed[id] = true
	}
	out := map[int64]Answer{}
	for _, a := range answers {
		if !allowed[a.QuestionID] {
			return nil, errors.Join(ErrInvalid, errors.New("提交了不属于该练习的题目"))
		}
		if _, ok := out[a.QuestionID]; ok {
			return nil, errors.Join(ErrInvalid, errors.New("重复提交同一道题"))
		}
		if a.Duration < 0 || a.Duration > 7*24*60*60*1000 {
			return nil, ErrInvalid
		}
		norm, err := normalized(a.Answer)
		if err != nil {
			return nil, ErrInvalid
		}
		a.Answer = norm
		if norm != "" {
			q, err := s.Question(ctx, a.QuestionID, false, p.Spec.PaperID)
			if err != nil {
				return nil, err
			}
			if q.AnswerType != "multi" && len(norm) != 1 {
				return nil, ErrInvalid
			}
			labels := map[string]bool{}
			for _, o := range q.Options {
				labels[o.Label] = true
			}
			for _, c := range norm {
				if !labels[string(c)] {
					return nil, errors.Join(ErrInvalid, errors.New("答案必须来自本题选项"))
				}
			}
		}
		out[a.QuestionID] = a
	}
	return out, nil
}
func (s *Service) Submit(ctx context.Context, user, id int64, answers []Answer, revision int64) (Session, error) {
	if revision < 0 {
		return Session{}, ErrInvalid
	}
	p, err := loadSession(ctx, s.DB, user, id)
	if err != nil {
		return p, err
	}
	if p.SubmittedAt != "" {
		return s.Session(ctx, user, id)
	}
	if p.DraftRevision != revision {
		return p, ErrDraftConflict
	}
	submitted, err := s.validateAnswers(ctx, p, answers)
	if err != nil {
		return p, err
	}
	err = store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		current, e := loadSession(ctx, tx, user, id)
		if e != nil {
			return e
		}
		if current.SubmittedAt != "" {
			return nil
		}
		if current.DraftRevision != revision {
			return ErrDraftConflict
		}
		p = current
		correct := 0
		var duration int64
		for i, qid := range p.QuestionIDs {
			a := submitted[qid]
			var official, typ string
			if err = tx.QueryRowContext(ctx, `SELECT answer,answer_type FROM question WHERE id=?`, qid).Scan(&official, &typ); err != nil {
				return err
			}
			ok := Grade(typ, official, a.Answer)
			if ok {
				correct++
			}
			duration += a.Duration
			if _, err = tx.ExecContext(ctx, `INSERT INTO practice_answer(session_id,question_id,ord,user_answer,is_correct,duration_ms,answered_at) VALUES(?,?,?,?,?,?,?)`, id, qid, i+1, a.Answer, ok, a.Duration, now()); err != nil {
				return err
			}
			if a.Answer != "" {
				if ok {
					_, err = tx.ExecContext(ctx, `UPDATE wrongbook SET resolved=1 WHERE user_id=? AND question_id=? AND source='auto'`, user, qid)
				} else {
					_, err = tx.ExecContext(ctx, `INSERT INTO wrongbook(user_id,question_id,source,wrong_count,last_wrong_at) VALUES(?,?,'auto',1,?) ON CONFLICT(user_id,question_id) DO UPDATE SET wrong_count=wrong_count+1,last_wrong_at=excluded.last_wrong_at,resolved=0`, user, qid, now())
				}
				if err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE practice_session SET submitted_at=?,correct=?,duration_ms=?,draft='[]',draft_revision=draft_revision+1 WHERE id=? AND user_id=? AND submitted_at IS NULL`, now(), correct, duration, id, user); err != nil {
			return err
		}
		if err = rebuildStats(ctx, tx, user); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return p, err
	}
	return s.Session(ctx, user, id)
}
func (s *Service) results(ctx context.Context, user, id int64) ([]Result, error) {
	out := []Result{}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.question_id,a.user_answer,a.is_correct FROM practice_answer a JOIN practice_session p ON p.id=a.session_id WHERE p.user_id=? AND p.id=? ORDER BY a.ord`, user, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r Result
		if err = rows.Scan(&r.QuestionID, &r.Answer, &r.IsCorrect); err != nil {
			rows.Close()
			return out, err
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for i := range out {
		if out[i].Answer == "" {
			continue
		}
		q, err := s.Question(ctx, out[i].QuestionID, true, 0)
		if err != nil {
			return out, err
		}
		out[i].CorrectAnswer = q.Answer
		out[i].Explanation = q.Explanation
		out[i].Label = q.Label
	}
	return out, nil
}
func (s *Service) Records(ctx context.Context, user int64, limit, offset int) (map[string]any, error) {
	_, limit = bounds(1, limit)
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM practice_session WHERE user_id=?`, user).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,kind,started_at,COALESCE(submitted_at,''),total,correct,duration_ms FROM practice_session WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, user, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Session{}
	for rows.Next() {
		var p Session
		if err = rows.Scan(&p.ID, &p.Kind, &p.StartedAt, &p.SubmittedAt, &p.Total, &p.Correct, &p.Duration); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return map[string]any{"total": total, "items": items}, rows.Err()
}
func rebuildStats(ctx context.Context, tx *sql.Tx, user int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_concept_stat WHERE user_id=?`, user); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO user_concept_stat(user_id,concept_id,answered,correct,mastery,updated_at)
 SELECT ?,c.id,COUNT(*),SUM(a.is_correct),1.0*SUM(a.is_correct)/COUNT(*),?
 FROM practice_answer a JOIN practice_session p ON p.id=a.session_id JOIN latest_label l ON l.question_id=a.question_id
 JOIN concept c ON c.name=l.tertiary AND c.module=l.subject
 WHERE p.user_id=? AND p.submitted_at IS NOT NULL AND a.user_answer<>'' GROUP BY c.id`, user, now(), user)
	return err
}
func (s *Service) RebuildStats(ctx context.Context, user int64) error {
	return store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error { return rebuildStats(ctx, tx, user) })
}

func (s *Service) Stats(ctx context.Context, user int64) (map[string]any, error) {
	var answered, correct, wrong, sessions int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(a.is_correct),0) FROM practice_answer a JOIN practice_session p ON p.id=a.session_id WHERE p.user_id=? AND a.user_answer<>''`, user).Scan(&answered, &correct)
	if err != nil {
		return nil, err
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wrongbook WHERE user_id=? AND source='auto' AND resolved=0`, user).Scan(&wrong); err != nil {
		return nil, err
	}
	var favorites int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM favorite WHERE user_id=?`, user).Scan(&favorites); err != nil {
		return nil, err
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM practice_session WHERE user_id=? AND submitted_at IS NOT NULL`, user).Scan(&sessions); err != nil {
		return nil, err
	}
	return map[string]any{"answered": answered, "correct": correct, "wrong": wrong, "favorites": favorites, "sessions": sessions}, nil
}

type Wrong struct {
	Brief
	Source     string `json:"source"`
	WrongCount int    `json:"wrong_count"`
	Resolved   bool   `json:"resolved"`
	LastWrong  string `json:"last_wrong_at"`
}
type Favorite struct {
	Brief
	CreatedAt string `json:"created_at"`
}

func (s *Service) Wrongbook(ctx context.Context, user int64, page, size int, status string) (map[string]any, error) {
	page, size = bounds(page, size)
	where := "w.user_id=? AND w.source='auto'"
	if status == "resolved" {
		where += " AND w.resolved=1"
	} else if status != "all" {
		where += " AND w.resolved=0"
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wrongbook w WHERE `+where, user).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+briefCols+`,w.source,w.wrong_count,w.resolved,w.last_wrong_at FROM wrongbook w JOIN question q ON q.id=w.question_id WHERE `+where+` ORDER BY w.last_wrong_at DESC LIMIT ? OFFSET ?`, user, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Wrong{}
	for rows.Next() {
		var v Wrong
		if err = rows.Scan(&v.ID, &v.Module, &v.Stem, &v.AnswerType, &v.OptionCount, &v.HasFigure, &v.HasLabel, &v.Source, &v.WrongCount, &v.Resolved, &v.LastWrong); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return map[string]any{"total": total, "items": items, "page": page, "size": size}, rows.Err()
}
func (s *Service) Favorites(ctx context.Context, user int64, page, size int) (map[string]any, error) {
	page, size = bounds(page, size)
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM favorite WHERE user_id=?`, user).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+briefCols+`,f.created_at FROM favorite f JOIN question q ON q.id=f.question_id WHERE f.user_id=? ORDER BY f.created_at DESC LIMIT ? OFFSET ?`, user, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Favorite{}
	for rows.Next() {
		var v Favorite
		if err = rows.Scan(&v.ID, &v.Module, &v.Stem, &v.AnswerType, &v.OptionCount, &v.HasFigure, &v.HasLabel, &v.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return map[string]any{"total": total, "items": items, "page": page, "size": size}, rows.Err()
}
func (s *Service) FavoriteAction(ctx context.Context, user, id int64, action string) error {
	switch action {
	case "add":
		var typ string
		if err := s.DB.QueryRowContext(ctx, `SELECT answer_type FROM question WHERE id=?`, id).Scan(&typ); err != nil {
			return ErrNotFound
		}
		_, err := s.DB.ExecContext(ctx, `INSERT INTO favorite(user_id,question_id,created_at) VALUES(?,?,?) ON CONFLICT(user_id,question_id) DO UPDATE SET created_at=excluded.created_at`, user, id, now())
		return err
	case "delete":
		res, err := s.DB.ExecContext(ctx, `DELETE FROM favorite WHERE user_id=? AND question_id=?`, user, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	default:
		return fmt.Errorf("%w: 未知收藏操作", ErrInvalid)
	}
}
func (s *Service) WrongAction(ctx context.Context, user, id int64, action string) error {
	switch action {
	case "favorite":
		return s.FavoriteAction(ctx, user, id, "add")
	case "resolve", "unresolve", "delete":
		sqlText := `UPDATE wrongbook SET resolved=? WHERE user_id=? AND question_id=? AND source='auto'`
		args := []any{action == "resolve", user, id}
		if action == "delete" {
			sqlText = `DELETE FROM wrongbook WHERE user_id=? AND question_id=? AND source='auto'`
			args = []any{user, id}
		}
		res, err := s.DB.ExecContext(ctx, sqlText, args...)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	default:
		return fmt.Errorf("%w: 未知错题操作", ErrInvalid)
	}
}
