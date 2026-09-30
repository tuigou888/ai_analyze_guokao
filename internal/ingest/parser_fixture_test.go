package ingest

import (
	"strings"
	"testing"

	"ai_analyze_guokao/internal/model"
)

// miniPaper 是一份覆盖各种版式特征的最小样本，用于不依赖 data/ 的快速回归。
const miniPaper = `---
类型: "真题"
试卷: "2022年国家公务员录用考试《行测》题（副省级网友回忆版）"
地区: "国考"
年份: "2022"
模块: "资料分析"
题数: "4"
来源: "tiku.db 原始题库（未蒸馏）"
---

# 2022年国家公务员录用考试《行测》题（副省级网友回忆版）

> 资料分析 · 4 题 · 国考 2022

## 材料 1

<p>2021年1-2月，J省发电量为167亿千瓦时。</p><p><img width="584px" src="../90-图片/题目图/abc123.png" /></p>

### 第 1 题　<sub>qid 4639483 · 增长</sub>

2020年第二季度发电量在以下哪个范围内？

- **A**. 不到200亿千瓦时　✅
- **B**. 在200亿~215亿千瓦时
- **C**. 在215亿~230亿千瓦时
- **D**. 超过230亿千瓦时

**答案**：A

**官方解析**

<p>定位图1可得 228.6 亿千瓦时，故 A 项正确。</p>

---

### 第 2 题　<sub>qid 4639489 · 增长</sub>

下列说法正确的是（　）。

- **A**. 甲　✅
- **B**. 乙　✅
- **C**. 丙
- **D**. 丁

**答案**：AB

**官方解析**

<p>公式 <img flag="tex" src="../90-图片/公式图/formula-aaa.png" /> 可算得甲乙正确。</p>

---

## 第 3 题　<sub>qid 2343376 · 逻辑判断</sub>

简政放权就是政府把该放的权利放掉。

- （选项）[]

**答案**：B

**官方解析**

<p>故题干表述错误。</p>

---

## 第 4 题　<sub>qid 9999999 · 图形推理</sub>

<p>从四个图中选出唯一的一项。</p><p><img width="300px" src="../90-图片/题目图/q4fig.png" /></p>

- **A**. 牛
- **B**. 虎
- **C**. 兔
- **D**. 龙
- **E**. 蛇
- **F**. 马
- **G**. 羊
- **H**. 猴　✅

**答案**：H

**官方解析**

<p>故选 H。</p>

---
`

