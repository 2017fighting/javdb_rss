// Package httpapi 暴露 RSS 路由。
//
// 它只依赖 catalog.Source 这个端口，不关心数据从哪来 ——
// 真实 App API、缓存装饰器、测试假实现都可以从外面塞进来。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/config"
	"github.com/2017fighting/javdb_rss/internal/feed"
	"github.com/2017fighting/javdb_rss/internal/health"
)

// Version 是 /version 端点报告的服务版本，构建时用 -ldflags 注入。
var Version = "dev"

// Server 组装路由。
type Server struct {
	cfg *config.Holder
	src catalog.Source
	log *slog.Logger
	// upstream 是可选的上游健康跟踪器。为 nil 时 /readyz 恒为就绪
	// （没有上游要检查，例如 provider=stub）。
	upstream *health.Tracker
	// now 可在测试里替换，让 pubDate 与 lastBuildDate 可确定。
	now func() time.Time
}

// New 构造 Server。log 为 nil 时使用 slog.Default()。
func New(cfg *config.Holder, src catalog.Source, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, src: src, log: log, now: time.Now}
}

// WithUpstream 挂上上游健康跟踪器，启用 /readyz 与 /healthz/upstream。
func (s *Server) WithUpstream(t *health.Tracker) *Server {
	s.upstream = t
	return s
}

// Handler 返回完整的 HTTP 处理器。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 发现端点 —— **不是 feed**。刻意放在 /rss/ 之外、也不带 .xml，
	// 因为它的产物是给人看的清单（拿去填配置或 URL），qBittorrent 不会碰它。
	mux.HandleFunc("GET /collected", s.handleCollected)
	// 清单的发现端点。它与 /collected 同形，但数据源**不同一个概念**：
	// /collected 是「你收藏的女优」，这里是「你建的清单」。
	mux.HandleFunc("GET /collected_lists", s.handleCollectedLists)

	// 用前缀匹配而不是 {code} 通配符：Go 的 ServeMux 要求通配符占满整个
	// 路径段，而我们要容忍结尾的 .xml，因此在这里自己剥。
	mux.HandleFunc("GET /rss/code/", s.handleCode)
	mux.HandleFunc("GET /rss/actress/", s.handleActress)
	mux.HandleFunc("GET /rss/list/", s.handleList)

	// 「想看」 feed。它是一张**固定路径**的列表 feed（清单内容由 App 里的标记
	// 决定，不在 URL 里），因此用精确路径注册而不是前缀剥尾。
	//
	// 它刻意**不受 feeds 白名单约束**：那份白名单描述的是「订阅」
	// （你要订哪些番号/女优），而这张清单的边界由你在 App 里画 ——
	// 一个会变的列表放不进配置。
	mux.HandleFunc("GET /rss/want.xml", s.handleWant)

	// 健康检查分两层，分别对应 k8s 的两种探针。这个区分很重要：
	//
	//   /healthz          存活探针。进程还在就 200。
	//                     **绝不能**掺入上游状态 —— 签名失效重启一千次也没用，
	//                     liveness 失败会导致重启循环。
	//   /readyz           就绪探针。上游签名坏了就 503，把实例从 Service
	//                     endpoints 里摘掉，但**不重启**。
	//   /healthz/upstream 详情 JSON，给 k8s CronJob 或告警系统抓。
	mux.HandleFunc("GET /healthz", s.handleLiveness)
	mux.HandleFunc("GET /readyz", s.handleReadiness)
	mux.HandleFunc("GET /healthz/upstream", s.handleUpstreamDetail)
	mux.HandleFunc("GET /version", s.handleVersion)

	return mux
}

// handleLiveness 只回答「进程还活着吗」。永远 200。
func (s *Server) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleReadiness 回答「现在能不能服务有效内容」。
//
// 三种情况：
//
//	没有上游（provider=stub）  → 200，没有可坏的依赖
//	尚未检查过                    → 200，探针刚启动时跑，窗口极小；
//	                               此处返回 503 会让启动过程莫名奇妙失败
//	检查过且失败                  → 503
func (s *Server) handleReadiness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	st, known := s.upstreamStatus()
	if s.upstream == nil || !known || st.OK {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("上游不可用: " + st.Err + "\n"))
}

