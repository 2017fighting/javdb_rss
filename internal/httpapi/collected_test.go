package httpapi

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

type collectedResponse struct {
	Actresses []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		VideosCount int    `json:"videos_count"`
		Feed        string `json:"feed"`
	} `json:"actresses"`
	// 截断信号。用指针以便区分「字段不存在」与「字段为 false」——
	// 本票要求未达上限时这个信号**完全不存在**。
	Truncated    *bool `json:"truncated"`
	PagesFetched *int  `json:"pages_fetched"`
	MaxPages     *int  `json:"max_pages"`
}

// TestCollectedReturnsTheList 是发现端点的正常路径。
//
// 它的产物是**给人看的清单**，不是 feed —— 用户看到后自己决定把哪些 id
// 填进配置或 URL。因此每一条都要带上可以直接用的 feed 路径。
func TestCollectedReturnsTheList(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/collected")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("content-type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q，它不该是 feed", ct)
	}

	var got collectedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("不是合法 JSON: %v\n%s", err, rec.Body.String())
	}
	if len(got.Actresses) != 3 {
		t.Fatalf("得到 %d 条，want 3", len(got.Actresses))
	}
	a := got.Actresses[0]
	if a.ID != "EvkJ" || a.Name != "河北彩花" || a.VideosCount != 229 {
		t.Errorf("字段映射不对: %+v", a)
	}
	if a.Feed != "/rss/actress/EvkJ.xml" {
		t.Errorf("feed 路径 = %q，用户要能直接拿去用", a.Feed)
	}
}

// TestCollectedWithoutTokenReturns503 是最要紧的一条。
//
// 没有 token 时**绝不能**返回 200 + 空列表：用户会以为「我没收藏任何人」，
// 而真相是「服务读不到」。两者需要完全不同的动作。
func TestCollectedWithoutTokenReturns503(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{NoToken: true})

	rec := do(t, h, "/collected")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，want 503（不能是 200，也不能是 404）", rec.Code)
	}
	body := rec.Body.String()
	// 文案必须指向动作，而不只是报一个状态码。
	if !strings.Contains(body, "token") {
		t.Errorf("文案里应当说明是 token 的问题: %s", body)
	}
	// 必须解释清楚，而不是一个光秃秃的空列表。
	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	if _, isList := parsed["actresses"]; isList {
		t.Error("不该返回一个空列表字段 —— 那会被理解成「确实没收藏」")
	}
}

// TestCollectedAuthErrorAlsoReturns503 确认 token 过期与 token 缺失都被判为
// 「需要用户动手」，但文案不同。
func TestCollectedAuthErrorAlsoReturns503(t *testing.T) {
	src := &stub.Source{}
	// 用一个会返回 AuthError 的包装来模拟 token 过期。
	h := newTestServer(t, "provider: stub\n", authErrSource{src})
	rec := do(t, h, "/collected")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "token") {
		t.Errorf("文案里应当提到 token: %s", rec.Body.String())
	}
}

// authErrSource 让 CollectedActresses 返回一个凭据类错误。
//
// 嵌入指针而不是值：stub.Source 的方法都是指针接收者，
// 按值嵌入不会提升它们。
type authErrSource struct{ *stub.Source }

func (authErrSource) CollectedActresses(context.Context) (catalog.Collection, error) {
	return catalog.Collection{}, &appapi.AuthError{Action: "TokenExpired", Message: "token 已過期"}
}

// TestCollectedUpstreamErrorReturns502 确认普通上游故障与凭据问题分开 ——
// 一个要重试，一个要用户动手。
func TestCollectedUpstreamErrorReturns502(t *testing.T) {
	src := &recordingSource{collectedErr: errors.New("上游炸了")}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/collected")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("状态码 = %d，want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "上游炸了") {
		t.Errorf("原因没有透出: %s", rec.Body.String())
	}
}

