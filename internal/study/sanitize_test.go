package study

import (
	"strings"
	"testing"
)

func TestSanitizeText(t *testing.T) {
	ok := map[string]string{
		"爱丽丝":          "爱丽丝",
		"  alice  ":    "alice",
		"a\tb":         "a b",
		"line1\nline2": "line1\nline2",
	}
	for in, want := range ok {
		if got, valid := sanitizeText(in); !valid || got != want {
			t.Fatalf("%q → %q (%v), want %q", in, got, valid, want)
		}
	}
	// 控制字符、零宽字符、尖括号与反引号一律拒绝（安全审计 L-2）。
	for _, in := range []string{
		"<script>alert(1)</script>",
		"nick\x00name",
		"zero​width",
		"rtl‮override",
		"back`tick",
		"a > b",
	} {
		if _, valid := sanitizeText(in); valid {
			t.Fatalf("%q accepted", in)
		}
	}
}

func TestSearchHighlightEscapesInput(t *testing.T) {
	h := searchHighlight("<img src=x onerror=alert(1)>", "keyword")
	got := h("增长率计算与增长量比较")
	if len(got) != 0 {
		t.Fatalf("non-matching query produced highlight: %v", got)
	}
	h = searchHighlight("增长", "keyword")
	got = h("增长率计算与增长量比较")
	if len(got) != 1 || !strings.Contains(got[0], "<mark>增长</mark>") {
		t.Fatalf("highlight = %v", got)
	}
	if strings.Contains(got[0], "增长\"") {
		t.Fatalf("raw query leaked: %v", got)
	}
	// 题干里的 HTML 必须被转义，即便被高亮包裹。
	h = searchHighlight("x", "keyword")
	got = h(`<script>x</script>`)
	if len(got) != 1 || strings.Contains(got[0], "<script>") {
		t.Fatalf("stem not escaped: %v", got)
	}
	if !strings.Contains(got[0], "&lt;script&gt;") {
		t.Fatalf("escape missing: %v", got)
	}
	// concept 模式不产出高亮片段。
	if got := searchHighlight("增长", "concept")("增长率"); len(got) != 0 {
		t.Fatalf("concept mode produced highlight: %v", got)
	}
}
