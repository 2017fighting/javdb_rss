package appapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// envBody 把负载包进上游的响应信封。
//
// 服务发的每个请求都会经过 GetJSON，它会先解信封再把 data 交给调用方 ——
// 因此夹具必须是完整信封，而不是 data 本身。
// （contractprobe 落盘的原始响应是**已解信封**的 data，别直接照抄成夹具。）
func envBody(data string) string {
	return `{"success":1,"action":null,"data":` + data + `}`
}

// TestCollectedListsWithoutTokenDoesNotCallUpstream 是「没 token 就别发请求」那条规矩。
//
// 发了也必然被拒（实测 JWTVerificationError），而返回空列表会被当成
// 「你没建过清单」—— 那是最难排查的一种错。
func TestCollectedListsWithoutTokenDoesNotCallUpstream(t *testing.T) {
	called := false
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(envBody(`{"lists":[]}`)))
	})
	// 刻意不设 Token。

	_, err := c.CollectedLists(context.Background())
	if err == nil {
		t.Fatal("没 token 时应当报错")
	}
	if !strings.Contains(err.Error(), catalog.ErrNoToken.Error()) {
		t.Errorf("错误应当包装 ErrNoToken，得到 %v", err)
	}
	if called {
		t.Error("没 token 时不该发请求")
	}
}

// TestCollectedListsParsesSimpleShape 钉住 `/api/v1/lists/simple` 的线格式。
//
// 契约是实测的（2026-10-04，真 token），见 notes/lists.md：
// {"lists":[{"id","name","privacy","is_default","movies_count","has_movie"}]}
//
// `has_movie` 刻意不解析 —— 它是「你有没有把某部片收进清单」，与本服务无关。
func TestCollectedListsParsesSimpleShape(t *testing.T) {
	var gotPath string
	var gotPages []string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotPages = append(gotPages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(envBody(`{"lists":[
				{"id":"k4EVE4","name":"遥控跳弹","privacy":"open","is_default":false,"movies_count":1,"has_movie":false},
				{"id":"R9r77","name":"default","privacy":"own","is_default":true,"movies_count":6,"has_movie":false}
			]}`)))
			return
		}
		_, _ = w.Write([]byte(envBody(`{"lists":[]}`)))
	})
	c.Token = "tk"

	got, err := c.CollectedLists(context.Background())
	if err != nil {
		t.Fatalf("CollectedLists: %v", err)
	}
	if gotPath != "/api/v1/lists/simple" {
		t.Errorf("打到的路径 = %q —— 不能是 /api/v1/users/collected_lists（实测 500）", gotPath)
	}
	if len(gotPages) == 0 || gotPages[0] != "1" {
		t.Errorf("第一页的 page 参数 = %v", gotPages)
	}
	if len(got.Lists) != 2 {
		t.Fatalf("得到 %d 份清单，want 2", len(got.Lists))
	}
	l := got.Lists[0]
	if l.ID != "k4EVE4" || l.Name != "遥控跳弹" || l.MoviesCount != 1 || l.Privacy != "open" {
		t.Errorf("字段映射不对: %+v", l)
	}
	if !got.Lists[1].IsDefault {
		t.Errorf("is_default 没透出: %+v", got.Lists[1])
	}
	if got.Truncated {
		t.Error("空页到底时不该报截断")
	}
}

// TestCollectedListsReportsTruncation 确认触顶时给信号，而且会多探一页。
//
// 「满页」不等于「还有更多」：恰好读到上限时下一页同样是空的，
// 只有探过一页才能分清两者。
func TestCollectedListsReportsTruncation(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		// 每一页都返回满页（10 条），永远到底不了。
		// ⚠️ id 必须**逐页唯一**：readAllPages 会按 id 去重，
		// 每页给同样的 id 会让 20 页塌成 10 条，测试就测不到触顶了。
		page := r.URL.Query().Get("page")
		var b strings.Builder
		b.WriteString(`{"lists":[`)
		for i := 0; i < 10; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"id":"p`)
			b.WriteString(page)
			b.WriteString(`-`)
			b.WriteString(strconv.Itoa(i))
			b.WriteString(`","name":"n","movies_count":1}`)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(envBody(b.String())))
	})
	c.Token = "tk"

	got, err := c.CollectedLists(context.Background())
	if err != nil {
		t.Fatalf("CollectedLists: %v", err)
	}
	if !got.Truncated {
		t.Fatal("永远满页时应当报截断，而不是给一份看起来完整的清单")
	}
	if got.MaxPages != maxListPages {
		t.Errorf("MaxPages = %d, want %d", got.MaxPages, maxListPages)
	}
	if len(got.Lists) != maxListPages*10 {
		t.Errorf("触顶时应当返回上限内的内容（%d 条），得到 %d", maxListPages*10, len(got.Lists))
	}
}