// TestCollectedIsNotAFeed 确认它不是 feed 路由 ——
// 别的路由都带 .xml 后缀且在 /rss/ 下。
func TestCollectedIsNotAFeed(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	if rec := do(t, h, "/rss/collected.xml"); rec.Code != http.StatusNotFound {
		t.Errorf("/rss/collected.xml = %d，发现端点不该伪装成 feed", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// 女优 feed 标题用真名字
// ---------------------------------------------------------------------------

// TestActressFeedTitleUsesRealName 确认标题从 id 换成了真名字。
func TestActressFeedTitleUsesRealName(t *testing.T) {
	src := &stub.Source{Names: map[string]string{"EvkJ": "河北彩花"}}
	h := newTestServer(t, "provider: stub\n", src)

	var f parsedFeed
	rec := do(t, h, "/rss/actress/EvkJ.xml")
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("不是合法 RSS: %v", err)
	}
	if f.Channel.Title != "JavDB · 河北彩花" {
		t.Errorf("channel title = %q，应当用真名字", f.Channel.Title)
	}
}

// TestActressFeedTitleFallsBackToID 是配套的兜底：
// 拿不到名字时标题退回 id，而且 **feed 必须照常工作**。
//
// 这是刻意的设计取舍：名字只是好看，不该因为一次取名失败就让整个 feed 挂掉。
func TestActressFeedTitleFallsBackToID(t *testing.T) {
	src := &stub.Source{} // Names 为 nil，且只有一个样例名字
	h := newTestServer(t, "provider: stub\n", src)

	var f parsedFeed
	rec := do(t, h, "/rss/actress/unknown-id.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("取名失败不该影响 feed：状态码 = %d", rec.Code)
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("不是合法 RSS: %v", err)
	}
	if f.Channel.Title != "JavDB · unknown-id" {
		t.Errorf("channel title = %q，应当退回 id", f.Channel.Title)
	}
	if len(f.Channel.Items) == 0 {
		t.Error("内容不该受影响")
	}
}

// TestCodeFeedTitleUnchanged 确认番号 feed 的标题没被这次改动波及。
func TestCodeFeedTitleUnchanged(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	var f parsedFeed
	rec := do(t, h, "/rss/code/KV-328.xml")
	_ = xml.Unmarshal(rec.Body.Bytes(), &f)
	if f.Channel.Title != "JavDB · KV-328" {
		t.Errorf("番号 feed 标题 = %q", f.Channel.Title)
	}
}

// TestCollectedFeedPathsMatchRealRoutes 是一致性检查：
// /collected 里给出的 feed 路径必须真的能被访问。
//
// 这条能抓到「清单里写了一个不存在的路由」这种低级但很难发现的错误 ——
// 用户会照着它去 qBittorrent 里填，然后拿到 404。
func TestCollectedFeedPathsMatchRealRoutes(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/collected")
	var got collectedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, a := range got.Actresses {
		if a.Feed == "" {
			t.Errorf("%s 没有 feed 路径", a.ID)
			continue
		}
		rr := do(t, h, a.Feed)
		if rr.Code != http.StatusOK {
			t.Errorf("/collected 给出的路径 %s 实际返回 %d —— 用户会照着它填进 qBittorrent",
				a.Feed, rr.Code)
		}
	}
}

// TestCollectedMarksTruncation 是本票在 HTTP 层的落点：
//
// 数据源报告触顶截断时，响应里必须有**可机读的明确信号**，
// 而不是照常 200 + 一份看起来完整的列表。
func TestCollectedMarksTruncation(t *testing.T) {
	src := &recordingSource{
		collected: &catalog.Collection{
			Actresses:    []catalog.Actress{{ID: "EvkJ"}},
			Truncated:    true,
			PagesFetched: 20,
			MaxPages:     20,
		},
	}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/collected")
	if rec.Code != http.StatusOK {
		// 截断仍然返回已读到的部分，因此是 200；判定靠字段而不是状态码。
		t.Fatalf("状态码 = %d，want 200", rec.Code)
	}
	var got collectedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Truncated == nil || !*got.Truncated {
		t.Fatalf("truncated 字段缺失或为 false —— 用户无法分辨「就这么多」与「只读到这么多」：%s", rec.Body.String())
	}
	if got.PagesFetched == nil || *got.PagesFetched != 20 {
		t.Errorf("pages_fetched 应当说明读了多少页: %v", got.PagesFetched)
	}
	if got.MaxPages == nil || *got.MaxPages != 20 {
		t.Errorf("max_pages 应当说明卡在哪: %v", got.MaxPages)
	}
	// 已读到的部分仍要交出去 —— 它比空列表有用。
	if len(got.Actresses) != 1 {
		t.Errorf("截断时也应当返回已读到的部分，得到 %d 条", len(got.Actresses))
	}
}

// TestCollectedOmitsTruncationSignalWhenComplete 是配套的另一半：
// 未达上限时，这个信号必须**完全不存在** —— 不能是一个永远为真的字段。
func TestCollectedOmitsTruncationSignalWhenComplete(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/collected")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var got collectedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Truncated != nil {
		t.Errorf("完整清单里不该出现 truncated 字段，得到 %v", *got.Truncated)
	}
	// 也不该出现解释信号的那两个字段。
	if got.PagesFetched != nil || got.MaxPages != nil {
		t.Errorf("完整清单里不该出现 pages_fetched/max_pages: %v %v", got.PagesFetched, got.MaxPages)
	}
	// 直接查原始 JSON，确保不是「有字段但为 false/0」。
	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	for _, k := range []string{"truncated", "pages_fetched", "max_pages"} {
		if _, ok := raw[k]; ok {
			t.Errorf("JSON 里不该有 %q 键", k)
		}
	}
}

// TestCollectedRejectsNonGET 确认只接受 GET（与其它路由一致）。
func TestCollectedRejectsNonGET(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	req := httptest.NewRequest(http.MethodPost, "/collected", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /collected = %d, want 405", rec.Code)
	}
}

// TestActressFeedFetchesNameConcurrently 钉住一个我引入过又修掉的延迟回归。
//
// 最初 actressTitle 在 Actress() **返回之后**才被调用，于是每次女优 feed 轮询
// 都白多等一个上游往返（实测约 200-430ms）—— 而 qBittorrent 每 15 分钟就会
// 打一次这个 feed，是热路径。两次请求互不依赖，没有理由串行。
//
// 这个测试用「双向依赖」把并发性变成可判定的：
// ActressName 等 Actress 先开始，Actress 等 ActressName 先被调用。
// 若两者串行，就会互等到超时；并发则立即通过。
func TestActressFeedFetchesNameConcurrently(t *testing.T) {
	actressStarted := make(chan struct{})
	nameCalled := make(chan struct{})

	// 用一个互相等待的 source —— 串行实现会互等到超时。
	h := newTestServer(t, "provider: stub\n", &barrierSource{
		actressStarted: actressStarted,
		nameCalled:     nameCalled,
	})

	done := make(chan int, 1)
	go func() {
		rec := do(t, h, "/rss/actress/EvkJ.xml")
		done <- rec.Code
	}()

	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Errorf("状态码 = %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取作品与取名字似乎串行了 —— 它们互不依赖，应当并发发起")
	}
}

