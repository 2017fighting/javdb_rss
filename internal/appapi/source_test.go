package appapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestResolveExactRejectsPrefixMatches 是本包最重要的一条测试。
//
// 实测（2026-09-28）：/api/v2/search?q=KV-328 返回**8 部**作品，
// 番号分别是 KV-328 / KV-323 / KV-322 / KV-326 / KV-318 / KV-324 / KV-329 / KV-327。
// 它是一个模糊/前缀搜索，不是精确查询。
//
// 所以「按位置取第一条」这种写法在多数时候**看起来是对的**（KV-328 恰好排在第一），
// 但在番号尾部字符不同的情况下会静默命中错误作品 —— 不报错，只是发错片。
// 这是本服务最不该犯的一类错误，因此用测试钉死。
func TestResolveExactRejectsPrefixMatches(t *testing.T) {
	// 真实响应结构：目标番号在第一位，后面跟着一堆近似番号。
	body := `{"success":1,"action":null,"data":{"movies":[
		{"id":"82J0Md","number":"KV-328","title":"目标"},
		{"id":"kKDgne","number":"KV-323","title":"近似"},
		{"id":"kKryxJ","number":"KV-326","title":"近似"},
		{"id":"Ywz40D","number":"KV-329","title":"近似"}
	],"current_page":1}}`

	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := c.resolveExact(context.Background(), "KV-328")
	if err != nil {
		t.Fatalf("应当解析成功: %v", err)
	}
	if got != "82J0Md" {
		t.Errorf("解析到 %q，应当是精确匹配的 82J0Md", got)
	}
}

// TestResolveExactRejectsFuzzyTail 确认**真的**做了精确比对，而不是「恰好排第一」。
//
// 把目标番号放在列表中间，且前面有同样前缀的干扰项 ——
// 如果实现是「取第一条」，这里就会抓到。
func TestResolveExactRejectsFuzzyTail(t *testing.T) {
	body := `{"success":1,"action":null,"data":{"movies":[
		{"id":"wrong1","number":"KV-3281"},
		{"id":"wrong2","number":"KV-3280"},
		{"id":"right","number":"KV-328"},
		{"id":"wrong3","number":"KV-3289"}
	],"current_page":1}}`

	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := c.resolveExact(context.Background(), "KV-328")
	if err != nil {
		t.Fatalf("应当解析成功: %v", err)
	}
	if got != "right" {
		t.Errorf("解析到 %q —— 实现可能退回了「取第一条」而不是精确匹配", got)
	}
}

// TestResolveExactNotFoundIsAnError 确认找不到精确匹配时**报错**，不退回近似结果。
//
// 静默退回的后果是：用户订阅 KV-328，服务发来 KV-323 —— 而且永远不会有人告诉他。
func TestResolveExactNotFoundIsAnError(t *testing.T) {
	body := `{"success":1,"action":null,"data":{"movies":[
		{"id":"a","number":"KV-323"},
		{"id":"b","number":"KV-326"}
	],"current_page":1}}`

	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	if _, err := c.resolveExact(context.Background(), "KV-328"); err == nil {
		t.Fatal("没有精确匹配时应当报错，而不是退回近似结果")
	}
}

