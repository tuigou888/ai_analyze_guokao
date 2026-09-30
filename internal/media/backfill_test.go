package media

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"ai_analyze_guokao/internal/store"
)

// openTestDB 建一个内存库，用于验证回填的替换逻辑。
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertImage(t *testing.T, db *sql.DB, url, kind, tex, status string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO image(url, kind, name, ref_count, sha256, ocr_tex, ocr_status)
		 VALUES (?,?,?,1,?,?,?)`,
		url, kind, url, "hash-"+url, tex, status)
	if err != nil {
		t.Fatalf("插入 image 失败: %v", err)
	}
}

func insertQuestion(t *testing.T, db *sql.DB, hash, expl string) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO question(content_hash, module, stem, answer_type, explanation, option_count)
		 VALUES (?, '资料分析', '题干', 'single', ?, 4)`, hash, expl)
	if err != nil {
		t.Fatalf("插入 question 失败: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestBackfillReplacesFormulaPlaceholders(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	insertImage(t, db, "公式图/a.png", KindFormula, `\frac{9}{10}`, "ok")
	insertImage(t, db, "公式图/b.png", KindFormula, `基期量`, "ok")
	insertImage(t, db, "公式图/c.png", KindFormula, ``, "error") // 识别失败
	insertImage(t, db, "题目图/d.png", KindQuestion, ``, "")     // 题目图不参与公式回填

	id := insertQuestion(t, db, "h1",
		`见图 ⟦IMG:公式图/a.png⟧ 与 ⟦IMG:公式图/b.png⟧，另有 ⟦IMG:题目图/d.png⟧，`+
			`未识别的 ⟦IMG:公式图/c.png⟧ 保留。`)
	// 不含占位符的题不应被触碰
	plainID := insertQuestion(t, db, "h2", `纯文本解析，没有图片。`)

	st, err := Backfill(ctx, db)
	if err != nil {
		t.Fatalf("Backfill 失败: %v", err)
	}

	// 3 处公式占位符：2 处命中，1 处未识别保留
	if st.Replaced != 2 {
		t.Errorf("Replaced = %d，期望 2", st.Replaced)
	}
	if st.Unresolved != 1 {
		t.Errorf("Unresolved = %d，期望 1", st.Unresolved)
	}
	if st.StaleRemain != 1 {
		t.Errorf("StaleRemain = %d，期望 1", st.StaleRemain)
	}

	var got string
	var missing int
	if err := db.QueryRow(
		`SELECT explanation_with_formula, formula_missing FROM question WHERE id=?`, id).
		Scan(&got, &missing); err != nil {
		t.Fatalf("读取回填结果失败: %v", err)
	}

	// 数学片段要包 $，中文片段不包，题目图与未识别项保留占位符
	if !strings.Contains(got, `$\frac{9}{10}$`) {
		t.Errorf("公式未按数学形态回填: %q", got)
	}
	if !strings.Contains(got, `基期量`) || strings.Contains(got, `$基期量$`) {
		t.Errorf("中文片段不应被包成 $...$: %q", got)
	}
	if !strings.Contains(got, `⟦IMG:题目图/d.png⟧`) {
		// 解析里只还原公式图；夹带的题面图保留占位符供前端渲染 <img>。
		t.Errorf("解析里的题目图占位符应保留: %q", got)
	}
	if !strings.Contains(got, `⟦IMG:公式图/c.png⟧`) {
		t.Errorf("未识别的公式占位符应保留以便后续补: %q", got)
	}
	if missing != 1 {
		t.Errorf("formula_missing = %d，期望 1", missing)
	}

	// 无占位符的题不应被写入
	var plain sql.NullString
	if err := db.QueryRow(
		`SELECT explanation_with_formula FROM question WHERE id=?`, plainID).Scan(&plain); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if plain.Valid {
		t.Errorf("没有占位符的题不应被回填，得到 %q", plain.String)
	}
}

// 回填是可重放的：同一份数据跑两次结果一致，且第二次不产生无谓写入。
func TestBackfillIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	insertImage(t, db, "公式图/a.png", KindFormula, `R_{2}`, "ok")
	insertQuestion(t, db, "h1", `计算 ⟦IMG:公式图/a.png⟧ 得结果。`)

	first, err := Backfill(ctx, db)
	if err != nil {
		t.Fatalf("第一次 Backfill 失败: %v", err)
	}
	second, err := Backfill(ctx, db)
	if err != nil {
		t.Fatalf("第二次 Backfill 失败: %v", err)
	}
	if first.Replaced != second.Replaced {
		t.Errorf("两次回填替换数不一致: %d vs %d", first.Replaced, second.Replaced)
	}

	var got string
	if err := db.QueryRow(`SELECT explanation_with_formula FROM question WHERE content_hash='h1'`).
		Scan(&got); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got != `计算 $R_{2}$ 得结果。` {
		t.Errorf("回填结果 = %q，期望 %q", got, `计算 $R_{2}$ 得结果。`)
	}
}

