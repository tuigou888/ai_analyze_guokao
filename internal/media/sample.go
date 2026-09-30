package media

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math/rand"
	"strings"
)

// Sample 一条供人工核对的抽样记录。
type Sample struct {
	URL     string
	Path    string
	Width   int
	Height  int
	Raw     string // OCR 原始输出
	Display string // 回填时实际插入的形态
	Kind    string // math_wrap / has_delim / cjk_text / empty
}

// Classify 给识别结果分类，用于抽样时按类型覆盖，避免只抽到某一种形态。
func Classify(raw string) string {
	s := NormalizeTeX(raw)
	switch {
	case s == "":
		return "empty"
	case HasMathDelimiter(s):
		return "has_delim"
	case ContainsCJK(s) && !HasMathMarker(s):
		return "cjk_text"
	default:
		return "math_wrap"
	}
}

// CollectSamples 分层随机抽样：按 Classify 的类别轮转，保证小类也能被抽到。
//
// 直接随机抽 30 张的话，math_wrap 占九成以上，几乎抽不到 cjk_text 与 empty——
// 而恰恰是这些小类最容易出问题（对应参考文档 §3.2「批中抽检会骗人」的教训）。
func CollectSamples(ctx context.Context, db *sql.DB, dataDir string, n int, seed int64) ([]Sample, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT url, COALESCE(ocr_tex,''), COALESCE(width,0), COALESCE(height,0)
		 FROM image WHERE kind=? AND ocr_status='ok'`, KindFormula)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	buckets := map[string][]Sample{}
	for rows.Next() {
		var s Sample
		if err := rows.Scan(&s.URL, &s.Raw, &s.Width, &s.Height); err != nil {
			return nil, err
		}
		s.Path = dataDir + "/" + imageDirName + "/" + s.URL
		s.Display = DisplayTeX(s.Raw)
		s.Kind = Classify(s.Raw)
		buckets[s.Kind] = append(buckets[s.Kind], s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewSource(seed))
	order := []string{"math_wrap", "cjk_text", "has_delim", "empty"}
	for _, k := range buckets {
		rng.Shuffle(len(k), func(i, j int) { k[i], k[j] = k[j], k[i] })
	}

	out := make([]Sample, 0, n)
	for round := 0; len(out) < n; round++ {
		progressed := false
		for _, k := range order {
			b := buckets[k]
			if round < len(b) {
				out = append(out, b[round])
				progressed = true
				if len(out) >= n {
					break
				}
			}
		}
		if !progressed {
			break
		}
	}
	return out, nil
}

// PrintSamples 输出抽样供人工核对：同时给出 OCR 原始输出与回填形态，
// 这样一次就能看出"识别错"和"包裹判据错"这两种不同的缺陷。
func PrintSamples(w io.Writer, samples []Sample) {
	counts := map[string]int{}
	for _, s := range samples {
		counts[s.Kind]++
	}
	var parts []string
	for _, k := range []string{"math_wrap", "cjk_text", "has_delim", "empty"} {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
		}
	}
	fmt.Fprintf(w, "抽样 %d 张（%s）\n", len(samples), strings.Join(parts, " "))
	fmt.Fprintln(w, strings.Repeat("─", 78))
	for i, s := range samples {
		fmt.Fprintf(w, "[%02d] %s  %dx%d  (%s)\n", i+1, s.URL, s.Width, s.Height, s.Kind)
		fmt.Fprintf(w, "     原始: %s\n", truncate(s.Raw, 150))
		if s.Display != s.Raw {
			fmt.Fprintf(w, "     回填: %s\n", truncate(s.Display, 150))
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
