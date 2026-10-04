package httpapi

import (
	"context"
	"encoding/json"
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
	"github.com/2017fighting/javdb_rss/internal/health"
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

	// 收藏列表相关的可控制行为
	collected       *catalog.Collection
	collectedErr    error
	collectedCalled int
	// 「想看」清单相关的可控制行为
	want       *catalog.WantList
	wantErr    error
	wantCalled int
	// names 控制 ActressName 的返回；不在表里的 id 返回错误。
	names map[string]string

	// 清单相关的可控制行为
	lists       *catalog.ListCollection
	listsErr    error
	listsCalled int
	// listWorks 是 List 的返回值；listID/listParams 记录收到的输入。
	listWorks  []catalog.Work
	listErr    error
	listID     string
	listParams url.Values
}

func (r *recordingSource) Code(_ context.Context, code string) ([]catalog.Work, error) {
	r.gotCode = code
	if r.err != nil {
		return nil, r.err
	}
	return r.works, nil
}

func (r *recordingSource) ActressName(_ context.Context, id string) (string, error) {
	if r.names != nil {
		if n, ok := r.names[id]; ok {
			return n, nil
		}
	}
	return "", errors.New("没有名字")
}

func (r *recordingSource) CollectedActresses(context.Context) (catalog.Collection, error) {
	r.collectedCalled++
	if r.collectedErr != nil {
		return catalog.Collection{}, r.collectedErr
	}
	if r.collected != nil {
		return *r.collected, nil
	}
	return catalog.Collection{}, nil
}

func (r *recordingSource) Actress(_ context.Context, id string, params url.Values) ([]catalog.Work, error) {
	r.gotActressID = id
	r.gotActressCur = params
	if r.err != nil {
		return nil, r.err
	}
	return r.works, nil
}

func (r *recordingSource) WantToWatch(context.Context) (catalog.WantList, error) {
	r.wantCalled++
	if r.wantErr != nil {
		return catalog.WantList{}, r.wantErr
	}
	if r.want != nil {
		return *r.want, nil
	}
	return catalog.WantList{}, nil
}

func (r *recordingSource) CollectedLists(context.Context) (catalog.ListCollection, error) {
	r.listsCalled++
	if r.listsErr != nil {
		return catalog.ListCollection{}, r.listsErr
	}
	if r.lists != nil {
		return *r.lists, nil
	}
	return catalog.ListCollection{}, nil
}

func (r *recordingSource) List(_ context.Context, id string, params url.Values) ([]catalog.Work, error) {
	r.listID = id
	r.listParams = params
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listWorks, nil
}

func (r *recordingSource) ListName(_ context.Context, id string) (string, error) {
	if r.names != nil {
		if n, ok := r.names[id]; ok {
			return n, nil
		}
	}
	return "", errors.New("没有名字")
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
	// 带中文字幕的那部作品应当选中中文字幕磁链。
	var sawSub bool
	for _, it := range f.Channel.Items {
		if strings.Contains(it.Title, "中文字幕") {
			sawSub = true
			if !strings.HasPrefix(it.GUID, "cc33dd44") {
				t.Errorf("中文字幕条选的不是第一条中文字幕磁链: %q", it.GUID)
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

// TestActressPassthrough 钉住 ticket 07 的做法：App 女优页的参数原样搬运。
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

// ---------------------------------------------------------------------------
// 上游健康检查端点
// ---------------------------------------------------------------------------

func newTestServerWithHealth(t *testing.T, cfgYAML string, src catalog.Source) (http.Handler, *health.Tracker) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	holder, err := config.NewHolder(p)
	if err != nil {
		t.Fatalf("配置: %v", err)
	}
	tr := health.NewTracker()
	s := New(holder, src, slog.New(slog.NewTextHandler(io.Discard, nil))).WithUpstream(tr)
	s.now = func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	return s.Handler(), tr
}

// TestReadinessBeforeFirstCheck 钉住启动瞬间的行为：
// 还没检查过时应当**就绪**，否则滚动发布会因为探针还没跑而卡住。
func TestReadinessBeforeFirstCheck(t *testing.T) {
	h, _ := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})
	if rec := do(t, h, "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("首次检查前 /readyz = %d, want 200", rec.Code)
	}
}

// TestLivenessIgnoresUpstream 是最重要的一条：
// 签名失效重启一千次也没用，把上游状态掺进 liveness 会造成重启循环。
func TestLivenessIgnoresUpstream(t *testing.T) {
	h, tr := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})
	tr.Record(health.Status{OK: false, CheckedAt: time.Now(),
		Action: "InvalidSignature", Err: "無效的簽名"})

	if rec := do(t, h, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("上游坏了时 /healthz = %d, want 200（进程还活着）", rec.Code)
	}
	if rec := do(t, h, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("上游坏了时 /readyz = %d, want 503", rec.Code)
	}
}

