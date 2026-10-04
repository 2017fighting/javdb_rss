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

// wantMovie 生成一条 review_movies 里的作品。
//
// 形态取自 movieSlim（与 /api/v2/search 共用的精简形态）：先例项目 javdb-cli 的
// `want --has-magnets` 正是按 magnets_count 筛的，因此可以确认这条响应里带着它 ——
// 也正因为带着它，本服务能**不拉磁链**就知道哪些作品还在等磁力。
func wantMovie(id, number string, magnetsCount int) string {
	return fmt.Sprintf(
		`{"id":%q,"number":%q,"title":"标题 %s","release_date":"2026-09-01",`+
			`"has_cnsub":false,"magnets_count":%d}`,
		id, number, number, magnetsCount)
}

func wantPage(movies ...string) string {
	return `{"success":1,"action":null,"data":{"movies":[` + strings.Join(movies, ",") + `]}}`
}

func emptyWantPage() string { return wantPage() }

// magnetsBody 生成一条磁链响应。字段名与 /api/v1/movies/{id}/magnets 一致。
func magnetsBody(hash string, cnsub bool, createdAt string) string {
	return fmt.Sprintf(
		`{"success":1,"action":null,"data":{"magnets":[`+
			`{"name":"KV-1","hash":%q,"size":3110,"cnsub":%t,"hd":true,`+
			`"files_count":2,"created_at":%q}]}}`,
		hash, cnsub, createdAt)
}

// TestWantToWatchPaginatesUntilEmpty 确认按 page 翻页、遇空页停止，且**带 token**。
func TestWantToWatchPaginatesUntilEmpty(t *testing.T) {
	var pages []string
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") == "" {
			t.Error("「想看」清单必须带 Authorization —— 它是需要 token 的端点")
		}
		if r.URL.Path != "/api/v2/users/review_movies" {
			t.Errorf("路径 = %s，应当是 /api/v2/users/review_movies", r.URL.Path)
			return
		}
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(wantPage(wantMovie("m1", "KV-1", 0))))
		case "2":
			_, _ = w.Write([]byte(wantPage(wantMovie("m2", "KV-2", 0))))
		default:
			_, _ = w.Write([]byte(emptyWantPage()))
		}
	})
	srv.Token = "tok"

	got, err := srv.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("应当成功: %v", err)
	}
	if len(got.Works) != 2 {
		t.Fatalf("得到 %d 部，want 2", len(got.Works))
	}
	if got.Works[0].ID != "m1" || got.Works[1].ID != "m2" {
		t.Errorf("顺序应当保持上游给出的顺序: %+v", got.Works)
	}
	if got.Truncated {
		t.Error("遇到空页就是完整的清单，不该报截断")
	}
	if len(pages) != 3 {
		t.Errorf("请求了 %v，应当在第 3 页（空页）停下", pages)
	}
}

// TestWantToWatchKeepsWorksWithoutMagnets 是本票最要紧的一条领域行为：
//
// 「标了想看但还没有种」是这份清单的**常态，不是错误**。这类作品必须
// 留在结果里（Magnets 为空），否则上层连「有几部在等磁力」都算不出来 ——
// 而那个计数正是给用户的可见信号。
//
// 同时确认 magnets_count==0 时**不发**磁链请求：省掉一次注定为空的往返。
func TestWantToWatchKeepsWorksWithoutMagnets(t *testing.T) {
	var magnetRequests int
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/users/review_movies" {
			magnetRequests++
			t.Errorf("magnets_count=0 的作品不该去拉磁链，却请求了 %s", r.URL.Path)
			return
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(wantPage(wantMovie("m1", "KV-1", 0))))
			return
		}
		_, _ = w.Write([]byte(emptyWantPage()))
	})
	srv.Token = "tok"

	got, err := srv.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("应当成功: %v", err)
	}
	if len(got.Works) != 1 {
		t.Fatalf("得到 %d 部 —— 尚无磁链的作品被丢掉了", len(got.Works))
	}
	if got.Works[0].Number != "KV-1" {
		t.Errorf("番号 = %q", got.Works[0].Number)
	}
	if len(got.Works[0].Magnets) != 0 {
		t.Errorf("不该凭空造出磁链: %+v", got.Works[0].Magnets)
	}
	if magnetRequests != 0 {
		t.Errorf("磁链请求次数 = %d，want 0", magnetRequests)
	}
}

// TestWantToWatchFetchesMagnetsWhenCounted 确认 magnets_count>0 时确实去拉磁链，
// 并把它们映射到领域模型上。
func TestWantToWatchFetchesMagnetsWhenCounted(t *testing.T) {
	var magnetPaths []string
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/users/review_movies" {
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(wantPage(wantMovie("m1", "KV-1", 1))))
				return
			}
			_, _ = w.Write([]byte(emptyWantPage()))
			return
		}
		magnetPaths = append(magnetPaths, r.URL.Path)
		_, _ = w.Write([]byte(magnetsBody("aa11", true, "09/27/2026")))
	})
	srv.Token = "tok"

	got, err := srv.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("应当成功: %v", err)
	}
	if len(magnetPaths) != 1 || magnetPaths[0] != "/api/v1/movies/m1/magnets" {
		t.Fatalf("磁链请求 = %v，应当正好问一次 /api/v1/movies/m1/magnets", magnetPaths)
	}
	if len(got.Works) != 1 || len(got.Works[0].Magnets) != 1 {
		t.Fatalf("磁链没有映射进来: %+v", got.Works)
	}
	m := got.Works[0].Magnets[0]
	if m.Infohash != "aa11" || !m.CNSub || m.SizeMiB != 3110 || m.CreatedAt != "09/27/2026" {
		t.Errorf("磁链字段映射不对: %+v", m)
	}
}