func TestParseMiniPaper(t *testing.T) {
	f, err := ParseString(miniPaper, "mini.md")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if got, want := len(f.Questions), 4; got != want {
		t.Fatalf("题目数 = %d，期望 %d", got, want)
	}
	if got, want := len(f.Materials), 1; got != want {
		t.Fatalf("材料数 = %d，期望 %d", got, want)
	}

	// 元数据
	if f.Paper.ExamType != "国考" || f.Paper.Variant != "副省级" {
		t.Errorf("考试类型/卷型 = %q/%q，期望 国考/副省级", f.Paper.ExamType, f.Paper.Variant)
	}
	if f.Paper.Year != 2022 || f.Paper.DeclaredCount != 4 {
		t.Errorf("年份/声明题数 = %d/%d，期望 2022/4", f.Paper.Year, f.Paper.DeclaredCount)
	}

	// 第 1 题：材料下的单选题，带材料图
	q1 := f.Questions[0]
	if q1.MaterialSeq != 1 {
		t.Errorf("第1题 MaterialSeq = %d，期望 1", q1.MaterialSeq)
	}
	if q1.QID != "4639483" || q1.Tag != "增长" {
		t.Errorf("第1题 qid/tag = %q/%q", q1.QID, q1.Tag)
	}
	if q1.AnswerType != model.AnswerSingle || q1.Answer != "A" {
		t.Errorf("第1题 形态/答案 = %q/%q，期望 single/A", q1.AnswerType, q1.Answer)
	}
	if len(q1.Options) != 4 || !q1.Options[0].IsCorrect || q1.Options[1].IsCorrect {
		t.Errorf("第1题 选项标记有误: %+v", q1.Options)
	}
	// 选项文本里不应残留 ✅
	if strings.Contains(q1.Options[0].Content, "✅") {
		t.Errorf("选项文本残留 ✅ 标记: %q", q1.Options[0].Content)
	}
	// 资料分析的图表在材料里：应标记 HasMaterialFigure，而题面本身无图。
	// 这个区分决定了蒸馏时是否走多模态，必须准。
	if q1.HasFigure {
		t.Error("第1题的图在材料里，不应标记 HasFigure")
	}
	if !q1.HasMaterialFigure {
		t.Error("第1题应标记 HasMaterialFigure")
	}

	// 第 2 题：多选 + 解析中的公式图占位
	q2 := f.Questions[1]
	if q2.AnswerType != model.AnswerMulti || q2.Answer != "AB" {
		t.Errorf("第2题 形态/答案 = %q/%q，期望 multi/AB", q2.AnswerType, q2.Answer)
	}
	if !strings.Contains(q2.Explanation, "⟦IMG:公式图/formula-aaa.png⟧") {
		t.Errorf("解析未保留公式图占位符: %q", q2.Explanation)
	}
	if q2.HasFigure {
		t.Error("第2题的图在解析里，不应标记 HasFigure")
	}

	// 第 3 题：判断题。二级标题的题不隶属材料，且无选项。
	q3 := f.Questions[2]
	if q3.MaterialSeq != 0 {
		t.Errorf("第3题 MaterialSeq = %d，期望 0（二级标题的题不隶属材料）", q3.MaterialSeq)
	}
	if q3.AnswerType != model.AnswerJudge {
		t.Errorf("第3题 形态 = %q，期望 judge", q3.AnswerType)
	}
	if !q3.OptionPlaceholder {
		t.Error("第3题 应标记 OptionPlaceholder")
	}
	if len(q3.Options) != 0 {
		t.Errorf("第3题 选项数 = %d，期望 0", len(q3.Options))
	}

	// 第 4 题：8 选项（陕西省考特征），答案字母可超出 D
	q4 := f.Questions[3]
	if len(q4.Options) != 8 {
		t.Errorf("第4题 选项数 = %d，期望 8", len(q4.Options))
	}
	if q4.Answer != "H" || q4.AnswerType != model.AnswerSingle {
		t.Errorf("第4题 答案/形态 = %q/%q，期望 H/single", q4.Answer, q4.AnswerType)
	}
	// 第 4 题也用了二级标题，材料上下文必须已经结束
	if q4.MaterialSeq != 0 {
		t.Errorf("第4题 不应隶属材料，得到 %d", q4.MaterialSeq)
	}
	// 题面里的图片必须变成可逆占位符，而不是被丢掉
	if !q4.HasFigure {
		t.Error("第4题题面含图，应标记 HasFigure")
	}
	if !strings.Contains(q4.Stem, "⟦IMG:题目图/q4fig.png⟧") {
		t.Errorf("题干未保留图片占位符: %q", q4.Stem)
	}
	if strings.Contains(q4.Stem, "公式图") {
		t.Errorf("题干混入了公式图: %q", q4.Stem)
	}

	// 材料正文应保留图片引用（资料分析的图表在材料里）
	if len(f.Materials[0].Images) != 1 || f.Materials[0].Images[0].Kind != KindQuestionImg {
		t.Errorf("材料图片收集有误: %+v", f.Materials[0].Images)
	}
}

func TestParseWarnings(t *testing.T) {
	f, err := ParseString(miniPaper, "mini.md")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	kinds := map[string]int{}
	for _, w := range f.Warnings {
		kinds[w.Kind]++
	}
	// 第 4 题有 8 个选项，必须报警；✅ 与答案一致则不应有 mark_mismatch。
	if kinds[model.WarnOptionCount] != 1 {
		t.Errorf("unusual_option_count = %d，期望 1", kinds[model.WarnOptionCount])
	}
	if kinds[model.WarnMarkMismatch] != 0 {
		t.Errorf("本样本无冲突，却报了 %d 条 correct_mark_mismatch", kinds[model.WarnMarkMismatch])
	}
	if kinds["count_mismatch"] != 0 {
		t.Errorf("声明 4 题且实际 4 题，不应报 count_mismatch")
	}
}

