package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"ai_analyze_guokao/internal/model"
)

// 断言解析契约：源数据实测 58890 个题块，入库必须一个不少。
func TestParseContractOnRealData(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实数据扫描")
	}
	files, err := Discover("../../data")
	if err != nil || len(files) == 0 {
		t.Skipf("找不到 data/ 目录，跳过: %v", err)
	}

	var (
		total, judge, other, multi, withFigure, materials int
		warnings                                          = map[string]int{}
	)
	for _, rel := range files {
		f, err := ParseFile("../../data/"+rel, rel)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", rel, err)
		}
		total += len(f.Questions)
		materials += len(f.Materials)
		for _, q := range f.Questions {
			switch q.AnswerType {
			case model.AnswerJudge:
				judge++
			case model.AnswerOther:
				other++
			case model.AnswerMulti:
				multi++
			}
			if q.HasFigure {
				withFigure++
			}
		}
		for _, w := range f.Warnings {
			warnings[w.Kind]++
		}
	}

	// 题目实体数是整个项目的成本分母（蒸馏按它计价），因此断言得很紧。
	entities := uniqueByHash(files)
	if entities != 27449 {
		t.Errorf("唯一题目实体 = %d，期望 27449（口径见 ContentHash 注释）", entities)
	}

	checks := []struct {
		name string
		got  int
		want int
	}{
		{"题块总数", total, 58890},
		{"材料段", materials, 2049},
		{"判断题", judge, 107},
		{"答案缺失", other, 4},
		{"多选", multi, 405},
		{"含题目图", withFigure, 5365},
		{"✅与答案冲突", warnings[model.WarnMarkMismatch], 0},
		{"答案不在选项内", warnings[model.WarnAnswerNotOpt], 0},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d，期望 %d", c.name, c.got, c.want)
		}
	}
	t.Logf("题块=%d 唯一题目实体=%d 判断题=%d 无选项占位=%d 多选=%d 含题图=%d 材料=%d",
		total, entities, judge, judge+other, multi, withFigure, materials)
	t.Logf("告警分布: %v", warnings)
}

// uniqueByHash 去重后的题目实体数，是蒸馏工作量的真实分母。
func uniqueByHash(files []string) int {
	seen := map[string]bool{}
	for _, rel := range files {
		f, err := ParseFile("../../data/"+rel, rel)
		if err != nil {
			continue
		}
		for i := range f.Questions {
			seen[ContentHash(&f.Questions[i])] = true
		}
	}
	return len(seen)
}

// TestDedupDefinition 对比几种指纹口径，用「唯一 qid」作为独立基准来选型。
//
// 数据集自带的 qid 由原始题库分配，是独立于我们归一化策略的第二个信号：
// 若某种口径的唯一数明显偏离唯一 qid 数，说明它在过度切分或过度合并。
func TestDedupDefinition(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实数据扫描")
	}
	files, err := Discover("../../data")
	if err != nil || len(files) == 0 {
		t.Skipf("找不到 data/ 目录，跳过: %v", err)
	}

	variants := []struct {
		name        string
		useMaterial bool
		stripPunct  bool
	}{
		{"题干+选项", false, false},
		{"题干+选项+材料", true, false},
		{"题干+选项+去标点", false, true},
		{"题干+选项+材料+去标点", true, true},
	}

	sets := make([]map[string]bool, len(variants))
	for i := range sets {
		sets[i] = map[string]bool{}
	}
	qidSet := map[string]bool{}

	for _, rel := range files {
		f, err := ParseFile("../../data/"+rel, rel)
		if err != nil {
			continue
		}
		body := map[int]string{}
		for _, m := range f.Materials {
			body[m.Seq] = m.Body
		}
		for i := range f.Questions {
			q := &f.Questions[i]
			if q.QID != "" {
				qidSet[q.QID] = true
			}
			for vi, v := range variants {
				mat := ""
				if v.useMaterial {
					mat = body[q.MaterialSeq]
				}
				key := variantHash(q, mat, v.stripPunct)
				sets[vi][key] = true
			}
		}
	}

	t.Logf("唯一 qid（基准）= %d", len(qidSet))
	for i, v := range variants {
		diff := len(sets[i]) - len(qidSet)
		t.Logf("%-26s 唯一 = %6d  与基准差 %+d", v.name, len(sets[i]), diff)
	}
}

// variantHash 是 ContentHash 的可调版本，仅供选型对比使用。
func variantHash(q *model.Question, materialBody string, stripPunct bool) string {
	norm := func(s string) string {
		out := NormalizeForHash(s)
		if !stripPunct {
			return out
		}
		var sb strings.Builder
		for _, r := range out {
			switch r {
			case '，', '。', '、', '；', '：', '“', '”', '‘', '’', '（', '）', '(', ')', '【', '】', '·':
				continue
			}
			sb.WriteRune(r)
		}
		return sb.String()
	}
	var sb strings.Builder
	if materialBody != "" {
		sb.WriteString(norm(materialBody))
		sb.WriteByte('|')
	}
	sb.WriteString(norm(q.Stem))
	sb.WriteByte('|')
	for _, o := range q.Options {
		sb.WriteString(o.Label)
		sb.WriteString(norm(o.Content))
		sb.WriteByte('|')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:16])
}
