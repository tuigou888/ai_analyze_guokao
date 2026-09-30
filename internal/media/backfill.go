package media

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
)

// BackfillStats 回填统计。
type BackfillStats struct {
	// 解析里的公式图（影响蒸馏质量与前端渲染的核心指标）
	Questions   int // 扫描的题目数
	Replaced    int // 替换成功的公式占位符数
	Unresolved  int // 找不到 OCR 结果、保留占位的公式数
	WithFormula int // 回填后解析含公式的题数
	StaleRemain int // 回填后仍有公式占位符残留的题数

	// 题面与材料里的题目图（走文本识别，为资料分析找回表格数据）
	StemsFilled     int // 写出 stem_with_text 的题数
	MaterialsFilled int // 写出 body_with_text 的材料数
	FiguresReplaced int // 替换成功的题面图占位符数
	FiguresNoText   int // 识别成功但图里确实没有文字（纯图形，属正常结果）
	FiguresMissing  int // 尚未识别或识别失败的题面图数（真正的缺口）
}

var placeholderRe = regexp.MustCompile(PlaceholderOpen + `([^` + PlaceholderClose + `]+)` + PlaceholderClose)

// Backfill 把占位符替换成可读文本，写入三处派生列：
//
//	question.explanation_with_formula  ← 公式图 → LaTeX（蒸馏与前端 KaTeX 都用它）
//	question.stem_with_text            ← 题面图 → 识别文本
//	material.body_with_text            ← 材料图 → 识别文本（资料分析的表格数据在这里）
//
// 这是**可重放的派生层**：原始 stem / explanation / material.body 全都不动，
// OCR 结果也不动，只有这三列是按当前规则算出来的。展示规则调错了重跑本步即可
// （秒级），不需要重跑 65 分钟的 OCR。
//
// 三处都执行同一条规则：所有能还原的占位符都替换，只有提取不到文字的图保留占位符。
// 不给某一类图开特例——早期版本只让材料还原题面图，结果材料正文里的公式图一直残留，
// 而资料分析的数据恰恰全在材料里。
func Backfill(ctx context.Context, db *sql.DB) (*BackfillStats, error) {
	ocr, err := loadOCR(ctx, db)
	if err != nil {
		return nil, err
	}
	st := &BackfillStats{}
	for _, task := range []func(context.Context, *sql.DB, *ocrStore, *BackfillStats) error{
		backfillExplanations, backfillStems, backfillMaterials,
	} {
		if err := task(ctx, db, ocr, st); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// ocrStore 已识别的图片。
//
// text 与 noText 必须分开：题目图里大量是纯图形（图形推理的图没有文字），
// 识别成功但结果为空是**正常结果**，不是"找不到 OCR 结果"。
// 混为一谈会让报告把几千张正常图形谎报成缺口。
type ocrStore struct {
	text   map[string]string // url → 可嵌入文本
	noText map[string]bool   // url → 识别成功但确实没有文字
}

// loadOCR 取出所有已成功识别的图片。
func loadOCR(ctx context.Context, db *sql.DB) (*ocrStore, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT url, COALESCE(ocr_engine,''), COALESCE(ocr_tex,'') FROM image WHERE ocr_status='ok'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	st := &ocrStore{text: map[string]string{}, noText: map[string]bool{}}
	for rows.Next() {
		var url, engine, raw string
		if err := rows.Scan(&url, &engine, &raw); err != nil {
			return nil, err
		}
		// 公式图要过展示层判据（决定要不要包 $）；题目图直接落已重建好表格行的文本。
		text := raw
		if engine != EngineText {
			text = DisplayTeX(raw)
		}
		if strings.TrimSpace(text) == "" {
			st.noText[url] = true
			continue
		}
		st.text[url] = text
	}
	return st, rows.Err()
}

// resolveCounts 一次替换的计数。
type resolveCounts struct {
	formula        int // 公式图 → LaTeX
	figure         int // 题面图 → 文本
	noText         int // 图里确实没有文字（纯图形，属正常结果）
	formulaMissing int // 公式图未还原（真正的缺口）
	figureMissing  int // 题面图未还原（真正的缺口）
}

// resolve 把两种占位符都尽量替换成文本。
//
// 统一规则：**派生列 = 原文里所有能还原的图片占位符都被替换，只有确实没有可提取
// 文字的图才保留占位符**（前端据此渲染图片，LLM 据此知道此处有图）。
// 不给某一类图开特例——早期版本只让 material 解析题面图，结果材料正文里的公式图
// 一直残留占位符，而资料分析的数据恰恰全在材料里。
func (st *ocrStore) resolve(s string) (string, resolveCounts) {
	var c resolveCounts
	out := placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		url := placeholderRe.FindStringSubmatch(m)[1]
		isFormula := strings.HasPrefix(url, KindFormula+"/")
		if t, ok := st.text[url]; ok {
			if isFormula {
				c.formula++
			} else {
				c.figure++
			}
			return t
		}
		if st.noText[url] {
			c.noText++
			return m
		}
		if isFormula {
			c.formulaMissing++
		} else {
			c.figureMissing++
		}
		return m // 尚未识别或识别失败，保留占位符以便后续补
	})
	return out, c
}