// 原解析必须保持不动：回填是派生层，不是原地改写。
func TestBackfillKeepsOriginalExplanation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	orig := `原解析 ⟦IMG:公式图/a.png⟧ 结束。`
	insertImage(t, db, "公式图/a.png", KindFormula, `x+1`, "ok")
	insertQuestion(t, db, "h1", orig)

	if _, err := Backfill(ctx, db); err != nil {
		t.Fatalf("Backfill 失败: %v", err)
	}
	var got string
	if err := db.QueryRow(`SELECT explanation FROM question WHERE content_hash='h1'`).Scan(&got); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got != orig {
		t.Errorf("原解析被改写了:\n  得到 %q\n  期望 %q", got, orig)
	}
}

// 题面图与材料图走文本识别，回填进 stem_with_text / body_with_text。
// 这是资料分析找回表格数据的路径。
func TestBackfillResolvesFiguresInStemAndMaterial(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	insertImage(t, db, "题目图/table.png", KindQuestion, "年份 | 粮食\n2006 | 2371.1", "ok")
	insertImage(t, db, "题目图/figure.png", KindQuestion, "", "error") // 未识别
	insertImage(t, db, "公式图/f.png", KindFormula, `x+1`, "ok")

	// 题干与材料里的题面图应被还原。解析留空，避免与题干的计数混在一起。
	stemText := `根据下表 ⟦IMG:题目图/table.png⟧ 回答，另有未识别的 ⟦IMG:题目图/figure.png⟧。`
	stemID := insertQuestion(t, db, "h-stem", "")
	if _, err := db.Exec(`UPDATE question SET stem=? WHERE id=?`, stemText, stemID); err != nil {
		t.Fatal(err)
	}
	pres, err := db.Exec(
		`INSERT INTO paper(name, module, source_path) VALUES ('测试卷', '资料分析', 'test.md')`)
	if err != nil {
		t.Fatal(err)
	}
	paperID, _ := pres.LastInsertId()
	if _, err := db.Exec(
		`INSERT INTO material(paper_id, seq, body, body_html) VALUES (?, 1, ?, '')`,
		paperID, `材料 ⟦IMG:题目图/table.png⟧ 结束`); err != nil {
		t.Fatal(err)
	}

	st, err := Backfill(ctx, db)
	if err != nil {
		t.Fatalf("Backfill 失败: %v", err)
	}
	if st.StemsFilled != 1 {
		t.Errorf("StemsFilled = %d，期望 1", st.StemsFilled)
	}
	if st.MaterialsFilled != 1 {
		t.Errorf("MaterialsFilled = %d，期望 1", st.MaterialsFilled)
	}
	if st.FiguresReplaced != 2 {
		t.Errorf("FiguresReplaced = %d，期望 2", st.FiguresReplaced)
	}
	if st.FiguresMissing != 1 {
		t.Errorf("FiguresMissing = %d，期望 1", st.FiguresMissing)
	}

	var stem string
	var missing int
	if err := db.QueryRow(`SELECT stem_with_text, figure_missing FROM question WHERE id=?`, stemID).
		Scan(&stem, &missing); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stem, "2006 | 2371.1") {
		t.Errorf("题面表格未回填: %q", stem)
	}
	if !strings.Contains(stem, "⟦IMG:题目图/figure.png⟧") {
		t.Errorf("未识别的题面图应保留占位: %q", stem)
	}
	if strings.Contains(stem, "⟦IMG:题目图/table.png⟧") {
		t.Errorf("已识别的题面图占位符应被替换: %q", stem)
	}
	if missing != 1 {
		t.Errorf("figure_missing = %d，期望 1", missing)
	}

	var body string
	if err := db.QueryRow(`SELECT body_with_text FROM material WHERE seq=1`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "2006 | 2371.1") {
		t.Errorf("材料表格未回填: %q", body)
	}

	// 原列必须不动
	var origStem string
	if err := db.QueryRow(`SELECT stem FROM question WHERE id=?`, stemID).Scan(&origStem); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(origStem, "⟦IMG:题目图/table.png⟧") {
		t.Errorf("原始 stem 被改写了: %q", origStem)
	}
}
