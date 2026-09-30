package distill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// 桶级动态领取。
//
// 静态按题数均分有个明显缺陷：它假设各模型吞吐相同。实测不是——
// verify-300 里 deepseek-v4-flash 只完成 48/102 时，另两个模型已经跑完 96/99 和
// 90/99，慢的那个成了整个批次的关键路径（预计拖长 45 分钟）。而配额状况还是
// 随时间变化的，事先根本测不准。
//
// 所以改成：桶放进一个共享队列，哪个执行器空就领下一个。快模型自然做得多。
//
// **但动态分配会破坏跨批次的"同一题型一个模型"不变量**：批次中断后续跑时，
// 某个桶可能已被 A 做了一部分，剩下的被 B 领走，桶内措辞就漂移了。
// 因此加一层归属粘滞：领取时把「桶 → 模型」落盘，续跑时已归属的桶回到原模型，
// 只有从未被领取过的桶才进共享队列。这样既适应吞吐差异，又保持桶内一致。

// bucketKey 是桶的稳定标识，用于落盘记录归属。
func bucketKey(module, tag string) string { return module + "\x1f" + tag }

// Key 返回桶的稳定标识。
func (b *Bucket) Key() string { return bucketKey(b.Module, b.Tag) }

// displayKey 用于人读的输出（日志、报告）。
func (b *Bucket) displayKey() string { return b.Module + "/" + b.Tag }

// Label 返回给界面/CLI 看的桶描述，形如「言语理解与表达/逻辑填空(6833 题)」。
func (b *Bucket) Label() string {
	return b.displayKey() + "(" + strconv.Itoa(len(b.Items)) + " 题)"
}

// bucketOwners 记录桶到执行器的归属，并保证并发读写安全。
type bucketOwners struct {
	mu   sync.Mutex
	path string
	m    map[string]string
}

func loadBucketOwners(path string) *bucketOwners {
	o := &bucketOwners{path: path, m: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return o
	}
	_ = json.Unmarshal(b, &o.m)
	return o
}

// owner 查桶的原归属。
func (o *bucketOwners) owner(key string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.m[key]
	return v, ok
}

// claim 记录归属并立即落盘。**必须立即落盘**：进程被杀时内存里的归属会丢，
// 续跑就失去了粘滞保证，而"没落盘的归属"等于没归属。
func (o *bucketOwners) claim(key, engine string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.m[key] = engine
	b, err := json.MarshalIndent(o.m, "", "  ")
	if err != nil {
		return
	}
	// 先写临时文件再改名，避免读到写了一半的内容
	tmp := o.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, o.path)
}

// buildWorkPlan 把桶分给各执行器：
//   - 已归属的桶回到原执行器（先做）
//   - 未归属的桶进共享队列（后做，谁空谁领）
//
// 返回每个执行器的「已归属桶」列表，以及共享队列。
func buildWorkPlan(buckets []*Bucket, engines []Engine, owners *bucketOwners) (
	owned [][]*Bucket, queue []*Bucket, reassigned int) {

	idx := map[string]int{}
	for i, e := range engines {
		idx[e.Model] = i
	}
	owned = make([][]*Bucket, len(engines))

	for _, b := range buckets {
		eng, ok := owners.owner(b.Key())
		if !ok {
			queue = append(queue, b)
			continue
		}
		i, exists := idx[eng]
		if !exists {
			// 原执行器不在本次列表里（比如它被限流下线了）。
			// 这里选择**重新分配**而不是放弃这些桶：桶内一致性会被破坏，
			// 但 label.model 记录了每条标注的实际产地，可追溯也能事后过滤。
			reassigned++
			queue = append(queue, b)
			continue
		}
		owned[i] = append(owned[i], b)
	}

	SortBucketsBySize(queue)
	for i := range owned {
		SortBucketsBySize(owned[i])
	}
	return owned, queue, reassigned
}

// SortBucketsBySize 按题目数降序排桶（同大小按键名，保证顺序可复现）。
//
// 大桶优先的意义：队列开头放最长任务能减少尾部空转——最后一个开始的大桶才是
// 决定总时长的那个。CLI 的预览也用它，让展示顺序与实际领取顺序一致。
func SortBucketsBySize(bs []*Bucket) {
	sort.SliceStable(bs, func(i, j int) bool {
		if len(bs[i].Items) != len(bs[j].Items) {
			return len(bs[i].Items) > len(bs[j].Items)
		}
		return bs[i].Key() < bs[j].Key()
	})
}

// BucketOwnerPath 返回归属文件的路径（放在批次目录下，随批次走）。
func BucketOwnerPath(runDir, runID string) string {
	return filepath.Join(runDir, runID, "bucket_owner.json")
}

// summarizeBuckets 拼一句人类可读的桶列表，用于预览与日志。
func summarizeBuckets(bs []*Bucket, limit int) string {
	parts := make([]string, 0, len(bs))
	for i, b := range bs {
		if i >= limit {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, b.displayKey()+"("+strconv.Itoa(len(b.Items))+")")
	}
	return strings.Join(parts, " ")
}
