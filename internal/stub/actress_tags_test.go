package stub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// TestActressTagsFixtureCarriesEvkJMainAttrs 钉住 fixture 的**结构性**性质：
// main 是 EvkJ 实测的 `p s m c` 四个（而不是词表 main 组的全部 6 个）——
// 页面「基本组只列她支持的那几个」这条行为只能靠这个形状离线验到。
func TestActressTagsFixtureCarriesEvkJMainAttrs(t *testing.T) {
	got, err := (&Source{}).ActressTags(context.Background(), "EvkJ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "河北彩花" {
		t.Errorf("Name = %q", got.Name)
	}
	var ids []string
	for _, m := range got.Main {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "p,s,m,c" {
		t.Errorf("Main = %v, want p,s,m,c（EvkJ 实测值，顺序原样）", ids)
	}
	if len(got.Main) == 6 {
		t.Error("Main 用了词表 main 组的全部 6 个 —— 那正是「只列她支持的」要避免的")
	}
	if len(got.Tags) == 0 {
		t.Fatal("Tags 为空，页面无从渲染")
	}
	for _, tag := range got.Tags {
		if tag.ID == "" || tag.Name == "" {
			t.Errorf("有标签缺 id 或名字: %+v", tag)
		}
		if tag.VideosCount == 0 {
			t.Errorf("标签 %s 的作品数没给 —— 上游逐项都有 videos_count", tag.ID)
		}
	}
}

// TestActressTagsFixtureKeepsNameDisambiguation 确认 fixture 保留了三处 id 撞号，
// **且名字正好对应真标签**，因此「按名字消歧」这段逻辑离线可验。
//
// 只留撞号而名字乱填的话，那条逻辑在离线永远落在「对不上 → 不显示」那一支，
// 看起来测试通过了，其实正确路径一次都没走过。
func TestActressTagsFixtureKeepsNameDisambiguation(t *testing.T) {
	s := &Source{}
	vocab, err := s.TagVocabulary(context.Background(), catalog.ZoneCensored)
	if err != nil {
		t.Fatal(err)
	}
	// 词表里某 id 的所有候选名字（跨分组）。
	candidates := func(id string) []string {
		var out []string
		for _, g := range vocab.Groups {
			for _, tg := range g.Tags {
				if tg.ID == id {
					out = append(out, tg.Name)
				}
			}
		}
		return out
	}

	got, err := s.ActressTags(context.Background(), "EvkJ")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, tag := range got.Tags {
		names := candidates(tag.ID)
		if len(names) < 2 {
			continue
		}
		checked++
		var matches int
		for _, n := range names {
			if n == tag.Name {
				matches++
			}
		}
		if matches != 1 {
			t.Errorf("id=%s name=%q 在词表候选 %v 里命中 %d 次，want 恰好 1 次（消歧要能唯一胜出）",
				tag.ID, tag.Name, names, matches)
		}
	}
	if checked < 3 {
		t.Errorf("fixture 里只有 %d 个撞号 id，want 至少 3（月份 12/7/3）—— 消歧逻辑离线验不足", checked)
	}
}

// TestActressTagsAnyIDReturnsAProfile 确认手输一个不在收藏里的 id 也能出标签 ——
// 那是页面的一条真实需求（没配 token 时仍能用女优标签订阅）。
func TestActressTagsAnyIDReturnsAProfile(t *testing.T) {
	got, err := (&Source{}).ActressTags(context.Background(), "HAND1")
	if err != nil {
		t.Fatalf("非收藏里的 id 也该能取到标签: %v", err)
	}
	if got.ID != "HAND1" || got.Name == "" {
		t.Errorf("got = %+v", got)
	}
	if len(got.Main) == 0 || len(got.Tags) == 0 {
		t.Error("结构与内置 fixture 应当一致")
	}
}

// TestActressTagsEmptyIDIsBadRequest 确认空 id 是可判定的用户错误，而不是一份空档。
func TestActressTagsEmptyIDIsBadRequest(t *testing.T) {
	_, err := (&Source{}).ActressTags(context.Background(), "   ")
	if !errors.Is(err, catalog.ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
}

// TestActressTagsOverrideReplacesFixtures 确认覆盖表非 nil 时**只有**表里的 id 可用 ——
// 含混的取值来源正是最容易让测试假通过的地方。
func TestActressTagsOverrideReplacesFixtures(t *testing.T) {
	s := &Source{ActressTagProfiles: map[string]catalog.ActressTags{
		"only": {ID: "only", Name: "只有一个", Main: []catalog.MainAttribute{{ID: "m", Name: "含磁鏈"}}},
	}}
	got, err := s.ActressTags(context.Background(), "only")
	if err != nil {
		t.Fatalf("表里的 id 应当可用: %v", err)
	}
	if got.Name != "只有一个" || len(got.Main) != 1 {
		t.Errorf("应当返回覆盖表里的内容: %+v", got)
	}
	if _, err := s.ActressTags(context.Background(), "EvkJ"); err == nil {
		t.Error("覆盖表之外的 id 应当报错，而不是悄悄退回内置 fixture")
	}
}
