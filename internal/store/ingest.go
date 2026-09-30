package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"ai_analyze_guokao/internal/ingest"
	"ai_analyze_guokao/internal/model"
)

// Ingester 把一个文件写成一个事务。
//
// 去重是自然落下来的：question.content_hash 上有唯一约束，INSERT OR IGNORE
// 之后按 hash 取回 id，于是同一道题在多套卷里的多次出现会共享一个 question 行，
// 而每次出现各占一条 question_occurrence。
type Ingester struct {
	db    *sql.DB
	stmts *stmts
	runID int64
}

type stmts struct {
	paper      *sql.Stmt
	material   *sql.Stmt
	question   *sql.Stmt
	findQ      *sql.Stmt
	occurrence *sql.Stmt
	option     *sql.Stmt
	image      *sql.Stmt
	warning    *sql.Stmt
}

// NewIngester 准备语句并登记一次 ingest run。
func NewIngester(db *sql.DB, note string) (*Ingester, error) {
	s, err := prepare(db)
	if err != nil {
		return nil, err
	}
	res, err := db.Exec(`INSERT INTO ingest_run(started_at, note) VALUES (?, ?)`,
		time.Now().Format(time.RFC3339), note)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Ingester{db: db, stmts: s, runID: id}, nil
}

func prepare(db *sql.DB) (*stmts, error) {
	s := &stmts{}
	var err error
	if s.paper, err = db.Prepare(`
		INSERT INTO paper(name, region, year, module, exam_type, paper_variant,
		                  declared_count, question_count, source_path)
		VALUES (?,?,?,?,?,?,?,?,?)`); err != nil {
		return nil, err
	}
	if s.material, err = db.Prepare(`
		INSERT INTO material(paper_id, seq, body, body_html, has_figure) VALUES (?,?,?,?,?)`); err != nil {
		return nil, err
	}
	if s.question, err = db.Prepare(`
		INSERT OR IGNORE INTO question(content_hash, qid, module, stem, stem_html,
			answer, answer_type, answer_mark, explanation, explanation_html,
			has_figure, has_material_figure, option_placeholder, option_count, image_count)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`); err != nil {
		return nil, err
	}
	if s.findQ, err = db.Prepare(`SELECT id FROM question WHERE content_hash = ?`); err != nil {
		return nil, err
	}
	if s.occurrence, err = db.Prepare(`
		INSERT OR IGNORE INTO question_occurrence(question_id, paper_id, material_id,
			number, raw_qid, raw_tag, in_material) VALUES (?,?,?,?,?,?,?)`); err != nil {
		return nil, err
	}
	if s.option, err = db.Prepare(`
		INSERT OR IGNORE INTO option(question_id, ord, label, content, content_html, is_correct)
		VALUES (?,?,?,?,?,?)`); err != nil {
		return nil, err
	}
	if s.image, err = db.Prepare(`
		INSERT INTO image(url, kind, name, ref_count, in_stem, in_option, in_material, in_explanation)
		VALUES (?,?,?,1,?,?,?,?)
		ON CONFLICT(url) DO UPDATE SET
			ref_count = ref_count + 1,
			in_stem = MAX(in_stem, excluded.in_stem),
			in_option = MAX(in_option, excluded.in_option),
			in_material = MAX(in_material, excluded.in_material),
			in_explanation = MAX(in_explanation, excluded.in_explanation)`); err != nil {
		return nil, err
	}
	if s.warning, err = db.Prepare(`
		INSERT INTO parse_warning(source_path, line, kind, detail) VALUES (?,?,?,?)`); err != nil {
		return nil, err
	}
	return s, nil
}

