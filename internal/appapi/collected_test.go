package appapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// collectedPage 生成一页收藏女优的响应体。
//
// 契约来自先例项目 javdb-cli 的实现与夹具（内部结构 {key: items} + page 参数翻页），
// 并经本仓库 2026-09-30 的实测确认端点存在、需要 token。
func collectedPage(items ...string) string {
	var parts []string
	for i, name := range items {
		parts = append(parts, fmt.Sprintf(
			`{"id":%q,"name":%q,"name_zht":"","videos_count":%d}`,
			strings.ToLower(name), name, (i+1)*10))
	}
	return `{"success":1,"action":null,"data":{"actors":[` + strings.Join(parts, ",") + `]}}`
}

func emptyCollectedPage() string {
	return `{"success":1,"action":null,"data":{"actors":[]}}`
}

// TestCollectedActressesPaginatesUntilEmpty 确认按 page 翻页、遇空页停止。
func TestCollectedActressesPaginatesUntilEmpty(t *testing.T) {
	var pages []string
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") == "" {
			t.Error("收藏列表必须带 Authorization —— 它是需要 token 的端点")
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		switch page {
		case "1":
			_, _ = w.Write([]byte(collectedPage("AAA", "BBB")))
		case "2":
			_, _ = w.Write([]byte(collectedPage("CCC")))
		default:
			_, _ = w.Write([]byte(emptyCollectedPage()))
		}
	})
	srv.Token = "tok"

	got, err := srv.CollectedActresses(context.Background())
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(got.Actresses) != 3 {
		t.Fatalf("得到 %d 位女优，want 3", len(got.Actresses))
	}
	if got.Truncated {
		t.Error("读到空页就到底了，不该报告截断")
	}
	if got.Actresses[0].ID != "aaa" || got.Actresses[1].ID != "bbb" || got.Actresses[2].ID != "ccc" {
		t.Errorf("顺序或内容不对: %+v", got.Actresses)
	}
	if got.Actresses[0].Name != "AAA" || got.Actresses[0].VideosCount != 10 {
		t.Errorf("字段映射不对: %+v", got.Actresses[0])
	}
	// page=1,2,3 —— 第 3 页是空页，用来确认「到底」了。
	want := []string{"1", "2", "3"}
	if strings.Join(pages, ",") != strings.Join(want, ",") {
		t.Errorf("请求的页码序列 = %v, want %v", pages, want)
	}
}

// TestCollectedActressesWithoutTokenReturnsErrNoToken 是最重要的一条：
//
// 没有 token 时**不能**返回空列表 —— 那会被用户理解成「我没收藏任何人」。
// 必须返回一个可判定的错误，让上层能说清楚「去导出 token」。
func TestCollectedActressesWithoutTokenReturnsErrNoToken(t *testing.T) {
	var hit bool
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write([]byte(emptyCollectedPage()))
	})
	// 刻意不设 Token。

	_, err := srv.CollectedActresses(context.Background())
	if err == nil {
		t.Fatal("没有 token 时应当报错，而不是返回空列表")
	}
	if !errors.Is(err, catalog.ErrNoToken) {
		t.Errorf("err 应当可用 errors.Is 判定为 ErrNoToken，得到: %v", err)
	}
	if hit {
		t.Error("没有 token 时不该发出任何请求 —— 必然会被拒，白打一次")
	}
}

// TestCollectedActressesDedupesAcrossPages 确认跨页按 id 去重。
//
// 上游分页在数据变动时可能重叠；重复的 id 会让用户看到同一个女优两次。
func TestCollectedActressesDedupesAcrossPages(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(collectedPage("AAA", "BBB")))
		case "2":
			// BBB 重复出现
			_, _ = w.Write([]byte(collectedPage("BBB", "CCC")))
		default:
			_, _ = w.Write([]byte(emptyCollectedPage()))
		}
	})
	srv.Token = "tok"

	got, err := srv.CollectedActresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Actresses) != 3 {
		t.Fatalf("去重后应当 3 位，得到 %d: %+v", len(got.Actresses), got.Actresses)
	}
}

// TestCollectedActressesReportsTruncationAtPageCap 是本票的核心：
//
// 一个永远返回满页的上游会让翻页在上限处停下，此时返回的清单是**已知不完整的**。
// 交付要求是「可机读的明确信号」—— 因此 Truncated 必须为 true，
// 而不是照常返回一份看起来完整的列表。
func TestCollectedActressesReportsTruncationAtPageCap(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(collectedPage("AAA", "BBB")))
	})
	srv.Token = "tok"

	got, err := srv.CollectedActresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated {
		t.Fatal("翻页在上限处停下时必须报告截断 —— 否则这是一份静默残缺的列表")
	}
	if got.PagesFetched != maxCollectedPages || got.MaxPages != maxCollectedPages {
		t.Errorf("PagesFetched=%d MaxPages=%d，应当都等于上限 %d",
			got.PagesFetched, got.MaxPages, maxCollectedPages)
	}
}

