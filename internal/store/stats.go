package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// Overview 全库概览。
type Overview struct {
	Papers      int
	Questions   int // 去重后的题目实体
	Occurrences int // 题目出现次数（= 源文件里的题块数）
	Options     int
	Materials   int
	Images      int
	Warnings    int
}

// ModuleRow 按模块的统计。
type ModuleRow struct {
	Module      string
	Occurrences int
	Entities    int
	WithFigure  int
}

// Breakdown 某一维度的分组计数。
type Breakdown struct {
	Key   string
	Count int
}

// Stats 汇总结果。
type Stats struct {
	Overview  Overview
	Modules   []ModuleRow
	Answer    []Breakdown
	ExamType  []Breakdown
	Variants  []Breakdown
	Warnings  []Breakdown
	Years     []Breakdown
	QIDIssues int // qid 与内容指纹互相矛盾的题目数
}

// Collect 跑一遍统计查询。
func Collect(db *sql.DB) (*Stats, error) {
	s := &Stats{}
	o := &s.Overview

	q := func(dst *int, query string, args ...any) error {
		return db.QueryRow(query, args...).Scan(dst)
	}
	for _, x := range []struct {
		dst   *int
		query string
	}{
		{&o.Papers, `SELECT COUNT(*) FROM paper`},
		{&o.Questions, `SELECT COUNT(*) FROM question`},
		{&o.Occurrences, `SELECT COUNT(*) FROM question_occurrence`},
		{&o.Options, `SELECT COUNT(*) FROM option`},
		{&o.Materials, `SELECT COUNT(*) FROM material`},
		{&o.Images, `SELECT COUNT(*) FROM image`},
		{&o.Warnings, `SELECT COUNT(*) FROM parse_warning`},
	} {
		if err := q(x.dst, x.query); err != nil {
			return nil, err
		}
	}

	var err error
	if s.Modules, err = collectModules(db); err != nil {
		return nil, err
	}
	if s.Answer, err = collectBreakdown(db, `SELECT answer_type, COUNT(*) FROM question GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, err
	}
	if s.ExamType, err = collectBreakdown(db, `SELECT COALESCE(exam_type,'?'), COUNT(*) FROM paper GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, err
	}
	if s.Variants, err = collectBreakdown(db, `SELECT COALESCE(NULLIF(paper_variant,''),'（无卷型）'), COUNT(*) FROM paper GROUP BY 1 ORDER BY 2 DESC LIMIT 12`); err != nil {
		return nil, err
	}
	if s.Warnings, err = collectBreakdown(db, `SELECT kind, COUNT(*) FROM parse_warning GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, err
	}
	if s.Years, err = collectBreakdown(db, `SELECT CAST(year AS TEXT), COUNT(*) FROM paper WHERE year IS NOT NULL GROUP BY 1 ORDER BY 1`); err != nil {
		return nil, err
	}
	// 同一份内容指纹对应多个 qid，或同一个 qid 对应多份内容——两种都是去重边界的存疑处。
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT content_hash FROM question WHERE qid IS NOT NULL
			GROUP BY content_hash HAVING COUNT(DISTINCT qid) > 1
			UNION ALL
			SELECT qid FROM question WHERE qid IS NOT NULL
			GROUP BY qid HAVING COUNT(DISTINCT content_hash) > 1
		)`).Scan(&s.QIDIssues); err != nil {
		return nil, err
	}
	return s, nil
}

