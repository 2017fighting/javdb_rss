package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

// tagsBody 是 /tags 响应的测试侧形态。
//
// VideosCount 用 *int 而不是 int：**这个键在不在**正是要断言的东西之一。
// 词表实测不返回作品数，编一个 `videos_count: 0` 出来等于宣布
// 「这个标签下一部片都没有」—— 那是静默假数据。
type tagsBody struct {
	Groups []struct {
		CategoryID string `json:"category_id"`
		Category   string `json:"category"`
		Tags       []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			VideosCount *int   `json:"videos_count"`
		} `json:"tags"`
	} `json:"groups"`
}

func decodeTags(t *testing.T, body string) tagsBody {
	t.Helper()
	var b tagsBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("不是合法 JSON: %v\n%s", err, body)
	}
	return b
}

func groupIDs(b tagsBody) []string {
	out := make([]string, 0, len(b.Groups))
	for _, g := range b.Groups {
		out = append(out, g.CategoryID)
	}
	return out
}

// TestTagsRouteReturnsGroupsInUpstreamOrder 是本端点存在的理由：
// 分组顺序、分组名、组内标签顺序**一律原样**——页面「按上游分组挑标签」
// 依赖的就是它，一旦我们在中间排序，同样的选择会得到不同的 URL。
func TestTagsRouteReturnsGroupsInUpstreamOrder(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/tags?type=0")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("content-type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q，want JSON（发现端点家族）", ct)
	}

	b := decodeTags(t, rec.Body.String())
	wantGroups := []string{"main", "year", "month", "subject", "role", "cloth", "body", "behavior", "play_method", "category", "duration"}
	if got := groupIDs(b); strings.Join(got, ",") != strings.Join(wantGroups, ",") {
		t.Errorf("分组顺序 = %v\n           want %v", got, wantGroups)
	}
	// 分组名与 category_id **都**保留：只留一个的话，页面要么丢中文名、
	// 要么丢可回传给上游的稳定键。
	if b.Groups[0].Category != "基本" || b.Groups[0].CategoryID != "main" {
		t.Errorf("第一组 = %+v，want {main 基本}", b.Groups[0])
	}
	// 组内顺序原样（样例刻意与上游一致：p,m,c,s,i,v）。
	var ids []string
	for _, tg := range b.Groups[0].Tags {
		ids = append(ids, tg.ID)
	}
	if strings.Join(ids, ",") != "p,m,c,s,i,v" {
		t.Errorf("基本组内标签顺序 = %v", ids)
	}
	// 上游没给作品数，我们就**不能**编一个出来。
	for _, g := range b.Groups {
		for _, tg := range g.Tags {
			if tg.VideosCount != nil {
				t.Errorf("标签 %s/%s 出现了上游没给过的 videos_count=%d", g.CategoryID, tg.ID, *tg.VideosCount)
			}
		}
	}
}

// TestTagsRouteRejectsInvalidType 是本票最要紧的一条。
//
// 上游对非法的 `type` 是**静默回落**：实测 `type=9` 与 `type=0` 的响应逐字节
// 相同。照原样透传等于把「另一个片库的词表」当成你要的答案给出去，
// 而且不会报错。因此缺省、非数字、越界一律 400，并在文案里写出有效取值。
func TestTagsRouteRejectsInvalidType(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	for _, q := range []string{"", "?type=", "?type=abc", "?type=4", "?type=-1", "?type=1.5", "?type=0x1"} {
		t.Run("type"+q, func(t *testing.T) {
			rec := do(t, h, "/tags"+q)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, want 400（上游会静默回落，绝不能透传）", rec.Code)
			}
			body := rec.Body.String()
			// 文案必须写出有效取值 —— 只说「非法」等于让用户去猜。
			for _, want := range []string{"0=有码", "1=无码", "2=欧美", "3=FC2"} {
				if !strings.Contains(body, want) {
					t.Errorf("文案里应当写出有效取值 %q: %s", want, body)
				}
			}
		})
	}
	// 四个合法取值都必须放行。
	for _, q := range []string{"0", "1", "2", "3"} {
		if rec := do(t, h, "/tags?type="+q); rec.Code != http.StatusOK {
			t.Errorf("type=%s = %d, want 200", q, rec.Code)
		}
	}
}

