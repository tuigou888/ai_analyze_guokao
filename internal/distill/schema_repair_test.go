package distill

import "testing"

// 50 题 MVP 实测命中 1/41：模型引用含 LaTeX 的原文时，偶尔把 `\\frac` 写成单反斜杠的 `\f`。
// JSON 里 `\f` 是合法转义（换页符），于是解析后 `\frac` 变成 「换页符 + rac」。
// 丢掉的正是「反斜杠 + f」两个字符，所以修复要把控制字符还原成两字符的转义。
func TestRepairEscapes(t *testing.T) {
	cases := map[string]string{
		"所求$\x0crac{1}{2}\\times\\frac{1}{2}$": `所求$\frac{1}{2}\times\frac{1}{2}$`,
		"退格\x08eta 常量":                         `退格\beta 常量`,
		"回车\x0dight 对齐":                        `回车\right 对齐`,
		`正常 \frac{1}{2}`:                       `正常 \frac{1}{2}`, // 已正确的不动
		"":                                     "",
	}
	for in, want := range cases {
		if got := repairEscapes(in); got != want {
			t.Errorf("repairEscapes(%q)\n  得到 %q\n  期望 %q", in, got, want)
		}
	}
	// 换行与制表符是合法空白，不能被改掉——这是刻意的取舍，见 repairEscapes 注释
	for _, keep := range []string{"第一行\n第二行", "列1\t列2"} {
		if got := repairEscapes(keep); got != keep {
			t.Errorf("不该改动合法空白: %q → %q", keep, got)
		}
	}
}

// 端到端复现：模型输出的 JSON 里写单反斜杠，解析后应被修复回正确的 LaTeX。
func TestParseLabelRepairsEscapesEndToEnd(t *testing.T) {
	// 注意这里的 \frac / \beta / \right 是**单个反斜杠**——正是模型犯的那个错。
	// 它们在 JSON 里是合法转义，json.Decode 之后变成控制字符。
	raw := `{"subject":"数量关系","secondary":"数学运算","tertiary":"数学运算-概率问题",
	"detail":"用到\frac 公式","question_model":"求\beta","reasoning_chain":["第1步用\right"],
	"fastest_solution":"代入","pitfalls":["漏\frac"],"template":"当…时",
	"key_features":["比例"],"boundary":"不适用","confusable":[{"考点":"X","区分信号":"\frac 不同"}],
	"typical_ask":["怎么算"],"conclusion":"A","doubt":""}`

	l, err := ParseLabel(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	want := map[string]string{
		"detail":               `用到\frac 公式`,
		"question_model":       `求\beta`,
		"reasoning_chain[0]":   `第1步用\right`,
		"pitfalls[0]":          `漏\frac`,
		"confusable[0].Signal": `\frac 不同`,
	}
	got := map[string]string{
		"detail":               l.Detail,
		"question_model":       l.QuestionModel,
		"reasoning_chain[0]":   l.ReasoningChain[0],
		"pitfalls[0]":          l.Pitfalls[0],
		"confusable[0].Signal": l.Confusable[0].Signal,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s 未修复: %q，期望 %q", k, got[k], w)
		}
	}
}