// TestWantToWatchDedupesAcrossPages 确认跨页按 movie id 去重 ——
// 上游分页在清单变动时可能重叠，重复会让同一部作品在 feed 里出现两次。
func TestWantToWatchDedupesAcrossPages(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(wantPage(wantMovie("m1", "KV-1", 0))))
		case "2":
			_, _ = w.Write([]byte(wantPage(wantMovie("m1", "KV-1", 0), wantMovie("m2", "KV-2", 0))))
		default:
			_, _ = w.Write([]byte(emptyWantPage()))
		}
	})
	srv.Token = "tok"

	got, err := srv.WantToWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Works) != 2 {
		t.Errorf("得到 %d 部，跨页重复应当被去掉", len(got.Works))
	}
}

// TestWantToWatchWithoutTokenReturnsErrNoToken 是最重要的一条：
//
// 没有 token 时**绝不能**返回 200 + 空清单 —— 那会被理解成「我没标过任何想看」，
// 而真相是「服务读不到」。两者需要完全不同的动作，因此必须可判定。
func TestWantToWatchWithoutTokenReturnsErrNoToken(t *testing.T) {
	srv := clientFor(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("没有 token 时不该发请求，却打了 %s", r.URL.Path)
	})
	// 刻意不设 Token。

	_, err := srv.WantToWatch(context.Background())
	if err == nil {
		t.Fatal("应当报错，而不是返回空清单")
	}
	if !errors.Is(err, catalog.ErrNoToken) {
		t.Errorf("err 应当可用 errors.Is 判定为 ErrNoToken，得到: %v", err)
	}
}

// TestWantToWatchPropagatesAuthError 确认 token 过期与「没配 token」被分开 ——
// 一个要重新导出，一个要填配置。
func TestWantToWatchPropagatesAuthError(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":0,"action":"TokenExpired","message":"token 已過期","data":null}`))
	})
	srv.Token = "expired"

	_, err := srv.WantToWatch(context.Background())
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

// TestWantToWatchReportsTruncationAtPageCap 确认触顶时不再静默。
//
// 上游**永远返回满页**（异常上游）时，我们必须在 maxWantPages 处停下 ——
// 否则就是一个自己打自己的死循环；停下之后必须说「这份清单不完整」。
func TestWantToWatchReportsTruncationAtPageCap(t *testing.T) {
	var asked []int
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		asked = append(asked, p)
		_, _ = w.Write([]byte(wantPage(wantMovie(fmt.Sprintf("m%d", p), fmt.Sprintf("KV-%d", p), 0))))
	})
	srv.Token = "tok"

	got, err := srv.WantToWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated {
		t.Error("上限之外还有数据，必须报 Truncated")
	}
	if got.PagesFetched != maxWantPages || got.MaxPages != maxWantPages {
		t.Errorf("页数信号 = %d/%d，want %d/%d", got.PagesFetched, got.MaxPages, maxWantPages, maxWantPages)
	}
	if len(got.Works) != maxWantPages {
		t.Errorf("触顶时只给到上限为止，得到 %d 部", len(got.Works))
	}
	// 上限 + 1 页的探针必须发生：不探这一页就分不清「恰好装满」与「还有更多」。
	if len(asked) != maxWantPages+1 {
		t.Errorf("请求了 %d 页，应当在第 %d 页探一次", len(asked), maxWantPages+1)
	}
}

// TestWantToWatchCompleteWhenCapExactlyFills 是边界：清单恰好装满上限时
// **不该**报截断 —— 一条永远为真的警告会让人忽略它。
func TestWantToWatchCompleteWhenCapExactlyFills(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if p > maxWantPages {
			_, _ = w.Write([]byte(emptyWantPage()))
			return
		}
		_, _ = w.Write([]byte(wantPage(wantMovie(fmt.Sprintf("m%d", p), fmt.Sprintf("KV-%d", p), 0))))
	})
	srv.Token = "tok"

	got, err := srv.WantToWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncated {
		t.Error("恰好装满上限是完整的清单，不该报截断")
	}
	if len(got.Works) != maxWantPages {
		t.Errorf("得到 %d 部，want %d", len(got.Works), maxWantPages)
	}
	if got.PagesFetched != maxWantPages+1 {
		t.Errorf("PagesFetched = %d，应当把那次探针也算进去", got.PagesFetched)
	}
}

// TestWantToWatchPassesStatusAndPage 确认用的参数名是上游认的那两个。
//
// status 必须**恰好**是 want_watch：先例项目把 watched 与 want_watch 当作
// 同一条端点的两种取值 —— 写错了（比如 want-watch）会静默拿到另一份清单。
func TestWantToWatchPassesStatusAndPage(t *testing.T) {
	var got url.Values
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(emptyWantPage()))
	})
	srv.Token = "tok"

	if _, err := srv.WantToWatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.Get("status") != "want_watch" {
		t.Errorf("status = %q，want want_watch", got.Get("status"))
	}
	if got.Get("page") != "1" {
		t.Errorf("第一页应当是 page=1，得到 %q", got.Get("page"))
	}
	// 公共参数不能因为多了 status/page 就丢掉。
	if got.Get("platform") == "" {
		t.Error("公共参数 platform 丢了 —— 少了它上游会报 ParameterInvalid")
	}
}