// handleUpstreamDetail 输出机读的详情，供 CronJob / 告警规则判断。
func (s *Server) handleUpstreamDetail(w http.ResponseWriter, _ *http.Request) {
	st, known := s.upstreamStatus()

	body := map[string]any{
		"checked": known,
		"ok":      st.OK,
	}
	if known {
		body["checked_at"] = st.CheckedAt.UTC().Format(time.RFC3339)
		body["latency_ms"] = st.Latency.Milliseconds()
		if st.Action != "" {
			// 上游报告的错误名，供告警规则做精确匹配。
			body["action"] = st.Action
		}
		if st.Err != "" {
			body["error"] = st.Err
		}
		if st.Guidance != "" {
			// 处置动作也透出给机读的消费方（CronJob / 告警规则）。
			// 它由检查方提供，因此这里只是转发 —— httpapi 同样不知道
			// 任何一个具体错误名的含义（ticket 05）。
			body["next_step"] = st.Guidance
		}
	}

	// 单独标出「需要改代码而不是重试」的那类失败，
	// 好让告警规则能直接对 signature_broken 做路由。
	code := http.StatusOK
	if known && !st.OK {
		body["signature_broken"] = appapi.IsSignatureAction(st.Action)
		code = http.StatusServiceUnavailable
	} else {
		body["signature_broken"] = false
	}

	writeJSON(w, code, body)
}

func (s *Server) upstreamStatus() (health.Status, bool) {
	if s.upstream == nil {
		return health.Status{}, false
	}
	return s.upstream.Snapshot()
}

// ---------------------------------------------------------------------------
// 收藏女优发现端点（需求 4）
// ---------------------------------------------------------------------------

// collectedEntry 是 /collected 里的一条。
type collectedEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VideosCount int    `json:"videos_count"`
	// Gender 是上游给的性别：0 = 女优，1 = 男优。
	//
	// 刻意**不带 omitempty**：0 是女优，而女优是这个端点的绝大多数 ——
	// 用 omitempty 会让最常见的那个取值从 JSON 里消失，消费方只能靠
	// 「键不在就当成 0」来猜，而那正是本服务反复要避免的隐式约定。
	Gender int `json:"gender"`
	// Feed 是可以直接拿去用的 feed 路径。
	//
	// 给出它而不是让用户自己拼：拼错了只会得到 404，而用户会以为服务坏了。
	// 测试里有一条一致性检查，保证这里给出的路径真的能访问。
	Feed string `json:"feed"`
}

// collectedBody 是 /collected 的响应体。
type collectedBody struct {
	Actresses []collectedEntry `json:"actresses"`
	// 截断信号。它们只在**确实触顶**时出现（omitempty），因此不同于
	// 一个永远为真的字段：消费方靠「键是否存在」判定，不靠值。
	//
	// 具体语义见 handleCollected。
	Truncated    bool `json:"truncated,omitempty"`
	PagesFetched int  `json:"pages_fetched,omitempty"`
	MaxPages     int  `json:"max_pages,omitempty"`
}