// TestCollectedActressesStopsAtMaxPages 确认页数有上限 ——
// 一个异常的上游不该让我们无限翻页。
//
// 上限之后还会多问一页（探针），用来分清「正好读满上限」与「还有更多」。
// 那一次探针是**异常分支独有的**，正常情况下不会发生。
func TestCollectedActressesStopsAtMaxPages(t *testing.T) {
	var calls int
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		// 永远返回满页，模拟「上游永远说有下一页」。
		_, _ = w.Write([]byte(collectedPage("AAA", "BBB")))
	})
	srv.Token = "tok"

	got, err := srv.CollectedActresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != maxCollectedPages+1 {
		t.Errorf("请求了 %d 页，应当是上限 %d 加一次探针", calls, maxCollectedPages)
	}
	if !got.Truncated {
		t.Error("永远满页时应当报告截断")
	}
}

// TestCollectedActressesCompleteWhenCapExactlyFills 是边界测试：
//
// 收藏数**正好等于**上限（maxCollectedPages 满页）时，探针会看到空页 ——
// 这必须被判定为**完整**，而不是「踩到上限就算截断」。
// 否则一个恰好收藏了整上限的用户会永远看到一条假的截断警告，
// 而这条警告会让他去调一个本来不需要调的上限。
func TestCollectedActressesCompleteWhenCapExactlyFills(t *testing.T) {
	const perPage = 2
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page <= maxCollectedPages {
			_, _ = w.Write([]byte(collectedPage(
				fmt.Sprintf("P%dA", page), fmt.Sprintf("P%dB", page))))
			return
		}
		_, _ = w.Write([]byte(emptyCollectedPage()))
	})
	srv.Token = "tok"

	got, err := srv.CollectedActresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncated {
		t.Errorf("收藏恰好等于上限（%d 页）时是完整清单，不该报告截断", maxCollectedPages)
	}
	if len(got.Actresses) != maxCollectedPages*perPage {
		t.Errorf("读到 %d 位，want %d", len(got.Actresses), maxCollectedPages*perPage)
	}
}

// TestCollectedActressesCompleteWhenLastPageIsPartialAtCap 是另一个边界：
//
// 收藏数落在上限所在页的中间（末页不满）时，探针同样会看到空页 ——
// 也必须是完整。这条正是最初的实现漏掉的：它只看「有没有遇到空页」，
// 于是把「末页不满但已到上限」误报成截断。
func TestCollectedActressesCompleteWhenLastPageIsPartialAtCap(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		switch {
		case page < maxCollectedPages:
			_, _ = w.Write([]byte(collectedPage(
				fmt.Sprintf("P%dA", page), fmt.Sprintf("P%dB", page))))
		case page == maxCollectedPages:
			// 末页只有一条，不满。
			_, _ = w.Write([]byte(collectedPage("Z9")))
		default:
			_, _ = w.Write([]byte(emptyCollectedPage()))
		}
	})
	srv.Token = "tok"

	got, err := srv.CollectedActresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncated {
		t.Error("末页不满说明已经到底，不该报告截断")
	}
}

// TestCollectedActressesTruncatedWhenDataBeyondCap 确认真的还有第 21 页时
// 才报告截断 —— 信号必须精确到「还有更多」，而不是「踩到了上限」。
func TestCollectedActressesTruncatedWhenDataBeyondCap(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page <= maxCollectedPages+1 {
			_, _ = w.Write([]byte(collectedPage(
				fmt.Sprintf("P%dA", page), fmt.Sprintf("P%dB", page))))
			return
		}
		_, _ = w.Write([]byte(emptyCollectedPage()))
	})
	srv.Token = "tok"

	got, err := srv.CollectedActresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated {
		t.Fatal("上限之外还有数据时必须报告截断")
	}
}

