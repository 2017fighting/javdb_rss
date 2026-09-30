package catalog

import "time"

// createdAtLayouts 是上游可能给出的 created_at 格式。
//
// 已知今天 `/movies/{id}/magnets` 给 "2006-01-02"（见 notes/api-recon.md §10.4.1
// 的更正）；更早的样本是 "01/02/2006" 且格式存疑，仍接受作为容错。
var createdAtLayouts = []string{"01/02/2006", "2006-01-02"}

// ParseCreatedAt 解析磁链创建时间。
//
// 原样保留在 Magnet.CreatedAt 里是刻意的（模型不替调用方决定语义）；
// 需要「按时间排序」的调用方用这个函数解析。
func ParseCreatedAt(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range createdAtLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// CompareCreatedAt 比较两条 created_at，返回 -1、0 或 +1。
//
// 结果刻意只有三种，保证调用方（选择规则、切换规则）能依赖它做确定性定序：
//
//	两边都能解析   → 按日期比较
//	只有一边能解析 → 能解析的算「更新」（空串/坏数据按最旧算）
//	两边都无法解析 → 0（视为同日，交给 infohash 字典序做 tie-break）
//
// 最后一条是 ticket 09 的明确定义：`created_at` 相同**含都为空/都无法解析**，
// 那时由 infohash 定序；不能拿原始字符串比较，否则两个不同的坏值会变成
// 「一旧一新」，绕过 infohash tie-break 并可能触发一次多余的 pin 切换。
func CompareCreatedAt(a, b string) int {
	ta, oka := ParseCreatedAt(a)
	tb, okb := ParseCreatedAt(b)
	switch {
	case oka && okb:
		return ta.Compare(tb)
	case oka:
		return +1
	case okb:
		return -1
	default:
		return 0
	}
}