// barrierSource 让 Actress 与 ActressName 互相等待，用来证明它们并发执行。
type barrierSource struct {
	stub.Source
	actressStarted chan struct{}
	nameCalled     chan struct{}
}

func (b *barrierSource) Actress(ctx context.Context, _ string, _ url.Values) ([]catalog.Work, error) {
	close(b.actressStarted)
	// 等名字请求也被发起。
	//
	// ⚠️ 兜底分支必须**返回错误**，不能照常返回数据：
	// 最初这里写的是「等 2 秒然后照常返回」，结果串行实现也能走完流程，
	// 测试拿到 200 就通过了 —— 一个测不出它要测的东西的测试。
	// 返回错误才能让串行实现明确地失败。
	select {
	case <-b.nameCalled:
	case <-time.After(2 * time.Second):
		return nil, errors.New("名字请求没有被并发发起 —— 它们被串行执行了")
	}
	return []catalog.Work{{Number: "A-1", Title: "T",
		Magnets: []catalog.Magnet{{Infohash: "h"}}}}, nil
}

func (b *barrierSource) ActressName(ctx context.Context, _ string) (string, error) {
	<-b.actressStarted
	close(b.nameCalled)
	return "并发名字", nil
}

// TestActressFeedReturns400ForMalformedMask 确认「你写错了 URL」与「上游出错了」
// 被分开：前者 400（重试无用），后者 502（重试有用）。
//
// 这条对应的真实失败模式：用户把主属性拼成 0:a:EvkJ:apmc::（而不是逗号分隔的
// 0:a:EvkJ:a,p,m,c::）。上游对拼错的掩码是**静默忽略**的 —— 不报错，
// 只是返回该女优的全部作品。因此如果不在这里拦，用户会拿到一个看起来正常、
// 但实际没有应用任何条件的 feed。
// 注意这一条验的是**映射**，不是校验本身 —— 掩码格式是上游契约，
// 因此校验归 appapi 所有（见 appapi 的 TestBuildEntityFilterRejectsConcatenatedFlags）。
// httpapi 的责任只是把 ErrBadRequest 翻译成 400 而不是 502。
func TestActressFeedMapsBadRequestTo400(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", badRequestSource{&stub.Source{}})

	rec := do(t, h, "/rss/actress/EvkJ.xml")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，want 400（不能是 502 —— 重试无用）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "参数") {
		t.Errorf("文案没透出: %s", rec.Body.String())
	}
}