// TestParseAnswerMarkMismatch 验证 ✅ 与答案字段的交叉校验会真的报警——
// 全量数据实测这个数字是 0，所以判据必须能抓到人为制造的冲突。
func TestParseAnswerMarkMismatch(t *testing.T) {
	doc := `---
试卷: "测试卷" 地区: "国考" 年份: "2022" 模块: "言语理解与表达" 题数: "1"
---
## 第 1 题　<sub>qid 1 · 片段阅读</sub>

题干

- **A**. 甲　✅
- **B**. 乙
- **C**. 丙
- **D**. 丁

**答案**：B

**官方解析**

<p>解析</p>
`
	f, err := ParseString(doc, "t.md")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	found := false
	for _, w := range f.Warnings {
		if w.Kind == model.WarnMarkMismatch {
			found = true
		}
	}
	if !found {
		t.Error("✅ 标记为 A 而答案是 B，应当报 correct_mark_mismatch")
	}
}

func TestContentHashIgnoresMaterialAndFormatting(t *testing.T) {
	base := &model.Question{
		Stem: "下列说法正确的是？",
		Options: []model.Option{
			{Label: "A", Content: "甲"}, {Label: "B", Content: "乙"},
			{Label: "C", Content: "丙"}, {Label: "D", Content: "丁"},
		},
	}
	// 空白与全角空格的差异不应改变指纹
	same := &model.Question{
		Stem: "下列说法正确的是？",
		Options: []model.Option{
			{Label: "A", Content: "甲 "}, {Label: "B", Content: "乙"},
			{Label: "C", Content: "　丙"}, {Label: "D", Content: "丁\n"},
		},
	}
	if ContentHash(base) != ContentHash(same) {
		t.Error("仅空白差异不应产生不同指纹")
	}

	// 选项内容不同则必须不同
	diff := &model.Question{Stem: base.Stem, Options: append([]model.Option(nil), base.Options...)}
	diff.Options[0].Content = "戊"
	if ContentHash(base) == ContentHash(diff) {
		t.Error("选项内容不同却得到相同指纹")
	}

	// 答案不同不应影响身份（答案是原文的从属信息）
	withAnswer := &model.Question{Stem: base.Stem, Options: base.Options, Answer: "A"}
	withAnswer2 := &model.Question{Stem: base.Stem, Options: base.Options, Answer: "B"}
	if ContentHash(withAnswer) != ContentHash(withAnswer2) {
		t.Error("答案不同不应改变题目身份")
	}
}

func TestClassifyPaper(t *testing.T) {
	cases := []struct {
		name, region  string
		exam, variant string
	}{
		{"2022年国家公务员录用考试《行测》题（副省级网友回忆版）", "国考", "国考", "副省级"},
		{"2022年国家公务员录用考试《行测》题（行政执法卷网友回忆版）", "国考", "国考", "行政执法"},
		{"2023年深圳市考公务员录用考试《行测》试题（网友回忆版）", "深圳", "市考", ""},
		{"2019年贵州省选调高校优秀毕业生到基层工作考试《行测》试题（网友回忆版）", "贵州", "选调", "选调"},
		{"2022年浙江省公务员录用考试《行测》题（C类）（网友回忆版）", "浙江", "省考", "C类"},
	}
	for _, c := range cases {
		exam, variant := ClassifyPaper(c.name, c.region)
		if exam != c.exam || variant != c.variant {
			t.Errorf("ClassifyPaper(%q) = %q/%q，期望 %q/%q", c.name, exam, variant, c.exam, c.variant)
		}
	}
}

func TestDiscoverExcludesNonQuestionFiles(t *testing.T) {
	files, err := Discover("../../data")
	if err != nil || len(files) == 0 {
		t.Skipf("找不到 data/，跳过: %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f, "90-图片") || strings.HasSuffix(f, "README.md") ||
			strings.Contains(f, "蒸馏方法") {
			t.Errorf("不应收录非题库文件: %s", f)
		}
	}
	if len(files) != 2956 {
		t.Errorf("笔记数 = %d，期望 2956", len(files))
	}
}
