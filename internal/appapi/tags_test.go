package appapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// tagVocabularyResponse 是上游 `/api/v2/tags` 的**真实形状**：
// 顶层那个键就叫 `tags`，装的却是分组（见 notes/tag-vocabulary.md 第 1 节）。
//
// 第三个标签刻意带 `videos_count`（词表实测不给，但女优 tags[] 会给）——
// 上游哪天把这个字段补上，我们要能原样透出去。
func tagVocabularyResponse() string {
	return `{"success":1,"action":null,"data":{"tags":[
		{"category":"基本","category_id":"main","tags":[
			{"id":"p","name":"可播放"},{"id":"m","name":"可下載"}]},
		{"category":"年份","category_id":"year","tags":[
			{"id":"2026","name":"2026"}]},
		{"category":"服裝","category_id":"cloth","tags":[
			{"id":"3","name":"眼鏡","videos_count":117}]}
	]}}`
}

// TestTagVocabularyPreservesUpstreamOrderAndIsAnonymous 是本方法的两条外部行为：
// 分组/组内顺序与字段名原样搬运；请求**不带** token（该端点匿名可用）。
func TestTagVocabularyPreservesUpstreamOrderAndIsAnonymous(t *testing.T) {
	var gotPath, gotType, gotAuth string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotType = r.URL.Query().Get("type")
		gotAuth = r.Header.Get("authorization")
		_, _ = w.Write([]byte(tagVocabularyResponse()))
	})

	got, err := c.TagVocabulary(context.Background(), 2)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if gotPath != "/api/v2/tags" {
		t.Errorf("路径 = %q, want /api/v2/tags", gotPath)
	}
	if gotType != "2" {
		t.Errorf("type = %q, want 2", gotType)
	}
	if gotAuth != "" {
		t.Errorf("标签词表不该带 token，却发了 %q", gotAuth)
	}

	// 分组顺序、分组名原样。
	wantGroups := []struct{ id, name string }{{"main", "基本"}, {"year", "年份"}, {"cloth", "服裝"}}
	if len(got.Groups) != len(wantGroups) {
		t.Fatalf("分组数 = %d, want %d", len(got.Groups), len(wantGroups))
	}
	for i, want := range wantGroups {
		if got.Groups[i].CategoryID != want.id || got.Groups[i].Category != want.name {
			t.Errorf("第 %d 组 = {%s %s}, want {%s %s}",
				i, got.Groups[i].CategoryID, got.Groups[i].Category, want.id, want.name)
		}
	}
	// 组内标签与顺序原样。
	main := got.Groups[0].Tags
	if len(main) != 2 || main[0].ID != "p" || main[0].Name != "可播放" || main[1].ID != "m" {
		t.Errorf("基本组 = %+v", main)
	}
	// 上游给了作品数就透出去。
	if got.Groups[2].Tags[0].VideosCount != 117 {
		t.Errorf("videos_count = %d, want 117", got.Groups[2].Tags[0].VideosCount)
	}
}

// TestTagVocabularyRejectsUnknownZoneWithoutRequest 是本包最要紧的一条。
//
// 上游对越界 type 是**静默回落**（type=9 与 type=0 的响应逐字节相同），
// 所以一旦把 4/9 原样发出去，用户拿到的是**另一个片库的词表**，
// 而且没有任何地方会报错。必须本地判死，且**不能**白打一次上游。
func TestTagVocabularyRejectsUnknownZoneWithoutRequest(t *testing.T) {
	called := false
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(tagVocabularyResponse()))
	})

	for _, zone := range []int{-1, 4, 9} {
		_, err := c.TagVocabulary(context.Background(), zone)
		if !errors.Is(err, catalog.ErrBadRequest) {
			t.Errorf("zone=%d 的错误 = %v，应当是 ErrBadRequest", zone, err)
		}
		if !strings.Contains(err.Error(), "0=有码") {
			t.Errorf("zone=%d 的文案应当写出有效取值: %v", zone, err)
		}
	}
	if called {
		t.Error("越界 zone 不该发请求 —— 上游会把它静默回落成 type=0")
	}
}

// TestTagVocabularyUpstreamErrorSurfaces 确认上游失败是个可见的错误，
// 而不是一份空词表（空词表会被页面当成「这个片库没有标签」）。
func TestTagVocabularyUpstreamErrorSurfaces(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":0,"action":"ServerError","message":"boom","data":null}`))
	})
	got, err := c.TagVocabulary(context.Background(), 0)
	if err == nil {
		t.Fatalf("上游失败应当报错，得到 %+v", got)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("原因应当透出: %v", err)
	}
}
