package stub

import (
	"context"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// 这份 fixture 是**结构性的**，不是上游那 130KB 的完整词表。
//
// 三个刻意的性质：
//
//  1. 分组 id / 名字 / 顺序与上游一致（基本/年份/月份/主題/…），因此页面按分组
//     渲染的代码在离线与真上游下走同一条路。
//  2. 四个片库的**组数不同**（0→11、1→8、2→11、3→5），因此「换片库换词表」
//     在离线也能被验出来 —— 一个写死的快照会让所有 type 返回同一份。
//  3. 组内标签可以缩写（上游那 355 条对测试没有信息增量），
//     但**刻意保留了几处 id 撞号**（月份 12 与 類別 12、月份 7 与 主題 7、
//     月份 3 与 服裝 3）—— 那是页面「按名字消歧」唯一能离线验到的入口。
//
// 组的构成按实测记录摆（.scratch/javdb-rss-ui/notes/tag-vocabulary.md 第 1 节）：
// 1 多一个 `other` 而少 body/behavior/play_method/category；2 多一个 `place`；
// 3 只有一个笼统的 `tag`。
var defaultVocabularies = map[int]catalog.TagVocabulary{
	catalog.ZoneCensored: {Groups: []catalog.TagGroup{
		fixtureGroup("main", "基本",
			fixtureTag("p", "可播放"), fixtureTag("m", "可下載"), fixtureTag("c", "含字幕"),
			fixtureTag("s", "單體影片"), fixtureTag("i", "含預覽圖"), fixtureTag("v", "含預覽視頻")),
		fixtureGroup("year", "年份", fixtureTag("2026", "2026"), fixtureTag("2025", "2025")),
		fixtureGroup("month", "月份", fixtureTag("12", "12"), fixtureTag("7", "7"), fixtureTag("3", "3")),
		fixtureGroup("subject", "主題", fixtureTag("68", "中文字幕"), fixtureTag("7", "處女")),
		fixtureGroup("role", "角色", fixtureTag("161", "女教師"), fixtureTag("212", "人妻")),
		fixtureGroup("cloth", "服裝", fixtureTag("3", "眼鏡"), fixtureTag("10", "西裝")),
		fixtureGroup("body", "體型", fixtureTag("28", "巨乳"), fixtureTag("312", "長身")),
		fixtureGroup("behavior", "行爲", fixtureTag("46", "潮吹")),
		fixtureGroup("play_method", "玩法", fixtureTag("65", "中出")),
		fixtureGroup("category", "類別", fixtureTag("12", "成人電影"), fixtureTag("45", "第一人稱攝影")),
		fixtureGroup("duration", "時長",
			fixtureTag("lt-45", "45分鐘以內"), fixtureTag("45-90", "45-90分鐘"),
			fixtureTag("90-120", "90-120分鐘"), fixtureTag("gt-120", "120分鐘以上")),
	}},
	catalog.ZoneUncensored: {Groups: []catalog.TagGroup{
		fixtureGroup("main", "基本", fixtureTag("p", "可播放"), fixtureTag("m", "可下載"), fixtureTag("c", "含字幕")),
		fixtureGroup("year", "年份", fixtureTag("2026", "2026")),
		fixtureGroup("month", "月份", fixtureTag("12", "12")),
		fixtureGroup("subject", "主題", fixtureTag("68", "中文字幕")),
		fixtureGroup("role", "角色", fixtureTag("161", "女教師")),
		fixtureGroup("cloth", "服裝", fixtureTag("3", "眼鏡")),
		fixtureGroup("other", "其他", fixtureTag("99", "其他")),
		fixtureGroup("duration", "時長", fixtureTag("gt-120", "120分鐘以上")),
	}},
	catalog.ZoneWestern: {Groups: []catalog.TagGroup{
		fixtureGroup("main", "基本", fixtureTag("p", "可播放"), fixtureTag("m", "可下載")),
		fixtureGroup("year", "年份", fixtureTag("2026", "2026")),
		fixtureGroup("month", "月份", fixtureTag("12", "12")),
		fixtureGroup("subject", "主題", fixtureTag("68", "中文字幕")),
		fixtureGroup("role", "角色", fixtureTag("161", "女教師")),
		fixtureGroup("cloth", "服裝", fixtureTag("3", "眼鏡")),
		fixtureGroup("body", "體型", fixtureTag("28", "巨乳")),
		fixtureGroup("behavior", "行爲", fixtureTag("46", "潮吹")),
		fixtureGroup("play_method", "玩法", fixtureTag("65", "中出")),
		fixtureGroup("category", "類別", fixtureTag("12", "成人電影")),
		fixtureGroup("place", "地點", fixtureTag("5", "戶外")),
	}},
	catalog.ZoneFC2: {Groups: []catalog.TagGroup{
		fixtureGroup("main", "基本", fixtureTag("p", "可播放"), fixtureTag("m", "可下載")),
		fixtureGroup("year", "年份", fixtureTag("2026", "2026")),
		fixtureGroup("month", "月份", fixtureTag("12", "12")),
		fixtureGroup("tag", "標籤", fixtureTag("100", "素人")),
		fixtureGroup("duration", "時長", fixtureTag("gt-120", "120分鐘以上")),
	}},
}

func fixtureTag(id, name string) catalog.Tag { return catalog.Tag{ID: id, Name: name} }
func fixtureGroup(categoryID, category string, tags ...catalog.Tag) catalog.TagGroup {
	return catalog.TagGroup{CategoryID: categoryID, Category: category, Tags: tags}
}

// TagVocabulary 实现 catalog.Source。
//
// 覆盖表（Source.TagVocabularies）非 nil 时就以它为准，连「没列出的片库」
// 也判成错误 —— 否则测试没法既给一个自定义词表、又保留内置 fixture 的其余部分，
// 而含混的取值来源正是最容易让测试假通过的地方。
func (s *Source) TagVocabulary(_ context.Context, zone int) (catalog.TagVocabulary, error) {
	if s.Err != nil {
		return catalog.TagVocabulary{}, s.Err
	}
	if s.TagVocabularies != nil {
		if v, ok := s.TagVocabularies[zone]; ok {
			return v, nil
		}
		return catalog.TagVocabulary{}, catalog.ErrUnknownZone(zone)
	}
	if v, ok := defaultVocabularies[zone]; ok {
		return v, nil
	}
	return catalog.TagVocabulary{}, catalog.ErrUnknownZone(zone)
}
