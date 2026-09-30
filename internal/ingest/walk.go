package ingest

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Discover 列出数据目录下所有真题笔记，返回相对于 dataDir 的路径。
//
// 排除项有明确理由：
//   - 90-图片/：图片资源目录，不是笔记
//   - README.md：索引文件，没有题目结构
//   - 蒸馏方法参考.md：方法论文档，来自数据集作者，不是题库内容
func Discover(dataDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "90-图片" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") || name == "README.md" {
			return nil
		}
		if strings.Contains(name, "蒸馏方法") {
			return nil
		}
		rel, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out) // 固定顺序，保证入库结果可复现
	return out, nil
}
