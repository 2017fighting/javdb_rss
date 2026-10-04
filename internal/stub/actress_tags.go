package stub

import (
	"context"
	"fmt"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// 这份 fixture 是**结构性的**，不是上游那份完整数据。
//
// 三个刻意的性质：
//
//  1. `main` 就是 EvkJ 实测的 `p s m c` 四个主属性（而不是词表 `main` 组的全部 6 个）——
//     「基本组只列她支持的那几个」这条页面行为只能靠这个形状离线验到。
//  2. tags 里**刻意保留了三处 id 撞号**（12 与月份、7 与月份、3 与月份），
//     且名字分别是真标签的名字（成人電影 / 處女 / 眼鏡）。
//     页面「按名字消歧」的逻辑唯一能离线验到的入口就是这里 ——
//     一组不撞号的 fixture 会让那段逻辑不可测。
//  3. 作品数是编的，但字段齐全（上游每项都有 videos_count）。
//
// id/名字与实测一致的部分来自 `.scratch/javdb-rss-ui/evidence/actor_EvkJ.json`；
// 数量被大幅缩写 —— 上游那 80 项对测试没有信息增量。
func defaultMainAttributes() []catalog.MainAttribute {
	// 顺序即上游顺序（实测 EvkJ 是 p s m c）。
	return []catalog.MainAttribute{
		{ID: "p", Name: "可播放"},
		{ID: "s", Name: "單體作品"},
		{ID: "m", Name: "含磁鏈"},
		{ID: "c", Name: "含字幕"},
	}
}

func defaultActressTagList() []catalog.Tag {
	return []catalog.Tag{
		// 三处撞号项：名字是真标签的名字，页面据此把它们落回真分组，
		// 而不是月份组。id 与名字都对得上 zone 0 的词表 fixture（见 tags.go）。
		{ID: "12", Name: "成人電影", VideosCount: 9},
		{ID: "7", Name: "處女", VideosCount: 5},
		{ID: "3", Name: "眼鏡", VideosCount: 7},
		// 不撞号的普通项，覆盖几个不同分组。
		{ID: "68", Name: "中文字幕", VideosCount: 31},
		{ID: "28", Name: "巨乳", VideosCount: 67},
		{ID: "161", Name: "女教師", VideosCount: 11},
		{ID: "65", Name: "中出", VideosCount: 8},
		{ID: "45", Name: "第一人稱攝影", VideosCount: 6},
	}
}

// ActressTags 实现 catalog.Source。
//
// 覆盖表（Source.ActressTagProfiles）非 nil 时就以它为准，连「没列出的 id」
// 也判成错误 —— 否则测试没法既给一个自定义档、又保留内置 fixture 的其余部分，
// 而含混的取值来源正是最容易让测试假通过的地方。
//
// 默认 fixture 对**任何非空 id** 都返回一份结构完整的档（手输一个不在收藏里的 id
// 也要能出标签，那是页面的一条真实需求），只是名字用 `Names` 表/ id 兜底。
func (s *Source) ActressTags(_ context.Context, id string) (catalog.ActressTags, error) {
	if s.Err != nil {
		return catalog.ActressTags{}, s.Err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return catalog.ActressTags{}, fmt.Errorf("女优 id 为空: %w", catalog.ErrBadRequest)
	}
	if s.ActressTagProfiles != nil {
		p, ok := s.ActressTagProfiles[id]
		if !ok {
			return catalog.ActressTags{}, fmt.Errorf("女优 %s 没有标签", id)
		}
		return p, nil
	}

	name := id
	if n, ok := s.actressName(id); ok {
		name = n
	}
	videos := 0
	if id == "EvkJ" {
		videos = 229
	}
	return catalog.ActressTags{
		ID:          id,
		Name:        name,
		VideosCount: videos,
		Main:        defaultMainAttributes(),
		Tags:        defaultActressTagList(),
	}, nil
}
