package httpapi

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

// TestBrowseRouteRendersFeed 是全站订阅的正常路径。
func TestBrowseRouteRendersFeed(t *testing.T) {
	src := &stub.Source{BrowseWorks: []catalog.Work{{ID: "w1", Number: "NMSL-033",
		Magnets: []catalog.Magnet{{Infohash: "h1", Name: "NMSL-033"}}}}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/tags/0.xml?main=m&tags=68&year=2020&month=3&duration=gt-120")
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	var f parsedFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("不是合法 RSS: %v", err)
	}
	// 标题要说清是哪个片库与筛了什么 —— 这是用户在 qBittorrent 里唯一看得到的文字。
	for _, want := range []string{"全站", "有码", "1 个标签", "2020 年 3 月", "120 分钟以上"} {
		if !strings.Contains(f.Channel.Title, want) {
			t.Errorf("标题 %q 里应当有 %q", f.Channel.Title, want)
		}
	}
	if len(f.Channel.Items) != 1 {
		t.Errorf("条目数 = %d", len(f.Channel.Items))
	}
	if !strings.Contains(f.Channel.Description, "标签 68") {
		t.Errorf("描述里应当有筛选条件: %q", f.Channel.Description)
	}
}

// TestBrowseAlwaysCarriesMagnetsFlag 钉住一个会让 feed 静默变空的坑。
//
// 实测：不发 m（含磁鏈）时 `0:t:::::` 返回的 50 部里 magnets_count **全是 0**
// （50/50），而 feed 发不出没有 enclosure 的条目 —— 结果是「看着坏了」的空 feed。
// 抓包里 App 浏览页自己发的就是 `0:t:m::::`，所以 m 是它的默认值；
// 对本服务它是必需项。
//
// 用户给的其它主属性要保留（把 m 并进去），而不是被覆盖掉。
func TestBrowseAlwaysCarriesMagnetsFlag(t *testing.T) {
	cases := []struct {
		main string
		want string
	}{
		{"", "m"},
		{"c", "c,m"},
		{"c,m", "c,m"},
		{"m", "m"},
		{"p,s", "p,s,m"},
	}
	for _, tc := range cases {
		src := &recordingSource{}
		h := newTestServer(t, "provider: stub\n", src)
		q := "/rss/tags/0.xml"
		if tc.main != "" {
			q += "?main=" + tc.main
		}
		if rec := do(t, h, q); rec.Code != 200 {
			t.Fatalf("%s = %d, want 200", q, rec.Code)
		}
		if src.browseSel.Main != tc.want {
			t.Errorf("main=%q → 发给上游的主属性 = %q, want %q", tc.main, src.browseSel.Main, tc.want)
		}
	}
}

// TestBrowseDescriptionSaysMagnetsWasAdded 确认这件事没被藏起来。
func TestBrowseDescriptionSaysMagnetsWasAdded(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	var f parsedFeed
	_ = xml.Unmarshal(do(t, h, "/rss/tags/0.xml?main=c").Body.Bytes(), &f)
	if !strings.Contains(f.Channel.Description, "本服务自己加的") {
		t.Errorf("描述里应当说明 m 是我们加的: %q", f.Channel.Description)
	}
	// 用户自己写了 m 时就不用解释了。
	_ = xml.Unmarshal(do(t, h, "/rss/tags/0.xml?main=m").Body.Bytes(), &f)
	if strings.Contains(f.Channel.Description, "本服务自己加的") {
		t.Errorf("用户自己给了 m，不该再解释: %q", f.Channel.Description)
	}
}

// TestBrowseRouteNotFoundForBadZone 确认片库号写错是 404（不是静默换库）。
func TestBrowseRouteNotFoundForBadZone(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	for _, p := range []string{"/rss/tags/4.xml", "/rss/tags/x.xml", "/rss/tags/.xml"} {
		if rec := do(t, h, p); rec.Code != 404 {
			t.Errorf("%s = %d, want 404", p, rec.Code)
		}
	}
}

