package appapi

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// actressBody 是 `/api/v1/actors/{id}` 的 data 部分，形状照实测的
// evidence/actor_EvkJ.json 抄（顶层 filter_tags 与 tags，actor 里只有基本信息）。
//
// 刻意把 `name_zht` 填上值：实现必须读 `name`（实测 name_zht 恒为空）。
const actressBody = `{"actor":{"id":"EvkJ","name":"河北彩花","name_zht":"不該用這個","videos_count":229},
	"filter_tags":[{"id":"p","name":"可播放"},{"id":"s","name":"單體作品"},{"id":"m","name":"含磁鏈"},{"id":"c","name":"含字幕"}],
	"tags":[{"id":"10","name":"4小時以上作品","videos_count":117},{"id":"28","name":"單體作品","videos_count":92},
	        {"id":"65","name":"苗條","videos_count":78}]}`

func actressResponse() string {
	return `{"success":1,"action":null,"data":` + actressBody + `}`
}

// TestActressTagsReturnsNameMainAndTags 是本方法存在的理由：
// 一次读取同时交出显示名、她**支持的主属性**（上游 filter_tags）、她自己的 tags[]。
func TestActressTagsReturnsNameMainAndTags(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(actressResponse()))
	})

	got, err := srv.ActressTags(context.Background(), "EvkJ")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "EvkJ" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Name != "河北彩花" {
		t.Errorf("Name = %q，应当读 name（name_zht 实测恒为空）", got.Name)
	}
	if got.VideosCount != 229 {
		t.Errorf("VideosCount = %d, want 229", got.VideosCount)
	}

	// main 来自 filter_tags，顺序原样（p s m c）。
	var main []string
	for _, m := range got.Main {
		main = append(main, m.ID)
	}
	if strings.Join(main, ",") != "p,s,m,c" {
		t.Errorf("Main = %v, want p,s,m,c（顺序即上游顺序）", main)
	}
	if got.Main[1].Name != "單體作品" {
		t.Errorf("Main[1].Name = %q", got.Main[1].Name)
	}

	// tags 来自顶层 tags[]，逐项带 videos_count。
	if len(got.Tags) != 3 {
		t.Fatalf("Tags = %d 项, want 3", len(got.Tags))
	}
	if got.Tags[0].ID != "10" || got.Tags[0].VideosCount != 117 {
		t.Errorf("Tags[0] = %+v", got.Tags[0])
	}
	// 上游不给分组：这一层不许编一个出来。
	for _, tag := range got.Tags {
		if tag.Name == "" {
			t.Errorf("标签 %s 缺名字 —— 页面要靠名字消歧", tag.ID)
		}
	}
}

// TestActressTagsReadsTheUpstreamOnce 钉住「合并成一次读取」：
// 名字、主属性、标签来自同一个上游端点，一次请求就该拿到全部。
//
// 写两次读取不会有任何测试失败，只会白多一个上游往返 ——
// 而这条路径在页面切女优时是热路径。
func TestActressTagsReadsTheUpstreamOnce(t *testing.T) {
	var hits atomic.Int64
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/actors/") {
			t.Errorf("打了别的路径: %s", r.URL.Path)
		}
		hits.Add(1)
		_, _ = w.Write([]byte(actressResponse()))
	})

	if _, err := srv.ActressTags(context.Background(), "EvkJ"); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("一次 ActressTags 打了 %d 次上游，want 1", got)
	}
}

// TestActressTagsNeedsNoToken 确认它匿名可读 —— 标签筛选本身是匿名的，
// 不该被「收藏读不到」连坐（与 ActressName 同一条性质）。
func TestActressTagsNeedsNoToken(t *testing.T) {
	var hadAuth bool
	srv := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(actressResponse()))
	})
	// 不设 Token

	if _, err := srv.ActressTags(context.Background(), "EvkJ"); err != nil {
		t.Fatalf("没有 token 也应当能取标签: %v", err)
	}
	if hadAuth {
		t.Error("不该为此带 Authorization")
	}
}

// TestActressTagsToleratesEmptyCollections 确认上游没给主属性/标签时
// 返回空列表而不是错误 —— 「这位女优还没有任何标签」是有信息量的状态，
// 把它报成 502 会让页面显示一个假的故障。
func TestActressTagsToleratesEmptyCollections(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"actor":{"id":"新","name":"新人","videos_count":0}}}`))
	})

	got, err := srv.ActressTags(context.Background(), "新")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(got.Main) != 0 || len(got.Tags) != 0 {
		t.Errorf("应当是两个空列表: main=%v tags=%v", got.Main, got.Tags)
	}
}

// TestActressTagsEmptyIDIsError 确认空 id 不发请求。
func TestActressTagsEmptyIDIsError(t *testing.T) {
	srv := clientFor(t, func(http.ResponseWriter, *http.Request) {
		t.Error("空 id 不该发请求")
	})
	if _, err := srv.ActressTags(context.Background(), "  "); err == nil {
		t.Error("空 id 应当报错")
	}
}

// TestActressTagsPropagatesUpstreamError 确认上游错误不被吞掉 ——
// 调用方据此返回 502，而不是一份看起来合法的空标签。
func TestActressTagsPropagatesUpstreamError(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	})
	if _, err := srv.ActressTags(context.Background(), "EvkJ"); err == nil {
		t.Error("上游错误应当透出")
	}
}

// TestActressTagsIsAnAnonymousReadEvenWhenTokenSet 确认有 token 时
// 也不会因此改变行为（只是多带一个头）—— 这条路径不依赖登录态。
func TestActressTagsIsAnAnonymousReadEvenWhenTokenSet(t *testing.T) {
	srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(actressResponse()))
	})
	srv.Token = "tok"

	got, err := srv.ActressTags(context.Background(), "EvkJ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "河北彩花" {
		t.Errorf("Name = %q", got.Name)
	}
	var _ catalog.ActressTags = got
}
