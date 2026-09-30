package distill

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Label 是蒸馏产出的 12 字段 + 疑点 + conclusion。
//
// 字段名与 label 表的列一一对应（见 internal/store/schema.go v5）。
type Label struct {
	Subject         string     `json:"subject"`
	Secondary       string     `json:"secondary"`
	Tertiary        string     `json:"tertiary"`
	Detail          string     `json:"detail"`
	QuestionModel   string     `json:"question_model"`
	ReasoningChain  []string   `json:"reasoning_chain"`
	FastestSolution string     `json:"fastest_solution"`
	Pitfalls        []string   `json:"pitfalls"`
	Template        string     `json:"template"`
	KeyFeatures     []string   `json:"key_features"`
	Boundary        string     `json:"boundary"`
	Confusable      []Confusal `json:"confusable"`
	TypicalAsk      []string   `json:"typical_ask"`
	Conclusion      string     `json:"conclusion"`
	Doubt           string     `json:"doubt"`
}

// Confusal 一条易混考点。
type Confusal struct {
	Concept string `json:"考点"`
	Signal  string `json:"区分信号"`
}

// Problem 一条质检问题。
type Problem struct {
	Field   string
	Message string
	// Fatal 为真表示必须重试；为假只是警告（记入产出但不算失败）。
	Fatal bool
}

// Validation 校验结果。
type Validation struct {
	Problems      []Problem
	NovelConcepts []string // 规范表外新增的三级考点，需回流人工审查
}

// FatalCount 返回必须重试的问题数。
func (v *Validation) FatalCount() int {
	n := 0
	for _, p := range v.Problems {
		if p.Fatal {
			n++
		}
	}
	return n
}

// FatalMessages 返回致命问题的描述。
func (v *Validation) FatalMessages() []string {
	var out []string
	for _, p := range v.Problems {
		if p.Fatal {
			out = append(out, p.Field+": "+p.Message)
		}
	}
	return out
}

// repairEscapes 修复被 JSON 转义吃掉的反斜杠。
//
// 现象（50 题 MVP 实测命中 1/41）：模型引用含 LaTeX 的原文时，偶尔把 `\frac` 写成
// 单反斜杠的 `\f`，于是 JSON 解析器把它当成**换页符**，`\frac` 就变成了 control+`rac`。
// 同一段里其它命令往往又是正确转义的——模型自己不一致。
//
// 修复的关键是**还原两个字符**（反斜杠 + 字母），而不是只还一个反斜杠：
// 模型想写的是 `\frac`（6 个字符），JSON 解析后成了 FF+`rac`（4 个字符），
// 丢掉的正是「反斜杠 + f」这两个字符。所以 U+000C 要还原成 `\f` 而不是 `\`。
//
// 刻意不动 U+0009(制表) 与 U+000A(换行)：它们在正文里是**合法空白**，
// 模型可能真的用来排版，一律当转义还原会误伤换行。
// 代价是 `\times`/`\neq`/`\not` 若被吃掉就修不回来——已知取舍，
// 因为误伤换行的代价更大，而且这两类命令比 \frac/\beta/\right 出现得少。
func repairEscapes(s string) string {
	if s == "" {
		return s
	}
	if !strings.ContainsAny(s, "\x0c\x08\x0d") {
		return s
	}
	return replaceControlEscapes(s)
}

// replaceControlEscapes 把三类控制字符还原成它们原本的两字符转义。
func replaceControlEscapes(s string) string {
	r := strings.NewReplacer(
		"\x0c", `\f`,
		"\x08", `\b`,
		"\x0d", `\r`,
	)
	return r.Replace(s)
}

// repairLabel 对全部文本字段做一次转义修复。
func repairLabel(l *Label) {
	l.Tertiary = repairEscapes(l.Tertiary)
	l.Detail = repairEscapes(l.Detail)
	l.QuestionModel = repairEscapes(l.QuestionModel)
	l.FastestSolution = repairEscapes(l.FastestSolution)
	l.Template = repairEscapes(l.Template)
	l.Boundary = repairEscapes(l.Boundary)
	l.Doubt = repairEscapes(l.Doubt)
	for i := range l.ReasoningChain {
		l.ReasoningChain[i] = repairEscapes(l.ReasoningChain[i])
	}
	for i := range l.Pitfalls {
		l.Pitfalls[i] = repairEscapes(l.Pitfalls[i])
	}
	for i := range l.KeyFeatures {
		l.KeyFeatures[i] = repairEscapes(l.KeyFeatures[i])
	}
	for i := range l.TypicalAsk {
		l.TypicalAsk[i] = repairEscapes(l.TypicalAsk[i])
	}
	for i := range l.Confusable {
		l.Confusable[i].Concept = repairEscapes(l.Confusable[i].Concept)
		l.Confusable[i].Signal = repairEscapes(l.Confusable[i].Signal)
	}
}

// ParseLabel 解析模型输出。
//
// 网关常在 JSON 外面套 Markdown 围栏或加前后缀，所以先剥壳再解析——
// 但不能容忍"只有一个 JSON 对象"以外的结构，否则下游字段会静默为空。
func ParseLabel(raw string) (*Label, error) {
	s := stripFence(strings.TrimSpace(raw))
	if s == "" {
		return nil, fmt.Errorf("模型返回空内容")
	}
	// 兜底：截取第一个 { 到最后一个 }，应对"以下是结果：{...}"这类前缀。
	if i := strings.Index(s, "{"); i > 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}

	var l Label
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w（原文前 160 字：%s）", err, firstRunes(s, 160))
	}
	repairLabel(&l)
	return &l, nil
}

