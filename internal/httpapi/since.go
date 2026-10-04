package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// strictDateLayout 是 `since` 与作品发行日期共用的唯一形态。
//
// 用 time.Parse 而不是正则：它顺带把 2026-13-45 这类不存在的日期也挡掉。
const strictDateLayout = "2006-01-02"

// sinceBound 是一个**已经校验过**的 `since` 下界。零值（空串）表示「不过滤」。
//
// 做成类型而不是裸字符串，是为了让「拿着未校验的值直接比」在类型上就不成立。
// 未校验的字符串按**字序**比较，坏起来全是静默的（实测）：
//
//	since=2026-1-1             丢掉 1–9 月
//	since=hello                只剩 1 部（等同空 feed）
//	since=2026-01-01T00:00:00Z 丢掉当天发行的作品（日期是它的前缀）
type sinceBound string

// parseSince 校验并归一化 `since`。
//
// year / month 是这条路由上**有效**的上游范围筛选（女优只认 year，全站 year/month，
// 清单两者都不是），空串表示这条路由没有这个维度。它们由调用方显式传入而不是
// 装满一个 map：调用点一眼就能看出这条路由到底认哪几个。
//
// 两道闸，顺序是有意的 —— 参数之间**打架**比参数**写错**更值得先说：
//
//  1. 与上游的「范围」维度互斥。year（女优、全站）与 month（全站）是上游筛选
//     （整个年份 / 整个月份），since 是本地的「这个日期起」，同时发的结果
//     必然是空 feed —— 而「选了年份反而是空的」会让人以为功能坏了。
//     清单路由没有这一项：`year` 在更下层（掩码构造）就被拒绝了。
//  2. 形态必须是**严格的** YYYY-MM-DD，见 sinceBound。
func parseSince(raw, year, month string) (sinceBound, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}

	// 固定 year → month 的顺序，让文案（和钉住文案的测试）稳定。
	var given []string
	for _, kv := range [][2]string{{"year", year}, {"month", month}} {
		if v := strings.TrimSpace(kv[1]); v != "" {
			given = append(given, kv[0]+"="+v)
		}
	}
	if len(given) > 0 {
		return "", fmt.Errorf("%w：%s 与 since=%s 不能同时给："+
			"since 是本服务的本地过滤（「这个日期起」），而 year / month 是上游筛选"+
			"（整个年份 / 整个月份）—— 两个一起发的结果一定是空 feed。"+
			"想要某一整年 / 整月就用 year / month，想要「从某天起」就用 since",
			catalog.ErrBadRequest, strings.Join(given, "、"), s)
	}

	if _, err := time.Parse(strictDateLayout, s); err != nil {
		return "", fmt.Errorf("%w：since=%q 必须是严格的 YYYY-MM-DD（例如 2026-01-01）。"+
			"这里按**字符串字序**比较这个日期，写宽一点不会报错，只会静默丢掉一整段："+
			"since=2026-1-1 会丢掉 1–9 月，since=2026-01-01T00:00:00Z 会丢掉当天发行的作品",
			catalog.ErrBadRequest, s)
	}
	return sinceBound(s), nil
}

// isStrictDate 报告一个非空字符串是不是严格的 YYYY-MM-DD。
//
// 作品发行日期用的是同一把尺子：形状不对的**一律保留**（见 filter），
// 因此这里必须与 parseSince 判得一样严 —— 否则会出现「since 合法、
// 作品的日期却被当成可比较」的错配，而那正是静默丢数据的那一类。
func isStrictDate(s string) bool {
	_, err := time.Parse(strictDateLayout, s)
	return err == nil
}

// applySince 是三条 feed 路由取数之后的**同一个**收尾步骤。
//
// 之所以收成一个方法而不是在三处各写一行：那三行里包含「窗口是否取满」这个判断，
// 而任何一条路由漏掉它的表现都是**静默地少一条告警** —— 看不见的那种不一致。
func (s *Server) applySince(bound sinceBound, works []catalog.Work, r *http.Request, values url.Values) []catalog.Work {
	if bound == "" {
		return works
	}
	return bound.filter(s.log, works, sinceEnv{
		// path 就是订阅身份：告警必须能指到具体是哪个 feed（去重之后尤其重要）。
		where: r.URL.Path,
		// pages 归一化成**生效值**（缺省 1、越界夹住），而不是原文：
		// 日志里写 pages="" 读起来像「没分页」，而键也该把 `?pages=1` 与
		// 不传 pages 视作同一份配置 —— 它们确实是。
		pages:  strconv.Itoa(catalog.PageCount(values)),
		full:   catalog.WindowFull(len(works), values),
		warned: &s.sinceWarned,
	})
}

