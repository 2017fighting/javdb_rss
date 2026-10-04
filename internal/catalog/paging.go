package catalog

import (
	"net/url"
	"strconv"
	"strings"
)

// UpstreamPageLimit 是上游每页条数的上限。
//
// 实测服务端上限就是 50（传 100/200/500 都只给 50），所以取作品时固定发 50。
const UpstreamPageLimit = 50

// MaxPages 是 `?pages=N` 的上限，防止一个 URL 把上游拖死。
//
// 一个女优约 230 部作品，5 页就够建全库；给到 20 页是很宽松的余量。
const MaxPages = 20

// PageCount 从透传参数里读页数，并夹到合理范围。
//
// # 它为什么住在 catalog 而不是某个实现里
//
// `pages` 是**跨层共享**的一个事实，现在有两个消费者：appapi 拿它决定翻几页，
// httpapi 拿它判断「取数窗是否取满」（见 WindowFull）。两处各写一份的话，
// 「夹到上限」这类规则迟早会漂 —— 而漂的表现是窗口判断算错，静默的。
// 这与 OwnParams 住在同一层的理由是一样的（见那个变量的注释）。
func PageCount(params url.Values) int {
	raw := strings.TrimSpace(params.Get("pages"))
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	if n > MaxPages {
		return MaxPages
	}
	return n
}

// WindowFull 报告「这一轮取数是否用满了请求的页数」。
//
// 它与 appapi 的「短页即到底」是**两个不同的问题**，别混：
//
//	短页即到底 —— 这一页没满，说明上游没有更多了（appapi 据此提前收工）
//	窗口取满   —— 取到的条数正好等于 页数 × 每页上限，因此**不能证明**上游已经到底
//
// 它只回答「不能证明到底」，不回答「一定还有更旧的」。调用方是 httpapi 的
// `since` 下界检查：窗口取满而最旧一部仍不早于 since 时，用户拿到的下界是
// 不完整的（更旧的作品里可能还有符合条件的），因此要提示「加大 pages」。
//
// ⚠️ **已知的漏报**：上游分页**重叠**时（appapi 按作品 id 去重），条数会低于
// 页数×上限，于是窗口明明取满却被判成「没取满」—— 少一条告警，不会多报。
// 漏报是可接受的，假报不行：一条会在数据完整时也响的告警，会让真响的那次
// 没人信（同 internal/appapi/readAllPages 里特意「多探一页」的理由）。
func WindowFull(workCount int, params url.Values) bool {
	if workCount <= 0 {
		return false
	}
	return workCount >= PageCount(params)*UpstreamPageLimit
}
