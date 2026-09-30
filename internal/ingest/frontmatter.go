package ingest

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter 是笔记头部的 YAML 元数据。
// 注意年份/题数在源文件里是带引号的字符串（"2022"），必须容错转换。
type Frontmatter struct {
	Kind      string `yaml:"类型"`
	PaperName string `yaml:"试卷"`
	Region    string `yaml:"地区"`
	Year      string `yaml:"年份"`
	Module    string `yaml:"模块"`
	Count     string `yaml:"题数"`
	Source    string `yaml:"来源"`
}

// splitFrontmatter 切出 YAML 头部与正文。返回 bodyStart 为正文起始行下标。
func splitFrontmatter(lines []string) (fm Frontmatter, bodyStart int, ok bool) {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, 0, false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return fm, 0, false
	}
	raw := strings.Join(lines[1:end], "\n")
	if err := yaml.Unmarshal([]byte(raw), &fm); err != nil {
		return fm, end + 1, false
	}
	return fm, end + 1, true
}

// YearInt 解析年份，失败返回 0。
func (f Frontmatter) YearInt() int {
	n, err := strconv.Atoi(strings.TrimSpace(f.Year))
	if err != nil {
		return 0
	}
	return n
}

// CountInt 解析题数，失败返回 0。
func (f Frontmatter) CountInt() int {
	n, err := strconv.Atoi(strings.TrimSpace(f.Count))
	if err != nil {
		return 0
	}
	return n
}
