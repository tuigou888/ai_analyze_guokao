package distill

import "ai_analyze_guokao/internal/store"

// Bucket 是一个不可拆分的标注单元：同模块同题型的一组题。
//
// 为什么以它为最小单元而不是"按题轮流分"：整个项目最难的一步是让考点收敛
// （参考文档里最惨痛的事故是 11,282 题产出 10,375 个三级考点，一题一个标签，
// 完全无法聚合）。不同模型对同一考点的措辞天然不同——一个写「实词辨析」、
// 另一个可能写「词语辨析」——混着跑只会让 1:1 率更差、聚合层被污染。
//
// 所以切分必须保证：**任何一个（模块 × 题型）单元内部的标注出自同一个模型**。
// 这样"可比较的题"才始终由同一个模型来标。
//
// 单元粒度用数据自带的粗标签（逻辑填空 / 片段阅读 / 增长 …）而不是蒸馏产出的
// 二级题型，原因是粗标签在标注**之前**就存在，而二级题型正是待产出的东西——
// 用未知的东西去切已知的工作是切不动的。
type Bucket struct {
	Module string
	Tag    string
	Items  []store.DistillQuestion
}

func (b *Bucket) String() string { return b.Module + "/" + b.Tag }

// BuildBuckets 把题目按（模块 × 粗标签）分桶。
func BuildBuckets(qs []store.DistillQuestion) []*Bucket {
	idx := map[string]*Bucket{}
	var out []*Bucket
	for _, q := range qs {
		tag := q.Tag
		if tag == "" {
			tag = "(无标签)"
		}
		key := q.Module + "\x00" + tag
		b, ok := idx[key]
		if !ok {
			b = &Bucket{Module: q.Module, Tag: tag}
			idx[key] = b
			out = append(out, b)
		}
		b.Items = append(b.Items, q)
	}
	return out
}

// 注：原先这里有一个静态的 Partition（按题数 LPT 均分）+ DescribePartition。
// 改桶级动态领取后它没有生产调用点了，删除以免留下会腐化的死代码
// （它假设各模型吞吐相同，而这正是要解决的问题）。
// 分配逻辑见 schedule.go 的 buildWorkPlan。
