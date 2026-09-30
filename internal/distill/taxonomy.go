// Package distill 实现蒸馏管线：把题目 + 官方解析交给 LLM，产出 12 字段的结构化标注。
//
// 设计要点见 docs/架构方案.md §6.3 / §6.4，其中最要紧的三条：
//
//  1. **prompt 由考点规范表程序化生成**，不手写枚举——手写会让模型把示例当偏好名单，
//     把没见过的新考法压平（参考文档 §5 坑 1、坑 2）。
//  2. **三级考点与考点细节物理分离**。混在一列会让 1:1 率飙到 90%+，考点无法聚合
//     （参考文档 §3.1 实测：11,282 题产出 10,375 个三级考点）。
//  3. **官方答案是唯一事实，不得改写**。模型与解析矛盾时只能写进 `疑点`。
//     疑点率是输入质量的报警器，不是标注质量的负面指标（§3.9）。
package distill

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Taxonomy 是考点规范表。
type Taxonomy struct {
	Version       string   `yaml:"version"`
	Modules       []Module `yaml:"modules"`
	BoundaryRules []string `yaml:"boundary_rules"`
	ExtensionRule string   `yaml:"extension_rule"`
}

// Module 一个一级科目。
type Module struct {
	Subject   string      `yaml:"subject"`
	Secondary []Secondary `yaml:"secondary"`
}

// Secondary 一个二级题型。
type Secondary struct {
	Name     string   `yaml:"name"`
	Tertiary []string `yaml:"tertiary"`
}

// LoadTaxonomy 读规范表。
func LoadTaxonomy(path string) (*Taxonomy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Taxonomy
	if err := yaml.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("解析规范表 %s: %w", path, err)
	}
	if len(t.Modules) == 0 {
		return nil, fmt.Errorf("规范表 %s 里没有任何模块", path)
	}
	return &t, nil
}

// Subjects 返回全部一级科目。
func (t *Taxonomy) Subjects() []string {
	out := make([]string, 0, len(t.Modules))
	for _, m := range t.Modules {
		out = append(out, m.Subject)
	}
	return out
}

// Find 取某科目的定义。
func (t *Taxonomy) Find(subject string) *Module {
	for i := range t.Modules {
		if t.Modules[i].Subject == subject {
			return &t.Modules[i]
		}
	}
	return nil
}

// SecondaryNames 返回某科目的二级题型名（封闭枚举）。
func (t *Taxonomy) SecondaryNames(subject string) []string {
	m := t.Find(subject)
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.Secondary))
	for _, s := range m.Secondary {
		out = append(out, s.Name)
	}
	return out
}

// TertiaryFor 返回某二级题型下的三级考点。
func (t *Taxonomy) TertiaryFor(subject, secondary string) []string {
	m := t.Find(subject)
	if m == nil {
		return nil
	}
	for _, s := range m.Secondary {
		if s.Name == secondary {
			return s.Tertiary
		}
	}
	return nil
}

// AllTertiary 返回某科目下全部三级考点（扁平），用于提示词与校验。
func (t *Taxonomy) AllTertiary(subject string) []string {
	m := t.Find(subject)
	if m == nil {
		return nil
	}
	var out []string
	for _, s := range m.Secondary {
		out = append(out, s.Tertiary...)
	}
	sort.Strings(out)
	return out
}

// PrimaryPrefix 取三级考点的大类前缀（"-" 之前的部分）。
// 前缀校验是刚需：给了规范表模型仍会漏前缀（参考文档 §5 坑 2 的实测）。
func PrimaryPrefix(tertiary string) string {
	if i := strings.Index(tertiary, "-"); i > 0 {
		return tertiary[:i]
	}
	return ""
}

// ValidateTertiary 校验一个三级考点是否合规。
//
// 三种结果：
//   - 在规范表里：合规
//   - 不在表里但带了合法前缀且所属二级题型正确：视为**扩展口**新增，合规但需要记入待审
//   - 前缀缺失或与二级题型不符：不合规，必须重试
//
// 返回 (合规, 是否为规范表外新增, 原因)
func (t *Taxonomy) ValidateTertiary(subject, secondary, tertiary string) (ok bool, novel bool, reason string) {
	tertiary = strings.TrimSpace(tertiary)
	if tertiary == "" {
		return false, false, "三级考点为空"
	}
	prefix := PrimaryPrefix(tertiary)
	if prefix == "" {
		return false, false, fmt.Sprintf("三级考点 %q 缺少「大类-」前缀", tertiary)
	}

	// 前缀必须等于它所属的二级题型名，否则会出现"归类到逻辑填空却用片段阅读-xxx 前缀"。
	if prefix != secondary {
		return false, false, fmt.Sprintf("三级考点 %q 的前缀 %q 与二级题型 %q 不一致", tertiary, prefix, secondary)
	}

	for _, known := range t.TertiaryFor(subject, secondary) {
		if known == tertiary {
			return true, false, ""
		}
	}
	return true, true, ""
}