// handleCollected 服务 GET /collected：列出 App 里收藏的女优。
//
// 这是需求 4 的落点。本服务**不**因为你收藏了谁就自动为它建 feed ——
// 它只把列表（带现成的 feed 路径）交给你，由你决定订哪些。
// 这样既满足了「读取订阅的女优」，又不破坏已定的「URL 即订阅」形态，
// 也不引入「一条 feed 对应 N 个订阅」那个高成本形态。
//
// # 截断信号
//
// 收藏列表按页拉取，翻页有上限。**这是本服务唯一会静默少给数据的地方**，
// 因此触顶时不能照常返回一份看起来完整的列表：
//
//	truncated: true      翻页是在达到上限时停下的，这份清单已知不完整
//	pages_fetched         实际读了多少页
//	max_pages             当时生效的翻页上限
//
// 三者仅在触顶时出现。未触顶时它们**完全不存在**，消费方据此判定完整性：
// 没有 truncated 键 == 这是一份完整清单。
//
// 之所以不像「没 token」那样直接返回错误码：截断时我们手里那部分数据是
// **正确且有用**的（列表里的 feed 路径都能用），丢掉它比多给一条信号更糟。
func (s *Server) handleCollected(w http.ResponseWriter, r *http.Request) {
	col, err := s.src.CollectedActresses(r.Context())
	if err != nil {
		s.log.ErrorContext(r.Context(), "取收藏女优失败", "err", err)
		writeListError(w, err, "收藏女优")
		return
	}

	out := collectedBody{Actresses: make([]collectedEntry, 0, len(col.Actresses))}
	for _, a := range col.Actresses {
		out.Actresses = append(out.Actresses, collectedEntry{
			ID:          a.ID,
			Name:        a.Name,
			VideosCount: a.VideosCount,
			Gender:      a.Gender,
			Feed:        "/rss/actress/" + url.PathEscape(a.ID) + ".xml",
		})
	}
	if col.Truncated {
		// 同时记一条 warn：即使没人来看这个 JSON，运维也该在日志里看到
		// 「收藏已经多到读不完了」—— 它意味着要调高上限。
		s.log.WarnContext(r.Context(), "收藏女优超过翻页上限，/collected 返回的是不完整清单",
			"已读页数", col.PagesFetched, "上限", col.MaxPages,
			"已读条数", len(col.Actresses))
		out.Truncated = true
		out.PagesFetched = col.PagesFetched
		out.MaxPages = col.MaxPages
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// 清单（片单）：发现端点 + feed
// ---------------------------------------------------------------------------

// listEntry 是 /collected_lists 里的一条。
//
// 与 collectedEntry 同形，但**多两个字段**：`movies_count` 是上游声明的
// 清单长度（实测与 feed 实际条数逐位相同，因此可以拿去对账），
// `is_default` 标出账号自带的那份默认清单。
type listEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MoviesCount int    `json:"movies_count"`
	IsDefault   bool   `json:"is_default,omitempty"`
	Privacy     string `json:"privacy,omitempty"`
	// Feed 是可以直接拿去用的 feed 路径。给出它而不是让用户自己拼：
	// 拼错了只会得到 404，而用户会以为服务坏了。
	Feed string `json:"feed"`
}

type listBody struct {
	Lists []listEntry `json:"lists"`
	// 截断信号，语义与 collectedBody 完全相同：
	// **看 truncated 键存不存在，而不是看它的值**。
	Truncated    bool `json:"truncated,omitempty"`
	PagesFetched int  `json:"pages_fetched,omitempty"`
	MaxPages     int  `json:"max_pages,omitempty"`
}

// handleCollectedLists 服务 GET /collected_lists：列出你在 App 里建的清单。
//
// # 它与 /collected 不是同一张清单
//
// /collected 读的是**收藏的女优**；这里读的是**你自己建的片单**。
// 上游那个名字最像的端点（`/users/collected_lists` = 你**关注**的清单）
// 实测返回 HTTP 500，因此这里用的是 `/api/v1/lists/simple`（你**建的**清单）。
// 「建的」与「关注的」不是一回事 —— 这一条写在笔记里，路由名保持与 /collected
// 同形，因为它同样是一个发现端点。
func (s *Server) handleCollectedLists(w http.ResponseWriter, r *http.Request) {
	col, err := s.src.CollectedLists(r.Context())
	if err != nil {
		s.log.ErrorContext(r.Context(), "取清单列表失败", "err", err)
		writeListError(w, err, "清单列表")
		return
	}

	out := listBody{Lists: make([]listEntry, 0, len(col.Lists))}
	for _, l := range col.Lists {
		out.Lists = append(out.Lists, listEntry{
			ID:          l.ID,
			Name:        l.Name,
			MoviesCount: l.MoviesCount,
			IsDefault:   l.IsDefault,
			Privacy:     l.Privacy,
			Feed:        "/rss/list/" + url.PathEscape(l.ID) + ".xml",
		})
	}
	if col.Truncated {
		s.log.WarnContext(r.Context(), "清单数超过翻页上限，/collected_lists 返回的是不完整清单",
			"已读页数", col.PagesFetched, "上限", col.MaxPages, "已读条数", len(col.Lists))
		out.Truncated = true
		out.PagesFetched = col.PagesFetched
		out.MaxPages = col.MaxPages
	}
	writeJSON(w, http.StatusOK, out)
}

// handleList 服务 GET /rss/list/{id}.xml?<透传参数>&since=<日期>。
//
// 它与 handleActress 几乎逐行同形，因为在上游它们本就是同一个端点
// （`/api/v1/movies/tags` + 同一个复合掩码，只有实体字母不同）。
// 两份分开写而不是抽一个共用 handler，是因为它们的**文案与白名单不同**：
// 女优那条会说「女优」，这条会说「清单」，而把差异做成参数会让两个 handler
// 的调用点都变得难读。共用的是更下面那一层（appapi 的 entityWorks）。
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	id, ok := pathParam(r.URL.Path, "/rss/list/")
	if !ok {
		writeError(w, http.StatusNotFound, "需要形如 /rss/list/{清单 id}.xml 的路径")
		return
	}
	if !s.cfg.Current().AllowsList(id) {
		// 与番号/女优白名单一样返回 404，不向调用方确认这个清单是否「存在但被禁止」。
		writeError(w, http.StatusNotFound, "未知的订阅")
		return
	}

	query := r.URL.Query()

	// 与女优订阅同一套参数分离：自有参数（since / page / limit）不进上游，
	// `pages` 除外（它由 appapi 消费，必须流到那一层）。
	params := make(map[string]string, len(query))
	for k, vs := range query {
		if len(vs) == 0 || (catalog.IsOwnParam(k) && k != "pages") {
			continue
		}
		params[k] = vs[0]
	}

	since := strings.TrimSpace(query.Get("since"))

	// 取作品与取名字互不依赖，并行发起。理由与女优那条相同：
	// qBittorrent 会周期轮询这条 feed，串行就白多等一个往返。
	nameCh := make(chan string, 1)
	go func() {
		// ⚠️ 这里的 recover 是必需的：net/http 只为 handler 所在的那个
		// goroutine 恢复 panic，逃出那层保护会杀掉整个进程。
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("取清单名字时 panic，标题退回 id", "id", id, "panic", rec)
				nameCh <- "JavDB · " + id
			}
		}()
		nameCh <- s.listTitle(r.Context(), id)
	}()

	works, err := s.src.List(r.Context(), id, toValues(params))
	if err != nil {
		if errors.Is(err, catalog.ErrBadRequest) {
			s.log.WarnContext(r.Context(), "清单订阅参数不合法", "id", id, "err", err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.log.ErrorContext(r.Context(), "取清单作品失败", "id", id, "err", err)
		writeUpstreamError(w, err)
		return
	}

	if since != "" {
		works = filterSince(s.log, works, since)
	}

	s.renderItems(w, r, feed.Meta{
		Title:       <-nameCh,
		Link:        s.feedURL(r),
		Description: "清单 " + id + " 的订阅源",
		Language:    s.cfg.Current().Feed.Language,
	}, feed.Build(works))
}

// listTitle 拼清单 feed 的标题，能用真名字就用。
//
// 与 actressTitle 同一套非关键路径语义。一处差别：清单名可能读不到
// （`privacy: "own"` 的清单匿名会返回 NoPermission），那时退回 id。
func (s *Server) listTitle(ctx context.Context, id string) string {
	name, err := s.src.ListName(ctx, id)
	if err != nil || strings.TrimSpace(name) == "" {
		s.log.DebugContext(ctx, "取清单名字失败，标题退回 id", "id", id, "err", err)
		return "JavDB · " + id
	}
	return "JavDB · " + name
}

// writeListError 把「读一份需要 token 的 App 清单」的三种失败分开。
//
// 关键是**不能返回 200 + 空列表**：那会被理解成「你没收藏任何人」/
// 「你没标过任何想看」，而真相是「服务读不到」—— 两者的下一步动作完全不同。
//
// subject 是要给用户看的清单名（「收藏女优」/「「想看」清单」）。
// 抽成参数而不是写一份通用文案：含糊的「读取失败」会让用户不知道去 App 里看哪儿。
func writeListError(w http.ResponseWriter, err error, subject string) {
	switch {
	case errors.Is(err, catalog.ErrNoToken):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "尚未配置 token，读不到 App 里的" + subject + "。" +
				"请从 App 导出后配置 app_api.token_file（见 README）。" +
				"注意：番号订阅与女优订阅不需要 token，不受此影响。",
		})
	case appapi.IsAuthError(err):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "token 已失效，请重新从 App 导出并更新 token_file。原因：" + err.Error(),
		})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "读取" + subject + "失败：" + err.Error(),
		})
	}
}

