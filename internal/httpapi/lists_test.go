package httpapi

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

type listsResponse struct {
	Lists []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		MoviesCount int    `json:"movies_count"`
		IsDefault   bool   `json:"is_default"`
		Privacy     string `json:"privacy"`
		Feed        string `json:"feed"`
	} `json:"lists"`
	// 用指针以便区分「字段不存在」与「字段为 false」。
	Truncated    *bool `json:"truncated"`
	PagesFetched *int  `json:"pages_fetched"`
	MaxPages     *int  `json:"max_pages"`
}

// TestCollectedListsReturnsTheList 是清单发现端点的正常路径。
func TestCollectedListsReturnsTheList(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/collected_lists")
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("content-type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q，它不该是 feed", ct)
	}

	var got listsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("不是合法 JSON: %v\n%s", err, rec.Body.String())
	}
	if len(got.Lists) != 2 {
		t.Fatalf("得到 %d 条，want 2", len(got.Lists))
	}
	l := got.Lists[0]
	if l.ID != "k4EVE4" || l.Name != "遥控跳弹" || l.MoviesCount != 1 {
		t.Errorf("字段映射不对: %+v", l)
	}
	if l.Feed != "/rss/list/k4EVE4.xml" {
		t.Errorf("feed 路径 = %q，用户要能直接拿去用", l.Feed)
	}
	// is_default 只在为真时才出现（omitempty），默认清单要能看出来。
	if got.Lists[1].ID != "R9r77" || !got.Lists[1].IsDefault {
		t.Errorf("默认清单的 is_default 没透出: %+v", got.Lists[1])
	}
}

// TestCollectedListsWithoutTokenReturns503 与 /collected 同一条规矩：
// 不能返回 200 + 空列表 —— 那会被理解成「你没建过清单」。
func TestCollectedListsWithoutTokenReturns503(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{NoToken: true})

	rec := do(t, h, "/collected_lists")
	if rec.Code != 503 {
		t.Fatalf("状态码 = %d，want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "token") {
		t.Errorf("文案里应当说明是 token 的问题: %s", body)
	}
	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	if _, isList := parsed["lists"]; isList {
		t.Error("不该返回一个空列表字段 —— 那会被理解成「确实没建过清单」")
	}
}

