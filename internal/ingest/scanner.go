// Package ingest 把 data/ 下的 Markdown 真题解析成结构化模型。
//
// 版式有两种，都必须支持：
//
//	## 第 1 题　<sub>qid 19283724 · 毛中特</sub>     版式 A：独立题（43,904 道）
//	## 材料 1                                        版式 B：一材多题（14,986 道）
//	### 第 1 题　<sub>qid 4639483 · 综合</sub>
package ingest

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"ai_analyze_guokao/internal/model"
)

// 注意：Go 的 `\s` 只匹配 ASCII 空白，而标题里题号与 <sub> 之间是全角空格 U+3000，
// 因此所有分隔处必须显式写成 [\s\x{3000}]。
const sp = `[\s\x{3000}]*`

var (
	reMaterial = regexp.MustCompile(`^##` + sp + `材料` + sp + `(\d+)` + sp + `$`)
	reQuestion = regexp.MustCompile(`^(#{2,3})` + sp + `第` + sp + `(\d+)` + sp + `题(?:` + sp + `<sub>(.*?)</sub>` + sp + `)?$`)
	reOption   = regexp.MustCompile(`^-` + sp + `\*\*([A-Z])\*\*` + sp + `[.．、]` + sp + `(.*)$`)
	reOptionPH = regexp.MustCompile(`^-` + sp + `（选项）` + sp + `\[\]` + sp + `$`)
	reAnswer   = regexp.MustCompile(`^\*\*答案\*\*` + sp + `[：:]` + sp + `(.*)$`)
	reExplain  = regexp.MustCompile(`^\*\*官方解析\*\*` + sp + `$`)
	reSubQID   = regexp.MustCompile(`qid` + sp + `(\d+)`)
	reAnswerCh = regexp.MustCompile(`^[A-Z]+$`)
)

// File 一篇笔记（= 一套卷的一个模块）的解析结果。
type File struct {
	Paper     model.Paper
	Materials []model.Material
	Questions []model.Question
	Warnings  []model.Warning
}

type parsePhase int

const (
	phHeader   parsePhase = iota // 试卷头部 / 分隔符之后：忽略正文
	phMaterial                   // 材料正文
	phStem
	phOption
	phAnswer
	phExplanation
)

type fileParser struct {
	f     *File
	rel   string
	cur   *model.Question
	phase parsePhase
	stem  []string
	expl  []string

	// 材料用「序号 + 累加缓冲」表示，而不是指向 f.Materials 元素的指针——
	// append 可能搬移底层数组，指针会失效。
	matSeq      int
	matBodyHTML strings.Builder
	matBody     string
	matImgs     []model.ImageRef
	matHasFig   bool
	matParsed   bool
}

// ParseFile 读取并解析一个文件。absPath 为磁盘路径，relPath 为记录用的相对路径。
func ParseFile(absPath, relPath string) (*File, error) {
	b, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	return ParseString(string(b), relPath)
}

// ParseString 解析文件内容，便于测试直接喂字符串。
func ParseString(content, relPath string) (*File, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")

	fm, bodyStart, hasFM := splitFrontmatter(lines)
	f := &File{}
	f.Paper = model.Paper{
		Name:          fm.PaperName,
		Region:        fm.Region,
		Year:          fm.YearInt(),
		Module:        fm.Module,
		DeclaredCount: fm.CountInt(),
		SourcePath:    relPath,
	}
	f.Paper.ExamType, f.Paper.Variant = ClassifyPaper(fm.PaperName, fm.Region)

	p := &fileParser{f: f, rel: relPath, phase: phHeader}
	if !hasFM {
		p.warn(1, "no_frontmatter", "缺少或无法解析 YAML 头部")
	}

	for i := bodyStart; i < len(lines); i++ {
		p.feed(strings.TrimSpace(lines[i]), i+1)
	}
	p.flushQuestion()
	p.flushMaterial()

	if f.Paper.DeclaredCount > 0 && f.Paper.DeclaredCount != len(f.Questions) {
		p.warn(1, "count_mismatch",
			fmt.Sprintf("frontmatter 声明 %d 题，实际解析 %d 题", f.Paper.DeclaredCount, len(f.Questions)))
	}
	return f, nil
}