// ---------------------------------------------------------------------------
// 「想看」 feed
// ---------------------------------------------------------------------------

// handleWant 服务 GET /rss/want.xml：把 App 里「想看」的作品渲染成 feed。
//
// # 为什么它是一条 feed，而不是像 /collected 那样的发现端点
//
// /collected 交出的是一份「你可以订什么」的清单，产物是给人看的；
// 这里是「我要什么」—— 它是一张**待办**：你在 App 里标一条，等于说
// 「这部片一有磁链就交给我」。它随上游出现磁链而自动变得可用，因此
// 天然适合交给 qBittorrent 周期轮询。两者形态不同是刻意的。
//
// # 尚无磁链的作品为什么要「说出来」，以及为什么写在**标题**里
//
// 「标了想看但还没有种」是这份清单的常态（领域定义如此），所以这类作品
// **不发条目** —— qBittorrent 用不了没有 enclosure 的条目。
// 但它们也不能静默消失：那会让你以为服务没读到这张清单，或是列表丢了。
//
// 写法上刻意不只放在 channel 描述里：实测 qBittorrent 的
// `GET /api/v2/rss/items` 响应里 feed 对象只有
// articles/hasError/isLoading/lastBuildDate/title/uid/url —— **根本没有 description**，
// 也就是说描述在 qBittorrent 里看不见，而 qBittorrent 正是这条 feed 的
// 主要消费者。计数因此写进**标题**（qbt 会呈现它），描述里同时给一句完整的话
// 给会读它的 RSS 阅读器。两处由 wantSummary 一处构造，不会各说各话。
// （对照 /collected 用 JSON 字段报截断 —— feed 里没有 JSON 字段可用。）
func (s *Server) handleWant(w http.ResponseWriter, r *http.Request) {
	list, err := s.src.WantToWatch(r.Context())
	if err != nil {
		s.log.ErrorContext(r.Context(), "取想看清单失败", "err", err)
		writeListError(w, err, "「想看」清单")
		return
	}

	// 先 build 再写 meta：那个「待磁链」数就是 build 跳过的条数，
	// 而它必须进标题。
	//
	// 刻意**不**另跑一遍 catalog.Select 去数：那会让「跳过」与「计数」
	// 各有一个执行点，而它们必须永远一致。
	items := feed.Build(list.Works)
	summary := wantSummary{List: list, Items: len(items)}

	if list.Truncated {
		// 与 /collected 一样：它不只是文案问题，运维也该在日志里看到
		// 「清单已经多到读不完了」—— 那意味着要上调上限。
		s.log.WarnContext(r.Context(), "想看清单超过翻页上限，本 feed 可能不完整",
			"已读页数", list.PagesFetched, "上限", list.MaxPages, "已读条数", len(list.Works))
	}
	if pending := summary.pending(); pending > 0 {
		// debug 级：等磁链是常态而不是故障，因此不占 warn/error。
		// 用户看的信号在标题与描述里（见下）。
		s.log.DebugContext(r.Context(), "想看清单里有尚无磁链的作品，未列入 feed",
			"想看", summary.total(), "待磁力", pending)
	}

	s.renderItems(w, r, feed.Meta{
		Title:       summary.title(),
		Link:        s.feedURL(r),
		Description: summary.description(),
		Language:    s.cfg.Current().Feed.Language,
	}, items)
}

