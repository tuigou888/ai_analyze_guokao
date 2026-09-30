package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	"ai_analyze_guokao/internal/model"
)

// 图片类型常量。
const (
	KindQuestionImg = "题目图"
	KindFormulaImg  = "公式图"
)

// NormalizeForHash 归一化文本用于计算题目指纹：去掉所有空白（含全角空格）、
// 统一大小写、丢弃 ✅ 标记。
//
// 刻意**不**删除标点：删标点会让不同题目更容易被误判为同一道，
// 而跨省重复的题目本来就是逐字相同的副本，不需要这么激进。
func NormalizeForHash(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		// 注意 unicode.IsSpace 包含 U+3000（全角空格），而 regexp 的 \s 不包含。
		if unicode.IsSpace(r) || r == '✅' {
			continue
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}

// ContentHash 计算题目实体指纹，作为跨省去重的唯一键。
//
// 只取「题干 + 选项」，不含答案、解析与材料正文。这个口径不是拍脑袋定的，
// 而是用数据集自带的 qid（原始题库分配，独立于我们的归一化策略）当基准实测出来的
// ——见 TestDedupDefinition：
//
//	口径                     唯一数    与唯一 qid(27473) 之差
//	题干+选项                27449    -24   ← 采用
//	题干+选项+材料           28669    +1196 ← 过切分
//	题干+选项+去标点          27447    -26
//	题干+选项+材料+去标点     28668    +1195
//
// 教训有两条：
//  1. **不能把材料正文纳入指纹**。资料分析看起来"必须带材料"，但同一道题在不同省份的
//     材料排版（空白、HTML 实体）并不一致，纳入后会多切出 4.4% 的伪新题，直接抬高蒸馏成本。
//     题干与选项才是稳定身份。
//  2. **不需要去标点**。去标点只多合并 2 道题，收益可忽略，却会提高不同题被误判为
//     同一道的风险。
func ContentHash(q *model.Question) string {
	var sb strings.Builder
	sb.Grow(len(q.Stem) + 64)
	sb.WriteString(NormalizeForHash(q.Stem))
	sb.WriteByte('|')
	for _, o := range q.Options {
		sb.WriteString(o.Label)
		sb.WriteString(NormalizeForHash(o.Content))
		sb.WriteByte('|')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:16]) // 128 位足够，且比全长更省索引
}

// HasFigure 判断题面是否含题目图（不含解析里的公式图）。
// 用途：决定这道题蒸馏时是否需要走多模态。实测只有 9.1% 的题需要。
func HasFigure(imgs []model.ImageRef) bool {
	for _, im := range imgs {
		if im.Kind == KindQuestionImg {
			return true
		}
	}
	return false
}
