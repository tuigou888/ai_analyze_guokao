package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// printQualityMetrics 输出 §6.6 的五个指标。
//
// 每个指标都带一个"坑"的提示——这些坑是参考文档实测踩出来的，写在输出里
// 是为了让读报告的人不会误用数字。
func printQualityMetrics(db *sql.DB, id string) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "\n=== 质检指标（批次 %s）===\n", id)

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM label WHERE run_id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		tw.Flush()
		fmt.Println("该批次没有产出")
		return nil
	}

	// 指标 1：字段完备率。按字段逐项统计，不只看平均——平均会掩盖某一题整片缺失。
	fmt.Fprint(tw, "\n-- 字段完备率 --\n")
	fields := []struct{ col, note string }{
		{"tertiary", "三级考点"},
		{"detail", "考点细节（与三级考点分离，1:1 率的关键）"},
		{"question_model", "问法模型"},
		{"fastest_solution", "最快解法"},
		{"template", "母题抽象"},
		{"boundary", "适用边界（最易漏）"},
	}
	for _, f := range fields {
		var filled int
		if err := db.QueryRow(`SELECT COUNT(*) FROM label WHERE run_id=? AND COALESCE(`+
			f.col+`,'')<>''`, id).Scan(&filled); err != nil {
			return err
		}
		fmt.Fprintf(tw, "%s\t%d/%d\t%.1f%%\n", f.note, filled, n, pctOf(filled, n))
	}
	for _, f := range []struct{ col, note string }{
		{"reasoning_chain", "推理链（非空数组）"},
		{"pitfalls", "易错点"},
		{"key_features", "题干关键特征"},
		{"typical_ask", "典型提问（检索命中率靠它）"},
	} {
		var filled int
		if err := db.QueryRow(`SELECT COUNT(*) FROM label WHERE run_id=? AND COALESCE(`+
			f.col+`,'[]') NOT IN ('','[]')`, id).Scan(&filled); err != nil {
			return err
		}
		fmt.Fprintf(tw, "%s\t%d/%d\t%.1f%%\n", f.note, filled, n, pctOf(filled, n))
	}

	// 指标 2：三级考点收敛度。
	//
	// **全局 1:1 率在跨模块抽样下是失真的**：分层抽样会把题摊到十几个二级题型上，
	// 其中不少题型只有 1 道题，而那 1 道必然"一题一考点"。于是全局值被这些
	// 结构性的 1:1 拉高，看起来像字段混用，其实是抽样设计的算术后果。
	// 真正有意义的是**题型内部**的收敛：同一个题型下 N 道题落到几个考点。
	fmt.Fprintf(tw, "\n-- 三级考点收敛度（按题型内部看，这才是有效口径）--\n")
	rows2, err := db.Query(`SELECT secondary, COUNT(*) n, COUNT(DISTINCT tertiary) d
		FROM label WHERE run_id=? GROUP BY 1 ORDER BY n DESC`, id)
	if err != nil {
		return err
	}
	defer rows2.Close()

	var (
		globalN, globalD int
		multiN, multiD   int
		multiTypes       int
		failedTypes      []string
	)
	for rows2.Next() {
		var sec string
		var cnt, d int
		if err := rows2.Scan(&sec, &cnt, &d); err != nil {
			return err
		}
		globalN += cnt
		globalD += d
		if cnt > 1 {
			multiN += cnt
			multiD += d
			multiTypes++
			// 题型内部一题一考点：可能真的跨了不同子类，也可能是字段混用，需要人工看
			if d == cnt {
				failedTypes = append(failedTypes, fmt.Sprintf("%s(%d 题 → %d 考点)", sec, cnt, d))
			}
		}
	}
	fmt.Fprintf(tw, "全局 1:1 率\t%.1f%%（含只有 1 题的题型，仅供参考）\n", pctOf(globalD, globalN))
	if multiN > 0 {
		fmt.Fprintf(tw, "题数>1 的题型内 1:1 率\t%.1f%%（%d 题 → %d 考点）\n", pctOf(multiD, multiN), multiN, multiD)
	}
	if len(failedTypes) > 0 {
		fmt.Fprintf(tw, "⚠ 题型内部仍一题一考点的：%s\n", strings.Join(failedTypes, "、"))
		fmt.Fprintf(tw, "  两种可能都要看：①该题型下题目确实分属不同子类（正常）；\n"+
			"  ②三级考点里混进了本题做法、或规范表该题型的分档不够（参考文档 §3.1）。\n"+
			"  用 gk distill concepts --run 看这些考点名即可区分。\n")
	}
	if multiN > 0 && pctOf(multiD, multiN) > 80 {
		fmt.Fprintf(tw, "⚠ 题型内 1:1 率仍 >80%%：优先怀疑规范表的分档过细，或字段混用\n")
	}

	// 指标 3：疑点率。安全阀动过的比例。突然为 0 是危险信号（安全阀没接上）。
	var doubt int
	if err := db.QueryRow(`SELECT COUNT(*) FROM label WHERE run_id=? AND COALESCE(doubt,'')<>''`,
		id).Scan(&doubt); err != nil {
		return err
	}
	fmt.Fprintf(tw, "\n-- 疑点率（安全阀）--\n")
	fmt.Fprintf(tw, "有疑点\t%d/%d\t%.1f%%\n", doubt, n, pctOf(doubt, n))
	if doubt == 0 {
		fmt.Fprintf(tw, "⚠ 疑点率为 0：通常是安全阀没接上，而不是解析全都可靠（参考文档 §3.9）\n")
	}

	// 指标 4：题型覆盖。单类目占比过高时结论不可外推。
	fmt.Fprintf(tw, "\n-- 题型覆盖 --\n")
	rows, err := db.Query(`SELECT secondary, COUNT(*) c FROM label WHERE run_id=? GROUP BY 1 ORDER BY c DESC`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	var top int
	for rows.Next() {
		var name string
		var c int
		if err := rows.Scan(&name, &c); err != nil {
			return err
		}
		if c > top {
			top = c
		}
		fmt.Fprintf(tw, "%s\t%d\t%.1f%%\n", name, c, pctOf(c, n))
	}
	if pctOf(top, n) >= 80 {
		fmt.Fprintf(tw, "⚠ 单个二级题型占比 ≥80%%：这批结论只对该题型成立，不可外推（参考文档 §3.2）\n")
	}
	fmt.Fprintf(tw, "（注意：分层抽样的配额是均衡的，按类目比例外推全库前要先看全库分布）\n")

	tw.Flush()
	return nil
}

// countTypes 只是为了让"题型数"这一行有值；真实题型数由调用方累计。
func countTypes(globalN, multiN int, failed []string) int {
	return len(failed)
}

func pctOf(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}
