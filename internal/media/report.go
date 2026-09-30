package media

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"text/tabwriter"
)

// Report 图片与 OCR 覆盖率。这是 P2 的验收凭据：
// 它回答"解析里还有多少公式对模型不可读"。
type Report struct {
	FormulaTotal   int // 公式图总数
	FormulaIndexed int // 已算 sha256
	FormulaOCR     int // 识别成功
	FormulaErr     int // 识别失败
	FormulaPending int // 待识别
	FormulaEmpty   int // 识别成功但结果为空（空白小图）
	FormulaCJK     int // 识别结果含中文

	QuestionTotal   int
	QWithFormula    int // 回填后解析含公式
	QFormulaMissing int // 仍有公式占位符残留
	QNoExplanation  int

	// 解析文本里公式占位符的替换情况
	PlaceholdersTotal int
	PlaceholdersLeft  int // explanation_with_formula 里残留的公式占位符

	QuestionImgs       int // 题目图总数
	QuestionImgIndexed int // 已算 sha256
	QuestionImgOCR     int // 文本识别成功
	QuestionImgErr     int // 文本识别失败

	// 表格数据还原（资料分析的可用性全靠这一项）
	MaterialsWithTable int // 材料正文里含图片占位符的条数（分母）
	MaterialsResolved  int // 已把题面图还原成文本的材料数
	StemsResolvedTotal int // 需要还原题面图的题数
	StemsResolved      int // 已把题面图还原成文本的题数
}

// TableCoverage 材料表格还原率，是资料分析能否被读懂的关键指标。
func (r *Report) TableCoverage() float64 {
	if r.MaterialsWithTable == 0 {
		return 100
	}
	return float64(r.MaterialsResolved) * 100 / float64(r.MaterialsWithTable)
}

// CollectReport 统计覆盖率。
func CollectReport(ctx context.Context, db *sql.DB) (*Report, error) {
	r := &Report{}
	one := func(dst *int, q string, args ...any) error {
		return db.QueryRowContext(ctx, q, args...).Scan(dst)
	}

	if err := one(&r.FormulaTotal, `SELECT COUNT(*) FROM image WHERE kind=?`, KindFormula); err != nil {
		return nil, err
	}
	if err := one(&r.FormulaIndexed, `SELECT COUNT(*) FROM image WHERE kind=? AND sha256 IS NOT NULL`, KindFormula); err != nil {
		return nil, err
	}
	if err := one(&r.FormulaOCR, `SELECT COUNT(*) FROM image WHERE kind=? AND ocr_status='ok'`, KindFormula); err != nil {
		return nil, err
	}
	if err := one(&r.FormulaErr, `SELECT COUNT(*) FROM image WHERE kind=? AND ocr_status='error'`, KindFormula); err != nil {
		return nil, err
	}
	if err := one(&r.FormulaPending,
		`SELECT COUNT(*) FROM image WHERE kind=? AND ocr_status IS NULL`, KindFormula); err != nil {
		return nil, err
	}
	// 空结果单独计数：空白小图上模型确实会输出空串，不算失败，但也不能当作还原成功。
	if err := one(&r.FormulaEmpty,
		`SELECT COUNT(*) FROM image WHERE kind=? AND ocr_status='ok' AND COALESCE(TRIM(ocr_tex),'')=''`,
		KindFormula); err != nil {
		return nil, err
	}
	if err := one(&r.FormulaCJK,
		`SELECT COUNT(*) FROM image WHERE kind=? AND ocr_status='ok' AND ocr_tex LIKE '%年%'`,
		KindFormula); err != nil {
		return nil, err
	}

	if err := one(&r.QuestionTotal, `SELECT COUNT(*) FROM question`); err != nil {
		return nil, err
	}
	if err := one(&r.QWithFormula,
		`SELECT COUNT(*) FROM question WHERE COALESCE(explanation_with_formula,'') <> ''`); err != nil {
		return nil, err
	}
	if err := one(&r.QFormulaMissing,
		`SELECT COUNT(*) FROM question WHERE formula_missing = 1`); err != nil {
		return nil, err
	}
	if err := one(&r.QNoExplanation,
		`SELECT COUNT(*) FROM question WHERE COALESCE(explanation,'') = ''`); err != nil {
		return nil, err
	}

	if err := one(&r.PlaceholdersTotal,
		`SELECT COUNT(*) FROM question WHERE explanation LIKE '%`+PlaceholderOpen+KindFormula+`%'`); err != nil {
		return nil, err
	}
	if err := one(&r.PlaceholdersLeft,
		`SELECT COUNT(*) FROM question WHERE COALESCE(explanation_with_formula,'') LIKE '%`+PlaceholderOpen+KindFormula+`%'`); err != nil {
		return nil, err
	}

	if err := one(&r.QuestionImgs, `SELECT COUNT(*) FROM image WHERE kind=?`, KindQuestion); err != nil {
		return nil, err
	}
	if err := one(&r.QuestionImgIndexed,
		`SELECT COUNT(*) FROM image WHERE kind=? AND sha256 IS NOT NULL`, KindQuestion); err != nil {
		return nil, err
	}
	if err := one(&r.QuestionImgOCR,
		`SELECT COUNT(*) FROM image WHERE kind=? AND ocr_status='ok'`, KindQuestion); err != nil {
		return nil, err
	}
	if err := one(&r.QuestionImgErr,
		`SELECT COUNT(*) FROM image WHERE kind=? AND ocr_status='error'`, KindQuestion); err != nil {
		return nil, err
	}

	// 材料与题面里提到题面图的，才是需要还原的范围；已写出派生列的算已还原。
	// 分母要算"含任意图片占位符的材料"：材料里既有题面图（表格/图表）也有公式图，
	// 只数题面图会让分子（已还原材料数）超过分母。
	if err := one(&r.MaterialsWithTable,
		`SELECT COUNT(*) FROM material WHERE body LIKE '%`+PlaceholderOpen+`%'`); err != nil {
		return nil, err
	}
	if err := one(&r.MaterialsResolved,
		`SELECT COUNT(*) FROM material WHERE body_with_text IS NOT NULL`); err != nil {
		return nil, err
	}
	if err := one(&r.StemsResolvedTotal,
		`SELECT COUNT(*) FROM question WHERE stem LIKE '%`+PlaceholderOpen+`%'`); err != nil {
		return nil, err
	}
	if err := one(&r.StemsResolved,
		`SELECT COUNT(*) FROM question WHERE stem_with_text IS NOT NULL`); err != nil {
		return nil, err
	}
	return r, nil
}