// TestBrowseRouteRespectsZoneWhitelist 确认白名单对全站订阅同样生效。
//
// 白名单的单位在这里是「片库」而不是「订阅」：全站订阅的组合空间
// （4 个 zone × 任意标签/年份）枚举不出来，可枚举且有意义的粒度是
// 「愿不愿意把哪个片库放出去」。
func TestBrowseRouteRespectsZoneWhitelist(t *testing.T) {
	cfg := "provider: stub\nfeeds:\n  codes: [KV-328]\n  zones: [\"0\"]\n"
	h := newTestServer(t, cfg, &stub.Source{})

	if rec := do(t, h, "/rss/tags/0.xml"); rec.Code != 200 {
		t.Errorf("白名单里的片库 = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "/rss/tags/1.xml"); rec.Code != 404 {
		t.Errorf("白名单外的片库 = %d, want 404", rec.Code)
	}
}

// TestBrowseRoutePassesMaskAndStripsOwnParams 确认掩码真的由服务构造、
// 且自有参数不会被当成未知参数送去上游。
func TestBrowseRoutePassesMaskAndStripsOwnParams(t *testing.T) {
	src := &recordingSource{browseWorks: []catalog.Work{
		{ID: "w1", Number: "A-1", Magnets: []catalog.Magnet{{Infohash: "h"}}}}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/tags/1.xml?main=c&tags=68,46&year=2020&duration=gt-120&month=3&sort_by=score&pages=2")
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if src.browseZone != 1 {
		t.Errorf("zone = %d, want 1", src.browseZone)
	}
	got := src.browseSel
	// m 是本服务补上的（见 TestBrowseAlwaysCarriesMagnetsFlag）。
	want := catalog.BrowseSelector{Main: "c,m", Tags: "68,46", Year: "2020", Duration: "gt-120", Month: "3"}
	if got != want {
		t.Errorf("selector = %+v\n        want %+v", got, want)
	}
	if src.browseParams.Get("sort_by") != "score" {
		t.Errorf("sort_by 应当透传，得到 %q", src.browseParams.Get("sort_by"))
	}
	if got := src.browseParams.Get("pages"); got != "2" {
		t.Errorf("pages 应当流到 appapi 那一层，得到 %q", got)
	}
	// 语义参数已经变成掩码了，不该再作为 query 参数出现在下游。
	for _, k := range []string{"main", "tags", "year", "month", "duration"} {
		if _, ok := src.browseParams[k]; ok {
			t.Errorf("%s 不该作为参数流到下游（它已经变成掩码的一部分）", k)
		}
	}
}

// TestBrowseRouteRejectsRawMask 确认这条路由不接受手写掩码。
//
// 掩码由本服务按语义参数构造；再接受一个原始 filter_by 就多出一条
// 完全未经校验的通道，而写错的掩码上游不报错、只忽略。
func TestBrowseRouteRejectsRawMask(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	rec := do(t, h, "/rss/tags/0.xml?filter_by=0:t:m::::")
	if rec.Code != 400 {
		t.Fatalf("状态码 = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "手写掩码") {
		t.Errorf("文案要指向该用什么: %s", rec.Body.String())
	}
}

// TestActressRouteAcceptsYearAndTags 是抓包验完之后的落点。
//
// 女优掩码的尾部（掩码第 5 段 = 年份）实测有效（`0:a:EvkJ::2021` → 19 条全 2021），
// 而标签走**独立参数** filter_by_tags（App 女优页就是这么发的）。
// 所以这两样在女优路由上要能用，而不是被拒。
func TestActressRouteAcceptsYearAndTags(t *testing.T) {
	src := &recordingSource{works: []catalog.Work{
		{Number: "A-1", ReleaseDate: "2021-06-01", Magnets: []catalog.Magnet{{Infohash: "h1"}}}}}
	h := newTestServer(t, "provider: stub\n", src)

	if rec := do(t, h, "/rss/actress/EvkJ.xml?year=2021&tags=48"); rec.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	got := src.gotActressCur
	if got.Get("year") != "2021" {
		t.Errorf("year 应当流到 appapi（它要拼进掩码），得到 %q", got.Get("year"))
	}
	if got.Get("filter_by_tags") != "48" {
		t.Errorf("tags 应当被翻译成 filter_by_tags，得到 %q", got.Get("filter_by_tags"))
	}
	if _, ok := got["tags"]; ok {
		t.Error("tags 是本服务的语义参数，不该原样流到下游")
	}
}

// TestActressRouteRejectsMonthAndDuration 是本轮新增里最要紧的一条防御。
//
// 女优筛选面板里没有这两项（用户确认），而掩码里**多写一段**会让解析变样：
// 实测 `0:a:EvkJ:c:2021:` 里 main 仍然生效、**年份被静默丢弃** ——
// 于是 feed 看起来筛了 2021、实际跨到 2026。这类「看着筛了其实没筛」
// 是本项目最不能接受的一类失败，所以判成 400。
func TestActressRouteRejectsMonthAndDuration(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	for _, q := range []string{"month=3", "duration=gt-120"} {
		rec := do(t, h, "/rss/actress/EvkJ.xml?"+q)
		if rec.Code != 400 {
			t.Errorf("?%s = %d, want 400（静默失效会让 feed 看着筛过其实没筛）", q, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "/rss/tags/") {
			t.Errorf("文案应当指出该用哪条路由: %s", rec.Body.String())
		}
	}
}

// TestListRouteRejectsTimeDimensions 确认清单路由上 year/month/duration 都被拦。
//
// 实测清单掩码里的年份槽被上游**忽略**（`0:l:p36Eww::2025` 返回整份清单 9 条），
// 所以这里不能默默接受。
func TestListRouteRejectsTimeDimensions(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	for _, q := range []string{"year=2020", "month=3", "duration=gt-120"} {
		if rec := do(t, h, "/rss/list/k4EVE4.xml?"+q); rec.Code != 400 {
			t.Errorf("清单路由上的 ?%s = %d, want 400", q, rec.Code)
		}
	}
	// 标签与主属性在清单上能用（走独立参数与掩码的主属性段）。
	if rec := do(t, h, "/rss/list/k4EVE4.xml?tags=68&main=c"); rec.Code != 200 {
		t.Errorf("清单上的 tags/main 应当可用，得到 %d", rec.Code)
	}
}

// TestTagsAndFilterByTagsAreMutuallyExclusive 确认两写时不猜。
func TestTagsAndFilterByTagsAreMutuallyExclusive(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	rec := do(t, h, "/rss/actress/EvkJ.xml?tags=68&filter_by_tags=46")
	if rec.Code != 400 {
		t.Fatalf("状态码 = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "只能给一个") {
		t.Errorf("文案要说明原因: %s", rec.Body.String())
	}
}

// TestBrowseRouteMapsBadSelectorTo400 确认「你写错了」与「上游出错了」分开：
// 前者 400（重试无用），后者 502（重试有用）。
//
// 校验规则本身归 appapi 所有（`tags` 超过 5 个、有时长没年份、月份越界…），
// 那些在 appapi/browse_test.go 里逐条验；这里验的只是**映射**。
// 用假实现返回 ErrBadRequest，而不是让 httpapi 自己再判一遍 ——
// 两处各判一遍就会有两套可以互相跑偏的规则。
func TestBrowseRouteMapsBadSelectorTo400(t *testing.T) {
	src := &recordingSource{browseErr: catalog.ErrBadRequest}
	h := newTestServer(t, "provider: stub\n", src)
	rec := do(t, h, "/rss/tags/0.xml?tags=68")
	if rec.Code != 400 {
		t.Fatalf("状态码 = %d, want 400（不能是 502 —— 重试无用）", rec.Code)
	}
}

// TestYearAndSinceAreMutuallyExclusive 确认两个「范围」不能同时给。
//
// year 是上游筛选（整个年份），since 是本服务的本地过滤（「这个日期起」）。
// 同时发的必然是空 feed —— 而「选了年份反而是空的」会让人以为是功能坏了。
//
// 检查放在 httpapi 而不是 appapi：`since` 是这一层算出来的（还可能来自白名单），
// 只有这里同时看得到两个。
func TestYearAndSinceAreMutuallyExclusive(t *testing.T) {
	src := &recordingSource{works: []catalog.Work{
		{Number: "A-1", Magnets: []catalog.Magnet{{Infohash: "h"}}}}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/actress/EvkJ.xml?year=2021&since=2026-01-01")
	if rec.Code != 400 {
		t.Fatalf("状态码 = %d, want 400（同时给必然空 feed）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "不能同时给") {
		t.Errorf("文案要说清原因: %s", rec.Body.String())
	}
	// 单独给任一个都要放行。
	if rec := do(t, h, "/rss/actress/EvkJ.xml?year=2021"); rec.Code != 200 {
		t.Errorf("只给 year = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "/rss/actress/EvkJ.xml?since=2026-01-01"); rec.Code != 200 {
		t.Errorf("只给 since = %d, want 200", rec.Code)
	}
}

// TestBrowseTitleMentionsMonthAlone 确认「只给月份」也写进标题。
//
// 实测月份单独给是有效的（`0:t:m::::3` 返回各年 3 月），因此标题漏掉它
// 就等于告诉用户「没筛」。
func TestBrowseTitleMentionsMonthAlone(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	var f parsedFeed
	_ = xml.Unmarshal(do(t, h, "/rss/tags/0.xml?month=4").Body.Bytes(), &f)
	if !strings.Contains(f.Channel.Title, "4 月") {
		t.Errorf("标题里应当有月份: %q", f.Channel.Title)
	}
	if !strings.Contains(f.Channel.Title, "每年") {
		t.Errorf("月份单独给是跨年的，标题要说清: %q", f.Channel.Title)
	}
}
