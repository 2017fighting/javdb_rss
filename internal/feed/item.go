// Package feed 把领域模型渲染成 RSS 2.0。
//
// 这一层是纯函数式的：给定一组作品和「现在几点」，产出一段确定的 XML。
// 它不发请求、不读配置、不碰磁盘 —— 因此可以用表驱动测试把一个字节一个字节钉住。
package feed

import (
	"fmt"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// Item 是 feed 里的一条条目：一部作品 + 它被选中的那一条磁链。
//
// 一部作品对应恰好一条 Item —— 这是 ticket 08 定下的「中文字幕优先、每部作品恒发 1 条」。
type Item struct {
	Work   catalog.Work
	Magnet catalog.Magnet
}

// MagnetURI 构造这条 Item 的磁力链接。
//
// dn 参数用与标题相同的基名，好处是 qBittorrent 会拿它给下载任务命名，
// 而不是显示一个光秃秃的 infohash。注意 dn 只影响显示 —— 身份仍然是 infohash（见 guid 规则）。
func (i Item) MagnetURI() string {
	return fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s",
		i.Magnet.Infohash, urlEncode(baseName(i)))
}

// GUID 是这条 Item 的稳定身份标识 —— **纯 infohash**。
//
// 这是 ticket 08 里最要紧的一条决定，也是 qBittorrent 去重是否可靠的根据：
//
//   - 内容相同即 guid 相同，于是同一部作品同时出现在番号订阅与女优订阅里时，
//     qBittorrent 只会下载一次；
//   - 洗版会产生新 infohash，于是 guid 变化、自动重新下载；
//   - guid 完全由内容决定，**不含时间戳、不含番号、不含槽位**，
//     因此进程重启、配置调整都不会改变它。
//
// 反过来说：任何把番号或「普通/中文字幕」槽位掺进 guid 的做法都会破坏第一条性质，
// 并可能在磁链重合时产生两条 guid 指向同一个 infohash，让客户端重复下载。
func (i Item) GUID() string { return i.Magnet.Infohash }

// Title 是条目在 RSS 阅读器（含 qBittorrent 的 RSS 列表）里显示的名字。
//
// 字幕版加「中文字幕 ·」前缀，让它在列表里一眼可辨。
//
// 基名的选择：优先作品标题，作品标题为空时退化为磁链显示名。
// 这是对 ticket 08 措辞的一处修订 —— 该票写的是「基名 = 磁链显示名」，
// 但实测 App API 返回的磁链 name 通常就是番号本身（如 "KV-328"），
// 直接用会渲染成 `[KV-328] KV-328` 这样的重复。作品标题才是有信息量的那个。
func (i Item) Title() string {
	base := baseName(i)
	if i.Magnet.CNSub {
		return fmt.Sprintf("[%s] 中文字幕 · %s", i.Work.Number, base)
	}
	return fmt.Sprintf("[%s] %s", i.Work.Number, base)
}

// baseName 是标题与 dn 共用的基名。作品标题为空时退化为磁链显示名，
// 两者都为空时退化为 infohash，保证任何情况下都不产出空标题。
func baseName(i Item) string {
	if t := strings.TrimSpace(i.Work.Title); t != "" {
		return t
	}
	if n := strings.TrimSpace(i.Magnet.Name); n != "" {
		return n
	}
	return i.Magnet.Infohash
}

// Build 把作品列表转成 feed 条目。
//
// 每一步都运用 catalog.Select 的槽位规则；没有磁链候选的作品被**跳过**
// （尚无磁链候选是常见状态，不该让整条 feed 失败，也不该产出没有 enclosure 的条目）。
// 输入顺序被保留 —— 呈现顺序属于上游。
func Build(works []catalog.Work) []Item {
	items := make([]Item, 0, len(works))
	for _, w := range works {
		m, ok := catalog.Select(w.Magnets)
		if !ok {
			continue
		}
		items = append(items, Item{Work: w, Magnet: m})
	}
	return items
}
