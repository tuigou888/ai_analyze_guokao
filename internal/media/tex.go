package media

import (
	"strings"
	"unicode"
)

// PP-FormulaNet 的输出有三种形态，回填时必须区别对待（分类依据是 502 张真实输出的人工核对）：
//
//	句子含行内公式   2014年...净面积 $=\cfrac{707000}{1447}>400$ 平方米  → 模型已给定界，原样插入
//	纯数学片段       \frac{9}{10} · R_{2} · (3409+3444+3364) · x-8      → 包成 $...$
//	纯中文文字片段    图 3 · 基期量 · 女研究生 > 男本科生                  → 原样插入，**不能**包 $
//
// 判据的关键是用 **CJK 当护栏**：
//
//   - 含中文、且没有数学标记  → 是中文文字（"基期量"、"视频增值服务 + 其他"），
//     包进 $ 会让 KaTeX 用数学模式渲染汉字，比不包更糟。
//   - 含中文、且有数学标记    → 是公式（`\frac{左上角数字}{右下角数字}=`，题干用中文占位），要包。
//   - 不含中文               → 按数学处理。实测这一侧不存在多词英文短语，
//     所以"无中文即数学"是安全的；而且把 `R_{2}` 漏包会让页面直接显示字面量 `R_{2}`，
//     是肉眼可见的缺陷，而把 `x-8` 当数学渲染只是字形微差——风险不对称，宁可包。
var mathMarkers = []string{
	`\`, "=", "≈", "≠", "≤", "≥", "×", "÷", "√", "∑", "∫", "∞", "±", "^", "_{", "^{",
}

// maxWrapRunes 超过这个长度且无中文的内容不包裹：多半是模型在噪声图上的跑飞输出，
// 不该被当成一个数学表达式塞进数学模式。
const maxWrapRunes = 80

// HasMathMarker 判断文本是否含数学标记。
func HasMathMarker(s string) bool {
	for _, m := range mathMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// HasMathDelimiter 判断文本是否已自带 $...$ 定界。
func HasMathDelimiter(s string) bool {
	return strings.Count(s, "$") >= 2
}

// NormalizeTeX 清理 OCR 原始输出：
//   - 去掉首尾空白、压掉换行（公式图都是单行渲染，换行只可能是识别噪声）
//   - 去掉模型习惯性加上的句末分号
//
// 刻意**不**剥掉包裹的 $...$：模型最清楚哪一段是数学，保住它的判断。
func NormalizeTeX(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	// 反复剥离，处理 "。;" 这类叠加
	for {
		t := strings.TrimRight(strings.TrimRight(s, " "), ";；")
		if t == s {
			break
		}
		s = t
	}
	s = strings.TrimSpace(s)
	return trimInsideDelimiters(s)
}

// trimInsideDelimiters 去掉 $...$ 定界符**紧内侧**的空白，保留定界符外的文字空格。
//
// 这不是洁癖：KaTeX 的 auto-render 遇到 `$ x $` 这类紧内侧带空格的定界
// 有可能不识别为公式，导致页面直接显示字面量。
//
// 必须真正区分开定界符与闭定界符——按"$ 后面的空格"和"空格后面跟着 $"来
// 盲替换会连带吃掉公式与正文之间该有的空格（`$a$ 和 $b$` 会变成 `$a$和$b$`）。
func trimInsideDelimiters(s string) string {
	if !HasMathDelimiter(s) {
		return s
	}
	r := []rune(s)
	out := make([]rune, 0, len(r))
	inMath := false
	for i := 0; i < len(r); i++ {
		if r[i] != '$' {
			out = append(out, r[i])
			continue
		}
		if !inMath {
			inMath = true
			out = append(out, '$')
			for i+1 < len(r) && (r[i+1] == ' ' || r[i+1] == '\t') {
				i++ // 跳过开定界符之后的空白
			}
			continue
		}
		inMath = false
		for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t') {
			out = out[:len(out)-1] // 去掉闭定界符之前的空白
		}
		out = append(out, '$')
	}
	return string(out)
}

// DisplayTeX 把 OCR 结果转成可直接嵌入解析文本的形态。
func DisplayTeX(raw string) string {
	s := NormalizeTeX(raw)
	if s == "" {
		return ""
	}
	if HasMathDelimiter(s) {
		return s // 模型已标好行内公式
	}
	if ContainsCJK(s) && !HasMathMarker(s) {
		return s // 中文文字片段
	}
	if len([]rune(s)) > maxWrapRunes {
		return s // 过长且不像公式，不冒包成数学模式的风险
	}
	return "$" + s + "$"
}

// ContainsCJK 用于统计与人工核查（中文与公式混排的图片占比）。
func ContainsCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