// wantSummary 是这条 feed 对外要说的那句话，两个去处（标题 / 描述）共用一份构造。
//
// # 为什么同一件事要写两处
//
// qBittorrent **只呈现 channel 标题**（它的 RSS API 响应里根本没有 description
// 字段，见 handleWant 的注释），而完整的句子更适合 description（RSS 阅读器、
// 肉眼看 XML 的人）。两处来自同一个值，因此不会一边说 12 部、一边说 13 部。
//
// 它持着整份 WantList 而不是接过几个 int：那些数字本来就从清单上算出来，
// 提前拆成参数只会让「谁负责算」变得含糊（也就多一处能算错的地方）。
type wantSummary struct {
	List catalog.WantList
	// Items 是最终进了 feed 的条数（feed.Build 的产物长度）。
	Items int
}

// total 是「想看」清单里的作品数。
func (w wantSummary) total() int { return len(w.List.Works) }

// pending 是尚无磁链候选、因而不会出现在 feed 里的作品数。
//
// 它就是 feed.Build 跳过的那些 —— 不另外判定一遍。
func (w wantSummary) pending() int { return w.total() - w.Items }

// title 是 channel 标题：简短，适合 qBittorrent 的订阅列表。
func (w wantSummary) title() string {
	parts := []string{fmt.Sprintf("%d 部", w.total())}
	if p := w.pending(); p > 0 {
		parts = append(parts, fmt.Sprintf("%d 部待磁链", p))
	}
	if w.List.Truncated {
		parts = append(parts, "列表可能不完整")
	}
	return "JavDB · 想看（" + strings.Join(parts, " · ") + "）"
}

