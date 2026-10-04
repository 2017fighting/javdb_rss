package httpapi

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

// actressTagsRaw 是响应体的**原始键集合**，用来断言「没有 filter_tags 这个键」。
//
// 这条断言有真实价值：上游那个顶层键就叫 filter_tags，装的却是主属性。
// 服务对外沿用它会教坏下一个读它的人 —— 以为里面是标签。
type actressTagsRaw map[string]json.RawMessage

type actressTagsJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VideosCount *int   `json:"videos_count"`
	Main        []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		VideosCount *int   `json:"videos_count"`
	} `json:"main"`
	Tags []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		VideosCount *int   `json:"videos_count"`
	} `json:"tags"`
}

func decodeActressTags(t *testing.T, body string) (actressTagsJSON, actressTagsRaw) {
	t.Helper()
	var b actressTagsJSON
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("不是合法 JSON: %v\n%s", err, body)
	}
	var raw actressTagsRaw
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("不是合法 JSON: %v\n%s", err, body)
	}
	return b, raw
}

func mainAttrIDs(b actressTagsJSON) []string {
	out := make([]string, 0, len(b.Main))
	for _, m := range b.Main {
		out = append(out, m.ID)
	}
	return out
}

// TestActressTagsRouteReturnsNameMainAndTags 是本端点存在的理由：
// 一次读取同时交出显示名、她**支持的主属性**、她自己的标签。
func TestActressTagsRouteReturnsNameMainAndTags(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/actress_tags/EvkJ")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("content-type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q，want JSON（发现端点家族）", ct)
	}

	b, raw := decodeActressTags(t, rec.Body.String())
	if b.ID != "EvkJ" {
		t.Errorf("id = %q", b.ID)
	}
	if b.Name != "河北彩花" {
		t.Errorf("name = %q", b.Name)
	}
	if b.VideosCount == nil {
		t.Error("videos_count 应当出现（上游给了它）")
	}
	// main 是**她支持的**那几个，顺序原样（上游顺序 p,s,m,c）。
	if got := strings.Join(mainAttrIDs(b), ","); got != "p,s,m,c" {
		t.Errorf("main = %v, want p,s,m,c", got)
	}
	// 标签逐项带 videos_count。
	if len(b.Tags) == 0 {
		t.Fatal("tags 为空")
	}
	for _, tag := range b.Tags {
		if tag.VideosCount == nil {
			t.Errorf("标签 %s 缺 videos_count —— 上游逐项都有它", tag.ID)
		}
	}

	// 字段**不叫** filter_tags：它装的是主属性，不是标签。
	if _, ok := raw["filter_tags"]; ok {
		t.Error("响应里出现了 filter_tags —— 名字纠正就是本票要做的事之一")
	}
	// main 的项不能凭空多出 videos_count：上游的 filter_tags 没有它，
	// 编一个 0 出来等于宣布这个主属性下一部片都没有。
	for _, m := range b.Main {
		if m.VideosCount != nil {
			t.Errorf("main 项 %s 出现了上游没给过的 videos_count", m.ID)
		}
	}
}

// TestActressTagsRouteIsAnonymous 确认它不需要 token ——
// 标签筛选本身是匿名的，不该被「收藏读不到」连坐（与 /collected 的 503 相反）。
func TestActressTagsRouteIsAnonymous(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{NoToken: true})
	rec := do(t, h, "/actress_tags/EvkJ")
	if rec.Code != http.StatusOK {
		t.Fatalf("没配 token 时 = %d, want 200（匿名可读）", rec.Code)
	}
	if b, _ := decodeActressTags(t, rec.Body.String()); len(b.Tags) == 0 {
		t.Error("200 却没有标签 —— 空档会被页面当成「她没有标签」")
	}
}

// TestActressTagsRouteUpstreamFailureIs502 确认上游出错是**可见的失败**，
// 而不是一份表面上合法的空档。
func TestActressTagsRouteUpstreamFailureIs502(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{Err: errors.New("上游炸了")})
	rec := do(t, h, "/actress_tags/EvkJ")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "上游炸了") {
		t.Errorf("原因应当透出: %s", rec.Body.String())
	}
}

// TestActressTagsRouteRespectsActressWhitelist 确认女优白名单对它同样生效 ——
// 与 /rss/actress/{id}.xml 同一套语义：没放行的 id 返回 404「未知的订阅」，
// 否则页面会渲染一个订不到的链接。
func TestActressTagsRouteRespectsActressWhitelist(t *testing.T) {
	cfg := "provider: stub\nfeeds:\n  codes: [KV-328]\n  actresses:\n    - id: EvkJ\n"
	h := newTestServer(t, cfg, &stub.Source{})

	if rec := do(t, h, "/actress_tags/EvkJ"); rec.Code != http.StatusOK {
		t.Errorf("白名单里的女优 = %d, want 200", rec.Code)
	}
	rec := do(t, h, "/actress_tags/other")
	if rec.Code != http.StatusNotFound {
		t.Errorf("白名单外的女优 = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "未知的订阅") {
		t.Errorf("文案应当是「未知的订阅」: %s", rec.Body.String())
	}
}

// TestActressTagsRoutePassesIDAndMapsSourceBadRequest 验证两件事：
// 收到的 id 确实交给了数据源；数据源判定「用户写错」时映射成 400 而不是 502。
func TestActressTagsRoutePassesIDAndMapsSourceBadRequest(t *testing.T) {
	src := &recordingSource{actressTagsErr: fmt.Errorf("%w：女优 id 不合法", catalog.ErrBadRequest)}
	h := newTestServer(t, "provider: stub\n", src)

	if rec := do(t, h, "/actress_tags/83V"); rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400（不能是 502 —— 重试无用）", rec.Code)
	}
	if src.gotActressTagsID != "83V" {
		t.Errorf("数据源收到的 id = %q, want 83V", src.gotActressTagsID)
	}
}

// TestActressTagsRouteMissingIDIs404 确认空 id 是干净的 404，
// 而不是拿到一份空档（那会让页面以为「这位女优没有标签」）。
func TestActressTagsRouteMissingIDIs404(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	for _, target := range []string{"/actress_tags", "/actress_tags/"} {
		if rec := do(t, h, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}
}

// TestActressTagsRouteIsGETOnly 确认写方法被挡在门外。
func TestActressTagsRouteIsGETOnly(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	req := httptest.NewRequest(http.MethodPost, "/actress_tags/EvkJ", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
}

// TestActressNamePathStillFallsBackToID 是回归守卫：新增端点后，
// feed 标题那条路径的行为必须一个字不变 —— 取不到名字仍退回 id，
// 绝不因为取名失败把一条本来能用的 feed 弄挂。
func TestActressNamePathStillFallsBackToID(t *testing.T) {
	// stub 默认只认识 EvkJ；别的 id 取名会失败。
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	rec := do(t, h, "/rss/actress/unknown.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("取名失败不该影响 feed：状态码 = %d", rec.Code)
	}
	var f parsedFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("不是合法 RSS: %v", err)
	}
	if f.Channel.Title != "JavDB · unknown" {
		t.Errorf("标题应当退回 id，得到 %q", f.Channel.Title)
	}
}
