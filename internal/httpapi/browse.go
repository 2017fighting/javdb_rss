package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/feed"
)

// browseOwnParams 是**只在本路由**有意义的自有参数。
//
// 它们会被拼进全站掩码（`{zone}:t:{main}:{tags}:{year}:{duration}:{month}`），
// 因此绝不该透传给上游 —— 透传的后果是上游把 `tags=68` 当未知参数忽略，
// 而 feed 看上去「筛过了」。
var browseOwnParams = []string{"main", "tags", "year", "month", "duration"}

// browseFeedTitle 拼全站订阅的标题。
//
// 刻意**不**去查标签名字：那要额外拉一次词汇表，而这条 feed 会被 qBittorrent
// 周期轮询。标题够用就行 —— 筛选条件本身就是用户刚选的东西。
func browseFeedTitle(zone int, sel catalog.BrowseSelector) string {
	name := catalog.ZoneName(zone)
	if name == "" {
		name = fmt.Sprintf("zone %d", zone)
	}
	parts := []string{name}
	if sel.Main != "" {
		parts = append(parts, "主属性 "+sel.Main)
	}
	if n := len(splitCSV(sel.Tags)); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 个标签", n))
	}
	// ⚠️ 月份**单独给**也是有效的（实测 `0:t:m::::3` 返回各年 3 月），
	// 所以这一格不能只在「有年份」时才写进标题 —— 标题漏掉筛过的维度，
	// 用户会以为没生效。
	switch {
	case sel.Year != "" && sel.Month != "":
		parts = append(parts, fmt.Sprintf("%s 年 %s 月", sel.Year, sel.Month))
	case sel.Year != "":
		parts = append(parts, sel.Year+" 年")
	case sel.Month != "":
		parts = append(parts, "每年 "+sel.Month+" 月")
	}
	if sel.Duration != "" {
		parts = append(parts, durationName(sel.Duration))
	}
	return "JavDB · 全站（" + strings.Join(parts, " · ") + "）"
}

