package appapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
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
	if len(got) != 1 || got[0].ID != "82J0Md" {
		t.Errorf("解析到 %+v，应当是精确匹配的 82J0Md", got)
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
	if len(got) != 1 || got[0].ID != "right" {
		t.Errorf("解析到 %+v —— 实现可能退回了「取第一条」而不是精确匹配", got)
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
			if len(got) != 1 || got[0].ID != "x" {
				t.Errorf("解析到 %+v", got)
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
	// 应当保留全部精确匹配候选（完整对象，不是光秃秃的 id）。
	if len(got) != 2 || got[0].ID != "id1" || got[1].ID != "id2" {
		t.Errorf("应当保留全部精确匹配候选，得到 %+v", got)
	}
	// 顺带确认元数据没被丢掉 —— 这正是这个签名存在的原因：
	// 带上它能省掉一次 /api/v4/movies/{id} 详情请求。
	if got[0].Number != "ABC-123" {
		t.Errorf("应当带上 number 等元数据，得到 %+v", got[0])
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
		// 主属性多个时**逗号分隔**。曾经这里写的是拼接的 "0:a:EvkJ:pm::"，
		// 那是错的：实测上游对拼接掩码静默忽略，会返回全部作品而非加了条件之后的。
		{"用户可覆盖", "EvkJ", url.Values{"filter_by": {"0:a:EvkJ:p,m::"}}, "0:a:EvkJ:p,m::", false},
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
// TestHydrateKeepsMovieID 钉住：hydrate 必须把上游的 movie id 带进 catalog.Work。
//
// 那个 id 在 feed 渲染里**毫无用处**（item 的身份是 infohash），因此很容易在
// 「映射线格式到领域模型」时被丢掉 —— 而 pin 表（ticket 08）正是按它键的。
// 用一个别人看不出用处的字段做持久状态的键，是这里最容易踩空的一步，
// 所以单列一条测试。
func TestHydrateKeepsMovieID(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"magnets":[
			{"name":"A-1","hash":"abc","size":1,"cnsub":false,"hd":false,"files_count":1,"created_at":"09/01/2026"}]}}`))
	})

	works, err := c.hydrate(context.Background(), []movieSlim{
		{ID: "82J0Md", Number: "A-1", MagnetsCount: 1},
		{ID: "kKDgne", Number: "A-2", MagnetsCount: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 {
		t.Fatalf("得到 %d 部作品", len(works))
	}
	// 有磁链的那部与没磁链的那部都要带 id —— 没磁链的作品也会进候选列表，
	// 将来补上磁链时仍要能认出来是同一部。
	if works[0].ID != "82J0Md" || works[1].ID != "kKDgne" {
		t.Errorf("movie id 被丢了: %q, %q", works[0].ID, works[1].ID)
	}
}

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

// TestBuildEntityFilterRejectsConcatenatedFlags 钉住一个**我在文档里犯过**的错误。
//
// 实测（2026-09-30）：主属性必须**逗号分隔**：
//
//	0:a:EvkJ:c,m::   ✅ 中文字幕过滤生效
//	0:a:EvkJ:cm::    ❌ 静默忽略，返回全部作品
//
// 而「静默忽略」是最坏的一种失败：用户写了 apmc 以为加了四个条件，
// 实际拿到的是全集，而且看不出来。
//
// 拼接形式（长度>1 且不含逗号）**永远**是笔误 —— 单个主属性就是一个字母，
// 多个用逗号连。因此这个校验不可能误伤合法配置。
//
// ⚠️ 顺带记一个坑：本测试的第一版把 "1:a:EvkJ:pm::" 留在了合法切片里，
// 靠 `good[:5]` 截断跳过，并留了一句自相矛盾的注释。那是错的 ——
// 应该被拒的用例属于 bad。现已移入。
func TestBuildEntityFilterRejectsConcatenatedFlags(t *testing.T) {
	bad := []string{
		"0:a:EvkJ:apmc::",
		"0:a:EvkJ:cm::",
		"0:a:EvkJ:pm::",
		"0:a:EvkJ:c,m,ps::", // 整体含逗号，但最后一段是拼接
		"1:a:EvkJ:pm::",     // 非 censored 区也一样要拦
	}
	for _, fb := range bad {
		t.Run(fb, func(t *testing.T) {
			_, err := buildEntityFilter("EvkJ", url.Values{"filter_by": {fb}})
			if err == nil {
				t.Fatalf("%q 是拼接笔误，应当报错而不是静默透传", fb)
			}
			if !errors.Is(err, catalog.ErrBadRequest) {
				t.Errorf("应当可用 errors.Is 判定为 ErrBadRequest（上层据此返回 400 而非 502）: %v", err)
			}
		})
	}
}

// TestBuildEntityFilterAcceptsValidMasks 确认校验不误伤合法形式。
func TestBuildEntityFilterAcceptsValidMasks(t *testing.T) {
	good := []string{
		"0:a:EvkJ",           // 无主属性
		"0:a:EvkJ:c::",       // 单个
		"0:a:EvkJ:c,m::",     // 逗号多个
		"0:a:EvkJ:p,m,c,s::", // 全部四个主属性
		"0:a:EvkJ:m,c::",     // 顺序无关
		"1:a:EvkJ",           // 非 censored 区
	}
	for _, fb := range good {
		t.Run(fb, func(t *testing.T) {
			got, err := buildEntityFilter("EvkJ", url.Values{"filter_by": {fb}})
			if err != nil {
				t.Fatalf("合法掩码不该被拒: %v", err)
			}
			if got != fb {
				t.Errorf("应当原样透传，得到 %q", got)
			}
		})
	}
}

// TestValidateMaskCatchesMissingSkeleton 钉住一个**我上一轮漏掉**的形态。
//
// 票 06 明确记录的灾难形态是「写成 a 或 apmc」—— 缺实体 id 的简写。
// 而我写的校验只看了主属性段（第 4 段），于是：
//
//	splitMask("apmc") -> parts[0]="apmc"，mainSeg="" -> 立即 return nil 放行
//
// 也就是说**我声称修好的那个陷阱，恰恰没被修**。
// 根因：我只校验了「形状细节」（主属性拼接），从没校验「骨架是否存在」——
// 这是正交的两维，我只想了一维。
func TestValidateMaskCatchesMissingSkeleton(t *testing.T) {
	bad := []string{
		"a",              // 只有一个字母
		"apmc",           // 四个字母拼一起，缺 zone:letter:id
		"abc",            // 同上
		"0:a",            // 只有两段，缺 id
		"0",              // 只有一段
		"x:a:EvkJ",       // zone 不是数字
		"0:ab:EvkJ",      // 实体字母不是单个
		"0:a:",           // id 为空
		"0:a:OtherActor", // ⭐ id 与 URL 里的女优不一致 —— 会静默展示别人的作品
		"0:s:EvkJ",       // ⭐ 实体字母不是 actor（路由是女优页）
	}
	for _, fb := range bad {
		t.Run(fb, func(t *testing.T) {
			_, err := buildEntityFilter("EvkJ", url.Values{"filter_by": {fb}})
			if err == nil {
				t.Fatalf("%q 应当被拒 —— 它会静默返回错误内容（缺骨架 → 全站作品；"+
					"id 不符 → 别人的作品）", fb)
			}
			if !errors.Is(err, catalog.ErrBadRequest) {
				t.Errorf("应当可判定为 ErrBadRequest: %v", err)
			}
		})
	}
}

// TestValidateMaskAcceptsLegitimateVariations 确认新校验不误伤合法写法。
func TestValidateMaskAcceptsLegitimateVariations(t *testing.T) {
	good := []string{
		"0:a:EvkJ",     // 基本
		"1:a:EvkJ",     // 无码区（zone 可变）
		"0:a:EvkJ:c::", // 带主属性
		"0:a:EvkJ:p,m,c,s::",
		"0:a:EvkJ:a::", // 主属性给个无效字母 —— 上游忽略，但不该本地报错
	}
	for _, fb := range good {
		t.Run(fb, func(t *testing.T) {
			got, err := buildEntityFilter("EvkJ", url.Values{"filter_by": {fb}})
			if err != nil {
				t.Fatalf("合法掩码不该被拒: %v", err)
			}
			if got != fb {
				t.Errorf("应当原样透传，得到 %q", got)
			}
		})
	}
}

// TestResolveExactPassesLimit 钉住一个曾经漏掉的参数。
//
// `/api/v2/search` 是**模糊/前缀搜索**，默认只返回 **10** 条。
// 不传 limit 时，当近似结果多于 10 条，真目标会被挤出第一页，
// 表现成一个莫名其妙的「没有精确匹配」。
//
// 实测（2026-09-30）：limit 生效且上限 50（与 /movies/tags 一致）。
func TestResolveExactPassesLimit(t *testing.T) {
	var got url.Values
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"movies":[{"id":"x","number":"KV-328"}]}}`))
	})

	if _, err := c.resolveExact(context.Background(), "KV-328"); err != nil {
		t.Fatal(err)
	}
	if got.Get("limit") != strconv.Itoa(limitPerPage) {
		t.Errorf("limit = %q，应当显式要满上限 %d —— 否则精确匹配可能被挤出前 10 条",
			got.Get("limit"), limitPerPage)
	}
}

