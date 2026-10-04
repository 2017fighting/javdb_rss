package httpapi

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

// wantFeedResponse 解析这条 feed 里我们真正依赖的三样东西：
// channel 描述（尚无磁链的可见信号）、条目的 guid（身份），以及磁链本身。
type wantFeedResponse struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Title       string `xml:"title"`
		Description string `xml:"description"`
		Items       []struct {
			Title     string `xml:"title"`
			GUID      string `xml:"guid"`
			Enclosure struct {
				URL string `xml:"url,attr"`
			} `xml:"enclosure"`
		} `xml:"item"`
	} `xml:"channel"`
}

func parseWantFeed(t *testing.T, body string) wantFeedResponse {
	t.Helper()
	var got wantFeedResponse
	if err := xml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("不是合法 RSS: %v\n%s", err, body)
	}
	return got
}

// wantWork 造一部「想看」里的作品。magnets 为空表示它还在等磁力。
func wantWork(number string, magnets ...catalog.Magnet) catalog.Work {
	return catalog.Work{
		ID:          "id-" + number,
		Number:      number,
		Title:       "标题 " + number,
		ReleaseDate: "2026-09-01",
		Magnets:     magnets,
	}
}

const wantHash = "0e8f4789bdcab713effc3a07d1309a776c867b3e"

// TestWantFeedRendersItemsAndCountsPendingOnes 是本票的主路径，一次钉住三件事：
//
//  1. 有磁链的作品渲染成条目，guid = 纯 infohash（与别的 feed 同一套身份规则）；
//  2. 尚无磁链的作品**不发条目**（qBittorrent 用不了没有 enclosure 的条目）；
//  3. 它也没静默消失 —— channel 描述里说得出「有几部在等磁力」。
//
// 第 3 条是用户明确要的：等待磁链是**正常状态**（领域定义如此），
// 但「少了一条」与「服务没读到这张清单」在订阅界面上长得一样。
func TestWantFeedRendersItemsAndCountsPendingOnes(t *testing.T) {
	src := &recordingSource{want: &catalog.WantList{Works: []catalog.Work{
		wantWork("KV-1", catalog.Magnet{Infohash: wantHash, Name: "KV-1", SizeMiB: 3110, CNSub: true}),
		wantWork("KV-2"), // 还没有种
	}}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/want.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("content-type"); !strings.HasPrefix(ct, "application/rss+xml") {
		t.Errorf("content-type = %q，它应当是 feed", ct)
	}

	got := parseWantFeed(t, rec.Body.String())
	if got.Channel.Title != "JavDB · 想看" {
		t.Errorf("channel 标题 = %q", got.Channel.Title)
	}
	if len(got.Channel.Items) != 1 {
		t.Fatalf("得到 %d 条条目，尚无磁链的那部不该有条目", len(got.Channel.Items))
	}
	item := got.Channel.Items[0]
	if item.GUID != wantHash {
		t.Errorf("guid = %q，它必须是纯 infohash", item.GUID)
	}
	if !strings.Contains(item.Enclosure.URL, "urn:btih:"+wantHash) {
		t.Errorf("enclosure 不是这条磁链: %q", item.Enclosure.URL)
	}
	if !strings.Contains(item.Title, "中文字幕") {
		t.Errorf("字幕版的标题应当有前缀: %q", item.Title)
	}
	for _, want := range []string{"共 2 部", "1 部尚无磁链"} {
		if !strings.Contains(got.Channel.Description, want) {
			t.Errorf("描述里应当有 %q，实际是 %q", want, got.Channel.Description)
		}
	}
}

// TestWantFeedDescriptionStaysCleanWhenNothingIsPending 确认那个信号是**有信息才出现**的。
//
// 一个永远挂着的「0 部尚无磁链」与永远为真的截断警告是同一种病：
// 它让人学会忽略这条描述，于是真有东西要说的时候也没人看。
func TestWantFeedDescriptionStaysCleanWhenNothingIsPending(t *testing.T) {
	src := &recordingSource{want: &catalog.WantList{Works: []catalog.Work{
		wantWork("KV-1", catalog.Magnet{Infohash: wantHash, Name: "KV-1"}),
	}}}
	h := newTestServer(t, "provider: stub\n", src)

	got := parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())
	if got.Channel.Description != "App 里「想看」的作品，共 1 部" {
		t.Errorf("描述 = %q，不该带上「0 部尚无磁链」这样的噪音", got.Channel.Description)
	}
}