func (p *fileParser) feed(line string, lineNo int) {
	if line == "" {
		return
	}

	// 块分隔符：结束当前题，但不影响材料上下文——材料下的多道题正是用 --- 分隔的。
	if line == "---" {
		p.flushQuestion()
		if p.phase == phMaterial {
			p.phase = phHeader
		}
		return
	}

	if m := reMaterial.FindStringSubmatch(line); m != nil {
		p.flushQuestion()
		p.flushMaterial()
		seq, _ := strconv.Atoi(m[1])
		p.matSeq = seq
		p.matBodyHTML.Reset()
		p.matImgs = nil
		p.matHasFig = false
		p.matParsed = false
		p.phase = phMaterial
		return
	}

	if m := reQuestion.FindStringSubmatch(line); m != nil {
		p.flushQuestion()
		if m[1] == "##" {
			// 二级标题的题不隶属任何材料；这也标志上一个材料段结束。
			p.flushMaterial()
		}
		num, _ := strconv.Atoi(m[2])
		qid, tag := splitSub(m[3])
		p.cur = &model.Question{Number: num, QID: qid, Tag: tag, LineNo: lineNo}
		if p.matSeq > 0 {
			p.cur.MaterialSeq = p.matSeq
		}
		p.phase = phStem
		return
	}

	if p.cur != nil {
		switch {
		case reAnswer.MatchString(line):
			p.cur.Answer = strings.TrimSpace(reAnswer.FindStringSubmatch(line)[1])
			p.phase = phAnswer
			return
		case reExplain.MatchString(line):
			p.phase = phExplanation
			return
		case reOptionPH.MatchString(line):
			// 判断题占位。数据里 111 道，答案用 A/B 表示 正确/错误。
			p.cur.OptionPlaceholder = true
			p.phase = phOption
			return
		case reOption.MatchString(line):
			m := reOption.FindStringSubmatch(line)
			p.addOption(m[1], m[2])
			p.phase = phOption
			return
		}
	}

	switch p.phase {
	case phMaterial:
		p.matBodyHTML.WriteString(line)
		p.matBodyHTML.WriteByte('\n')
	case phStem:
		p.stem = append(p.stem, line)
	case phExplanation:
		p.expl = append(p.expl, line)
	case phOption:
		// 实测选项恒为单行（0 例续行）。若出现，说明数据版式变了，必须报警而不是静默吞掉。
		p.warn(lineNo, "option_continuation", snippet(line))
	}
}

func (p *fileParser) addOption(label, raw string) {
	correct := strings.Contains(raw, "✅")
	raw = strings.TrimRight(strings.ReplaceAll(raw, "✅", ""), "　 \t")
	txt, imgs := HTMLToText(raw)
	for i := range imgs {
		imgs[i].In = "option"
	}
	p.cur.Options = append(p.cur.Options, model.Option{
		Label:       label,
		ContentHTML: raw,
		Content:     txt,
		IsCorrect:   correct,
	})
	p.cur.Images = append(p.cur.Images, imgs...)
}

func (p *fileParser) flushQuestion() {
	if p.cur == nil {
		return
	}
	q := p.cur

	stemHTML := strings.Join(p.stem, "\n")
	q.StemHTML = stemHTML
	stem, simgs := HTMLToText(stemHTML)
	q.Stem = stem
	for i := range simgs {
		simgs[i].In = "stem"
	}
	q.Images = append(q.Images, simgs...)

	// 在拼入解析图之前判定题面是否含图——解析里的公式图不构成"需要看图"。
	q.HasFigure = HasFigure(q.Images)
	p.ensureMaterial()
	q.HasMaterialFigure = p.matHasFig

	expHTML := strings.Join(p.expl, "\n")
	q.ExplanationHTML = expHTML
	exp, eimgs := HTMLToText(expHTML)
	q.Explanation = exp
	for i := range eimgs {
		eimgs[i].In = "explanation"
	}
	q.Images = append(q.Images, eimgs...)

	p.validate(q)
	p.f.Questions = append(p.f.Questions, *q)

	p.cur = nil
	p.stem = nil
	p.expl = nil
}

// ensureMaterial 把材料正文转成纯文本并收集图片。题总在材料正文之后，
// 因此第一道题 flush 时材料已读完，可以就地解析。
func (p *fileParser) ensureMaterial() {
	if p.matParsed || p.matSeq == 0 {
		return
	}
	body, imgs := HTMLToText(p.matBodyHTML.String())
	p.matBody = body
	for i := range imgs {
		imgs[i].In = "material"
	}
	p.matImgs = imgs
	p.matHasFig = HasFigure(imgs)
	p.matParsed = true
}