// TestTagsRouteIsAnonymous 确认它**不需要 token**：上游该端点实测匿名可用，
// 因此没配 token 时也必须是 200（与 /collected 的 503 相反 ——
// 后者读的是 App 里的私有标记）。
func TestTagsRouteIsAnonymous(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{NoToken: true})
	rec := do(t, h, "/tags?type=0")
	if rec.Code != http.StatusOK {
		t.Fatalf("没配 token 时 = %d, want 200（匿名可读）", rec.Code)
	}
	if len(decodeTags(t, rec.Body.String()).Groups) == 0 {
		t.Error("200 却没有分组 —— 空词表会被页面当成「这个片库没有标签」")
	}
}

// TestTagsRouteUpstreamFailureIs502 确认上游出错是**可见的失败**，
// 而不是一份表面上合法的空词表。
func TestTagsRouteUpstreamFailureIs502(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{Err: errors.New("上游炸了")})
	rec := do(t, h, "/tags?type=0")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "上游炸了") {
		t.Errorf("原因应当透出: %s", rec.Body.String())
	}
}

// TestTagsRouteRespectsZoneWhitelist 确认 feeds.zones 对词表端点同样生效 ——
// 与 /rss/tags/{zone}.xml 同一套语义。没放行的片库返回 404「未知的订阅」，
// 否则页面会渲染一个订不到的片库。
func TestTagsRouteRespectsZoneWhitelist(t *testing.T) {
	cfg := "provider: stub\nfeeds:\n  codes: [KV-328]\n  zones: [\"0\"]\n"
	h := newTestServer(t, cfg, &stub.Source{})

	if rec := do(t, h, "/tags?type=0"); rec.Code != http.StatusOK {
		t.Errorf("白名单里的片库 = %d, want 200", rec.Code)
	}
	rec := do(t, h, "/tags?type=1")
	if rec.Code != http.StatusNotFound {
		t.Errorf("白名单外的片库 = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "未知的订阅") {
		t.Errorf("文案应当是「未知的订阅」: %s", rec.Body.String())
	}
	// 越界仍在白名单之前判 —— 它是「你写错了」，不是「被禁止了」。
	if rec := do(t, h, "/tags?type=4"); rec.Code != http.StatusBadRequest {
		t.Errorf("越界片库 = %d, want 400（先于白名单）", rec.Code)
	}
}

// TestTagsVocabularyDiffersByZone 证明词表是**按片库取的**，不是写死的快照。
//
// 上游实测 0/1/2/3 各返回不同的词表（0 有 body/behavior/play_method/category、
// 2 多一个 place、3 只有 5 组）。这条在离线用 fixture 验同一件事。
func TestTagsVocabularyDiffersByZone(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	zone0 := decodeTags(t, do(t, h, "/tags?type=0").Body.String())
	zone2 := decodeTags(t, do(t, h, "/tags?type=2").Body.String())
	zone3 := decodeTags(t, do(t, h, "/tags?type=3").Body.String())

	if strings.Join(groupIDs(zone0), ",") == strings.Join(groupIDs(zone2), ",") {
		t.Error("type=0 与 type=2 返回了同一份词表 —— 词表必须是按片库取的")
	}
	if !containsString(groupIDs(zone2), "place") {
		t.Errorf("type=2 应当有 place 组: %v", groupIDs(zone2))
	}
	if containsString(groupIDs(zone0), "place") {
		t.Errorf("type=0 不该有 place 组: %v", groupIDs(zone0))
	}
	if len(zone3.Groups) >= len(zone0.Groups) {
		t.Errorf("type=3 的组数 = %d，应当明显少于 type=0 的 %d", len(zone3.Groups), len(zone0.Groups))
	}
}

// TestTagsRoutePassesZoneAndMapsSourceBadRequest 验证两件事：
// 收到的片库号确实交给了数据源；数据源判定「用户写错」时映射成 400 而不是 502。
func TestTagsRoutePassesZoneAndMapsSourceBadRequest(t *testing.T) {
	src := &recordingSource{tagErr: fmt.Errorf("%w：片库号不存在", catalog.ErrBadRequest)}
	h := newTestServer(t, "provider: stub\n", src)

	if rec := do(t, h, "/tags?type=2"); rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400（不能是 502 —— 重试无用）", rec.Code)
	}
	if src.tagZone != 2 {
		t.Errorf("数据源收到的片库号 = %d, want 2", src.tagZone)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