// TestListUsesEntityLetterL 钉住清单订阅的 filter_by 形状。
//
// 这是本次新增里最容易静默错的一处：实体字母写成别的、或 zone 写成非 0，
// 上游都**不报错**，只会返回别的作品。
// 实测（2026-10-04）：4 份清单在 0:l:{id} 下返回 9/1/2/6 条，
// 与上游声明的 movies_count 逐位相同；换成 2:l:{id} 全部变成 50 条。
func TestListUsesEntityLetterL(t *testing.T) {
	var gotFilter string
	var gotLimit string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotFilter = r.URL.Query().Get("filter_by")
		gotLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(envBody(`{"movies":[{"id":"m1","number":"A-1","magnets_count":0}]}`)))
	})
	works, err := c.List(context.Background(), "k4EVE4", url.Values{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotFilter != "0:l:k4EVE4" {
		t.Errorf("filter_by = %q，want 0:l:k4EVE4（zone 必须是 0，字母必须是 l）", gotFilter)
	}
	if gotLimit != "50" {
		t.Errorf("limit = %q，want 50（上游上限）", gotLimit)
	}
	if len(works) != 1 {
		t.Fatalf("得到 %d 部作品", len(works))
	}
}

// TestListRejectsMaskPointingAtAnotherEntity 是清单订阅上最危险的一种笔误。
//
// 用户自己传 filter_by 时把实体 id 写成别的清单（或把字母写成 a），
// 上游会静默返回**别人的作品**，而 feed 标题写着你这份。
func TestListRejectsMaskPointingAtAnotherEntity(t *testing.T) {
	called := false
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(envBody(`{"movies":[]}`)))
	})
	for _, bad := range []string{
		"0:l:OTHER::",     // id 与 URL 不符
		"0:a:k4EVE4::",    // 实体字母是女优 —— 会返回某个女优的作品
		"k4EVE4",          // 缺骨架
		"0:l:k4EVE4:cm::", // 主属性拼在一起
	} {
		_, err := c.List(context.Background(), "k4EVE4", url.Values{"filter_by": {bad}})
		if err == nil {
			t.Errorf("filter_by=%q 应当被拦下", bad)
			continue
		}
		if !strings.Contains(err.Error(), catalog.ErrBadRequest.Error()) {
			t.Errorf("filter_by=%q 的错误应当可判定为 ErrBadRequest，得到 %v", bad, err)
		}
	}
	if called {
		t.Error("被拦下的掩码不该发到上游")
	}
}

// TestListAcceptsExplicitZone 确认显式写对 zone 的掩码会被放行（不只认默认值）。
func TestListAcceptsExplicitZone(t *testing.T) {
	var gotFilter string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotFilter = r.URL.Query().Get("filter_by")
		_, _ = w.Write([]byte(envBody(`{"movies":[]}`)))
	})
	if _, err := c.List(context.Background(), "k4EVE4",
		url.Values{"filter_by": {"0:l:k4EVE4:c::"}}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotFilter != "0:l:k4EVE4:c::" {
		t.Errorf("filter_by = %q，应当原样透传", gotFilter)
	}
}

// TestListNameReadsDetailEndpoint 钉住取名走的端点。
//
// 它匿名可读（公开清单），因此即使没配 token，feed 标题也能好看；
// 私有清单匿名会 NoPermission，那时调用方退回 id。
func TestListNameReadsDetailEndpoint(t *testing.T) {
	var gotPath string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(envBody(`{"is_creator":false,"list":{"id":"k4EVE4","name":"遥控跳弹"}}`)))
	})
	name, err := c.ListName(context.Background(), "k4EVE4")
	if err != nil {
		t.Fatalf("ListName: %v", err)
	}
	if gotPath != "/api/v1/lists/k4EVE4" {
		t.Errorf("打到的路径 = %q", gotPath)
	}
	if name != "遥控跳弹" {
		t.Errorf("名字 = %q", name)
	}
}