// TestResolveExactReturnsFullMetadata 确认不再只返回 id。
//
// 之前它返回逗号拼接的 id 字符串，调用方 split 之后再打一次
// `/api/v4/movies/{id}` 拿元数据 —— 一次多余的请求，而且把
// 「线格式 → 领域模型」的映射散到了两处。
func TestResolveExactReturnsFullMetadata(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"movies":[
			{"id":"m1","number":"KV-328","title":"标题","release_date":"2026-08-28","magnets_count":3}]}}`))
	})

	got, err := c.resolveExact(context.Background(), "KV-328")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("得到 %d 条", len(got))
	}
	m := got[0]
	if m.Title != "标题" || m.ReleaseDate != "2026-08-28" || m.MagnetsCount != 3 {
		t.Errorf("元数据不完整: %+v", m)
	}
}

// TestHydrateDoesNotReturnPartialDataOnCancel 钉住一条**行为契约**：
// ctx 被取消时 hydrate 必须报错，不能返回一批缺了磁链的作品。
//
// 「静默少给数据」是本项目最不愿出现的一类失败 —— 上层只会看到一次成功调用。
//
// ⚠️ 说明这条测试**实际覆盖的是哪条路径**（我一开始的注释说错了）：
//
// 取消时，**在飞的那几个请求本身就会失败**，于是 firstErr 被设上、函数返回错误 ——
// 走的是「任一失败即整次失败」那条路。把 hydrate 里那段
// 「ctx.Err() != nil || aborted」的兜底删掉，这条测试**依然通过**。
//
// 那段兜底覆盖的是另一个更窄的窗口：在飞的请求**全部成功**、
// 而排队中的 worker 被取消 —— 那时一条错误都没记到，却会返回残缺结果。
// 那个窗口没法用真实 HTTP 稳定复现（时序取决于调度），因此它只有代码兜底，
// 没有测试覆盖。这里如实记下，不假装测到了。
func TestHydrateDoesNotReturnPartialDataOnCancel(t *testing.T) {
	const n = 8
	// 所有磁链请求都阻塞，直到我们取消 ctx。
	release := make(chan struct{})
	var started atomic.Int64

	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/magnets") {
			started.Add(1)
			<-release
		}
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"magnets":[{"hash":"h","size":1,"name":"n","created_at":"09/01/2026"}]}}`))
	})

	movies := make([]movieSlim, n)
	for i := range movies {
		movies[i] = movieSlim{ID: fmt.Sprintf("m%d", i), Number: fmt.Sprintf("N-%d", i), MagnetsCount: 1}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		works []catalog.Work
		err   error
	}, 1)
	go func() {
		w, err := c.hydrate(ctx, movies)
		done <- struct {
			works []catalog.Work
			err   error
		}{w, err}
	}()

	// 等确实有请求在飞，再取消。
	deadline := time.After(3 * time.Second)
	for started.Load() == 0 {
		select {
		case <-deadline:
			close(release)
			t.Fatal("没有磁链请求发出")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	close(release)

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("ctx 取消时应当报错，而不是返回 %d 条（可能缺磁链的）作品", len(got.works))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hydrate 没有返回 —— 可能有 goroutine 卡住")
	}
}