func TestUpstreamDetailJSON(t *testing.T) {
	h, tr := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})

	// 未检查过
	var body map[string]any
	rec := do(t, h, "/healthz/upstream")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("不是合法 JSON: %v", err)
	}
	if body["checked"] != false {
		t.Errorf("checked = %v, want false", body["checked"])
	}
	if rec.Code != http.StatusOK {
		t.Errorf("未检查过时应当 200，得到 %d", rec.Code)
	}

	// 检查过且失败
	tr.Record(health.Status{OK: false, CheckedAt: time.Now(), Latency: 444 * time.Millisecond,
		Action: "InvalidSignature", Err: "javdb api (HTTP 400): InvalidSignature: 無效的簽名",
		Guidance: "去看 javdb-cli 是否已跟进"})
	rec = do(t, h, "/healthz/upstream")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("不是合法 JSON: %v", err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("失败时应当 503，得到 %d", rec.Code)
	}
	// signature_broken 是告警规则要匹配的字段，必须准确。
	if body["signature_broken"] != true {
		t.Errorf("signature_broken = %v, want true", body["signature_broken"])
	}
	if body["action"] != "InvalidSignature" {
		t.Errorf("action = %v", body["action"])
	}
	if body["latency_ms"] != float64(444) {
		t.Errorf("latency_ms = %v", body["latency_ms"])
	}
	// 检查方给的处置动作要原样透出：机读的消费方（CronJob / 告警规则）
	// 拿到它才能说出「该干什么」，而不是只报一个错误名。
	if body["next_step"] != "去看 javdb-cli 是否已跟进" {
		t.Errorf("next_step = %v, want 检查方给的处置动作", body["next_step"])
	}
}

// TestSignatureBrokenFieldForBothShapes 确认两种签名失败形态都被标为 broken。
func TestSignatureBrokenFieldForBothShapes(t *testing.T) {
	for _, action := range []string{"InvalidSignature", "ParameterInvalid"} {
		t.Run(action, func(t *testing.T) {
			h, tr := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})
			tr.Record(health.Status{OK: false, CheckedAt: time.Now(), Action: action, Err: "x"})

			var body map[string]any
			rec := do(t, h, "/healthz/upstream")
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["signature_broken"] != true {
				t.Errorf("action=%s 时 signature_broken = %v, want true", action, body["signature_broken"])
			}
		})
	}
}

// TestTransientFailureIsNotSignatureBroken 确认普通故障不会被标成签名问题 ——
// 误报的代价是有人半夜被叫起来改代码，而其实只需要重试。
func TestTransientFailureIsNotSignatureBroken(t *testing.T) {
	h, tr := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})
	tr.Record(health.Status{OK: false, CheckedAt: time.Now(),
		Err: "请求 /api/v1/startup: dial tcp: connection refused"})

	var body map[string]any
	rec := do(t, h, "/healthz/upstream")
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("应当 503，得到 %d", rec.Code)
	}
	if body["signature_broken"] != false {
		t.Errorf("普通网络故障不该标成签名问题: %v", body["signature_broken"])
	}
}

// TestUpstreamFailureIsVisibleNotDegraded 钉住上游坏掉时的**真实**可见性。
//
// 这里曾经有个 TestDegradedFeedCarriesWarning，断言 channel 描述里有
// 「上游已停更」告警。它**假通过**了 —— 因为它用的是永远成功的 stub 数据源，
// 于是走到了那段渲染逻辑。而真实路径下上游一坏，src.Code(...) 就先返回错误、
// 直接 502，那段描述永远渲染不出来（是不可达的死代码，已删）。
//
// 真实行为是：上游坏了就 502。客户端看到的是明确的失败，而不是一条
// 「安静的空 feed」—— 后者会被误认为「没有新片」，那才是最难排查的。
func TestUpstreamFailureIsVisibleNotDegraded(t *testing.T) {
	src := &recordingSource{err: errors.New("上游炸了")}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/code/KV-328.xml")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("上游失败应当 502（可见的失败），得到 %d", rec.Code)
	}
	if ct := rec.Header().Get("content-type"); strings.HasPrefix(ct, "application/rss+xml") {
		t.Error("失败时不该返回 RSS —— 那会让客户端以为这是一条正常但为空的 feed")
	}
	if !strings.Contains(rec.Body.String(), "上游炸了") {
		t.Errorf("原因应当透出: %s", rec.Body.String())
	}
}

// TestOwnParamsSplitIsCorrect 钉住自有参数的流转规则 —— 一处很容易写错的地方。
//
// 四个自有参数的去向**并不相同**：
//
//	since  本层消费，不该往下流
//	page   无人消费，不该往下流（分页由 pages 控制）
//	limit  无人消费，不该往下流（固定为上游上限 50）
//	pages  由 **appapi** 消费，**必须**往下流
//
// 我在实现这条时第一版把四个全剔了，包括 pages —— 那样翻页会静默失效。
// 所以这条测试的重点是那个例外：pages 必须在。
func TestOwnParamsSplitIsCorrect(t *testing.T) {
	src := &recordingSource{works: []catalog.Work{{
		Number: "A-1", Title: "T", Magnets: []catalog.Magnet{{Infohash: "h"}},
	}}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/actress/EvkJ.xml?since=2026-01-01&page=9&limit=5&pages=3&sort_by=score")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}

	got := src.gotActressCur
	if got == nil {
		t.Fatal("下游没收到任何参数")
	}
	// 必须流下去的
	if got.Get("pages") != "3" {
		t.Errorf("pages = %q —— 它由 appapi 消费，被误剔就会静默失去翻页能力", got.Get("pages"))
	}
	if got.Get("sort_by") != "score" {
		t.Errorf("透传参数 sort_by = %q，不该被动", got.Get("sort_by"))
	}
	// 不该流下去的
	for _, k := range []string{"since", "page", "limit"} {
		if v := got.Get(k); v != "" {
			t.Errorf("%s = %q，不该流到下游（它会污染 dedupe 的合并 key）", k, v)
		}
	}
}
