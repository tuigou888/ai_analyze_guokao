package distill

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ai_analyze_guokao/internal/store"
)

// 构造一批题目，模块与粗标签的组合可控。
func mkQuestions(specs map[string]int) []store.DistillQuestion {
	var out []store.DistillQuestion
	id := int64(1)
	// 固定遍历顺序，保证测试可复现
	var keys []string
	for k := range specs {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, k := range keys {
		var module, tag string
		fmt.Sscanf(k, "%s", &module)
		if i := indexByte(k, '|'); i >= 0 {
			module, tag = k[:i], k[i+1:]
		}
		for n := 0; n < specs[k]; n++ {
			out = append(out, store.DistillQuestion{ID: id, Module: module, Tag: tag})
			id++
		}
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func TestBuildBucketsGroupsByModuleAndTag(t *testing.T) {
	qs := mkQuestions(map[string]int{
		"言语理解与表达|逻辑填空": 3,
		"言语理解与表达|片段阅读": 5,
		"判断推理|图形推理":    2,
	})
	buckets := BuildBuckets(qs)
	if len(buckets) != 3 {
		t.Fatalf("桶数 = %d，期望 3", len(buckets))
	}
	sizes := map[string]int{}
	for _, b := range buckets {
		sizes[b.String()] = len(b.Items)
	}
	want := map[string]int{
		"言语理解与表达/逻辑填空": 3,
		"言语理解与表达/片段阅读": 5,
		"判断推理/图形推理":    2,
	}
	for k, v := range want {
		if sizes[k] != v {
			t.Errorf("桶 %s 大小 = %d，期望 %d", k, sizes[k], v)
		}
	}
}

// 多模型并发最重要的一条不变量：**同一个（模块 × 题型）单元绝不被拆到两个执行器**。
//
// 一旦拆开，同一个考点的措辞就会出自不同模型（一个写「实词辨析」、另一个写「词语辨析」），
// 1:1 率会变差、聚合层被污染——那正是参考文档里 11,282 题产出 10,375 个三级考点的翻版。
// 动态领取不改变这条：桶是领取的最小单位。
func TestWorkPlanNeverSplitsBucket(t *testing.T) {
	qs := mkQuestions(map[string]int{
		"言语理解与表达|片段阅读": 7177,
		"言语理解与表达|逻辑填空": 6833,
		"数量关系|数学运算":    6448,
		"常识判断|人文常识":    5359,
		"判断推理|类比推理":    4761,
	})
	engines := []Engine{{Model: "m1"}, {Model: "m2"}, {Model: "m3"}}
	owners := loadBucketOwners(filepath.Join(t.TempDir(), "none.json"))

	owned, queue, _ := buildWorkPlan(BuildBuckets(qs), engines, owners)

	// 收集每个桶落在哪些执行器（含共享队列视为"未定")
	place := map[string][]string{}
	for i, bs := range owned {
		for _, b := range bs {
			place[b.Key()] = append(place[b.Key()], engines[i].Model)
		}
	}
	for _, b := range queue {
		place[b.Key()] = append(place[b.Key()], "queue")
	}
	for key, who := range place {
		// 一个桶要么在恰好一个执行器手里，要么在共享队列里（等待领取），不能两头都有
		if len(who) != 1 {
			t.Errorf("桶 %q 同时出现在 %v —— 会被拆开", key, who)
		}
	}
}

// 续跑粘滞：已归属的桶必须回到原执行器，否则桶会被两个模型各做一半。
func TestWorkPlanStickyOwnership(t *testing.T) {
	qs := mkQuestions(map[string]int{
		"言语理解与表达|逻辑填空": 30, "判断推理|图形推理": 20, "资料分析|增长": 10,
	})
	engines := []Engine{{Model: "m1"}, {Model: "m2"}}
	ownerPath := filepath.Join(t.TempDir(), "bucket_owner.json")

	// 第一次：无归属，全部进共享队列
	owners := loadBucketOwners(ownerPath)
	owned1, queue1, _ := buildWorkPlan(BuildBuckets(qs), engines, owners)
	if len(queue1) != 3 || owned1[0] != nil && len(owned1[0]) > 0 {
		t.Fatalf("首次计划应全部进共享队列，得到 queue=%d owned0=%d", len(queue1), len(owned1[0]))
	}
	// 模拟 m1 领走了「逻辑填空」和「增长」
	owners.claim(bucketKey("言语理解与表达", "逻辑填空"), "m1")
	owners.claim(bucketKey("资料分析", "增长"), "m1")

	// 续跑：这两个桶必须回到 m1，剩下的图推进队列
	owners2 := loadBucketOwners(ownerPath)
	owned2, queue2, reassign := buildWorkPlan(BuildBuckets(qs), engines, owners2)
	if reassign != 0 {
		t.Errorf("不应有重新分配，得到 %d", reassign)
	}
	if got := len(owned2[0]); got != 2 {
		t.Errorf("m1 的续跑归属 = %d 个桶，期望 2", got)
	}
	if got := len(queue2); got != 1 {
		t.Errorf("共享队列 = %d 个桶，期望 1（只有图推未被领取过）", got)
	}
	// 且归属文件确实落盘了（进程被杀时不丢）
	if _, err := os.Stat(ownerPath); err != nil {
		t.Errorf("归属文件未落盘: %v", err)
	}
}

// 原归属模型不在本次列表里（比如被限流下线）：不丢桶，改为重新分配并计数。
func TestWorkPlanReassignsWhenOwnerGone(t *testing.T) {
	qs := mkQuestions(map[string]int{"判断推理|图形推理": 10, "资料分析|增长": 8})
	ownerPath := filepath.Join(t.TempDir(), "o.json")
	owners := loadBucketOwners(ownerPath)
	owners.claim(bucketKey("判断推理", "图形推理"), "gone-model")

	owned, queue, reassign := buildWorkPlan(BuildBuckets(qs), []Engine{{Model: "m1"}}, owners)
	if reassign != 1 {
		t.Errorf("reassign = %d，期望 1", reassign)
	}
	if len(queue) != 2 {
		t.Errorf("两个桶都该进队列，得到 %d", len(queue))
	}
	if len(owned[0]) != 0 {
		t.Errorf("m1 不应有预归属桶，得到 %d", len(owned[0]))
	}
}

// 桶不能丢也不能重复。
func TestWorkPlanCoversAllQuestions(t *testing.T) {
	qs := mkQuestions(map[string]int{
		"言语理解与表达|片段阅读": 100, "判断推理|图形推理": 60, "资料分析|增长": 40, "常识判断|法律常识": 7,
	})
	engines := []Engine{{Model: "m1"}, {Model: "m2"}}
	ownerPath := filepath.Join(t.TempDir(), "o.json")
	owners := loadBucketOwners(ownerPath)
	owners.claim(bucketKey("资料分析", "增长"), "m2")

	owned, queue, _ := buildWorkPlan(BuildBuckets(qs), engines, owners)
	seen := map[int64]int{}
	total := 0
	for _, bs := range owned {
		for _, b := range bs {
			for _, q := range b.Items {
				seen[q.ID]++
				total++
			}
		}
	}
	for _, b := range queue {
		for _, q := range b.Items {
			seen[q.ID]++
			total++
		}
	}
	if total != len(qs) {
		t.Errorf("计划覆盖 %d 题，原 %d 题", total, len(qs))
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("题 %d 出现 %d 次", id, c)
		}
	}
}

// 队列与归属桶都按大小降序：先派大桶，减少尾部空转。
func TestWorkPlanOrdersBigBucketsFirst(t *testing.T) {
	qs := mkQuestions(map[string]int{
		"资料分析|增长": 5, "言语理解与表达|片段阅读": 500, "判断推理|图形推理": 50,
	})
	owners := loadBucketOwners(filepath.Join(t.TempDir(), "o.json"))
	_, queue, _ := buildWorkPlan(BuildBuckets(qs), []Engine{{Model: "m1"}}, owners)
	if len(queue) != 3 {
		t.Fatalf("队列应有 3 个桶，得到 %d", len(queue))
	}
	for i := 1; i < len(queue); i++ {
		if len(queue[i-1].Items) < len(queue[i].Items) {
			t.Errorf("队列未按大小降序：第 %d 个 %d 题 > 第 %d 个 %d 题",
				i-1, len(queue[i-1].Items), i, len(queue[i].Items))
		}
	}
}