// flushMaterial 结束当前材料段落。幂等：重复调用不会产生空材料。
func (p *fileParser) flushMaterial() {
	if p.matSeq == 0 {
		return
	}
	p.ensureMaterial()
	p.f.Materials = append(p.f.Materials, model.Material{
		Seq:      p.matSeq,
		BodyHTML: p.matBodyHTML.String(),
		Body:     p.matBody,
		Images:   p.matImgs,
	})
	p.matSeq = 0
	p.matBodyHTML.Reset()
	p.matImgs = nil
	p.matHasFig = false
	p.matParsed = false
}

// validate 判定作答形态并记录异常。这些检查是解析契约的一部分：
// 实测基线是 58890 题、✅ 与答案 0 冲突、111 道判断题占位。
func (p *fileParser) validate(q *model.Question) {
	ans := strings.TrimSpace(q.Answer)

	switch {
	case q.Answer == "":
		q.AnswerType = model.AnswerOther
		p.warn(q.LineNo, model.WarnNoAnswerLine, fmt.Sprintf("第 %d 题无答案行", q.Number))
		return
	case ans == "（缺）":
		q.AnswerType = model.AnswerOther
		p.warn(q.LineNo, model.WarnAnswerMiss, fmt.Sprintf("第 %d 题答案缺失", q.Number))
		return
	case len(q.Options) == 0:
		// 判断题：占位选项 + 答案是 A/B（A=正确、B=错误）。
		q.AnswerType = model.AnswerJudge
		if !q.OptionPlaceholder {
			p.warn(q.LineNo, model.WarnNoOptions, fmt.Sprintf("第 %d 题无选项且无占位标记", q.Number))
		}
		return
	}

	if reAnswerCh.MatchString(ans) {
		if len([]rune(ans)) > 1 {
			q.AnswerType = model.AnswerMulti
		} else {
			q.AnswerType = model.AnswerSingle
		}
	} else {
		q.AnswerType = model.AnswerOther
	}

	// 交叉校验：行尾 ✅ 是独立于答案字段的第二个信号，两者不一致说明数据有问题。
	marked := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		if o.IsCorrect {
			marked = append(marked, o.Label)
		}
	}
	if got := strings.Join(marked, ""); got != ans {
		p.warn(q.LineNo, model.WarnMarkMismatch,
			fmt.Sprintf("第 %d 题 ✅ 标记为 %q，答案字段为 %q", q.Number, got, ans))
	}

	// 答案字母必须落在实际选项里，否则判分一定出错。
	labels := make(map[byte]bool, len(q.Options))
	for _, o := range q.Options {
		if len(o.Label) == 1 {
			labels[o.Label[0]] = true
		}
	}
	for i := 0; i < len(ans); i++ {
		if !labels[ans[i]] {
			p.warn(q.LineNo, model.WarnAnswerNotOpt,
				fmt.Sprintf("第 %d 题答案 %q 不在选项内", q.Number, ans))
			break
		}
	}

	if n := len(q.Options); n != 4 {
		p.warn(q.LineNo, model.WarnOptionCount,
			fmt.Sprintf("第 %d 题有 %d 个选项", q.Number, n))
	}
}

func (p *fileParser) warn(line int, kind, detail string) {
	p.f.Warnings = append(p.f.Warnings, model.Warning{
		SourcePath: p.rel, Line: line, Kind: kind, Detail: detail,
	})
}

func splitSub(s string) (qid, tag string) {
	if m := reSubQID.FindStringSubmatch(s); m != nil {
		qid = m[1]
	}
	if i := strings.Index(s, "·"); i >= 0 {
		tag = strings.TrimSpace(s[i+len("·"):])
	}
	return qid, tag
}

func snippet(s string) string {
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

// ClassifyPaper 从试卷名与地区推导「考试类型」与「卷型」。
// 全量数据里国考只占 8%，这两列是做任何统计报表的前置过滤维度。
func ClassifyPaper(name, region string) (examType, variant string) {
	variant = classifyVariant(name)
	switch {
	case strings.Contains(name, "国家公务员") || region == "国考":
		examType = "国考"
	case strings.Contains(name, "选调"):
		examType = "选调"
	case strings.Contains(name, "市考") || strings.Contains(name, "深圳市") || strings.Contains(name, "上海市"):
		examType = "市考"
	case strings.Contains(name, "公务员") || strings.Contains(name, "联考"):
		examType = "省考"
	default:
		examType = "其他"
	}
	return examType, variant
}

func classifyVariant(name string) string {
	for _, v := range []string{"副省级", "地市级", "行政执法", "乡镇", "甲级", "乙级", "A类", "B类", "C类", "定向", "选调"} {
		if strings.Contains(name, v) {
			return v
		}
	}
	return ""
}
