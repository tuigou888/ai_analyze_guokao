package distill

import (
	"os"
	"strings"
	"testing"
)

// 规范表必须通过它自己的校验器。
//
// 这条测试是有来历的：v1.1 之前，常识判断与政治理论下的三级考点前缀写短了
// （"人文-""马原-""时政-"），而二级题型名是"人文常识""马克思主义基本原理""时事政治"。
// 于是校验器拒绝了规范表**自己列出的**考点，50 题 MVP 里白烧了 4 次 API 调用
// ——在限流端点上是双重浪费。这个错误人工看很难发现（两边看着都对），
// 机器一跑就露。
func TestShippedTaxonomyIsSelfConsistent(t *testing.T) {
	path := "../../taxonomy/v1/taxonomy.yaml"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("找不到规范表: %v", err)
	}
	tax, err := LoadTaxonomy(path)
	if err != nil {
		t.Fatalf("加载规范表失败: %v", err)
	}

	var checked int
	for _, m := range tax.Modules {
		if strings.TrimSpace(m.Subject) == "" {
			t.Error("存在空的 subject")
		}
		if len(m.Secondary) == 0 {
			t.Errorf("模块 %q 下没有任何二级题型", m.Subject)
		}
		for _, s := range m.Secondary {
			if strings.TrimSpace(s.Name) == "" {
				t.Errorf("模块 %q 下存在空的二级题型名", m.Subject)
				continue
			}
			if len(s.Tertiary) == 0 {
				t.Errorf("%s / %s 下没有任何三级考点", m.Subject, s.Name)
			}
			for _, ter := range s.Tertiary {
				checked++
				ok, _, reason := tax.ValidateTertiary(m.Subject, s.Name, ter)
				if !ok {
					t.Errorf("规范表自相矛盾：%s / %s / %s —— %s", m.Subject, s.Name, ter, reason)
				}
			}
		}
	}
	if checked < 100 {
		t.Errorf("只校验了 %d 个三级考点，规范表是不是被截断了？", checked)
	}
	t.Logf("校验通过：%d 个模块 / %d 个三级考点", len(tax.Modules), checked)
}

// 二级题型名不得重复——重复会让"按题型聚合"静默算错。
func TestTaxonomyNoDuplicateSecondary(t *testing.T) {
	tax, err := LoadTaxonomy("../../taxonomy/v1/taxonomy.yaml")
	if err != nil {
		t.Skipf("找不到规范表: %v", err)
	}
	for _, m := range tax.Modules {
		seen := map[string]bool{}
		for _, s := range m.Secondary {
			if seen[s.Name] {
				t.Errorf("模块 %q 下二级题型 %q 重复", m.Subject, s.Name)
			}
			seen[s.Name] = true
		}
	}
}
