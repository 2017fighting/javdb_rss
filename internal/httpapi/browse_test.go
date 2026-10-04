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

// TestActressRouteRejectsBrowseOnlyParams 是本轮新增里最要紧的一条防御。
//
// `tags`/`year`/`month`/`duration`/`main` 只在全站路由上有意义。要是它们在
// 女优路由上被**静默忽略**，用户会拿到一条「看上去筛了、实际没筛」的 feed ——
// 而那是本项目最不能接受的一类失败。所以判成 400。
//
// ⚠️ 这是临时的严格：女优页掩码的尾部语法还没从抓包里验出来。
// 验出来之后这里应当改成「拼进掩码」。
func TestActressRouteRejectsBrowseOnlyParams(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	for _, q := range []string{"tags=68", "year=2020", "month=3", "duration=gt-120", "main=c"} {
		rec := do(t, h, "/rss/actress/EvkJ.xml?"+q)
		if rec.Code != 400 {
			t.Errorf("?%s = %d, want 400（静默忽略会让 feed 看着筛过其实没筛）", q, rec.Code)
		}
	}
	// 不影响本来就能用的参数。
	if rec := do(t, h, "/rss/actress/EvkJ.xml?filter_by_tags=68&since=2026-01-01"); rec.Code != 200 {
		t.Errorf("filter_by_tags/since 应当照常可用，得到 %d", rec.Code)
	}
}

// TestListRouteRejectsBrowseOnlyParams 同上，清单路由也拦。
func TestListRouteRejectsBrowseOnlyParams(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	if rec := do(t, h, "/rss/list/k4EVE4.xml?tags=68"); rec.Code != 400 {
		t.Errorf("清单路由上的 tags = %d, want 400", rec.Code)
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