// TestCollectedActressesNoTruncationWhenLastPageIsShort 确认「最后一页不满」这种
// 正常情况**不会**被误报成截断。
//
// 它是「信号只在真截断时出现」的另一半 —— 只测「截断时有信号」会放过一个
// 永远为真的字段。
func TestCollectedActressesNoTruncationWhenLastPageIsShort(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(collectedPage("AAA", "BBB")))
		case "2":
			_, _ = w.Write([]byte(collectedPage("CCC"))) // 不满页，但也不是空页
		default:
			_, _ = w.Write([]byte(emptyCollectedPage()))
		}
	})
	srv.Token = "tok"

	got, err := srv.CollectedActresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncated {
		t.Error("读到空页才停下，不是截断")
	}
	if len(got.Actresses) != 3 {
		t.Errorf("应当读到 3 位，得到 %d", len(got.Actresses))
	}
}

// TestCollectedActressesPropagatesAuthError 确认 token 过期与「没配 token」被分开。
//
// 两者都要用户动手，但改的东西不同：一个是配置里没填，一个是填了但过期了。
func TestCollectedActressesPropagatesAuthError(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":0,"action":"TokenExpired","message":"token 已過期","data":null}`))
	})
	srv.Token = "expired"

	_, err := srv.CollectedActresses(context.Background())
	if err == nil {
		t.Fatal("应当报错")
	}
	if !IsAuthError(err) {
		t.Errorf("应当被识别为凭据错误: %v", err)
	}
	if errors.Is(err, catalog.ErrNoToken) {
		t.Errorf("token 过期不应被误判成「没配 token」: %v", err)
	}
}

// TestActressNameUsesNameField 钉住一个实测结论：
//
// **不要读 name_zht** —— 实测新版服务端上它恒为空，与 lang 无关。
// 随 lang 变化的是 name 本身（lang=en → "Kawakita Saika"，lang=zh-CN → "河北彩花"）。
//
// 先例项目的夹具给 name_zht 填了值，照抄就会写出「优先 name_zht」的实现，
// 结果在真实服务端上永远落到 fallback。这条测试把真实行为固定下来。
func TestActressNameUsesNameField(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		// 故意同时给出两个字段，且 name_zht 有值 —— 实现必须用 name。
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"actor":{
			"id":"EvkJ","name":"河北彩花","name_zht":"不該用這個"}}}`))
	})

	got, err := srv.ActressName(context.Background(), "EvkJ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "河北彩花" {
		t.Errorf("ActressName = %q，应当取 name 字段（name_zht 实测恒为空，不该读它）", got)
	}
}

// TestActressNameNeedsNoToken 确认取名字不需要登录态 ——
// 因此即使没配 token，feed 标题也能好看些。
func TestActressNameNeedsNoToken(t *testing.T) {
	var hadAuth bool
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"actor":{"id":"x","name":"名字"}}}`))
	})
	// 不设 Token

	got, err := srv.ActressName(context.Background(), "x")
	if err != nil {
		t.Fatalf("没有 token 也应当能取名字: %v", err)
	}
	if got != "名字" {
		t.Errorf("ActressName = %q", got)
	}
	if hadAuth {
		t.Error("不该为此带 Authorization")
	}
}

// TestActressNameErrorsWhenMissing 确认名字缺失时返回错误而不是空串 ——
// 调用方据此退回 id，而不是给 feed 一个空标题。
func TestActressNameErrorsWhenMissing(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"name 为空", `{"success":1,"action":null,"data":{"actor":{"id":"x","name":""}}}`},
		{"name 只有空白", `{"success":1,"action":null,"data":{"actor":{"id":"x","name":"   "}}}`},
		{"没有 actor 对象", `{"success":1,"action":null,"data":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			})
			if _, err := srv.ActressName(context.Background(), "x"); err == nil {
				t.Error("应当报错，让调用方退回 id")
			}
		})
	}
}

// TestActressNamePropagatesUpstreamError 确认上游错误不被吞掉。
func TestActressNamePropagatesUpstreamError(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	})
	if _, err := srv.ActressName(context.Background(), "x"); err == nil {
		t.Error("上游错误应当透出")
	}
}

// TestCollectedActressesPassesPageParam 确认页码用的是上游认的参数名。
func TestCollectedActressesPassesPageParam(t *testing.T) {
	var got url.Values
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(emptyCollectedPage()))
	})
	srv.Token = "tok"

	if _, err := srv.CollectedActresses(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["page"]; !ok {
		t.Error("应当带 page 参数")
	}
	if got.Get("page") != strconv.Itoa(1) {
		t.Errorf("第一页应当是 page=1，得到 %q", got.Get("page"))
	}
	// 公共参数不能因为多了 page 就丢掉。
	if got.Get("platform") == "" {
		t.Error("公共参数丢了")
	}
}