func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```JSON")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// Validate 按 §6.6 的判据校验产出。
//
// 致命项（必须重试）：一级科目不在规范表、二级题型不在枚举、「大类-」前缀缺失或与二级题型不符、
// 推理链为空、字段整片缺失。
// 警告项（记账不重试）：单条推理链过短、易错点为空、疑点非空（疑点是安全阀，不是错误）。
func (v *Validation) Validate(l *Label, t *Taxonomy, module string, answer string) {
	add := func(field, msg string, fatal bool) {
		v.Problems = append(v.Problems, Problem{Field: field, Message: msg, Fatal: fatal})
	}

	// 一级科目：必须与题目所属模块一致。模型把判断推理的题标成言语理解会让整个聚合层错位。
	if l.Subject == "" {
		add("subject", "为空", true)
	} else if t.Find(l.Subject) == nil {
		add("subject", fmt.Sprintf("%q 不在规范表的模块列表里", l.Subject), true)
	} else if module != "" && l.Subject != module {
		add("subject", fmt.Sprintf("为 %q，与题目所属模块 %q 不一致", l.Subject, module), true)
	}

	// 二级题型：封闭枚举，且不得添加后缀（参考文档 §5 坑 2 的实测）。
	names := t.SecondaryNames(l.Subject)
	if l.Secondary == "" {
		add("secondary", "为空", true)
	} else if !contains(names, l.Secondary) {
		add("secondary", fmt.Sprintf("%q 不在 %s 的二级题型枚举里（可选：%s）",
			l.Secondary, l.Subject, strings.Join(names, "、")), true)
	}

	// 三级考点：前缀 + 归属校验；规范表外的按扩展口接受但要回流审查。
	if l.Secondary != "" && t.Find(l.Subject) != nil {
		ok, novel, reason := t.ValidateTertiary(l.Subject, l.Secondary, l.Tertiary)
		if !ok {
			add("tertiary", reason, true)
		} else if novel {
			v.NovelConcepts = append(v.NovelConcepts, l.Tertiary)
		}
	}

	// 字段完备性：整片缺失是最该拦住的形态（平均完备率会掩盖单题整片空）。
	if strings.TrimSpace(l.Detail) == "" {
		add("detail", "为空（考点细节必须与三级考点分离填写）", true)
	}
	if strings.TrimSpace(l.QuestionModel) == "" {
		add("question_model", "为空", true)
	}
	if strings.TrimSpace(l.FastestSolution) == "" {
		add("fastest_solution", "为空", true)
	}
	if strings.TrimSpace(l.Template) == "" {
		add("template", "为空", true)
	}
	if strings.TrimSpace(l.Boundary) == "" {
		// 适用边界是最容易漏的一段，但它只影响质量不影响可用性，故不致命。
		add("boundary", "为空（这是最容易漏的一段，建议重试一次）", false)
	}
	if len(l.ReasoningChain) == 0 {
		add("reasoning_chain", "为空", true)
	} else if len(l.ReasoningChain) < 3 {
		add("reasoning_chain", fmt.Sprintf("仅 %d 步，期望 3–8 步", len(l.ReasoningChain)), false)
	}
	for i, step := range l.ReasoningChain {
		if strings.TrimSpace(step) == "" {
			add("reasoning_chain", fmt.Sprintf("第 %d 步为空", i+1), true)
		}
	}
	if len(l.KeyFeatures) == 0 {
		add("key_features", "为空", true)
	}
	if len(l.Pitfalls) == 0 {
		add("pitfalls", "为空", false)
	}
	if len(l.TypicalAsk) == 0 {
		add("typical_ask", "为空（缺这个字段检索命中率会明显下降）", false)
	}

	// conclusion 与官方答案的差异**不是错误**：它是答案一致率指标的原始数据。
	// 官方答案是唯一事实，模型无从改写（产出里根本没有答案字段）。
	if l.Conclusion != "" && answer != "" && !answerMatches(l.Conclusion, answer) {
		add("conclusion", fmt.Sprintf("模型判断 %q 与官方答案 %q 不一致（已记录，答案以官方为准）",
			l.Conclusion, answer), false)
	}
}

// answerMatches 判断模型给的结论是否等于官方答案。
// 容错处理几种常见写法："A"、"选A"、"A项"、"A、B"。
func answerMatches(conclusion, answer string) bool {
	c := strings.ToUpper(strings.TrimSpace(conclusion))
	a := strings.ToUpper(strings.TrimSpace(answer))
	if c == a {
		return true
	}
	// 从结论里抽出字母集合，与官方答案的字母集合比较
	letters := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r >= 'A' && r <= 'H' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	cl, al := letters(c), letters(a)
	if cl == "" || al == "" {
		return false
	}
	// 多选答案需要集合相等，单选需要包含关系
	if len(al) > 1 {
		return sameRuneSet(cl, al)
	}
	return strings.Contains(cl, al)
}

func sameRuneSet(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[rune]int{}
	for _, r := range a {
		seen[r]++
	}
	for _, r := range b {
		seen[r]--
		if seen[r] < 0 {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