// TestCollectedListsUpstreamErrorReturns502 确认上游故障走 502。
func TestCollectedListsUpstreamErrorReturns502(t *testing.T) {
	src := &recordingSource{listsErr: errors.New("上游炸了")}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/collected_lists")
	if rec.Code != 502 {
		t.Errorf("状态码 = %d，want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "上游炸了") {
		t.Errorf("原因没有透出: %s", rec.Body.String())
	}
}

// TestCollectedListsMarksTruncation 与 /collected 同构：触顶时给可机读信号。
func TestCollectedListsMarksTruncation(t *testing.T) {
	src := &recordingSource{lists: &catalog.ListCollection{
		Lists:        []catalog.MovieList{{ID: "k4EVE4", Name: "遥控跳弹"}},
		Truncated:    true,
		PagesFetched: 20,
		MaxPages:     20,
	}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/collected_lists")
	var got listsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Truncated == nil || !*got.Truncated {
		t.Fatalf("truncated 缺失 —— 用户分不清「就这么多」与「只读到这么多」")
	}
	if len(got.Lists) != 1 {
		t.Errorf("截断时也要交回已读到的部分，得到 %d 条", len(got.Lists))
	}
}

// TestCollectedListsOmitsTruncationSignalWhenComplete 是配套的另一半。
func TestCollectedListsOmitsTruncationSignalWhenComplete(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	rec := do(t, h, "/collected_lists")

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"truncated", "pages_fetched", "max_pages"} {
		if _, ok := raw[k]; ok {
			t.Errorf("完整清单里不该有 %q 键", k)
		}
	}
}

// TestCollectedListsFeedPathsMatchRealRoutes 是一致性检查：
// /collected_lists 给出的 feed 路径必须真的能访问。
func TestCollectedListsFeedPathsMatchRealRoutes(t *testing.T) {
	src := &stub.Source{ListWorks: []catalog.Work{{Number: "A-1",
		Magnets: []catalog.Magnet{{Infohash: "h1"}}}}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/collected_lists")
	var got listsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Lists) == 0 {
		t.Fatal("样例里应当有清单")
	}
	for _, l := range got.Lists {
		rr := do(t, h, l.Feed)
		if rr.Code != 200 {
			t.Errorf("/collected_lists 给出的路径 %s 实际返回 %d —— 用户会照着它填进 qBittorrent",
				l.Feed, rr.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// 清单 feed
// ---------------------------------------------------------------------------

// TestListFeedRendersAndUsesName 是清单 feed 的正常路径。
func TestListFeedRendersAndUsesName(t *testing.T) {
	src := &stub.Source{
		Names: map[string]string{"k4EVE4": "遥控跳弹"},
		ListWorks: []catalog.Work{{Number: "DASS-564", Title: "T",
			Magnets: []catalog.Magnet{{Infohash: "h1", Name: "DASS-564"}}}},
	}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/list/k4EVE4.xml")
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	var f parsedFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("不是合法 RSS: %v", err)
	}
	if f.Channel.Title != "JavDB · 遥控跳弹" {
		t.Errorf("channel title = %q，应当用清单名", f.Channel.Title)
	}
	if len(f.Channel.Items) != 1 {
		t.Errorf("条目数 = %d，want 1", len(f.Channel.Items))
	}
}

// TestListFeedTitleFallsBackToID 确认取名失败不影响 feed。
//
// 这条比女优那条更容易发生：`privacy: "own"` 的清单**匿名读不到名字**
// （上游返回 NoPermission），而订阅本身照常可用。
func TestListFeedTitleFallsBackToID(t *testing.T) {
	src := &stub.Source{
		ListWorks: []catalog.Work{{Number: "DASS-564",
			Magnets: []catalog.Magnet{{Infohash: "h1"}}}},
	}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/list/unknown-list.xml")
	if rec.Code != 200 {
		t.Fatalf("取名失败不该影响 feed：状态码 = %d", rec.Code)
	}
	var f parsedFeed
	_ = xml.Unmarshal(rec.Body.Bytes(), &f)
	if f.Channel.Title != "JavDB · unknown-list" {
		t.Errorf("标题应当退回 id，得到 %q", f.Channel.Title)
	}
	if len(f.Channel.Items) == 0 {
		t.Error("内容不该受影响")
	}
}

// TestListFeedBadPath404 确认路径形状不对时是 404 而不是 500。
func TestListFeedBadPath404(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})
	for _, p := range []string{"/rss/list/.xml", "/rss/list/a/b.xml"} {
		if rec := do(t, h, p); rec.Code != 404 {
			t.Errorf("%s = %d, want 404", p, rec.Code)
		}
	}
}

// TestListFeedRespectsWhitelist 确认白名单对清单同样生效。
//
// 这一条重要是因为白名单的承诺是「只有列出的订阅会被服务」——
// 如果新路由绕开它，那句承诺就变成了半真。
func TestListFeedRespectsWhitelist(t *testing.T) {
	cfg := "provider: stub\nfeeds:\n  codes: [KV-328]\n  lists: [k4EVE4]\n"
	h := newTestServer(t, cfg, &stub.Source{
		ListWorks: []catalog.Work{{Number: "A-1", Magnets: []catalog.Magnet{{Infohash: "h"}}}},
	})

	if rec := do(t, h, "/rss/list/k4EVE4.xml"); rec.Code != 200 {
		t.Errorf("白名单里的清单 = %d，want 200", rec.Code)
	}
	if rec := do(t, h, "/rss/list/other.xml"); rec.Code != 404 {
		t.Errorf("白名单外的清单 = %d，want 404（不是 403 —— 不确认它是否存在）", rec.Code)
	}
}

// TestListFeedPassesParamsAndStripsOwn 确认参数分离与女优订阅一致：
// since/page/limit 不进上游，pages 进，其余原样透传。
func TestListFeedPassesParamsAndStripsOwn(t *testing.T) {
	src := &recordingSource{listWorks: []catalog.Work{
		{Number: "A-1", ReleaseDate: "2026-08-01", Magnets: []catalog.Magnet{{Infohash: "h"}}},
		{Number: "A-2", ReleaseDate: "2025-01-01", Magnets: []catalog.Magnet{{Infohash: "h2"}}},
	}}
	h := newTestServer(t, "provider: stub\n", src)

	rec := do(t, h, "/rss/list/k4EVE4.xml?pages=3&sort_by=score&since=2026-01-01&page=9&limit=7")
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	if src.listID != "k4EVE4" {
		t.Errorf("List 收到的 id = %q", src.listID)
	}
	if got := src.listParams.Get("pages"); got != "3" {
		t.Errorf("pages 应当流到 appapi 那一层，得到 %q", got)
	}
	if got := src.listParams.Get("sort_by"); got != "score" {
		t.Errorf("sort_by 应当原样透传，得到 %q", got)
	}
	for _, k := range []string{"since", "page", "limit"} {
		if _, ok := src.listParams[k]; ok {
			t.Errorf("%s 是本服务自有参数，不该往下流", k)
		}
	}

	// since 在本层消费：只留 2026 之后的。
	var f parsedFeed
	_ = xml.Unmarshal(rec.Body.Bytes(), &f)
	if len(f.Channel.Items) != 1 {
		t.Errorf("since=2026-01-01 应当只剩 1 条，得到 %d 条", len(f.Channel.Items))
	}
}

// TestListFeedMapsBadRequestTo400 确认「URL 写错」与「上游出错」分开。
//
// 清单订阅上有一个真实且**危险**的写错方式：自己传 filter_by 时把实体 id
// 写成别的清单 —— 上游会静默返回**另一份清单**的作品，而 feed 标题写着你这份。
func TestListFeedMapsBadRequestTo400(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", badRequestListSource{&stub.Source{}})

	rec := do(t, h, "/rss/list/k4EVE4.xml?filter_by=0:l:OTHER::")
	if rec.Code != 400 {
		t.Fatalf("状态码 = %d，want 400（不能是 502 —— 重试无用）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "参数") {
		t.Errorf("文案没透出: %s", rec.Body.String())
	}
}

type badRequestListSource struct{ *stub.Source }

func (badRequestListSource) List(context.Context, string, url.Values) ([]catalog.Work, error) {
	return nil, fmt.Errorf("%w：filter_by 的实体 id 与 URL 不一致", catalog.ErrBadRequest)
}

// TestListFeedSurvivesPanicInNameLookup 确认取名处的 panic 不会杀掉进程。
func TestListFeedSurvivesPanicInNameLookup(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", panicListNameSource{&stub.Source{
		ListWorks: []catalog.Work{{Number: "A-1", Magnets: []catalog.Magnet{{Infohash: "h"}}}},
	}})

	rec := do(t, h, "/rss/list/k4EVE4.xml")
	if rec.Code != 200 {
		t.Fatalf("取名 panic 不该影响 feed：状态码 = %d", rec.Code)
	}
	var f parsedFeed
	_ = xml.Unmarshal(rec.Body.Bytes(), &f)
	if f.Channel.Title != "JavDB · k4EVE4" {
		t.Errorf("标题应当退回 id，得到 %q", f.Channel.Title)
	}
}