// sinceEnv 是一次 since 过滤的「环境」（与作品本身无关的那几样）。
//
// 收成一个结构体而不是给 filter 挂四个参数：它们总是同时出现，散成参数就是
// 一处数据泥团，而且调用点会长到看不出谁是谁。
//
// full 由调用方给（见 catalog.WindowFull）：它回答「不能证明上游已经到底」，
// 而不是「一定还有更旧的」—— 后者只有 appapi 知道，且我们不猜。
type sinceEnv struct {
	where  string // 订阅身份（URL path）
	pages  string // 生效的页数（归一化过的），用于文案与去重键
	full   bool   // 取数窗是否取满
	warned *warnOnce
}

// warnOnce 让「对某个订阅会**永远为真**的告警」每个进程只报一次。
//
// 为什么需要它：下面两条告警报的是**配置的性质**，不是故障 —— 同一个 URL 每次轮询
// 都会命中。qBittorrent 默认 15 分钟一次，于是真话变成 96 条/天，
// 而噪声的代价很具体：真出问题时没人再看 WARN。
//
// 实测撑住了这个判断（2026-10-05，真实上游）：`/rss/tags/0.xml?since=<今天>&pages=1`
// 只给 50 部，而 `pages=2` 给 100 部且**全部满足 since** —— 也就是 pages=1
// 真的少给了 50 部符合条件的作品。因此这里的结论是「保留告警、杀掉重复」，
// 而不是降级或加一个「明显错配」阈值（那会把这个真警报一起闷掉）。
//
// 它是**可丢弃的观测状态**（丢了只意味着重启后再提醒一次），不是领域状态 ——
// 本服务的领域状态仍然只有 pin 那一个例外。与 health.Tracker 的
// 「重复失败不重复刷屏」是同一类东西。
type warnOnce struct {
	mu   sync.Mutex
	seen map[string]bool
}

// warnOnceMaxKeys 是去重表的键数上限，防止它无限长。
//
// 键空间是 path × since × pages，而 path 与 since 都写在一个用户可以自己拼的
// URL 里 —— 也就是可枚举的。上限取 1024：一个实例常见的订阅数是个位数到百位级
// （`/collected` 实测 144 位女优，全订也就 144 个 path），1024 是很宽的余量。
const warnOnceMaxKeys = 1024

// first 报告这个键是不是第一次出现。
func (w *warnOnce) first(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen == nil {
		w.seen = make(map[string]bool)
	}
	if w.seen[key] {
		return false
	}
	if len(w.seen) >= warnOnceMaxKeys {
		// 清空而不是逐个淘汰：它只影响「还会不会再提醒一次」，
		// 最坏的结果是多提醒几次 —— 比让一个可枚举的键空间无限长要好。
		w.seen = make(map[string]bool)
	}
	w.seen[key] = true
	return true
}