func collectModules(db *sql.DB) ([]ModuleRow, error) {
	rows, err := db.Query(`
		SELECT q.module,
		       COUNT(o.id)                AS occurrences,
		       COUNT(DISTINCT q.id)       AS entities,
		       COUNT(DISTINCT CASE WHEN q.has_figure = 1 THEN q.id END) AS with_figure
		FROM question q
		JOIN question_occurrence o ON o.question_id = q.id
		GROUP BY q.module
		ORDER BY occurrences DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModuleRow
	for rows.Next() {
		var r ModuleRow
		var module sql.NullString
		if err := rows.Scan(&module, &r.Occurrences, &r.Entities, &r.WithFigure); err != nil {
			return nil, err
		}
		r.Module = module.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func collectBreakdown(db *sql.DB, query string) ([]Breakdown, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Breakdown
	for rows.Next() {
		var b Breakdown
		var k sql.NullString
		if err := rows.Scan(&k, &b.Count); err != nil {
			return nil, err
		}
		b.Key = k.String
		out = append(out, b)
	}
	return out, rows.Err()
}

// Contract 是解析契约：入库结果必须与之吻合，否则说明解析器坏了。
type Contract struct {
	Occurrences    int
	Entities       int
	WantedTotal    int
	MarkMismatch   int
	JudgeQuestions int
	AnswerMissing  int

	// 去重与源题库 qid 的交叉校验。必须查 question_occurrence.raw_qid，
	// 不能查 question.qid——后者每道实体只记首次出现的 qid，"无多 qid"恒真，指标是空的。
	//
	// 两个方向的含义完全不同：
	//   QIDSplit   一个 qid 被拆成多道实体 = 过度切分 = 同一道题被重复蒸馏（白花钱）
	//   QIDMerged  一个指纹覆盖多个 qid = 过度合并 = 把源题库认为不同的题当成同一道
	QIDSplit  int // 期望 0：指纹从不拆分源题库视为同一道的题
	QIDMerged int // 期望 24：需要人工复核的合并裁决队列
}

// CheckContract 校验关键不变量。
// wantedTotal 由调用方给出期望的题块总数（数据集实测为 58890）。
func CheckContract(db *sql.DB, wantedTotal int) (*Contract, error) {
	c := &Contract{WantedTotal: wantedTotal}
	row := func(dst *int, query string) error {
		return db.QueryRow(query).Scan(dst)
	}
	if err := row(&c.Occurrences, `SELECT COUNT(*) FROM question_occurrence`); err != nil {
		return nil, err
	}
	if err := row(&c.Entities, `SELECT COUNT(*) FROM question`); err != nil {
		return nil, err
	}
	if err := row(&c.MarkMismatch, `SELECT COUNT(*) FROM parse_warning WHERE kind = 'correct_mark_mismatch'`); err != nil {
		return nil, err
	}
	if err := row(&c.JudgeQuestions, `SELECT COUNT(*) FROM question WHERE answer_type = 'judge'`); err != nil {
		return nil, err
	}
	if err := row(&c.AnswerMissing, `SELECT COUNT(*) FROM parse_warning WHERE kind = 'answer_missing'`); err != nil {
		return nil, err
	}
	if err := row(&c.QIDSplit, `SELECT COUNT(*) FROM (
			SELECT o.raw_qid FROM question_occurrence o
			WHERE o.raw_qid IS NOT NULL AND o.raw_qid <> ''
			GROUP BY o.raw_qid HAVING COUNT(DISTINCT o.question_id) > 1)`); err != nil {
		return nil, err
	}
	if err := row(&c.QIDMerged, `SELECT COUNT(*) FROM (
			SELECT o.question_id FROM question_occurrence o
			GROUP BY o.question_id HAVING COUNT(DISTINCT o.raw_qid) > 1)`); err != nil {
		return nil, err
	}
	return c, nil
}

// 契约期望值，来自对 data/ 的实测（见 docs/架构方案.md §1）。
// 这些数字同时被 internal/ingest 的测试断言，任何一条对不上都说明解析器坏了。
const (
	WantOccurrences    = 58890
	WantMarkMismatch   = 0
	WantAnswerMissing  = 4
	WantUniqueEntities = 27449
	WantQIDSplit       = 0
	WantQIDMerged      = 24

	// WantJudgeQuestions 注意不是 111。源文件里用 `- （选项）[]` 占位的题共 111 道，
	// 其中 4 道答案本身标注为缺失（（缺）），会被归入 other 而不是 judge。
	// 107 + 4 = 111，两个数字必须一起看。
	WantJudgeQuestions = 107
)

// ContractOK 判断契约是否通过。
func (c *Contract) ContractOK() (bool, []string) {
	var problems []string
	if c.Occurrences != c.WantedTotal {
		problems = append(problems, fmt.Sprintf("题块数 %d，期望 %d", c.Occurrences, c.WantedTotal))
	}
	if c.MarkMismatch != WantMarkMismatch {
		problems = append(problems, fmt.Sprintf("✅ 与答案字段冲突 %d 条，期望 %d", c.MarkMismatch, WantMarkMismatch))
	}
	if c.JudgeQuestions != WantJudgeQuestions {
		problems = append(problems, fmt.Sprintf("判断题 %d 道，期望 %d", c.JudgeQuestions, WantJudgeQuestions))
	}
	if c.AnswerMissing != WantAnswerMissing {
		problems = append(problems, fmt.Sprintf("答案缺失 %d 道，期望 %d", c.AnswerMissing, WantAnswerMissing))
	}
	if c.Entities != WantUniqueEntities {
		problems = append(problems, fmt.Sprintf("题目实体 %d 道，期望 %d（去重口径变了？见 ingest.ContentHash 注释）",
			c.Entities, WantUniqueEntities))
	}
	// 过度切分会重复蒸馏、白花钱，必须硬失败；过度合并只影响 24 道题，进人工复核队列。
	if c.QIDSplit != WantQIDSplit {
		problems = append(problems, fmt.Sprintf("被拆分的 qid %d 个，期望 %d（过度切分会重复蒸馏）",
			c.QIDSplit, WantQIDSplit))
	}
	return len(problems) == 0, problems
}

// QIDConflicts 列出「一个指纹覆盖多个 qid」的合并裁决队列。
// 这是去重唯一的存疑面，共 24 条，值得人工逐条看一眼。
type QIDConflict struct {
	QuestionID int64
	Hash       string
	QIDs       string
	Module     string
	StemBrief  string
	Occurs     int
}

// ListQIDConflicts 取合并裁决队列。
func ListQIDConflicts(db *sql.DB) ([]QIDConflict, error) {
	rows, err := db.Query(`
		SELECT q.id, q.content_hash,
		       GROUP_CONCAT(DISTINCT o.raw_qid) AS qids,
		       COALESCE(q.module,''),
		       substr(COALESCE(q.stem,''), 1, 40),
		       COUNT(o.id)
		FROM question q
		JOIN question_occurrence o ON o.question_id = q.id
		GROUP BY q.id
		HAVING COUNT(DISTINCT o.raw_qid) > 1
		ORDER BY COUNT(o.id) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QIDConflict
	for rows.Next() {
		var c QIDConflict
		if err := rows.Scan(&c.QuestionID, &c.Hash, &c.QIDs, &c.Module, &c.StemBrief, &c.Occurs); err != nil {
			return nil, err
		}
		c.StemBrief = strings.ReplaceAll(c.StemBrief, "\n", " ")
		out = append(out, c)
	}
	return out, rows.Err()
}

// DuplicateTop 重复次数最多的题目，用于观察跨省联考的热门题。
type DuplicateTop struct {
	QID       string
	Module    string
	Count     int
	StemBrief string
}

// TopDuplicates 取被重复收录最多的题。
func TopDuplicates(db *sql.DB, limit int) ([]DuplicateTop, error) {
	rows, err := db.Query(`
		SELECT COALESCE(q.qid,''), COALESCE(q.module,''),
		       COUNT(o.id) AS n, substr(COALESCE(q.stem,''), 1, 40)
		FROM question q
		JOIN question_occurrence o ON o.question_id = q.id
		GROUP BY q.id
		HAVING n > 1
		ORDER BY n DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DuplicateTop
	for rows.Next() {
		var d DuplicateTop
		if err := rows.Scan(&d.QID, &d.Module, &d.Count, &d.StemBrief); err != nil {
			return nil, err
		}
		d.StemBrief = strings.ReplaceAll(d.StemBrief, "\n", " ")
		out = append(out, d)
	}
	return out, rows.Err()
}
