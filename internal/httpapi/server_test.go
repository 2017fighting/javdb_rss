package httpapi

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/config"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

// 编译期确认假实现满足端口 —— 接口是结构性满足的，没有这行就只会在
// main 装配时才炸。
var _ catalog.Source = (*stub.Source)(nil)

// recordingSource 记下收到的调用，用来验证透传参数确实原样到达了数据源。
type recordingSource struct {
	works         []catalog.Work
	gotActressID  string
	gotActressCur url.Values
	gotCode       string
	err           error
}

func (r *recordingSource) Code(_ context.Context, code string) ([]catalog.Work, error) {
	r.gotCode = code
	if r.err != nil {
		return nil, r.err
	}
	return r.works, nil
}

func (r *recordingSource) Actress(_ context.Context, id string, params url.Values) ([]catalog.Work, error) {
	r.gotActressID = id
	r.gotActressCur = params
	if r.err != nil {
		return nil, r.err
	}
	return r.works, nil
}

func newTestServer(t *testing.T, cfgYAML string, src catalog.Source) http.Handler {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	holder, err := config.NewHolder(p)
	if err != nil {
		t.Fatalf("配置: %v", err)
	}
	s := New(holder, src, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	return s.Handler()
}

type parsedFeed struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title     string `xml:"title"`
			Link      string `xml:"link"`
			GUID      string `xml:"guid"`
			Category  string `xml:"category"`
			Enclosure struct {
				URL string `xml:"url,attr"`
			} `xml:"enclosure"`
		} `xml:"item"`
	} `xml:"channel"`
}

func do(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestCodeFeedEndToEnd 走完整链路：路由 → 假数据源 → 槽位规则 → RSS。
func TestCodeFeedEndToEnd(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/rss/code/KV-328.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("content-type"); !strings.HasPrefix(ct, "application/rss+xml") {
		t.Errorf("content-type = %q", ct)
	}

	var f parsedFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("响应不是合法 RSS: %v\n%s", err, rec.Body.String())
	}
	if len(f.Channel.Items) != 2 {
		t.Fatalf("得到 %d 条 item，want 2（样例里有一条无磁链应被跳过）\n%s",
			len(f.Channel.Items), rec.Body.String())
	}
	for _, it := range f.Channel.Items {
		if it.GUID == "" {
			t.Error("有 item 缺 guid")
		}
		if !strings.HasPrefix(it.Enclosure.URL, "magnet:?xt=urn:btih:") {
			t.Errorf("enclosure 不是磁力链接: %q", it.Enclosure.URL)
		}
		if it.GUID != strings.TrimPrefix(strings.SplitN(it.Enclosure.URL, "&", 2)[0], "magnet:?xt=urn:btih:") {
			t.Errorf("guid 与 enclosure 的 infohash 不一致: %q vs %q", it.GUID, it.Enclosure.URL)
		}
	}
	// 带字幕的那部作品应当选中字幕磁链。
	var sawSub bool
	for _, it := range f.Channel.Items {
		if strings.Contains(it.Title, "中文字幕") {
			sawSub = true
			if !strings.HasPrefix(it.GUID, "cc33dd44") {
				t.Errorf("字幕条选的不是第一条字幕磁链: %q", it.GUID)
			}
		}
	}
	if !sawSub {
		t.Error("没有渲染出中文字幕条目")
	}
}

// TestGUIDStableAcrossRequests 无状态的关键断言：
// 同样的数据，两次请求产出的 guid 必须逐字节相同，否则客户端会重复下载。
func TestGUIDStableAcrossRequests(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	first := extractGUIDs(t, do(t, h, "/rss/code/KV-328.xml").Body.String())
	// 第二次请求的 pubDate 不同（now 被测试固定，这里换一个 handler 也一样），
	// 但 guid 必须完全一致。
	second := extractGUIDs(t, do(t, h, "/rss/code/KV-328.xml").Body.String())

	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("guid 数量不一致: %v vs %v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("guid[%d] 不稳定: %q vs %q", i, first[i], second[i])
		}
	}
}

func extractGUIDs(t *testing.T, body string) []string {
	t.Helper()
	var f parsedFeed
	if err := xml.Unmarshal([]byte(body), &f); err != nil {
		t.Fatalf("解析 feed: %v", err)
	}
	out := make([]string, 0, len(f.Channel.Items))
	for _, it := range f.Channel.Items {
		out = append(out, it.GUID)
	}
	return out
}