// filter 只保留 release_date 不早于 since 的作品，并把「发生了什么」如实说清。
//
// # 为什么比的是作品的发行日期（ticket 10 的定稿）
//
// 用户的原话是「我已经有这个人的所有作品了，只需要追新就行了」—— 在有全量旧作
// 的前提下，「新」= **这部作品**是新的。否掉「比被选中磁链的 created_at」是实测
// 结论（见 notes/api-recon.md §10.4.2）：磁链的 created_at 普遍早于发售日
// （-5 天很常见；新上架的合集甚至复用六年前的磁链），按它过滤在最近 30 天的窗口里
// 一条不剩 —— 把新作品本身筛掉了。而它想救的「旧作品被补上新磁链」结构上不可达：
// 取数是按 release_date 倒序分页的，一部 2020 年的作品根本不在第 1 页。
//
// # 三条刻意的取舍
//
//  1. **闭区间** `>=`。订阅链接生成器的追新模式发的就是 `since=<今天>`，
//     开区间会把当天发行的作品挡在外面 —— 「只追新」反而丢掉最新的那批。
//
//  2. **形状不对或为空的一律保留**，且与「丢弃」分开计数：上游字段一旦变格式，
//     「按错误规则丢弃数据」比「多给几条」危险得多，后者用户看得见。
//
//  3. **只报异常**：正常路径一条 Debug。qBittorrent 每 15 分钟轮询一次，
//     每条都 WARN 只会把日志淹掉，真出问题时反而没人看。
//
//  4. **两条告警每条订阅只报一次**（见 warnOnce）：它们说的是配置的性质，
//     每轮都真、因而每轮都重复。
func (b sinceBound) filter(log *slog.Logger, works []catalog.Work, env sinceEnv) []catalog.Work {
	if b == "" {
		return works
	}
	since := string(b)

	out := make([]catalog.Work, 0, len(works))
	var (
		dropped    int
		noDate     int
		badShape   int
		withMagnet int
		keptMagnet int
		// oldest 是取数窗里最旧的一条**可比较**日期。它回答的是
		// 「窗口有没有走到 since 那一边」，因此必须看全部入参，而不只是留下的那批。
		oldest string
	)
	for _, w := range works {
		_, hasMagnet := catalog.Select(w.Magnets)
		if hasMagnet {
			withMagnet++
		}

		d := strings.TrimSpace(w.ReleaseDate)
		switch {
		case d == "":
			noDate++
		case !isStrictDate(d):
			badShape++
		default:
			if oldest == "" || d < oldest {
				oldest = d
			}
			if d < since {
				dropped++
				continue
			}
		}
		out = append(out, w)
		if hasMagnet {
			keptMagnet++
		}
	}

	// 作品数与「真的会出现在 feed 里」的条目数是两回事：没有磁链候选的作品，
	// feed.Build 会跳过（上游常见状态，不是错误）。实测第 1 页 50 部里有 33 部
	// 没有磁链候选 —— 只报作品数会让这句数字骗人。
	log.Debug("since 过滤",
		"订阅", env.where, "since", since, "比较字段", "release_date",
		"取到", len(works), "有磁链候选", withMagnet,
		"保留", len(out), "保留且能成条目", keptMagnet, "丢弃", dropped,
		"缺发行日期", noDate, "发行日期形状不对", badShape)

	// 异常一：合法的 since 却把本轮作品一条不剩地筛掉了。
	// 它在 `since` 手滑写成未来某天时就会出现，而那时用户只看得到一个空 feed。
	//
	// 与「上游本就没返回作品」分开：后者不是因为 since，说成「被 since 筛掉了」
	// 会把排查指错方向（那种情况下 Debug 行里的「取到=0」才是线索）。
	if len(out) == 0 && len(works) > 0 && env.warned.first(env.where+"|empty|"+since) {
		log.Warn("since 把本轮作品一条都没剩",
			"订阅", env.where, "since", since, "取到", len(works), "丢弃", dropped,
			"缺发行日期", noDate, "发行日期形状不对", badShape,
			"提示", "若这不是你要的结果，检查 since 是否写成了未来的日期（同一条只报一次）")
	}

	// 异常二：取数窗取满了，而最旧一条仍**晚于** since —— 下界不完整。
	//
	// 「取满了」与「上游到底了」是两回事，这里只报前者能证明的那个结论。
	// 边界是严格大于：最旧一条正好等于 since 时，未取到的那些作品按倒序只会更旧，
	// 因此它们都不可能满足 since —— 下界其实是完整的，不该报警。
	if env.full && (oldest == "" || oldest > since) &&
		env.warned.first(env.where+"|window|"+since+"|"+env.pages) {
		log.Warn("取数窗没走到 since：这次拿到的下界不完整",
			"订阅", env.where, "since", since, "取到", len(works),
			"最旧可比较的发行日期", oldest, "pages", env.pages,
			"提示", "本轮只取了 pages 指定的页数，更旧的作品里可能还有符合 since 的；"+
				"要更深的历史请加大 pages（上限 20）。同一条只报一次")
	}
	return out
}