func durationName(id string) string {
	switch id {
	case "lt-45":
		return "45 分钟以内"
	case "45-90":
		return "45–90 分钟"
	case "90-120":
		return "90–120 分钟"
	case "gt-120":
		return "120 分钟以上"
	}
	return id
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// handleBrowse 服务 GET /rss/tags/{zone}.xml?main=&tags=&year=&month=&duration=…
//
// # 为什么这条路由存在，而 filter_by_tags 不够
//
// 实测（2026-10-04）：`filter_by_tags` 作为**独立参数**只对女优实体生效 ——
// 在清单、搜索、latest、top 上全被静默忽略（用不存在的 id 做对照可证伪）。
// 而 App「浏览」页的全站筛选根本不发那个参数：它把标签放进 **filter_by 掩码的
// 一个槽位**里，与主属性、年份、月份、时长并列。这条路由就是那个形态。
//
// # 为什么 zone 在路径里
//
// 掩码的第一段是片库号，而实测 0/1/2/3 返回**四个不同的集合**
// （有码 / 无码 / 欧美 / FC2）—— 它不是可有可无的装饰，
// 而且标签 id 空间按片库分（月份 1–12 与真标签 id 全撞号）。
// 写错片库不会报错，只会给另一个库的作品，因此它必须是 URL 的一部分、可见可改。
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	raw, ok := pathParam(r.URL.Path, "/rss/tags/")
	if !ok {
		writeError(w, http.StatusNotFound, "需要形如 /rss/tags/{片库号}.xml 的路径")
		return
	}
	zone, err := strconv.Atoi(raw)
	if err != nil || catalog.ZoneName(zone) == "" {
		writeError(w, http.StatusNotFound, fmt.Sprintf(
			"片库号 %q 不认识。实测有效的只有 0=有码 1=无码 2=欧美 3=FC2", raw))
		return
	}
	if !s.cfg.Current().AllowsZone(strconv.Itoa(zone)) {
		// 与别的白名单一样返回 404，不确认它是否「存在但被禁止」。
		writeError(w, http.StatusNotFound, "未知的订阅")
		return
	}

	query := r.URL.Query()

	// 手写掩码在这条路由上**不支持**：掩码由本服务按语义参数构造，
	// 再接受一个原始 filter_by 就会多出一条完全未经校验的通道 ——
	// 而写错的掩码上游不报错、只忽略。
	if v := strings.TrimSpace(query.Get("filter_by")); v != "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"本路由自己构造 filter_by（%s），不接受手写掩码；"+
				"要手写掩码请用女优/清单订阅", strings.Join(browseOwnParams, " / ")))
		return
	}

	sel := catalog.BrowseSelector{
		Main:     strings.TrimSpace(query.Get("main")),
		Tags:     strings.TrimSpace(query.Get("tags")),
		Year:     strings.TrimSpace(query.Get("year")),
		Month:    strings.TrimSpace(query.Get("month")),
		Duration: strings.TrimSpace(query.Get("duration")),
	}

	// 主属性里**总是**带上 m（含磁鏈）。两个理由：
	//
	//  1. 抓包里 App 浏览页自己发的就是 `0:t:m::::` —— m 是它的默认值。
	//  2. 对本服务来说它不只“默认”，而是**必需**：实测不发 m 时
	//     `0:t:::::` 返回的 50 部里 magnets_count 全是 0（50/50），
	//     而 feed 发不出没有 enclosure 的条目 —— 结果是一条看着坏了的空 feed。
	//
	// 用户给的其它主属性（如 c）保留，只是把 m 并进去：他要的是「带字幕的片」，
	// 而只有带磁鏈的那些能进 feed。这件事会写进 channel 描述，不藏起来。
	mainAdded := addMagnetsFlag(&sel)

	// 自有参数（含那几个筛选维度）一律不进上游 —— 它们已经变成了掩码。
	// `pages` 是例外：它是本服务自有，但由 **appapi 消费**（决定翻几页），
	// 因此必须流到那一层。漏掉它的表现是 `?pages=5` 静默只取一页。
	params := make(map[string]string, len(query))
	for k, vs := range query {
		if len(vs) == 0 || (catalog.IsOwnParam(k) && k != "pages") {
			continue
		}
		params[k] = vs[0]
	}

	since := strings.TrimSpace(query.Get("since"))

	works, err := s.src.Browse(r.Context(), zone, sel, toValues(params))
	if err != nil {
		if errors.Is(err, catalog.ErrBadRequest) {
			s.log.WarnContext(r.Context(), "全站订阅参数不合法", "zone", zone, "err", err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.log.ErrorContext(r.Context(), "全站浏览失败", "zone", zone, "err", err)
		writeUpstreamError(w, err)
		return
	}

	if since != "" {
		works = filterSince(s.log, works, since)
	}

	s.renderItems(w, r, feed.Meta{
		Title:       browseFeedTitle(zone, sel),
		Link:        s.feedURL(r),
		Description: browseDescription(zone, sel, mainAdded),
		Language:    s.cfg.Current().Feed.Language,
	}, feed.Build(works))
}

// addMagnetsFlag 保证主属性里带 m，并报告它是不是我们加上的。
//
// 保留用户原有的顺序，没有 m 就补到最后：`c` → `c,m`。
func addMagnetsFlag(sel *catalog.BrowseSelector) bool {
	for _, seg := range strings.Split(sel.Main, ",") {
		if strings.TrimSpace(seg) == "m" {
			return false
		}
	}
	if sel.Main == "" {
		sel.Main = "m"
	} else {
		sel.Main += ",m"
	}
	return true
}

// browseDescription 是 channel 描述：完整地说清这条 feed 究竟在发什么。
//
// 它比标题长没关系 —— 描述本来就是给会读它的人（RSS 阅读器、肉眼看 XML），
// 而 qBittorrent 只看标题。
func browseDescription(zone int, sel catalog.BrowseSelector, mainAdded bool) string {
	desc := "全站订阅（" + catalog.ZoneName(zone) + "）· " + filterSummary(sel)
	if mainAdded {
		desc += "。（主属性里的 m = 含磁鏈是本服务自己加的：没有磁链的条目发不出去，" +
			"去掉它只会得到一条空 feed）"
	}
	return desc
}

// filterSummary 是 channel 描述里那句人话。
func filterSummary(sel catalog.BrowseSelector) string {
	var parts []string
	if sel.Main != "" {
		parts = append(parts, "主属性 "+sel.Main)
	}
	if sel.Tags != "" {
		parts = append(parts, "标签 "+sel.Tags)
	}
	if sel.Year != "" {
		parts = append(parts, "年份 "+sel.Year)
	}
	if sel.Month != "" {
		parts = append(parts, "月份 "+sel.Month)
	}
	if sel.Duration != "" {
		parts = append(parts, "时长 "+durationName(sel.Duration))
	}
	if len(parts) == 0 {
		return "无筛选条件（该片库的最新作品）"
	}
	return strings.Join(parts, "；")
}

// splitOwnSelectors 把「筛选维度」这几个自有参数从 query 里读出来，
// 并按**这条路由支不支持**分别处理。
//
// 为什么必须逐条判，而不是一律忽略或一律拒绝：
//
//	year      女优订阅支持（掩码第 5 段，实测整年）；清单**不支持**（实测被忽略）
//	main      女优与清单都支持（掩码的主属性段）
//	tags      女优与清单都支持 —— 但它们走的是**独立参数 filter_by_tags**，
//	          而不是掩码里的槽位（那是全站形态用的）
//	month/duration  只有全站形态支持；女优/清单上加了会静默失效
//
// 所以「不支持」的那些必须返回 400：静默忽略会造出一条**看着筛过、其实没筛**
// 的 feed，而那是本项目最不能接受的一类失败。
func splitOwnSelectors(w http.ResponseWriter, r *http.Request, route routeKind) (url.Values, bool) {
	q := r.URL.Query()
	out := url.Values{}

	tags := strings.TrimSpace(q.Get("tags"))
	year := strings.TrimSpace(q.Get("year"))
	month := strings.TrimSpace(q.Get("month"))
	duration := strings.TrimSpace(q.Get("duration"))
	main := strings.TrimSpace(q.Get("main"))

	switch route {
	case routeBrowse:
		// 全站形态：五样都走掩码，由 handleBrowse 自己处理。
		return nil, true
	case routeList:
		var unsupported []string
		if year != "" {
			unsupported = append(unsupported, "year")
		}
		if month != "" {
			unsupported = append(unsupported, "month")
		}
		if duration != "" {
			unsupported = append(unsupported, "duration")
		}
		if len(unsupported) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"清单订阅不支持 %s。实测清单掩码里的年份槽被上游**忽略**"+
					"（`0:l:p36Eww::2025` 返回的是整份清单），月份/时长更是只有全站形态才有。"+
					"加了不会报错，只会让你以为筛了", strings.Join(unsupported, "、")))
			return nil, false
		}
	case routeActress:
		var unsupported []string
		if month != "" {
			unsupported = append(unsupported, "month")
		}
		if duration != "" {
			unsupported = append(unsupported, "duration")
		}
		if len(unsupported) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"女优订阅不支持 %s —— App 的女优筛选面板里也没有这两项，"+
					"而且掩码里多写一段会让整条掩码的解析变样（实测 main 仍生效、年份被丢掉）。"+
					"这两个维度请用全站订阅 /rss/tags/{片库号}.xml",
				strings.Join(unsupported, "、")))
			return nil, false
		}
	}

	if main != "" {
		out.Set("main", main)
	}
	if year != "" {
		out.Set("year", year)
	}
	if tags != "" {
		// 标签在实体路由上走**独立参数**（那是 App 女优页发的形态），
		// 而不是掩码里的槽位。已经给了 filter_by_tags 就不要再给 tags ——
		// 两个都写会让人不知道以哪个为准。
		if strings.TrimSpace(q.Get("filter_by_tags")) != "" {
			writeError(w, http.StatusBadRequest,
				"tags 与 filter_by_tags 是同一件事的两种写法，只能给一个："+
					"tags 是本服务的语义参数（会被翻译成 filter_by_tags）")
			return nil, false
		}
		out.Set("filter_by_tags", tags)
	}
	return out, true
}

// routeKind 是「这条路由是哪种订阅」。三种形态的掩码语法不同。
type routeKind int

const (
	routeActress routeKind = iota
	routeList
	routeBrowse
)