// TestActressPassthrough 钉住 ticket 07 的做法：App 演员页的参数原样搬运。
func TestActressPassthrough(t *testing.T) {
	src := &recordingSource{works: []catalog.Work{{
		Number: "A-1", Title: "T", Magnets: []catalog.Magnet{{Infohash: "h"}},
	}}}
	h := newTestServer(t, `
provider: stub
feeds:
  actresses:
    - id: EvkJ
      params:
        filter_by: apmc
        sort_by: release
`, src)

	rec := do(t, h, "/rss/actress/EvkJ.xml?order_by=asc&filter_by_tags=8,10&since=2026-01-01")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if src.gotActressID != "EvkJ" {
		t.Errorf("id = %q", src.gotActressID)
	}
	// 白名单配的参数与 URL 上的参数应当合并。
	if got := src.gotActressCur.Get("filter_by"); got != "apmc" {
		t.Errorf("filter_by = %q, want apmc（来自配置）", got)
	}
	if got := src.gotActressCur.Get("sort_by"); got != "release" {
		t.Errorf("sort_by = %q", got)
	}
	if got := src.gotActressCur.Get("order_by"); got != "asc" {
		t.Errorf("order_by = %q, want asc（URL 覆盖）", got)
	}
	if got := src.gotActressCur.Get("filter_by_tags"); got != "8,10" {
		t.Errorf("filter_by_tags = %q，多值/逗号参数应原样搬运", got)
	}
	// since 是本服务自有参数，不该被当成透传参数送给上游。
	if got := src.gotActressCur.Get("since"); got != "" {
		t.Errorf("since 被误当成透传参数送上游了: %q", got)
	}
}

// TestSinceFilter 确认 since 真的在过滤，且缺发行日期的作品被保留。
func TestSinceFilter(t *testing.T) {
	src := &recordingSource{works: []catalog.Work{
		{Number: "OLD-1", Title: "旧", ReleaseDate: "2025-12-31",
			Magnets: []catalog.Magnet{{Infohash: "old"}}},
		{Number: "NEW-1", Title: "新", ReleaseDate: "2026-06-01",
			Magnets: []catalog.Magnet{{Infohash: "new"}}},
		{Number: "NODATE-1", Title: "没有日期",
			Magnets: []catalog.Magnet{{Infohash: "nodate"}}},
	}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/actress/EvkJ.xml?since=2026-01-01")
	guids := extractGUIDs(t, rec.Body.String())

	if len(guids) != 2 {
		t.Fatalf("得到 %d 条，want 2（旧作被滤掉、缺日期的保留）: %v", len(guids), guids)
	}
	joined := strings.Join(guids, ",")
	if strings.Contains(joined, "old") {
		t.Error("2025-12-31 的作品没有被 since 滤掉")
	}
	if !strings.Contains(joined, "new") || !strings.Contains(joined, "nodate") {
		t.Errorf("该保留的没保留: %v", guids)
	}
}

// TestAllowlistReturns404 白名单外的订阅返回 404，且不区分「不存在」与「被禁止」。
func TestAllowlistReturns404(t *testing.T) {
	h := newTestServer(t, "provider: stub\nfeeds:\n  codes: [KV-328]\n  actresses: []\n", &stub.Source{})
	if rec := do(t, h, "/rss/code/KV-328.xml"); rec.Code != http.StatusOK {
		t.Errorf("白名单内的番号应当 200，得到 %d", rec.Code)
	}
	if rec := do(t, h, "/rss/code/NOT-LISTED.xml"); rec.Code != http.StatusNotFound {
		t.Errorf("白名单外的番号应当 404，得到 %d", rec.Code)
	}
}

func TestRouting(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	tests := []struct {
		target string
		want   int
	}{
		{"/rss/code/KV-328.xml", http.StatusOK},
		{"/rss/code/KV-328", http.StatusOK},        // 容忍省略 .xml
		{"/rss/code/", http.StatusNotFound},        // 缺番号
		{"/rss/code/a/b.xml", http.StatusNotFound}, // 多段
		{"/rss/actress/EvkJ.xml", http.StatusOK},   //
		{"/healthz", http.StatusOK},                //
		{"/version", http.StatusOK},                //
		{"/", http.StatusNotFound},                 // 没有首页
		{"/rss/nope/x.xml", http.StatusNotFound},   //
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			if got := do(t, h, tt.target).Code; got != tt.want {
				t.Errorf("GET %s = %d, want %d", tt.target, got, tt.want)
			}
		})
	}
}

// TestAuthErrorSurfacesAs502 上游凭据问题必须是可见的失败，
// 而不是一条永远为空的 feed —— 空 feed 会被误认为「没有新片」。
func TestAuthErrorSurfacesAs502(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &recordingSource{err: errors.New("boom")})
	rec := do(t, h, "/rss/code/KV-328.xml")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("上游错误应当映射为 502，得到 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("错误原因没有透出: %s", rec.Body.String())
	}
}

func TestVersionAndHealth(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	if rec := do(t, h, "/healthz"); strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Errorf("healthz = %q", rec.Body.String())
	}
	if rec := do(t, h, "/version"); !strings.Contains(rec.Body.String(), `"provider":"stub"`) {
		t.Errorf("version = %q", rec.Body.String())
	}
}
