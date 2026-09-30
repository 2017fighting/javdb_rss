// Package httpapi 暴露 RSS 路由。
//
// 它只依赖 catalog.Source 这个端口，不关心数据从哪来 ——
// 真实 App API、缓存装饰器、测试假实现都可以从外面塞进来。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
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

	// 用前缀匹配而不是 {code} 通配符：Go 的 ServeMux 要求通配符占满整个
	// 路径段，而我们要容忍结尾的 .xml，因此在这里自己剥。
	mux.HandleFunc("GET /rss/code/", s.handleCode)
	mux.HandleFunc("GET /rss/actress/", s.handleActress)

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
	// Feed 是可以直接拿去用的 feed 路径。
	//
	// 给出它而不是让用户自己拼：拼错了只会得到 404，而用户会以为服务坏了。
	// 测试里有一条一致性检查，保证这里给出的路径真的能访问。
	Feed string `json:"feed"`
}

// handleCollected 服务 GET /collected：列出 App 里收藏的女优。
//
// 这是需求 4 的落点。本服务**不**因为你收藏了谁就自动为它建 feed ——
// 它只把列表（带现成的 feed 路径）交给你，由你决定订哪些。
// 这样既满足了「读取订阅的女优」，又不破坏已定的「URL 即订阅」形态，
// 也不引入「一条 feed 对应 N 个订阅」那个高成本形态。
func (s *Server) handleCollected(w http.ResponseWriter, r *http.Request) {
	actresses, err := s.src.CollectedActresses(r.Context())
	if err != nil {
		s.log.ErrorContext(r.Context(), "取收藏女优失败", "err", err)
		writeCollectedError(w, err)
		return
	}

	out := struct {
		Actresses []collectedEntry `json:"actresses"`
	}{Actresses: make([]collectedEntry, 0, len(actresses))}
	for _, a := range actresses {
		out.Actresses = append(out.Actresses, collectedEntry{
			ID:          a.ID,
			Name:        a.Name,
			VideosCount: a.VideosCount,
			Feed:        "/rss/actress/" + url.PathEscape(a.ID) + ".xml",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// writeCollectedError 把三种失败分开。
//
// 关键是**不能返回 200 + 空列表**：那会被理解成「你没收藏任何人」，
// 而真相是「服务读不到」—— 两者的下一步动作完全不同。
func writeCollectedError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrNoToken):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "尚未配置 token，读不到 App 里的收藏女优。" +
				"请从 App 导出后配置 app_api.token_file（见 README）。" +
				"注意：番号订阅与女优订阅不需要 token，不受此影响。",
		})
	case appapi.IsAuthError(err):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "token 已失效，请重新从 App 导出并更新 token_file。原因：" + err.Error(),
		})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "读取收藏女优失败：" + err.Error(),
		})
	}
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

	s.renderFeed(w, r, feed.Meta{
		Title:       "JavDB · " + code,
		Link:        s.feedURL(r),
		Description: "番号 " + code + " 的订阅源",
		Language:    s.cfg.Current().Feed.Language,
	}, works)
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

	s.renderFeed(w, r, feed.Meta{
		Title:       <-nameCh,
		Link:        s.feedURL(r),
		Description: "女优 " + id + " 的订阅源",
		Language:    s.cfg.Current().Feed.Language,
	}, works)
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

// renderFeed 是两条路由共用的收尾：选磁链、渲染 RSS。
//
// feed 是**请求时现算**的。这不是最终形态 —— ticket 09 会决定要不要在
// catalog.Source 外面包一层缓存或后台刷新。之所以现在不提前决定，
// 是因为成本量级还没量出来；而选择「现算 + Source 作为唯一端口」的好处是，
// 将来加缓存只需要包一层装饰器，这里一行都不用改。
func (s *Server) renderFeed(w http.ResponseWriter, r *http.Request, meta feed.Meta, works []catalog.Work) {
	items := feed.Build(works)

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
