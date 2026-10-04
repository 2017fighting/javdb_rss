package stub

import (
	"context"
	"errors"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// TestTagVocabularyFixturesCoverEveryZoneWithDifferentShapes 钉住 fixture 的
// **结构性**要求：0/1/2/3 各一套、组数不同、组名与 category_id 都在。
//
// 组数不同不是装饰：四个片库若返回同一份词表，「换片库换词表」这条页面行为
// 在离线就永远验不出来（一个写死的快照也能让测试通过）。
func TestTagVocabularyFixturesCoverEveryZoneWithDifferentShapes(t *testing.T) {
	s := &Source{}
	counts := map[int]int{}
	for _, zone := range []int{0, 1, 2, 3} {
		v, err := s.TagVocabulary(context.Background(), zone)
		if err != nil {
			t.Fatalf("zone %d: %v", zone, err)
		}
		if len(v.Groups) == 0 {
			t.Fatalf("zone %d 一个分组都没有", zone)
		}
		counts[zone] = len(v.Groups)
		for _, g := range v.Groups {
			if g.CategoryID == "" || g.Category == "" {
				t.Errorf("zone %d 有分组缺 id 或名字: %+v", zone, g)
			}
			if len(g.Tags) == 0 {
				t.Errorf("zone %d 的分组 %s 没有标签（空组不是上游的形状）", zone, g.CategoryID)
			}
			for _, tg := range g.Tags {
				if tg.ID == "" || tg.Name == "" {
					t.Errorf("zone %d 的 %s 组有标签缺 id 或名字: %+v", zone, g.CategoryID, tg)
				}
			}
		}
	}
	// 四个片库的组数必须**不全相同**。
	seen := map[int]bool{}
	for _, n := range counts {
		seen[n] = true
	}
	if len(seen) < 2 {
		t.Errorf("四个片库的组数都是 %v —— fixture 退化成了同一份词表", counts)
	}
	if counts[0] == counts[3] {
		t.Errorf("zone 0 与 zone 3 的组数相同（%d）—— 换片库的差异离线验不出来", counts[0])
	}
}

// TestTagVocabularyFixtureKeepsIDCollisions 确认 fixture 保留了「月份与真标签
// 撞号」这个**正确**的形状：上游那份词表里 12 个 id 被多个组共用。
//
// 页面的「按名字消歧」只能靠这个形状离线验到；fixture 里没有撞号，
// 那条逻辑就是不可测的。
func TestTagVocabularyFixtureKeepsIDCollisions(t *testing.T) {
	v, err := (&Source{}).TagVocabulary(context.Background(), catalog.ZoneCensored)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]map[string]bool{}
	for _, g := range v.Groups {
		for _, tg := range g.Tags {
			if groups[tg.ID] == nil {
				groups[tg.ID] = map[string]bool{}
			}
			groups[tg.ID][g.CategoryID] = true
		}
	}
	for _, id := range []string{"12", "7", "3"} {
		if len(groups[id]) < 2 {
			t.Errorf("fixture 里 id=%s 只出现在 %v —— 撞号样本丢了", id, groups[id])
		}
	}
}

// TestTagVocabularyUnknownZoneIsBadRequest 确认越界片库是可判定的用户错误，
// 而不是一份空词表。
func TestTagVocabularyUnknownZoneIsBadRequest(t *testing.T) {
	_, err := (&Source{}).TagVocabulary(context.Background(), 9)
	if !errors.Is(err, catalog.ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
}

// TestTagVocabularyOverrideReplacesFixtures 确认覆盖表非 nil 时**只有**表里的
// 片库可用 —— 含混的取值来源正是最容易让测试假通过的地方。
func TestTagVocabularyOverrideReplacesFixtures(t *testing.T) {
	s := &Source{TagVocabularies: map[int]catalog.TagVocabulary{
		catalog.ZoneWestern: {Groups: []catalog.TagGroup{{CategoryID: "only", Category: "只有一个"}}},
	}}
	got, err := s.TagVocabulary(context.Background(), catalog.ZoneWestern)
	if err != nil {
		t.Fatalf("表里的片库应当可用: %v", err)
	}
	if len(got.Groups) != 1 || got.Groups[0].CategoryID != "only" {
		t.Errorf("应当返回覆盖表里的内容: %+v", got.Groups)
	}
	if _, err := s.TagVocabulary(context.Background(), catalog.ZoneCensored); !errors.Is(err, catalog.ErrBadRequest) {
		t.Errorf("覆盖表之外的片库 = %v, want ErrBadRequest", err)
	}
}