type panicListNameSource struct{ *stub.Source }

func (panicListNameSource) ListName(context.Context, string) (string, error) {
	panic("故意在取名时炸")
}

// TestListAndActressFeedsShareGuid 确认同一部作品在两条 feed 里的 guid 相同。
//
// 这是「跨 feed 自动去重」的落点：guid 取磁链 infohash，而槽位规则是同一个
// 函数（catalog.Select）—— 因此清单订阅与女优订阅不会把同一部片发两次。
func TestListAndActressFeedsShareGuid(t *testing.T) {
	src := &stub.Source{
		ActressWorks: []catalog.Work{{ID: "w1", Number: "A-1",
			Magnets: []catalog.Magnet{{Infohash: "same-hash", CreatedAt: "09/27/2026"}}}},
		ListWorks: []catalog.Work{{ID: "w1", Number: "A-1",
			Magnets: []catalog.Magnet{{Infohash: "same-hash", CreatedAt: "09/27/2026"}}}},
	}
	h := newTestServer(t, "provider: stub\n", src)

	guidOf := func(path string) string {
		var f parsedFeed
		rec := do(t, h, path)
		if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
			t.Fatalf("%s 不是合法 RSS: %v", path, err)
		}
		if len(f.Channel.Items) == 0 {
			t.Fatalf("%s 没有条目", path)
		}
		return f.Channel.Items[0].GUID
	}
	a, l := guidOf("/rss/actress/EvkJ.xml"), guidOf("/rss/list/k4EVE4.xml")
	if a != l || a == "" {
		t.Errorf("同一部作品在两条 feed 里的 guid 应当相同（%q vs %q）", a, l)
	}
}