// description 是 channel 描述：完整句子，给会读它的人。
//
// 措辞刻意不用「失败」「错误」—— 尚无磁链是正常状态。必须说清楚的是
// **它为何没出现在下面**（未列入本 feed），否则用户只会发现条目少了。
func (w wantSummary) description() string {
	var b strings.Builder
	fmt.Fprintf(&b, "App 里「想看」的作品，共 %d 部", w.total())
	if p := w.pending(); p > 0 {
		fmt.Fprintf(&b, "；其中 %d 部尚无磁链，未列入本 feed", p)
	}
	if w.List.Truncated {
		fmt.Fprintf(&b, "；清单在读取第 %d 页时触顶，可能不完整", w.List.MaxPages)
	}
	return b.String()
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":  Version,
		"provider": s.cfg.Current().Provider,
	})
}

// handleCode 服务 GET /rss/code/{番号}.xml。
func (s *Server) handleCode(w http.ResponseWriter, r *http.Request) {
	code, ok := pathParam(r.URL.Path, "/rss/code/")
	if !ok {
		writeError(w, http.StatusNotFound, "需要形如 /rss/code/{番号}.xml 的路径")
		return
	}
	if !s.cfg.Current().AllowsCode(code) {
		// 白名单未放行时返回 404 而不是 403：不向调用方确认这个番号
		// 是否「存在但被禁止」。
		writeError(w, http.StatusNotFound, "未知的订阅")
		return
	}

	works, err := s.src.Code(r.Context(), code)
	if err != nil {
		s.log.ErrorContext(r.Context(), "取番号作品失败", "code", code, "err", err)
		writeUpstreamError(w, err)
		return
	}

	s.renderItems(w, r, feed.Meta{
		Title:       "JavDB · " + code,
		Link:        s.feedURL(r),
		Description: "番号 " + code + " 的订阅源",
		Language:    s.cfg.Current().Feed.Language,
	}, feed.Build(works))
}