// TestWantFeedReportsTruncation 确认触顶时 feed 自己说得出来。
//
// feed 里没有 JSON 字段可用（对照 /collected 的 truncated 键），
// 描述是它唯一的可见通道 —— 因此这条路径必须被钉住，否则截断会变成静默少给数据。
func TestWantFeedReportsTruncation(t *testing.T) {
	src := &recordingSource{want: &catalog.WantList{
		Works:        []catalog.Work{wantWork("KV-1")},
		Truncated:    true,
		PagesFetched: 20,
		MaxPages:     20,
	}}
	h := newTestServer(t, "provider: stub\n", src)

	got := parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())
	if !strings.Contains(got.Channel.Description, "触顶") {
		t.Errorf("描述里应当说明清单可能不完整，实际是 %q", got.Channel.Description)
	}
}

// TestWantFeedWithoutTokenReturns503 确认没配 token 时**不是**一个空 feed。
//
// 200 + 空 feed 会被理解成「我没标过任何想看」；真相是「服务读不到」。
// 空 feed 还会让 qBittorrent 静静地显示「0 条」，没有任何地方会响。
func TestWantFeedWithoutTokenReturns503(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{NoToken: true})

	rec := do(t, h, "/rss/want.xml")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "token") {
		t.Errorf("文案里应当说明是 token 的问题: %s", rec.Body.String())
	}
}

// TestWantFeedAuthErrorReturns503 确认 token 过期与 token 缺失都被判为
// 「需要用户动手」。
func TestWantFeedAuthErrorReturns503(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", wantAuthErrSource{&stub.Source{}})

	rec := do(t, h, "/rss/want.xml")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "token") {
		t.Errorf("文案里应当提到 token: %s", rec.Body.String())
	}
}

// wantAuthErrSource 让 WantToWatch 返回一个凭据类错误。
//
// 不能复用 authErrSource：那个只覆盖了 CollectedActresses，
// 而这里要的恰恰是另一条路径。
type wantAuthErrSource struct{ *stub.Source }

func (wantAuthErrSource) WantToWatch(context.Context) (catalog.WantList, error) {
	return catalog.WantList{}, &appapi.AuthError{Action: "TokenExpired", Message: "token 已過期"}
}

// TestWantFeedUpstreamErrorReturns502 确认普通上游故障与凭据问题分开 ——
// 一个要重试，一个要用户动手。
func TestWantFeedUpstreamErrorReturns502(t *testing.T) {
	src := &recordingSource{wantErr: errors.New("上游炸了")}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/want.xml")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("状态码 = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "上游炸了") {
		t.Errorf("原因没有透出: %s", rec.Body.String())
	}
}

// TestWantFeedEmptyListIsStillAFeed 确认「一部都没标」是一件**正常**的事：
// 它必须返回合法 RSS（qBittorrent 要能解析它），而不是 404 或错误。
func TestWantFeedEmptyListIsStillAFeed(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &recordingSource{want: &catalog.WantList{}})

	rec := do(t, h, "/rss/want.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，空的想看清单不是错误", rec.Code)
	}
	got := parseWantFeed(t, rec.Body.String())
	if len(got.Channel.Items) != 0 {
		t.Errorf("不该有条目: %+v", got.Channel.Items)
	}
	if !strings.Contains(got.Channel.Description, "共 0 部") {
		t.Errorf("描述 = %q，它要说清楚「你还没标过任何想看」", got.Channel.Description)
	}
}

// TestWantFeedIgnoresAllowlist 钉住一条形态上的决定：
//
// 这条 feed **不受 feeds 白名单约束**。白名单描述的是「你要订哪些番号/女优」，
// 而「想看」清单的边界由你在 App 里画 —— 一个会变的列表放不进配置。
//
// 测试里同时验证白名单在同一个实例上**确实生效**（否则这条测试是空转的：
// 白名单被整个忽略时，它照样通过）。
func TestWantFeedIgnoresAllowlist(t *testing.T) {
	cfg := "provider: stub\nfeeds:\n  codes:\n    - \"ONLY-THIS\"\n"
	src := &recordingSource{want: &catalog.WantList{Works: []catalog.Work{
		wantWork("KV-1", catalog.Magnet{Infohash: wantHash, Name: "KV-1"}),
	}}}
	h := newTestServer(t, cfg, src)

	if rec := do(t, h, "/rss/code/KV-1.xml"); rec.Code != http.StatusNotFound {
		t.Fatalf("番号 feed 应当被白名单挡住（证明白名单生效），得到 %d", rec.Code)
	}
	if rec := do(t, h, "/rss/want.xml"); rec.Code != http.StatusOK {
		t.Errorf("「想看」 feed 不该被白名单挡住，得到 %d", rec.Code)
	}
}

// TestWantFeedRejectsNonGET 确认路由只认 GET。
func TestWantFeedRejectsNonGET(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	req := httptest.NewRequest(http.MethodPost, "/rss/want.xml", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
}