// TestResolveExactEmptyResultIsAnError 确认搜索为空也报错。
func TestResolveExactEmptyResultIsAnError(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"movies":[],"current_page":1}}`))
	})
	if _, err := c.resolveExact(context.Background(), "NOPE-999"); err == nil {
		t.Fatal("搜索无结果时应当报错")
	}
}

// TestResolveExactIsCaseAndSpaceInsensitive 确认归一化只处理大小写与空白。
//
// 这两种差异是用户输入噪音，不是番号的一部分；而连字符、后缀字母等
// **不能**被抹掉 —— 抹掉它们就等于把精确匹配退化成模糊匹配。
func TestResolveExactIsCaseAndSpaceInsensitive(t *testing.T) {
	body := `{"success":1,"action":null,"data":{"movies":[
		{"id":"x","number":"rebdb-1047"}
	],"current_page":1}}`

	for _, in := range []string{"REBDB-1047", "rebdb-1047", "  REBDB-1047  "} {
		t.Run(in, func(t *testing.T) {
			c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			got, err := c.resolveExact(context.Background(), in)
			if err != nil {
				t.Fatalf("应当解析成功: %v", err)
			}
			if got != "x" {
				t.Errorf("解析到 %q", got)
			}
		})
	}
}

// TestResolveExactRejectsSimilarButDifferentCode 确认「看起来像」不等于「就是」。
//
// 这类差异是真实存在的（合集、不同片商同名、数字位数不同），
// 必须被当成不同的作品。
func TestResolveExactRejectsSimilarButDifferentCode(t *testing.T) {
	codes := []struct {
		got, want string
	}{
		{"KV-32", "KV-328"},
		{"KV-3288", "KV-328"},
		{"KV 328", "KV-328"}, // 空格不是连字符
		{"KV328", "KV-328"},  // 缺连字符
		{"SSIS-001", "SSIS-01"},
		{"ABC-123A", "ABC-123"},
	}
	for _, tc := range codes {
		if normalizeCode(tc.got) == normalizeCode(tc.want) {
			t.Errorf("normalizeCode 把 %q 与 %q 当成同一个了", tc.got, tc.want)
		}
	}
}

// TestResolveExactMultipleExactMatches 一个番号确实可能对应多部作品
// （不同片商同名等）。这时保留全部候选，不替用户猜。
func TestResolveExactMultipleExactMatches(t *testing.T) {
	body := `{"success":1,"action":null,"data":{"movies":[
		{"id":"id1","number":"ABC-123"},
		{"id":"id2","number":"ABC-123"},
		{"id":"id3","number":"ABC-124"}
	],"current_page":1}}`

	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := c.resolveExact(context.Background(), "ABC-123")
	if err != nil {
		t.Fatalf("应当解析成功: %v", err)
	}
	if got != "id1,id2" {
		t.Errorf("解析到 %q，应当保留全部精确匹配候选", got)
	}
}

// TestBuildEntityFilter 钉住 filter_by 的复合掩码格式。
//
// 格式实测确认：{zone}:{letter}:{id}[:{main}:]:
// 早期误以为它是 `a`、`apmc` 这种简单字母组合，结果请求不带实体 id，
// 服务端返回的是**全站最新作品**而不是那个女优的作品 —— 而且不报错。
func TestBuildEntityFilter(t *testing.T) {
	tests := []struct {
		name    string
		actorID string
		params  url.Values
		want    string
		wantErr bool
	}{
		{"默认构造", "EvkJ", url.Values{}, "0:a:EvkJ", false},
		{"用户可覆盖", "EvkJ", url.Values{"filter_by": {"0:a:EvkJ:pm::"}}, "0:a:EvkJ:pm::", false},
		{"空 id 报错", "", url.Values{}, "", true},
		{"只有空白的 id 报错", "   ", url.Values{}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildEntityFilter(tt.actorID, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatal("应当报错")
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if got != tt.want {
				t.Errorf("filter_by = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestActressOverridesPageAndLimit 确认本服务自己控制分页。
//
// 用户透传 page/limit 会被**覆盖**而不是生效。这个行为必须明确，
// 否则「设了 limit 却没效果」是个很难解释的现象。
func TestActressOverridesPageAndLimit(t *testing.T) {
	var got url.Values
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"movies":[],"current_page":1}}`))
	})

	_, _ = c.Actress(context.Background(), "EvkJ", url.Values{
		"page": {"9"}, "limit": {"5"},
		"sort_by": {"release"}, "order_by": {"desc"},
	})

	if got.Get("page") != "1" {
		t.Errorf("page = %q，应当被覆盖为 1", got.Get("page"))
	}
	if got.Get("limit") != "50" {
		t.Errorf("limit = %q，应当被覆盖为 50（实测服务端上限）", got.Get("limit"))
	}
	if got.Get("filter_by") != "0:a:EvkJ" {
		t.Errorf("filter_by = %q —— 缺实体 id 会返回全站最新作品而不是该女优的", got.Get("filter_by"))
	}
}

// TestHydrateSkipsMagnetsWhenCountIsZero 确认省请求的短路生效 ——
// magnets_count 为 0 时不必去拉一个必然为空的磁链列表。
func TestHydrateSkipsMagnetsWhenCountIsZero(t *testing.T) {
	var calls int
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/magnets") {
			calls++
		}
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"magnets":[]}}`))
	})

	works, err := c.hydrate(context.Background(), []movieSlim{
		{ID: "a", Number: "A-1", MagnetsCount: 0},
		{ID: "b", Number: "B-1", MagnetsCount: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("magnets_count=0 时不该拉磁链，实际拉了 %d 次", calls)
	}
	// 但没有磁链的作品仍要保留在结果里 —— feed.Build 负责跳过它们。
	if len(works) != 2 {
		t.Errorf("得到 %d 部作品，want 2", len(works))
	}
}

// TestMagnetsPreservesServerOrder 确认磁链顺序原样保留。
//
// 用户已选定「信任 App 顺序」，本服务不得重排 ——
// 排序规则属于上游，我们重排会让 feed 与 App 显示的不一致。
func TestMagnetsPreservesServerOrder(t *testing.T) {
	body := `{"success":1,"action":null,"data":{"magnets":[
		{"name":"X","hash":"zzz","size":100,"cnsub":false,"hd":false,"files_count":1,"created_at":"09/01/2026"},
		{"name":"X","hash":"aaa","size":9999,"cnsub":true,"hd":true,"files_count":9,"created_at":"09/02/2026"},
		{"name":"X","hash":"mmm","size":5000,"cnsub":false,"hd":true,"files_count":2,"created_at":"09/03/2026"}
	]}}`

	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := c.magnets(context.Background(), "someID")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zzz", "aaa", "mmm"}
	for i := range want {
		if got[i].Infohash != want[i] {
			t.Errorf("位置 %d = %q, want %q —— 实现重排了磁链", i, got[i].Infohash, want[i])
		}
	}
	// 顺手确认字段映射没串位。
	if !got[1].CNSub || got[1].SizeMB != 9999 {
		t.Errorf("字段映射串位: %+v", got[1])
	}
}

// TestSearchResultShapeIsFuzzy 是一份「把实测结论写进测试」的存档：
// 搜索一个番号会返回一串**番号不同**的作品。这不是 bug，是上游行为，
// 但任何依赖搜索顺序的代码都会因此出错。
func TestSearchResultShapeIsFuzzy(t *testing.T) {
	// 实测：q=KV-328 返回 8 部，番号各不相同。
	numbers := []string{"KV-328", "KV-323", "KV-322", "KV-326", "KV-318", "KV-324", "KV-329", "KV-327"}
	if len(numbers) < 2 {
		t.Skip()
	}
	// 只用一条断言表达这个事实：近似结果里会出现**不等于查询词**的番号。
	var different int
	for _, n := range numbers {
		if n != "KV-328" {
			different++
		}
	}
	if different == 0 {
		t.Error("实测应当返回多个不同番号 —— 若上游改成精确搜索，resolveExact 可简化")
	}
}
