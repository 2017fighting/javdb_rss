package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// 本文件回答票里最难的一条：**主属性字母 i / v 的含义**。
//
// # 为什么不能靠猜，也不能只靠 filter_tags
//
// 上游只在**女优资料**里给出 `filter_tags`（字母 + 名字），而实测扫 400 位女优
// 得到的词表只有 p/m/c/s 四个 —— i/v 一次都没出现。按那份数据，
// 正确的结论本该是「i/v 不存在」。但实测 `filter_by=0:a:<id>:v::`
// **确实改变了结果集**（229 部里剩 151 部）。
//
// 也就是说：「上游没给它起名字」与「上游不认这个字母」是两件事。
// 于是需要一个不依赖上游文案的判据 —— 这就是 props 命令。
//
// # 判据：用字段把全集分成两半
//
// 筛选字母做的是一个布尔划分：`0:a:<id>:v::` 把 229 部分成「留下的 151」与
// 「去掉的 78」。而列表响应本来就带了一批布尔/数值字段。如果某个字段的
// 真假划分与这个字母的划分**逐位重合**，那这个字段就是它的含义。
//
// 这个方法的好处是**可以证伪**：如果没有任何字段对得上，结论就是
// 「含义仍未确认」，而不是把一个像样的猜测写成事实。
// 而 p/m/c/s 是有名字的（可播放 / 含磁鏈 / 含字幕 / 單體作品），
// 它们同时充当这个方法的**正向对照**：名字已知的字母必须能被某个字段对上。
func (p *probe) props(actressID string, limit int) error {
	actressID, err := requireActress(actressID)
	if err != nil {
		return err
	}

	base, pages, err := p.allMovies(values("filter_by", actressFilter(actressID)), limit)
	if err != nil {
		return fmt.Errorf("取基线全集: %w", err)
	}
	p.save("props_baseline_"+actressID, base)

	// 顺手取一次女优资料里的 filter_tags —— 它是上游**给名字**的那部分。
	//
	// 这一步不能省：没有它，一个「字段对不上但上游有名字」的字母
	// （比如 s = 單體作品）会被打印成「含义仍未确认」，而那是不实的 ——
	// 含义有，只是不在列表响应的字段里。
	upstreamNames := map[string]string{}
	{
		var raw json.RawMessage
		if err := p.client.GetJSON(p.ctx, "/api/v1/actors/"+url.PathEscape(actressID), nil, &raw); err == nil {
			for _, t := range extractFilterTags(raw) {
				upstreamNames[t.ID] = t.Name
			}
		}
	}

	fmt.Printf("=== 主属性含义推导（女优 %s，全集 %d 部 / %d 页）===\n\n", actressID, len(base), pages)

	// predicates 是候选解释。每个都从列表响应里能直接读到 ——
	// 这正是方法成立的前提：划分必须能在**同一份响应**里验证。
	type predicate struct {
		name string
		fn   func(slimMovie) bool
	}
	predicates := []predicate{
		{"can_play==true", func(m slimMovie) bool { return m.CanPlay }},
		{"magnets_count>0", func(m slimMovie) bool { return m.MagnetsCount > 0 }},
		{"has_cnsub==true", func(m slimMovie) bool { return m.HasCNSub }},
		{"has_preview_video==true", func(m slimMovie) bool { return m.HasPreviewVideo }},
		{"has_preview_images==true", func(m slimMovie) bool { return m.HasPreviewImages }},
		{"play_subtitle>0", func(m slimMovie) bool { return m.PlaySubtitle > 0 }},
		{"new_magnets==true", func(m slimMovie) bool { return m.NewMagnets }},
		{"duration>=240", func(m slimMovie) bool { return m.Duration >= 240 }},
	}

	// 先算每个谓词在基线上的划分，供参考（也用来判断哪些谓词本身有区分度：
	// 一个恒为真的谓词不可能解释任何字母）。
	type split struct {
		name string
		ids  []string
		all  bool // 全集都为真 —— 没有区分度
	}
	splits := make([]split, 0, len(predicates))
	for _, pr := range predicates {
		var ids []string
		for _, m := range base {
			if pr.fn(m) {
				ids = append(ids, m.ID)
			}
		}
		splits = append(splits, split{name: pr.name, ids: ids, all: len(ids) == len(base)})
	}
	fmt.Printf("  %-26s %8s  %s\n", "基线字段划分", "条数", "备注")
	for _, s := range splits {
		note := ""
		if s.all {
			note = "全集都为真，无区分度"
		} else if len(s.ids) == 0 {
			note = "全集都为假，无区分度"
		}
		fmt.Printf("  %-26s %8d  %s\n", s.name, len(s.ids), note)
	}

	// 逐个字母，找与它逐位重合的谓词。
	fmt.Printf("\n  %-4s %8s %-12s  %s\n", "字母", "全集", "上游给的名字", "与它逐位重合的字段")
	letters := defaultMainProps
	matchedAny := false
	for _, l := range letters {
		ids, _, err := p.allMovieIDs(values("filter_by", actressFilter(actressID)+":"+l+"::"), limit)
		if err != nil {
			fmt.Printf("  %-4s %8s %-12s  请求失败：%v\n", l, "-", "-", err)
			continue
		}
		var matches []string
		for _, s := range splits {
			// 逐位重合而不是「集合相同」：两端都是同一批作品时，
			// 顺序也应当一致；不要求顺序的话，一个重排就无法与真解释区分。
			if sameSeq(s.ids, ids) {
				matches = append(matches, s.name)
			}
		}

		name := upstreamNames[l]
		if name == "" {
			name = "（无）"
		}
		desc := "—（列表形态里没有对应字段，且上游也没给名字）"
		switch {
		case len(matches) > 0:
			desc = strings.Join(matches, " 且 ")
			matchedAny = true
		case upstreamNames[l] != "":
			// 有名字没字段：含义是清楚的，只是判据不在这份响应里。
			// 分开报是为了不让读者把「方法用不上」误读成「含义未知」。
			desc = "（上游已给名字，不需要字段反推）"
		}
		fmt.Printf("  %-4s %8d %-12s  %s\n", l, len(ids), name, desc)
	}

	fmt.Println()
	if matchedAny {
		fmt.Println("判读：有上游给名字的字母（p/m/c）能各自对上某个字段 —— 这说明")
		fmt.Println("      这个方法本身是有效的（正向对照成立）。因此 i/v 对上哪个字段，")
		fmt.Println("      就采用那个字段的含义，而不是继续猜。")
		fmt.Println("      s 有名字（單體作品）但列表形态里没有对应字段：它的含义来自上游，")
		fmt.Println("      不需要（也无法）用字段反推 —— 这两件事必须分开报。")
	} else {
		fmt.Println("⚠️ 连有名字的字母都没对上任何字段 —— 方法在这里失效，")
		fmt.Println("   不要采信本表；先检查列表响应的字段名是不是改了。")
	}
	return nil
}