func backfillExplanations(ctx context.Context, db *sql.DB, ocr *ocrStore, st *BackfillStats) error {
	return eachRow(ctx, db,
		`SELECT id, COALESCE(explanation,'') FROM question WHERE explanation LIKE '%`+PlaceholderOpen+`%'`,
		func(id int64, text string) (bool, []any) {
			st.Questions++
			out, c := ocr.resolve(text)
			st.Replaced += c.formula
			st.FiguresReplaced += c.figure
			st.FiguresNoText += c.noText
			st.Unresolved += c.formulaMissing
			st.FiguresMissing += c.figureMissing
			if c.formula > 0 {
				st.WithFormula++
			}
			miss := 0
			if c.formulaMissing > 0 {
				miss = 1
				st.StaleRemain++
			}
			if out == text && miss == 0 {
				return false, nil
			}
			return true, []any{out, miss, id}
		},
		`UPDATE question SET explanation_with_formula=?, formula_missing=? WHERE id=?`)
}

func backfillStems(ctx context.Context, db *sql.DB, ocr *ocrStore, st *BackfillStats) error {
	return eachRow(ctx, db,
		`SELECT id, COALESCE(stem,'') FROM question WHERE stem LIKE '%`+PlaceholderOpen+`%'`,
		func(id int64, text string) (bool, []any) {
			out, c := ocr.resolve(text)
			st.Replaced += c.formula
			st.FiguresReplaced += c.figure
			st.FiguresNoText += c.noText
			st.FiguresMissing += c.figureMissing + c.formulaMissing
			if out == text {
				return false, nil
			}
			st.StemsFilled++
			return true, []any{out, c.figureMissing + c.formulaMissing, id}
		},
		`UPDATE question SET stem_with_text=?, figure_missing=? WHERE id=?`)
}

func backfillMaterials(ctx context.Context, db *sql.DB, ocr *ocrStore, st *BackfillStats) error {
	return eachRow(ctx, db,
		`SELECT id, COALESCE(body,'') FROM material WHERE body LIKE '%`+PlaceholderOpen+`%'`,
		func(id int64, text string) (bool, []any) {
			out, c := ocr.resolve(text)
			st.Replaced += c.formula
			st.FiguresReplaced += c.figure
			st.FiguresNoText += c.noText
			st.FiguresMissing += c.figureMissing + c.formulaMissing
			if out == text {
				return false, nil
			}
			st.MaterialsFilled++
			return true, []any{out, id}
		},
		`UPDATE material SET body_with_text=? WHERE id=?`)
}

// eachRow 扫描 (id, text) 行，交给 fn 决定是否更新及其参数，最后在一个事务里批量写回。
func eachRow(ctx context.Context, db *sql.DB, query string,
	fn func(id int64, text string) (bool, []any), update string) error {

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	var items [][]any
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			rows.Close()
			return err
		}
		if need, args := fn(id, text); need {
			items = append(items, args)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, update)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, args := range items {
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
