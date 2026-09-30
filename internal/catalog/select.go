package catalog

// Select 从一部作品的磁链候选里选出唯一一条，即 feed 里该作品最终呈现的那条。
//
// 规则由 ticket 04 定下、ticket 09 精确到可测：
//
//   - 候选里有中文字幕（cnsub=true）的 → 取 created_at **最新**的那条中文字幕
//   - 否则                              → 取 created_at **最新**的那条候选
//   - created_at 相同（同日或都为空）    → infohash 字典序（升序）定序
//   - 候选为空                          → 第二返回值 false
//
// **不读取切片顺序。** 上游的 `magnets[]` 既不是时间序、也不可用任何字段重放
// （ticket 04 实测：211 部作品，见 notes/api-recon.md §10.4.1），因此
// 「顺序无关的确定性规则」是钉住（pin）能给出稳定 guid 的前提。
//
// 注意这不是「挑最优」：本函数不在体积、分辨率、做种数之间做任何权衡。
func Select(magnets []Magnet) (Magnet, bool) {
	if len(magnets) == 0 {
		return Magnet{}, false
	}
	best := magnets[0]
	for _, cand := range magnets[1:] {
		if betterMagnet(cand, best) {
			best = cand
		}
	}
	return best, true
}

// betterMagnet 判断 a 是否应当取代当前的 best。
//
// 排序键依次是：
//
//  1. cnsub 优先 —— 有中文字幕的永远胜过没有，与 created_at 无关
//  2. created_at 较新
//  3. created_at 相同 → infohash 字典序较小
func betterMagnet(a, b Magnet) bool {
	if a.CNSub != b.CNSub {
		return a.CNSub
	}
	if c := CompareCreatedAt(a.CreatedAt, b.CreatedAt); c != 0 {
		return c > 0
	}
	return a.Infohash < b.Infohash
}