// File 写入一篇笔记的解析结果，返回（新增题目实体数，题目出现数）。
func (g *Ingester) File(f *ingest.File) (newEntities, occurrences int, err error) {
	tx, err := g.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	// 事务内的语句包装只建一次：5.9 万题下每行都新建包装会明显拖慢写入。
	txPaper := tx.Stmt(g.stmts.paper)
	txMaterial := tx.Stmt(g.stmts.material)
	txQuestion := tx.Stmt(g.stmts.question)
	txFindQ := tx.Stmt(g.stmts.findQ)
	txOccurrence := tx.Stmt(g.stmts.occurrence)
	txOption := tx.Stmt(g.stmts.option)
	txImage := tx.Stmt(g.stmts.image)
	txWarning := tx.Stmt(g.stmts.warning)

	// paper
	res, err := txPaper.Exec(f.Paper.Name, f.Paper.Region, nullInt(f.Paper.Year),
		f.Paper.Module, f.Paper.ExamType, nullStr(f.Paper.Variant),
		f.Paper.DeclaredCount, len(f.Questions), f.Paper.SourcePath)
	if err != nil {
		return 0, 0, fmt.Errorf("写入 paper %s: %w", f.Paper.SourcePath, err)
	}
	paperID, _ := res.LastInsertId()

	// materials
	materialIDs := make(map[int]int64, len(f.Materials))
	for _, m := range f.Materials {
		r, err := txMaterial.Exec(paperID, m.Seq, m.Body, m.BodyHTML, b2i(ingest.HasFigure(m.Images)))
		if err != nil {
			return 0, 0, err
		}
		id, _ := r.LastInsertId()
		materialIDs[m.Seq] = id
	}

	// 材料里的图片也必须登记。资料分析的材料表格/图表只出现在材料正文里，
	// 早期版本漏登记它们，导致这批图没有 sha256、拿不到 OCR 结果，
	// 材料表格回填率因此是 0。
	for _, m := range f.Materials {
		if err := insertImages(txImage, m.Images); err != nil {
			return 0, 0, err
		}
	}

	for i := range f.Questions {
		q := &f.Questions[i]
		hash := ingest.ContentHash(q)

		r, err := txQuestion.Exec(hash, nullStr(q.QID), f.Paper.Module,
			q.Stem, q.StemHTML, nullStr(q.Answer), string(q.AnswerType), nullStr(answerMark(q)),
			q.Explanation, q.ExplanationHTML,
			b2i(q.HasFigure), b2i(q.HasMaterialFigure), b2i(q.OptionPlaceholder),
			len(q.Options), len(q.Images))
		if err != nil {
			return 0, 0, fmt.Errorf("写入题目 %s#%d: %w", f.Paper.SourcePath, q.Number, err)
		}
		// RowsAffected==1 表示新实体；0 表示命中了已有题（跨省重复），走查询取 id。
		if n, _ := r.RowsAffected(); n > 0 {
			newEntities++
			qid, _ := r.LastInsertId()
			if err := insertOptions(txOption, qid, q); err != nil {
				return 0, 0, err
			}
		}

		var questionID int64
		if err := txFindQ.QueryRow(hash).Scan(&questionID); err != nil {
			return 0, 0, fmt.Errorf("回查题目 %s: %w", hash, err)
		}

		var materialID any
		if q.MaterialSeq > 0 {
			materialID = materialIDs[q.MaterialSeq]
		}
		if _, err := txOccurrence.Exec(questionID, paperID, materialID,
			q.Number, nullStr(q.QID), nullStr(q.Tag), b2i(q.MaterialSeq > 0)); err != nil {
			return 0, 0, err
		}
		occurrences++

		if err := insertImages(txImage, q.Images); err != nil {
			return 0, 0, err
		}
	}

	for _, w := range f.Warnings {
		if _, err := txWarning.Exec(w.SourcePath, w.Line, w.Kind, w.Detail); err != nil {
			return 0, 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return newEntities, occurrences, err
	}
	return newEntities, occurrences, nil
}

func insertOptions(st *sql.Stmt, questionID int64, q *model.Question) error {
	for i, o := range q.Options {
		if _, err := st.Exec(questionID, i, o.Label, o.Content, o.ContentHTML, b2i(o.IsCorrect)); err != nil {
			return err
		}
	}
	return nil
}

func insertImages(st *sql.Stmt, refs []model.ImageRef) error {
	for _, im := range refs {
		var inStem, inOpt, inMat, inExp int
		switch im.In {
		case "stem":
			inStem = 1
		case "option":
			inOpt = 1
		case "material":
			inMat = 1
		case "explanation":
			inExp = 1
		}
		if _, err := st.Exec(im.URL, im.Kind, im.Name,
			inStem, inOpt, inMat, inExp); err != nil {
			return err
		}
	}
	return nil
}

// Finish 收尾 ingest run 并汇总计数。
func (g *Ingester) Finish(files, questions, occurrences, warnings int) error {
	_, err := g.db.Exec(`UPDATE ingest_run SET finished_at = ?, files = ?, questions = ?,
		occurrences = ?, warnings = ? WHERE id = ?`,
		time.Now().Format(time.RFC3339), files, questions, occurrences, warnings, g.runID)
	return err
}

// Close 释放预编译语句。
func (g *Ingester) Close() error {
	for _, s := range []*sql.Stmt{
		g.stmts.paper, g.stmts.material, g.stmts.question, g.stmts.findQ,
		g.stmts.occurrence, g.stmts.option, g.stmts.image, g.stmts.warning,
	} {
		if s != nil {
			s.Close()
		}
	}
	return nil
}

// answerMark 由 ✅ 标记推出答案，作为独立于 answer 字段的第二个信号。
func answerMark(q *model.Question) string {
	var sb strings.Builder
	for _, o := range q.Options {
		if o.IsCorrect {
			sb.WriteString(o.Label)
		}
	}
	return sb.String()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
