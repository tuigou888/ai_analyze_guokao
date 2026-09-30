package distill

import (
	"fmt"
	"strings"

	"ai_analyze_guokao/internal/model"
)

// PromptVersion 提示词版本。产出必须记录它，否则无从回溯"这批标注是哪版规则产的"。
const PromptVersion = "v1"

// QuestionInput 是喂给模型的题目材料。
type QuestionInput struct {
	Module      string
	Tag         string // 数据自带的粗标签，作为弱提示
	Stem        string // 已还原公式与图片文本的题干
	Options     []OptionInput
	Answer      string
	AnswerType  model.AnswerType
	Explanation string // 已还原公式的官方解析
	HasFigure   bool
}

// OptionInput 一个选项。
type OptionInput struct {
	Label   string
	Content string
	Correct bool
}

// BuildSystemPrompt 生成系统提示词。
//
// 三段顺序是有讲究的：
//  1. 铁律放最前——答案不可改、不得引入外部知识、只输出一个 JSON 对象
//  2. 字段定义
//  3. 封闭规范表 + 扩展口 + 归类边界
//
// 规范表由 Taxonomy 程序化拼出，不手写。手写枚举会把示例变成"偏好名单"，
// 模型遇到新考法会往示例上压平（参考文档 §5 坑 1）。
func BuildSystemPrompt(t *Taxonomy) string {
	var b strings.Builder

	b.WriteString(`你是公务员考试（行测）的资深教研员，任务是把一道真题拆解成可复用的考点标注。

【铁律，违反即失败】
1. 官方答案是唯一事实，**不得改写**。如果你的分析与官方解析矛盾，只能在 doubt 字段记录疑点，不得改 answer。
2. 不得引入题目与解析之外的知识。通用解题方法论（如"代入排除"）可用，具体事实（如"某年某法规定…"）不可编。
3. 只输出**一个 JSON 对象**，不要任何解释文字、不要 Markdown 代码围栏。
4. reasoning_chain 的每一步都必须引用题干或材料里的**具体数字或词句**，不允许写"根据题意可知"这类空话。
5. 三级考点必须是纯考点名，**不得把本题的具体做法塞进去**。本题做法写在 detail 字段。

`)

	b.WriteString("【字段定义】\n")
	fieldDocs := []struct{ name, desc string }{
		{"subject", "一级科目，必须取自下方规范表的模块名"},
		{"secondary", "二级题型，必须是该科目下规范表列出的名字，不得添加后缀"},
		{"tertiary", "三级考点，格式为「大类-考点名」，大类必须等于 secondary"},
		{"detail", "考点细节：本题在该考点下的具体特征（如「增长量为绝对量型」）。与 tertiary 分开，不要把做法写进 tertiary"},
		{"question_model", "问法模型：一句话说明题干怎么问、该用什么模型"},
		{"reasoning_chain", "推理链：3–8 步的字符串数组，每步引用题干中的具体数字或词句"},
		{"fastest_solution", "最快解法：考场上怎么最快做出来"},
		{"pitfalls", "易错点：1–3 条，将来做诱饵题的素材"},
		{"template", "母题抽象：填成「当题干出现…特征时，用…方法，验证…」"},
		{"key_features", "题干关键特征：3–6 个特征词，用于匹配同类题"},
		{"boundary", "适用边界：什么时候**不能**用这个方法，要写具体误判场景，不要写空话"},
		{"confusable", "易混考点：数组，每项 {\"考点\":\"…\",\"区分信号\":\"…\"}"},
		{"typical_ask", "典型提问：学习者会怎么口语化地问（用于检索命中），1–3 条"},
		{"conclusion", "按你的推理，本题应选的选项字母。**这只是你的判断，不是答案**；与官方答案不一致时不要改答案，差异会由质检统计出来"},
		{"doubt", "疑点：官方解析疑似有误、或材料缺失导致无法验证时写在这里；无疑点填空字符串"},
	}
	for _, f := range fieldDocs {
		fmt.Fprintf(&b, "- %s：%s\n", f.name, f.desc)
	}

	b.WriteString("\n【考点规范表】\n")
	b.WriteString("**只能使用下表内的二级题型。三级考点优先从下表选用；确属新考法时按扩展口规则新增。**\n\n")
	for _, m := range t.Modules {
		fmt.Fprintf(&b, "## %s\n", m.Subject)
		for _, s := range m.Secondary {
			fmt.Fprintf(&b, "### %s\n", s.Name)
			for _, ter := range s.Tertiary {
				fmt.Fprintf(&b, "- %s\n", ter)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("【归类边界】\n")
	for _, r := range t.BoundaryRules {
		fmt.Fprintf(&b, "- %s\n", r)
	}

	b.WriteString("\n【扩展口】\n")
	b.WriteString(t.ExtensionRule + "\n")

	b.WriteString(`
【输出格式】
只输出一个 JSON 对象，字段名与上面完全一致：
{
  "subject": "…", "secondary": "…", "tertiary": "…", "detail": "…",
  "question_model": "…", "reasoning_chain": ["…"], "fastest_solution": "…",
  "pitfalls": ["…"], "template": "…", "key_features": ["…"],
  "boundary": "…", "confusable": [{"考点": "…", "区分信号": "…"}],
  "typical_ask": ["…"], "conclusion": "A", "doubt": ""
}

注意：你在文字里引用 LaTeX 公式时，反斜杠在 JSON 字符串中必须写成两个字符（例如 \frac 要写成 \\frac）。
只写一个会被当成控制字符，\f 会变成换页符、\t 变成制表符，公式就损坏了。
`)
	return b.String()
}

// BuildUserPrompt 生成用户提示词，即题目本体。
//
// 材料里的图片占位符与 LaTeX 已在 L3 还原过（公式图 → $LaTeX$，题面图 → 表格文本）。
// 仍然残留的 ⟦IMG:…⟧ 是提取不到文字的纯图形，模型需要知道此处有图而不是漏了内容。
func BuildUserPrompt(q QuestionInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "科目（数据自带的模块）：%s\n", q.Module)
	if q.Tag != "" {
		fmt.Fprintf(&b, "数据自带的粗标签（仅供参考，可能是兜底标签「综合」）：%s\n", q.Tag)
	}
	b.WriteString("\n【题干】\n")
	b.WriteString(strings.TrimSpace(q.Stem))
	b.WriteString("\n\n【选项】\n")
	if len(q.Options) == 0 {
		b.WriteString("（本题无选项，为判断题；答案中的 A 表示「正确」、B 表示「错误」）\n")
	}
	for _, o := range q.Options {
		fmt.Fprintf(&b, "%s. %s\n", o.Label, strings.TrimSpace(o.Content))
	}
	fmt.Fprintf(&b, "\n【官方答案】%s\n", q.Answer)
	b.WriteString("\n【官方解析】\n")
	expl := strings.TrimSpace(q.Explanation)
	if expl == "" {
		expl = "（本题没有官方解析）"
	}
	b.WriteString(expl)
	b.WriteString("\n")

	if q.HasFigure {
		b.WriteString("\n注意：本题题面含图形，题干中的 ⟦IMG:题目图/…⟧ 是未能提取出文字的纯图形。\n" +
			"你无法看到图形本身，若解题必须依赖图形细节，请在 doubt 字段说明这一点，不要凭猜测编造图形内容。\n")
	}
	return b.String()
}
