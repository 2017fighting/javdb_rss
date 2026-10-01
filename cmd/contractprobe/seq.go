package main

import (
	"sort"
	"strings"
)

// 本文件是探针里的**纯算法**：序列比较、集合运算、组合枚举。
//
// 单独成文件是因为它们与「发请求、打表格」没有关系，而且是最容易悄悄写错、
// 错了之后**结论会看起来完全正常**的那一类（见 compare_test.go 里每条测试的说明）。
// 放在一堆网络 I/O 中间，读的人会以为它们只是实现细节。

// ============================ 纯函数（有单测） ============================

// sameSeq 报告两个 id 序列是否**逐位相同**（长度、内容、顺序）。
func sameSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// diffStat 比较两个序列，返回首个不同的下标（-1 表示逐位相同）、
// 逐位比较中不同的位数，以及两者的集合是否相同。
//
// 三个量一起返回，是因为它们回答的是不同的问题：
// 「同序」说明参数没生效；「同集合但不同序」说明只改了排序；
// 「集合也不同」说明真的筛掉了东西。混成一个布尔会把结论说糊。
func diffStat(base, got []string) (firstDiff, diffCount int, sameSet bool) {
	firstDiff = -1
	n := len(base)
	if len(got) < n {
		n = len(got)
	}
	for i := 0; i < n; i++ {
		if base[i] != got[i] {
			diffCount++
			if firstDiff < 0 {
				firstDiff = i
			}
		}
	}
	// 长度不同时，多出来的部分也算「不同」。
	if len(base) != len(got) {
		diffCount += abs(len(base) - len(got))
		if firstDiff < 0 {
			firstDiff = n
		}
	}
	return firstDiff, diffCount, sameElements(base, got)
}

// sameElements 报告两个序列是否含同一组元素（不看顺序、不看重数）。
func sameElements(a, b []string) bool {
	sa, sb := setOf(a), setOf(b)
	if len(sa) != len(sb) {
		return false
	}
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
}

func setOf(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// intersectSeq 返回「在 a 里且也在 b 里」的元素，**保留 a 的顺序**。
//
// 保留 a 的顺序是有意的：组合的期望结果应当与基线同序，
// 若交集本身被重排，我们就分不清「组合筛错了」与「交集函数写错了」。
func intersectSeq(a, b []string) []string {
	bs := setOf(b)
	var out []string
	for _, id := range a {
		if bs[id] {
			out = append(out, id)
		}
	}
	return out
}

// unionSeq 把若干字母对应的单属性序列并起来，按 letters 的顺序、保留各自出现顺序。
func unionSeq(singles map[string][]string, letters []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range letters {
		for _, id := range singles[l] {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// subsets 返回给定字母的全部非空子集，按「先短后长、同长按字典序」排列。
//
// 顺序固定是为了让两次运行的输出能逐行对照 —— 复勘工具的输出会被贴进笔记，
// 排列不确定的话就没法比较两次复勘。
func subsets(letters []string) [][]string {
	sorted := append([]string(nil), letters...)
	sort.Strings(sorted)
	var out [][]string
	for mask := 1; mask < 1<<len(sorted); mask++ {
		var c []string
		for i, l := range sorted {
			if mask&(1<<i) != 0 {
				c = append(c, l)
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return strings.Join(out[i], ",") < strings.Join(out[j], ",")
	})
	return out
}

// orderedPairs 返回全部二元有序组合，用于「顺序无关」的正反对照。
func orderedPairs(letters []string) [][]string {
	sorted := append([]string(nil), letters...)
	sort.Strings(sorted)
	var out [][]string
	for i := range sorted {
		for j := range sorted {
			if i == j {
				continue
			}
			out = append(out, []string{sorted[i], sorted[j]})
		}
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// isSubset 报告 a 的每个元素是否都在 b 里。
func isSubset(a, b []string) bool {
	bs := setOf(b)
	for _, x := range a {
		if !bs[x] {
			return false
		}
	}
	return true
}

// commonCount 是两序列按集合计的共同元素个数，前面带“/len(base)”更好读。
func commonCount(base, got []string) int {
	bs := setOf(base)
	n := 0
	for id := range setOf(got) {
		if bs[id] {
			n++
		}
	}
	return n
}