// TestEntityMaskUsesYearSlotExactly 钉住「年份只能落在第 5 段」这条实测规则。
//
// 三处都是**静默失败**的入口，所以每一处都值得一条断言：
//
//	6 段以上且第 5 段非空  → 上游把年份丢掉，main 仍然生效
//	清单实体               → 年份槽被完全忽略（返回整份清单）
//	main 空但 year 有值    → 第 4 段必须写出来，否则年份落到第 4 段上
func TestEntityMaskUsesYearSlotExactly(t *testing.T) {
	cases := []struct {
		name   string
		letter string
		id     string
		params url.Values
		want   string
	}{
		{"只有实体", "a", "EvkJ", url.Values{}, "0:a:EvkJ"},
		{"主属性", "a", "EvkJ", url.Values{"main": {"c"}}, "0:a:EvkJ:c"},
		{"只看年份（第 4 段必须留出来）", "a", "EvkJ", url.Values{"year": {"2021"}}, "0:a:EvkJ::2021"},
		{"主属性+年份", "a", "EvkJ", url.Values{"main": {"c"}, "year": {"2021"}}, "0:a:EvkJ:c:2021"},
		{"清单的主属性", "l", "k4EVE4", url.Values{"main": {"c"}}, "0:l:k4EVE4:c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildEntityMask(entityOf(tc.letter, tc.id), tc.params,
				entityNouns{route: "女优页", what: "女优", empty: "空"})
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if got != tc.want {
				t.Errorf("掩码 = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestListEntityRejectsYear 确认清单上的年份被拦而不是被静默忽略。
func TestListEntityRejectsYear(t *testing.T) {
	_, err := buildEntityMask(entityOf("l", "k4EVE4"), url.Values{"year": {"2025"}},
		entityNouns{route: "清单 feed", what: "清单", empty: "空"})
	if err == nil {
		t.Fatal("清单上的 year 应当被拦下 —— 实测上游忽略它，等于没筛")
	}
	if !strings.Contains(err.Error(), catalog.ErrBadRequest.Error()) {
		t.Errorf("应当可判定为 ErrBadRequest: %v", err)
	}
}

// TestMaskRejectsYearFollowedByMore 确认「年份后面还有东西」被拦。
//
// 实测 `0:a:EvkJ:c:2021:` 里 main 仍然生效、年份被丢掉 ——
// feed 会看起来筛了 2021、实际跨到 2026。这条正是为此而拦。
func TestMaskRejectsYearFollowedByMore(t *testing.T) {
	for _, bad := range []string{"0:a:EvkJ:c:2021:", "0:a:EvkJ::2021:gt-120", "0:a:EvkJ:c:2021:x"} {
		_, err := buildEntityFilter("EvkJ", url.Values{"filter_by": {bad}})
		if err == nil {
			t.Errorf("%q 应当被拦下（年份后面不能再有东西）", bad)
			continue
		}
		if !strings.Contains(err.Error(), catalog.ErrBadRequest.Error()) {
			t.Errorf("%q 的错误应当可判定: %v", bad, err)
		}
	}
	// 尾部的**空**段是历史形态，实测无害，继续放行（README 里就写着它）。
	if _, err := buildEntityFilter("EvkJ", url.Values{"filter_by": {"0:a:EvkJ:c::"}}); err != nil {
		t.Errorf("0:a:EvkJ:c:: 实测能筛，不该被误杀: %v", err)
	}
	// 恰好 5 段的年份形态要放行。
	if _, err := buildEntityFilter("EvkJ", url.Values{"filter_by": {"0:a:EvkJ:c:2021"}}); err != nil {
		t.Errorf("0:a:EvkJ:c:2021 实测能筛，不该被误杀: %v", err)
	}
}

// TestFilterByTagsRejectsOverFive 确认独立标签参数也受 5 个上限约束。
func TestFilterByTagsRejectsOverFive(t *testing.T) {
	called := false
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(envBody(`{"movies":[]}`)))
	})

	_, err := c.Actress(context.Background(), "EvkJ",
		url.Values{"filter_by_tags": {"1,2,3,4,5,6"}})
	if err == nil {
		t.Fatal("6 个标签应当被拦下 —— 上游只认前 5 个，第 6 个静默丢弃")
	}
	if called {
		t.Error("超限的请求不该发到上游")
	}
	if _, err := c.Actress(context.Background(), "EvkJ",
		url.Values{"filter_by_tags": {"1,2,3,4,5"}}); err != nil {
		t.Errorf("5 个标签应当放行: %v", err)
	}
}