// badRequestSource 让 Actress 返回一个「用户参数写错」的错误。
type badRequestSource struct{ *stub.Source }

func (badRequestSource) Actress(context.Context, string, url.Values) ([]catalog.Work, error) {
	return nil, fmt.Errorf("%w：filter_by 的主属性应当用逗号分隔", catalog.ErrBadRequest)
}

// TestCodeFeedUnaffectedByMaskValidation 确认新校验只作用于女优订阅。
func TestCodeFeedUnaffectedByMaskValidation(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	if rec := do(t, h, "/rss/code/KV-328.xml"); rec.Code != http.StatusOK {
		t.Errorf("番号 feed 不该受影响：%d", rec.Code)
	}
}

// TestActressFeedSurvivesPanicInNameLookup 确认取名处的 panic 不会杀掉进程。
//
// net/http 只为 handler 所在的 goroutine 恢复 panic，而取名跑在一个新起的
// goroutine 里 —— 它逃出了那层保护。没有这个 recover 的话，一个 nil 解引用
// 之类的小 bug 会带走整个服务，而不只是这一个请求。
func TestActressFeedSurvivesPanicInNameLookup(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", panicNameSource{&stub.Source{}})

	rec := do(t, h, "/rss/actress/EvkJ.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("取名 panic 不该影响 feed：状态码 = %d", rec.Code)
	}
	var f parsedFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("不是合法 RSS: %v", err)
	}
	if f.Channel.Title != "JavDB · EvkJ" {
		t.Errorf("标题应当退回 id，得到 %q", f.Channel.Title)
	}
}

type panicNameSource struct{ *stub.Source }

func (panicNameSource) ActressName(context.Context, string) (string, error) {
	panic("故意在取名时炸")
}

// TestCollectedExposesGender 确认 /collected 把 gender 交给前端。
//
// 带不带 omitempty 是有意的：0 是女优（绝大多数），用 omitempty 会让最常见的
// 取值从 JSON 里消失，消费方只能靠「键不在就当成 0」来猜。
func TestCollectedExposesGender(t *testing.T) {
	src := &recordingSource{collected: &catalog.Collection{
		Actresses: []catalog.Actress{
			{ID: "D2EdJ", Name: "花守夏歩", VideosCount: 179, Gender: catalog.GenderFemale},
			{ID: "PpQ0", Name: "森林原人", VideosCount: 12, Gender: catalog.GenderMale},
		},
	}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/collected")
	var raw struct {
		Actresses []map[string]any `json:"actresses"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Actresses) != 2 {
		t.Fatalf("得到 %d 条", len(raw.Actresses))
	}
	for i, want := range []float64{catalog.GenderFemale, catalog.GenderMale} {
		got, ok := raw.Actresses[i]["gender"]
		if !ok {
			t.Fatalf("第 %d 条的 JSON 里没有 gender 键 —— 前端分不出男女", i+1)
		}
		if got != want {
			t.Errorf("第 %d 条 gender = %v, want %v", i+1, got, want)
		}
	}
}
