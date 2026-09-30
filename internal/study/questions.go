package study

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Filter struct {
	Module     string `json:"module,omitempty"`
	Year       int    `json:"year,omitempty"`
	Region     string `json:"region,omitempty"`
	ExamType   string `json:"exam_type,omitempty"`
	Variant    string `json:"variant,omitempty"`
	PaperID    int64  `json:"paper_id,omitempty"`
	ConceptID  int64  `json:"concept_id,omitempty"`
	Q          string `json:"q,omitempty"`
	Order      string `json:"order,omitempty"`
	Page       int    `json:"page,omitempty"`
	Size       int    `json:"size,omitempty"`
	SearchMode string `json:"-"`
}

func FilterFrom(v url.Values) Filter {
	year, _ := strconv.Atoi(v.Get("year"))
	paper, _ := strconv.ParseInt(v.Get("paper_id"), 10, 64)
	concept, _ := strconv.ParseInt(v.Get("concept_id"), 10, 64)
	page, _ := strconv.Atoi(v.Get("page"))
	size, _ := strconv.Atoi(v.Get("size"))
	return Filter{Module: v.Get("module"), Year: year, Region: v.Get("region"), ExamType: v.Get("exam_type"), Variant: v.Get("variant"), PaperID: paper, ConceptID: concept, Q: v.Get("q"), Order: v.Get("order"), Page: page, Size: size}
}
func bounds(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	if page > 100000 {
		page = 100000
	}
	return page, size
}
func (f Filter) where() (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}
	if f.Module != "" {
		clauses = append(clauses, "q.module=?")
		args = append(args, f.Module)
	}
	if f.ConceptID > 0 {
		clauses = append(clauses, `EXISTS(SELECT 1 FROM latest_label l JOIN concept c ON c.module=l.subject AND c.name=l.tertiary WHERE l.question_id=q.id AND c.id=?)`)
		args = append(args, f.ConceptID)
	}
	occ := []string{"o.question_id=q.id"}
	oa := []any{}
	if f.PaperID > 0 {
		occ = append(occ, "p.id=?")
		oa = append(oa, f.PaperID)
	}
	if f.Year > 0 {
		occ = append(occ, "p.year=?")
		oa = append(oa, f.Year)
	}
	for _, kv := range []struct{ k, v string }{{"region", f.Region}, {"exam_type", f.ExamType}, {"paper_variant", f.Variant}} {
		if kv.v != "" {
			occ = append(occ, "p."+kv.k+"=?")
			oa = append(oa, kv.v)
		}
	}
	if len(occ) > 1 {
		clauses = append(clauses, `EXISTS(SELECT 1 FROM question_occurrence o JOIN paper p ON p.id=o.paper_id WHERE `+strings.Join(occ, " AND ")+`)`)
		args = append(args, oa...)
	}
	if f.Q != "" {
		term := strings.TrimSpace(f.Q)
		if f.SearchMode == "concept" {
			clauses = append(clauses, `EXISTS(SELECT 1 FROM latest_label l WHERE l.question_id=q.id AND instr(l.tertiary,?)>0)`)
			args = append(args, term)
		} else if utf8.RuneCountInString(term) >= 3 {
			clauses = append(clauses, `(q.id IN (SELECT rowid FROM question_fts WHERE question_fts MATCH ?) OR EXISTS(SELECT 1 FROM latest_label l WHERE l.question_id=q.id AND instr(l.tertiary,?)>0))`)
			args = append(args, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`, term)
		} else {
			clauses = append(clauses, `(instr(q.stem,?)>0 OR EXISTS(SELECT 1 FROM latest_label l WHERE l.question_id=q.id AND instr(l.tertiary,?)>0))`)
			args = append(args, term, term)
		}
	}
	return strings.Join(clauses, " AND "), args
}

type Brief struct {
	ID          int64  `json:"id"`
	Module      string `json:"module"`
	Stem        string `json:"stem"`
	AnswerType  string `json:"answer_type"`
	OptionCount int    `json:"option_count"`
	HasFigure   bool   `json:"has_figure"`
	HasLabel    bool   `json:"has_label"`
}
type Page struct {
	Total int     `json:"total"`
	Items []Brief `json:"items"`
	Page  int     `json:"page"`
	Size  int     `json:"size"`
}

const briefCols = `q.id,COALESCE(q.module,''),COALESCE(q.stem,''),q.answer_type,q.option_count,q.has_figure,EXISTS(SELECT 1 FROM label l WHERE l.question_id=q.id)`

func scanBrief(rows *sql.Rows) (Brief, error) {
	var b Brief
	err := rows.Scan(&b.ID, &b.Module, &b.Stem, &b.AnswerType, &b.OptionCount, &b.HasFigure, &b.HasLabel)
	return b, err
}
func (s *Service) Questions(ctx context.Context, f Filter) (Page, error) {
	f.Page, f.Size = bounds(f.Page, f.Size)
	p := Page{Items: []Brief{}, Page: f.Page, Size: f.Size}
	where, args := f.where()
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM question q WHERE `+where, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	order := "q.id DESC"
	if f.Order == "oldest" {
		order = "q.id"
	}
	if f.Order == "random" {
		order = "random()"
	}
	if f.PaperID > 0 {
		order = `(SELECT o.number FROM question_occurrence o WHERE o.question_id=q.id AND o.paper_id=` + strconv.FormatInt(f.PaperID, 10) + `)`
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+briefCols+` FROM question q WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, f.Size, (f.Page-1)*f.Size)...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		b, err := scanBrief(rows)
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, b)
	}
	return p, rows.Err()
}

type Option struct {
	Label   string `json:"label"`
	Content string `json:"content"`
}
type Question struct {
	ID           int64          `json:"id"`
	Module       string         `json:"module"`
	Stem         string         `json:"stem"`
	StemText     string         `json:"stem_with_text"`
	Material     string         `json:"material_body"`
	MaterialText string         `json:"material_with_text"`
	AnswerType   string         `json:"answer_type"`
	Options      []Option       `json:"options"`
	HasFigure    bool           `json:"has_figure"`
	HasLabel     bool           `json:"has_label"`
	Label        map[string]any `json:"label,omitempty"`
	Answer       string         `json:"answer,omitempty"`
	Explanation  string         `json:"explanation_with_formula,omitempty"`
	Sources      []Paper        `json:"sources"`
}

func (s *Service) Question(ctx context.Context, id int64, reveal bool, paperID int64) (Question, error) {
	q := Question{Options: []Option{}, Sources: []Paper{}}
	err := s.DB.QueryRowContext(ctx, `SELECT id,COALESCE(module,''),COALESCE(stem,''),COALESCE(stem_with_text,stem,''),answer_type,has_figure FROM question WHERE id=?`, id).Scan(&q.ID, &q.Module, &q.Stem, &q.StemText, &q.AnswerType, &q.HasFigure)
	if errors.Is(err, sql.ErrNoRows) {
		return q, ErrNotFound
	}
	if err != nil {
		return q, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(m.body,''),COALESCE(m.body_with_text,m.body,'') FROM question_occurrence o LEFT JOIN material m ON m.id=o.material_id WHERE o.question_id=? ORDER BY (o.paper_id=?) DESC,o.id LIMIT 1`, id, paperID).Scan(&q.Material, &q.MaterialText)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return q, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT label,COALESCE(content,'') FROM option WHERE question_id=? ORDER BY ord`, id)
	if err != nil {
		return q, err
	}
	for rows.Next() {
		var o Option
		if err = rows.Scan(&o.Label, &o.Content); err != nil {
			rows.Close()
			return q, err
		}
		q.Options = append(q.Options, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return q, err
	}
	if q.AnswerType == "judge" {
		q.Options = []Option{{"A", "正确"}, {"B", "错误"}}
	}
	q.Label, err = s.label(ctx, id, reveal)
	if err != nil {
		return q, err
	}
	q.HasLabel = q.Label != nil
	q.Sources, err = s.papers(ctx, `WHERE EXISTS(SELECT 1 FROM question_occurrence o WHERE o.paper_id=p.id AND o.question_id=?)`, []any{id}, 100)
	if err != nil {
		return q, err
	}
	if reveal {
		err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(answer,''),COALESCE(NULLIF(explanation_with_formula,''),explanation,'') FROM question WHERE id=?`, id).Scan(&q.Answer, &q.Explanation)
	}
	return q, err
}
func (s *Service) label(ctx context.Context, id int64, full bool) (map[string]any, error) {
	cols := []string{"subject", "secondary", "tertiary"}
	if full {
		cols = append(cols, "detail", "question_model", "reasoning_chain", "fastest_solution", "pitfalls", "template", "key_features", "boundary", "confusable", "typical_ask", "doubt", "model", "run_id")
	}
	selects := make([]string, len(cols))
	dest := make([]any, len(cols))
	vals := make([]string, len(cols))
	for i, c := range cols {
		selects[i] = "COALESCE(" + c + ",'')"
		dest[i] = &vals[i]
	}
	err := s.DB.QueryRowContext(ctx, `SELECT `+strings.Join(selects, ",")+` FROM latest_label WHERE question_id=?`, id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for i, c := range cols {
		if c == "reasoning_chain" || c == "pitfalls" || c == "key_features" || c == "confusable" || c == "typical_ask" {
			var v any
			if json.Unmarshal([]byte(vals[i]), &v) == nil {
				out[c] = v
			} else {
				out[c] = []any{}
			}
		} else {
			out[c] = vals[i]
		}
	}
	return out, nil
}
func (s *Service) Reveal(ctx context.Context, user, id int64) (Question, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM practice_answer a JOIN practice_session p ON p.id=a.session_id WHERE p.user_id=? AND a.question_id=? AND a.is_correct=1 AND p.submitted_at IS NOT NULL`, user, id).Scan(&n)
	if err != nil {
		return Question{}, err
	}
	if n == 0 {
		return Question{}, ErrForbidden
	}
	return s.Question(ctx, id, true, 0)
}

type Paper struct {
	ID       int64  `json:"paper_id"`
	Name     string `json:"name"`
	ExamType string `json:"exam_type"`
	Year     int    `json:"year"`
	Region   string `json:"region"`
	Module   string `json:"module"`
	Variant  string `json:"variant"`
	Count    int    `json:"question_count"`
}

func (s *Service) papers(ctx context.Context, where string, args []any, limit int) ([]Paper, error) {
	items := []Paper{}
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id,p.name,COALESCE(p.exam_type,''),COALESCE(p.year,0),COALESCE(p.region,''),COALESCE(p.module,''),COALESCE(p.paper_variant,''),COALESCE(p.question_count,0) FROM paper p `+where+` ORDER BY p.year DESC,p.id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return items, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Paper
		if err = rows.Scan(&p.ID, &p.Name, &p.ExamType, &p.Year, &p.Region, &p.Module, &p.Variant, &p.Count); err != nil {
			return items, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}
func (s *Service) Papers(ctx context.Context, f Filter) ([]Paper, error) {
	w := []string{"1=1"}
	a := []any{}
	for _, kv := range []struct{ k, v string }{{"module", f.Module}, {"exam_type", f.ExamType}, {"region", f.Region}, {"paper_variant", f.Variant}} {
		if kv.v != "" {
			w = append(w, "p."+kv.k+"=?")
			a = append(a, kv.v)
		}
	}
	if f.Year > 0 {
		w = append(w, "p.year=?")
		a = append(a, f.Year)
	}
	return s.papers(ctx, "WHERE "+strings.Join(w, " AND "), a, 3000)
}
func (s *Service) Filters(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	for key, col := range map[string]string{"modules": "module", "years": "year", "regions": "region", "exam_types": "exam_type", "variants": "paper_variant"} {
		rows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`SELECT DISTINCT %s FROM paper WHERE %s IS NOT NULL AND %s<>'' ORDER BY %s DESC`, col, col, col, col))
		if err != nil {
			return nil, err
		}
		vs := []any{}
		for rows.Next() {
			var v any
			if err = rows.Scan(&v); err != nil {
				rows.Close()
				return nil, err
			}
			vs = append(vs, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		out[key] = vs
	}
	return out, nil
}