// handleActress 服务 GET /rss/actress/{id}.xml?<透传参数>&since=<日期>。
func (s *Server) handleActress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathParam(r.URL.Path, "/rss/actress/")
	if !ok {
		writeError(w, http.StatusNotFound, "需要形如 /rss/actress/{id}.xml 的路径")
		return
	}

	sub, allowed := s.cfg.Current().ActressSub(id)
	if !allowed {
		writeError(w, http.StatusNotFound, "未知的订阅")
		return
	}

	query := r.URL.Query()

	// 透气参数：以白名单里配的为底，URL 上的覆盖它。
	//
	// 剥掉自有参数里**本层与无人消费**的那些（since / page / limit）——
	// 它们不该往下流，否则会污染 dedupe 的合并 key。
	//
	// `pages` 是刻意留下的例外：它是本服务自有，但由**appapi 消费**
	// （决定翻几页），因此必须流到那一层。
	params := make(map[string]string, len(sub.Params)+len(query))
	keep := func(k string) bool { return !catalog.IsOwnParam(k) || k == "pages" }
	for k, v := range sub.Params {
		if keep(k) {
			params[k] = v
		}
	}
	for k, vs := range query {
		if len(vs) == 0 || !keep(k) {
			continue
		}
		params[k] = vs[0]
	}

	since := sub.Since
	if v := strings.TrimSpace(query.Get("since")); v != "" {
		since = v
	}

	// 取作品与取名字是两次**互不依赖**的上游请求，因此并行发起。
	//
	// 串行的话每次轮询会白多等一个往返（实测单次约 200-430ms），
	// 而 qBittorrent 每 15 分钟就会打一次这个 feed —— 这是热路径上的浪费。
	//
	// 用带缓冲(1) 的 channel：即使下面提前 return（作品取失败），
	// goroutine 也能写完而不阻塞，不会泄漏。
	nameCh := make(chan string, 1)
	go func() {
		// ⚠️ 这里的 recover 是必需的，不是防御性冗余。
		//
		// net/http 只为**handler 所在的那个 goroutine** 恢复 panic。
		// 这里新起的 goroutine 逃出了那层保护 —— 它一旦 panic，
		// 杀掉的是**整个进程**，而不是这一个请求。
		// 取名是条非关键路径（失败就退回 id），不值得用它赌上整个服务。
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("取女优名字时 panic，标题退回 id",
					"id", id, "panic", r)
				nameCh <- "JavDB · " + id
			}
		}()
		nameCh <- s.actressTitle(r.Context(), id)
	}()

	works, err := s.src.Actress(r.Context(), id, toValues(params))
	if err != nil {
		// 用户参数写错（400）与上游出错（502）必须分开：
		// 前者重试无用，后者重试有用。
		if errors.Is(err, catalog.ErrBadRequest) {
			s.log.WarnContext(r.Context(), "女优订阅参数不合法", "id", id, "err", err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.log.ErrorContext(r.Context(), "取女优作品失败", "id", id, "err", err)
		writeUpstreamError(w, err)
		return
	}

	if since != "" {
		works = filterSince(s.log, works, since)
	}

	s.renderItems(w, r, feed.Meta{
		Title:       <-nameCh,
		Link:        s.feedURL(r),
		Description: "女优 " + id + " 的订阅源",
		Language:    s.cfg.Current().Feed.Language,
	}, feed.Build(works))
}

// actressTitle 拼女优 feed 的标题，能用真名字就用。
//
// 这是**刻意的非关键路径**：名字只是好看，任何失败（上游出错、没名字、
// 超时）都退回 id，绝不让取名失败把一个本来能用的 feed 弄挂。
// 因此这里的错误只记 debug 级，且不返回给调用方。
func (s *Server) actressTitle(ctx context.Context, id string) string {
	name, err := s.src.ActressName(ctx, id)
	if err != nil || strings.TrimSpace(name) == "" {
		s.log.DebugContext(ctx, "取女优名字失败，标题退回 id", "id", id, "err", err)
		return "JavDB · " + id
	}
	return "JavDB · " + name
}

// renderItems 是所有 feed 路由共用的收尾：把**已经选好的条目**写成 RSS。
//
// 选磁链（feed.Build）刻意留给调用方自己做，而不是在这里顺手做掉：
// 「想看」那条路由要拿 build 的**产物**算一个数字（有几部作品被跳过了），
// 而那个数字必须写进 channel 标题 —— 也就是必须在 meta 之前就算出来。
// 在这里再 build 一次的话，同一道判据就有了两个执行点，而它们必须永远一致。
//
// feed 是**请求时现算**的。这不是最终形态 —— ticket 09 会决定要不要在
// catalog.Source 外面包一层缓存或后台刷新。之所以现在不提前决定，
// 是因为成本量级还没量出来；而选择「现算 + Source 作为唯一端口」的好处是，
// 将来加缓存只需要包一层装饰器，这里一行都不用改。
func (s *Server) renderItems(w http.ResponseWriter, r *http.Request, meta feed.Meta, items []feed.Item) {

	// 曾经这里有一段「上游已知损坏时把警告写进 channel 描述」，已删除：
	//
	// 它是**不可达的死代码**。上游真坏掉时，上面的 s.src.Code(...) 会先返回
	// 错误并让本函数根本不被调用（直接 502）—— 所以那段描述永远渲染不出来。
	// 而它当初「测试通过」只是因为 httpapi 的测试用的是 stub 数据源，
	// stub 永远返回成功，于是走到了这里。
	//
	// 删掉而不是留着：它只在「探针失败但请求成功」这种短暂网络抖动下会出现，
	// 那时给用户一条「上游已停更」的假警告，比不警告更糟。
	// 上游真坏时的可见性是靠 502 + /readyz 503 + CronJob 告警三者共同保证的。

	w.Header().Set("content-type", "application/rss+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := feed.Render(w, meta, items, s.now()); err != nil {
		// 响应头已经发出，无法再改状态码；只能记录。
		s.log.ErrorContext(r.Context(), "渲染 RSS 失败", "err", err)
	}
}

// feedURL 尽力重建本 feed 的对外地址，用作 channel 的 <link>。
func (s *Server) feedURL(r *http.Request) string {
	if base := strings.TrimRight(s.cfg.Current().Feed.BaseURL, "/"); base != "" {
		return base + r.URL.RequestURI()
	}
	return r.URL.RequestURI()
}

// pathParam 从 path 里剥出前缀之后、可选的 .xml 后缀之前的部分。
func pathParam(path, prefix string) (string, bool) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path { // 前缀不匹配
		return "", false
	}
	rest = strings.TrimSuffix(rest, ".xml")
	rest = strings.Trim(rest, "/")
	// 番号与女优 id 都是单段标识；出现斜杠说明 URL 结构不对。
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

// filterSince 只保留 release_date 不早于 since 的作品。
//
// 语义是**临时的**：ticket 09 尚未决定「只追新」应当拿发行日期还是上架时间比较。
// 这里按发行日期实现，因为用户的原话是「我已经有这个人的所有作品了，
// 只需要追新就行了」——在有全量旧作的前提下，发行日期正是「新」的含义。
//
// 两条刻意的取舍：
//
//  1. 日期格式按 YYYY-MM-DD 做字典序比较，不引入时间解析。
//  2. **解析不了的作品一律保留**。因为线上字段格式一旦变化，
//     「按错误规则丢弃数据」比「多给几条」危险得多 —— 后者用户看得见，
//     前者会让 feed 静默变空。
func filterSince(log *slog.Logger, works []catalog.Work, since string) []catalog.Work {
	since = strings.TrimSpace(since)
	if since == "" {
		return works
	}
	out := make([]catalog.Work, 0, len(works))
	dropped, unparsed := 0, 0
	for _, w := range works {
		d := strings.TrimSpace(w.ReleaseDate)
		if d == "" {
			unparsed++
			out = append(out, w)
			continue
		}
		if d >= since {
			out = append(out, w)
			continue
		}
		dropped++
	}
	log.Warn("since 过滤已启用，但其语义尚未定稿",
		"since", since, "比较字段", "release_date",
		"保留", len(out), "丢弃", dropped, "缺发行日期而保留", unparsed,
		"见", "ticket 09")
	return out
}

// writeUpstreamError 把上游错误映射成合适的 HTTP 状态。
//
// 凭据类错误单独处理：它需要用户去重新导出 token，而不是重试。
func writeUpstreamError(w http.ResponseWriter, err error) {
	if appapi.IsAuthError(err) {
		writeError(w, http.StatusBadGateway, "上游要求有效凭据："+err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "上游请求失败："+err.Error())
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// toValues 把透传参数字典转成 url.Values。
func toValues(m map[string]string) url.Values {
	v := make(url.Values, len(m))
	for k, val := range m {
		v.Set(k, val)
	}
	return v
}
