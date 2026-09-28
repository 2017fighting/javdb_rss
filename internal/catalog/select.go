package catalog

// Select 从一部作品的磁链候选里选出唯一一条，即 feed 里该作品最终呈现的那条。
//
// 规则由 ticket 08 定下 —— **字幕优先，每部作品恒发 1 条**：
//
//   - 候选里存在中文字幕的 → 取**第一条**中文字幕的
//   - 否则                 → 取**第一条**
//   - 候选为空             → 第二返回值 false
//
// 两条边界由上面的措辞直接决定，但值得写明：
//
//  1. 「第一条」是切片顺序，不做任何重排 —— 排序规则属于上游。
//  2. 若 magnets[0] 本身就是中文字幕版，两个分支指向同一条，因此仍然只选出一条。
//     这是选择「字幕优先单条」而非「普通 + 字幕两条」所消掉的边界情况
//     （见 ticket 08：两条方案在这个场景下会产生两条 guid 指向同一个 infohash，
//     导致 qBittorrent 重复下载同一份内容）。
//
// 注意这不是「挑最优」：本函数不在体积、做种数、分辨率之间做任何权衡。
// 那些判断属于上游的排序，本服务只负责按既定规则取一条。
func Select(magnets []Magnet) (Magnet, bool) {
	if len(magnets) == 0 {
		return Magnet{}, false
	}
	for _, m := range magnets {
		if m.CNSub {
			return m, true
		}
	}
	return magnets[0], true
}
