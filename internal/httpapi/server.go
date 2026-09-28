// Package httpapi 暴露 RSS 路由。
//
// 它只依赖 catalog.Source 这个端口，不关心数据从哪来 ——
// 真实 App API、缓存装饰器、测试假实现都可以从外面塞进来。
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/config"
	"github.com/2017fighting/javdb_rss/internal/feed"
)

// Version 是 /version 端点报告的服务版本，构建时用 -ldflags 注入。
var Version = "dev"

// Server 组装路由。
type Server struct {
	cfg *config.Holder
	src catalog.Source
	log *slog.Logger
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

// Handler 返回完整的 HTTP 处理器。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 用前缀匹配而不是 {code} 通配符：Go 的 ServeMux 要求通配符占满整个
	// 路径段，而我们要容忍结尾的 .xml，因此在这里自己剥。
	mux.HandleFunc("GET /rss/code/", s.handleCode)
	mux.HandleFunc("GET /rss/actress/", s.handleActress)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /version", s.handleVersion)

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
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
	params := make(map[string]string, len(sub.Params)+len(query))
	for k, v := range sub.Params {
		params[k] = v
	}
	// since 是本服务自有参数，不进透传集合；其余原样搬运。
	const ownParam = "since"
	for k, vs := range query {
		if k == ownParam || len(vs) == 0 {
			continue
		}
		params[k] = vs[0]
	}

	since := sub.Since
	if v := strings.TrimSpace(query.Get(ownParam)); v != "" {
		since = v
	}

	works, err := s.src.Actress(r.Context(), id, toValues(params))
	if err != nil {
		s.log.ErrorContext(r.Context(), "取女优作品失败", "id", id, "err", err)
		writeUpstreamError(w, err)
		return
	}

	if since != "" {
		works = filterSince(s.log, works, since)
	}

	s.renderFeed(w, r, feed.Meta{
		Title:       "JavDB · " + id,
		Link:        s.feedURL(r),
		Description: "女优 " + id + " 的订阅源",
		Language:    s.cfg.Current().Feed.Language,
	}, works)
}

// renderFeed 是两条路由共用的收尾：选磁链、渲染 RSS。
//
// feed 是**请求时现算**的。这不是最终形态 —— ticket 09 会决定要不要在
// catalog.Source 外面包一层缓存或后台刷新。之所以现在不提前决定，
// 是因为成本量级还没量出来；而选择「现算 + Source 作为唯一端口」的好处是，
// 将来加缓存只需要包一层装饰器，这里一行都不用改。
func (s *Server) renderFeed(w http.ResponseWriter, r *http.Request, meta feed.Meta, works []catalog.Work) {
	items := feed.Build(works)

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
