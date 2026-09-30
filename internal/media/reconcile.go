package media

import (
	"context"
	"database/sql"
	"sort"
)

// refCounter 统计每个图片 url 被引用的情况。
type refCounter struct {
	inStem        int
	inOption      int
	inMaterial    int
	inExplanation int
}

// Reconcile 让 image 表与"数据里实际引用到的图片"一致：补齐缺失的行、刷新引用计数。
//
// 为什么需要它：image 表由 ingest 写入，而 ingest 的登记点是容易漏的——
// 资料分析的材料表格只出现在 material.body 里，早期版本只登记题干与选项的图片，
// 结果这 1,140 张图（843 题目图 + 297 公式图）没有行、没有 sha256、拿不到 OCR 结果，
// 材料表格回填率直接是 0。
//
// 所以这里不从"写入口"而是从"引用口"重建：扫遍所有可能出现占位符的文本列，
// 以引用为准补全。它是幂等的，且**不会碰已有的 OCR 结果**——只 INSERT 缺失行、
// UPDATE 引用计数与出现位置。
func Reconcile(ctx context.Context, db *sql.DB) (added int, refreshed int, err error) {
	refs := map[string]*refCounter{}
	get := func(url string) *refCounter {
		if c, ok := refs[url]; ok {
			return c
		}
		c := &refCounter{}
		refs[url] = c
		return c
	}

	scan := func(query string, mark func(*refCounter)) error {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err != nil {
				return err
			}
			for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
				mark(get(m[1]))
			}
		}
		return rows.Err()
	}

	ph := `%` + PlaceholderOpen + `%`
	if err := scan(`SELECT COALESCE(stem,'') FROM question WHERE stem LIKE '`+ph+`'`,
		func(c *refCounter) { c.inStem++ }); err != nil {
		return 0, 0, err
	}
	if err := scan(`SELECT COALESCE(explanation,'') FROM question WHERE explanation LIKE '`+ph+`'`,
		func(c *refCounter) { c.inExplanation++ }); err != nil {
		return 0, 0, err
	}
	if err := scan(`SELECT COALESCE(content,'') FROM option WHERE content LIKE '`+ph+`'`,
		func(c *refCounter) { c.inOption++ }); err != nil {
		return 0, 0, err
	}
	if err := scan(`SELECT COALESCE(body,'') FROM material WHERE body LIKE '`+ph+`'`,
		func(c *refCounter) { c.inMaterial++ }); err != nil {
		return 0, 0, err
	}

	urls := make([]string, 0, len(refs))
	for u := range refs {
		urls = append(urls, u)
	}
	sort.Strings(urls)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	ins, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO image(url, kind, name, ref_count, in_stem, in_option, in_material, in_explanation)
		 VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, 0, err
	}
	defer ins.Close()
	upd, err := tx.PrepareContext(ctx,
		`UPDATE image SET ref_count=?, in_stem=?, in_option=?, in_material=?, in_explanation=? WHERE url=?`)
	if err != nil {
		return 0, 0, err
	}
	defer upd.Close()

	for _, url := range urls {
		c := refs[url]
		total := c.inStem + c.inOption + c.inMaterial + c.inExplanation
		kind, name, ok := splitURL(url)
		if !ok {
			continue
		}
		res, err := ins.ExecContext(ctx, url, kind, name, total,
			flag(c.inStem), flag(c.inOption), flag(c.inMaterial), flag(c.inExplanation))
		if err != nil {
			return 0, 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
			continue
		}
		if _, err := upd.ExecContext(ctx, total,
			flag(c.inStem), flag(c.inOption), flag(c.inMaterial), flag(c.inExplanation), url); err != nil {
			return 0, 0, err
		}
		refreshed++
	}
	return added, refreshed, tx.Commit()
}

// splitURL 把 "公式图/xxx.png" 拆成 (kind, name)。
func splitURL(url string) (kind, name string, ok bool) {
	for i := 0; i < len(url); i++ {
		if url[i] == '/' {
			return url[:i], url[i+1:], i > 0 && i+1 < len(url)
		}
	}
	return "", "", false
}

// flag 把计数转成 0/1 标记。
func flag(n int) int {
	if n > 0 {
		return 1
	}
	return 0
}
