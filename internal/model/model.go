// Package model 定义项目的领域模型，不依赖任何外部包。
package model

// AnswerType 题目作答形态。judge 需要特别处理：数据里有 111 道判断题用
// `- （选项）[]` 占位，答案是 A/B 而非选项字母（A=正确、B=错误），
// 前端必须以「正确/错误」按钮渲染，不能当普通选择题。
type AnswerType string

const (
	AnswerSingle AnswerType = "single" // 单选
	AnswerMulti  AnswerType = "multi"  // 多选
	AnswerJudge  AnswerType = "judge"  // 判断（无选项占位）
	AnswerOther  AnswerType = "other"  // 其他（如答案缺失）
)

// Paper 一套卷的一个模块。数据集里「一套卷」按模块拆成多篇笔记，
// 因此这里的一条记录是「卷 × 模块」，而非整套卷。
type Paper struct {
	ID            int64
	Name          string // 试卷名
	Region        string // 地区：国考/浙江/选调/...
	Year          int
	Module        string // 模块
	DeclaredCount int    // frontmatter 声明的题数
	SourcePath    string // 相对 data 根目录的路径

	// ExamType 考试类型：国考/省考/选调/市考/其他。
	// 全量数据里国考只占 8%，这一列是做任何统计报表的前置过滤维度。
	ExamType string
	// Variant 卷型：副省级/地市级/行政执法/A类/B类/...
	Variant string
}

// Material 资料分析/言语篇章等「一材多题」的材料段。
type Material struct {
	Seq      int // 文件内的材料序号
	BodyHTML string
	Body     string // 纯文本（图片转占位符）
	Images   []ImageRef
}

// Question 一道题（文件内的原始形态，尚未去重）。
type Question struct {
	Number          int
	QID             string // 原始题库 id
	Tag             string // 数据自带的粗标签（如 增长、片段阅读、综合）
	MaterialSeq     int    // 0 = 独立题，>0 = 挂在材料下
	LineNo          int    // 题干行号，用于报错定位
	StemHTML        string
	Stem            string // 纯文本（图片转占位符）
	Options         []Option
	Answer          string // 原始答案文本（A/B/AB/（缺））
	AnswerType      AnswerType
	ExplanationHTML string
	Explanation     string
	Images          []ImageRef

	// OptionPlaceholder 源文件用 `- （选项）[]` 占位表示判断题（实测 111 道）。
	// 这类题前端必须以「正确/错误」渲染，答案 A/B 分别对应 正确/错误。
	OptionPlaceholder bool

	// HasFigure 题面含题目图（不含解析里的公式图）。实测仅 9.1% 的题命中，
	// 集中在判断推理与数量关系；蒸馏时这批题需要走多模态。
	HasFigure bool
	// HasMaterialFigure 所属材料含图表。资料分析的材料图表不在题内，
	// 但决定了这道题能不能只靠文本读懂。
	HasMaterialFigure bool
}

// Option 一个选项。
type Option struct {
	Label       string // A/B/...（判断题为空）
	ContentHTML string
	Content     string
	IsCorrect   bool // 来自行尾的 ✅ 标记
}

// ImageRef 一处图片引用。
type ImageRef struct {
	Kind string // 题目图 / 公式图
	Name string // 文件名
	URL  string // 归一化后的相对路径：题目图/x.png
	In   string // 出现位置：stem / option / material / explanation
}

// Occurrence 题目的一次出现（卷 × 题号）。同一道题会在多套卷里重复出现，
// 这是跨省联考造成的，本身是有价值的高频信号，不能删除。
type Occurrence struct {
	PaperID  int64
	Question *Question
}

// Warning 解析过程中发现的异常，必须落库以便人工核查。
type Warning struct {
	SourcePath string
	Line       int
	Kind       string
	Detail     string
}

// 警告类型。
const (
	WarnNoOptions    = "no_options"     // 无选项（判断题占位）
	WarnAnswerMiss   = "answer_missing" // 答案缺失
	WarnAnswerNotOpt = "answer_not_in_options"
	WarnOptionCount  = "unusual_option_count"
	WarnNoAnswerLine = "no_answer_line"
	WarnMarkMismatch = "correct_mark_mismatch" // ✅ 与答案字段不一致
)