// Print 输出报告。
func (r *Report) Print(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "=== 公式图 OCR ===")
	fmt.Fprintf(tw, "公式图总数\t%d\n", r.FormulaTotal)
	fmt.Fprintf(tw, "已索引（有 sha256）\t%d\n", r.FormulaIndexed)
	fmt.Fprintf(tw, "识别成功\t%d\n", r.FormulaOCR)
	fmt.Fprintf(tw, "  其中结果为空\t%d\n", r.FormulaEmpty)
	fmt.Fprintf(tw, "识别失败\t%d\n", r.FormulaErr)
	fmt.Fprintf(tw, "待识别\t%d\n", r.FormulaPending)

	fmt.Fprintln(tw, "\n=== 解析回填 ===")
	fmt.Fprintf(tw, "题目总数\t%d\n", r.QuestionTotal)
	fmt.Fprintf(tw, "解析含公式的题\t%d\n", r.QWithFormula)
	fmt.Fprintf(tw, "仍有公式占位符残留的题\t%d\n", r.QFormulaMissing)
	fmt.Fprintf(tw, "原解析里提到公式图的题\t%d\n", r.PlaceholdersTotal)
	fmt.Fprintf(tw, "回填后仍残留公式占位符的题\t%d\n", r.PlaceholdersLeft)
	fmt.Fprintf(tw, "无解析的题\t%d\n", r.QNoExplanation)

	fmt.Fprintln(tw, "\n=== 题目图文本识别 ===")
	fmt.Fprintf(tw, "题目图总数\t%d\n", r.QuestionImgs)
	fmt.Fprintf(tw, "已索引\t%d\n", r.QuestionImgIndexed)
	fmt.Fprintf(tw, "文本识别成功\t%d\n", r.QuestionImgOCR)
	fmt.Fprintf(tw, "文本识别失败\t%d\n", r.QuestionImgErr)

	fmt.Fprintln(tw, "\n=== 材料/题面表格还原（资料分析的可用性） ===")
	fmt.Fprintf(tw, "含图材料条数\t%d\n", r.MaterialsWithTable)
	fmt.Fprintf(tw, "已还原材料数\t%d\n", r.MaterialsResolved)
	fmt.Fprintf(tw, "含图题目数\t%d\n", r.StemsResolvedTotal)
	fmt.Fprintf(tw, "已还原题面数\t%d\n", r.StemsResolved)
	tw.Flush()

	// 说明一句，避免"未还原"被误读成失败
	fmt.Fprintf(w, "\n注：仍有占位符的图多半是纯图形（图形推理的图没有文字），\n"+
		"    提取不到文字属正常结果；这类图保留占位符供前端渲染 <img>，\n"+
		"    同时让蒸馏模型知道此处有图。\n")
	if r.PlaceholdersTotal > 0 {
		left := float64(r.PlaceholdersLeft) * 100 / float64(r.PlaceholdersTotal)
		fmt.Fprintf(w, "\n公式占位符残留率 %.1f%%（P2 验收线 <5%%）\n", left)
	}
	fmt.Fprintf(w, "材料表格还原率 %.1f%%\n", r.TableCoverage())
}
