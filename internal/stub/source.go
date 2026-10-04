// Package stub 提供一个返回固定数据的 catalog.Source 实现。
//
// 它存在的理由很具体：ticket 03 要求「骨架 + 假数据就能写完，能起 HTTP 服务、
// 渲染出合法 RSS」，而真实数据源要等 ticket 02（签名实装的取舍）与
// ticket 06（番号解析规则）落地。有了它，HTTP 层、feed 渲染、
// 配置与路由今天就能被端到端地跑通和测试。
//
// 它**不**是生产实现，也不应该长期留在主路径上：main 只会在配置显式选择
// provider: stub 时才装配它。
package stub

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// Source 是固定数据的假实现。零值即可用。
type Source struct {
	// Err 非 nil 时所有方法都返回它，用来在测试里模拟上游故障。
	Err error
	// CodeWorks 是 Code 的返回值；为 nil 时用内置的样例数据。
	CodeWorks []catalog.Work
	// ActressWorks 是 Actress 的返回值；为 nil 时用内置的样例数据。
	ActressWorks []catalog.Work
	// Names 覆盖女优显示名。ActressName 与 ActressTags 共用它：
	// 前者拿不到名字就报错（feed 标题据此退回 id），后者用 id 兜底。
	// 不在表里的 id 对 ActressName 返回错误（模拟上游没有这个名字）。
	Names map[string]string
	// Collected 是 CollectedActresses 的返回值；为 nil 时用内置样例。
	// 它是一个完整的 catalog.Collection，因此需要构造截断场景的测试
	// 可以同时给出 Truncated / PagesFetched / MaxPages，而不是只给一部分。
	Collected *catalog.Collection
	// NoToken 为 true 时 CollectedActresses 与 WantToWatch 返回
	// catalog.ErrNoToken，用来验证上层对「没配 token」的处置。
	NoToken bool
	// Want 是 WantToWatch 的返回值；为 nil 时用内置样例。
	// 它也是一个完整的 catalog.WantList，因此要构造截断场景的测试
	// 可以同时给出 Truncated / PagesFetched / MaxPages。
	Want *catalog.WantList
	// Lists 是 CollectedLists 的返回值；为 nil 时用内置样例。
	Lists *catalog.ListCollection
	// ListWorks 是 List 的返回值；为 nil 时用内置样例。
	ListWorks []catalog.Work
	// BrowseWorks 是 Browse 的返回值；为 nil 时用内置样例。
	BrowseWorks []catalog.Work
	// TagVocabularies 覆盖 TagVocabulary 的返回值（按片库号）。
	//
	// 非 nil 时**只有**表里列出的片库可用，其余返回包装了 catalog.ErrBadRequest
	// 的错误 —— 这样测试可以精确控制某几个片库，而不必依赖内置 fixture。
	TagVocabularies map[int]catalog.TagVocabulary
	// ActressTagProfiles 覆盖 ActressTags 的返回值（按女优 id）。
	//
	// 与 TagVocabularies 同一套语义：非 nil 时**只有**表里列出的 id 可用，
	// 其余返回错误 —— 含混的取值来源正是最容易让测试假通过的地方。
	ActressTagProfiles map[string]catalog.ActressTags
}

// 样例数据刻意覆盖三种关键形态，好让端到端跑起来时一眼能看出规则生效：
// 无中文字幕、有中文字幕（选它的理由）、以及没有任何磁链候选（应被跳过）。
var (
	sampleNoSub = catalog.Work{
		Number:      "KV-328",
		Title:       "おしゃぶり予備校110 深月めい",
		ReleaseDate: "2026-08-28",
		Magnets: []catalog.Magnet{
			{Infohash: "0e8f4789bdcab713effc3a07d1309a776c867b3e", Name: "KV-328",
				SizeMiB: 3110, CNSub: false, HD: true, FilesCount: 2, CreatedAt: "09/27/2026"},
			{Infohash: "aa11bb22cc33dd44ee55ff6600112233445566aa", Name: "KV-328",
				SizeMiB: 4800, CNSub: false, HD: true, FilesCount: 3, CreatedAt: "09/28/2026"},
		},
	}
	sampleWithSub = catalog.Work{
		Number:      "REBDB-1047",
		Title:       "Meguri3 楽園の甘い囁き",
		ReleaseDate: "2026-08-20",
		Magnets: []catalog.Magnet{
			{Infohash: "bb22cc33dd44ee55ff6600112233445566778899", Name: "REBDB-1047",
				SizeMiB: 2200, CNSub: false, HD: true, FilesCount: 1, CreatedAt: "09/20/2026"},
			{Infohash: "cc33dd44ee55ff6600112233445566778899aabb", Name: "REBDB-1047",
				SizeMiB: 2600, CNSub: true, HD: true, FilesCount: 1, CreatedAt: "09/25/2026"},
		},
	}
	sampleNoMagnet = catalog.Work{
		Number:      "ABC-123",
		Title:       "まだ種が無い作品",
		ReleaseDate: "2026-09-01",
		Magnets:     nil,
	}
)

func (s *Source) codeWorks() []catalog.Work {
	if s.CodeWorks != nil {
		return s.CodeWorks
	}
	return []catalog.Work{sampleNoSub, sampleWithSub, sampleNoMagnet}
}

func (s *Source) actressWorks() []catalog.Work {
	if s.ActressWorks != nil {
		return s.ActressWorks
	}
	return []catalog.Work{sampleNoSub, sampleWithSub, sampleNoMagnet}
}

// Code 实现 catalog.Source。
func (s *Source) Code(_ context.Context, code string) ([]catalog.Work, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	if code == "" {
		return nil, fmt.Errorf("番号为空")
	}
	return s.codeWorks(), nil
}

// Actress 实现 catalog.Source。
func (s *Source) Actress(_ context.Context, id string, params url.Values) ([]catalog.Work, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	if id == "" {
		return nil, fmt.Errorf("女优 id 为空")
	}
	return s.actressWorks(), nil
}

// ActressName 实现 catalog.Source。
//
// 默认只给样例里的那个女优名字，其余返回错误 —— 这样既测得到「用名字」
// 又测得到「拿不到就退回 id」两条路径。
func (s *Source) ActressName(_ context.Context, id string) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	if name, ok := s.actressName(id); ok {
		return name, nil
	}
	return "", fmt.Errorf("女优 %s 没有名字", id)
}

// actressName 是内置的名字解析：Names 表优先，默认样例里只有 EvkJ 有名字。
//
// 它与 ActressName 的分歧只有一处：拿不到时 ActressName 报错（feed 标题据此退回 id），
// 而 ActressTags 用 id 兜底（页面手输的 id 也要能显示）。两处共用这一份解析，
// 免得「谁认识哪个 id」有两份可以各自跑偏的答案。
func (s *Source) actressName(id string) (string, bool) {
	if name, ok := s.Names[id]; ok && name != "" {
		return name, true
	}
	if s.Names == nil && id == "EvkJ" {
		return "河北彩花", true
	}
	return "", false
}

// CollectedActresses 实现 catalog.Source。
func (s *Source) CollectedActresses(_ context.Context) (catalog.Collection, error) {
	if s.NoToken {
		return catalog.Collection{}, fmt.Errorf("取收藏女优: %w", catalog.ErrNoToken)
	}
	if s.Err != nil {
		return catalog.Collection{}, s.Err
	}
	if s.Collected != nil {
		return *s.Collected, nil
	}
	// 样例里刻意**有一位男优**：页面「只看女优 / 全部演员」那个开关接的就是
	// /collected 的 gender 字段，离线 fixture 少了男优，那条路就没法在
	// 浏览器套件里被验到。名字/条数取自真实收藏的一小截（见 evidence）。
	return catalog.Collection{Actresses: []catalog.Actress{
		{ID: "EvkJ", Name: "河北彩花", VideosCount: 229, Gender: catalog.GenderFemale},
		{ID: "D2EdJ", Name: "花守夏歩", VideosCount: 179, Gender: catalog.GenderFemale},
		{ID: "PpQ0", Name: "森林原人", VideosCount: 3345, Gender: catalog.GenderMale},
	}}, nil
}

// WantToWatch 实现 catalog.Source。
//
// 默认样例刻意包含一部**没有磁链**的作品（sampleNoMagnet）：
// 「标了想看但还没有种」是这份清单的常态，而它正是 feed 描述里那个计数信号的
// 唯一来源 —— 样例里少一部，那条路径就没有端到端的兜底。
func (s *Source) WantToWatch(_ context.Context) (catalog.WantList, error) {
	if s.NoToken {
		return catalog.WantList{}, fmt.Errorf("取想看清单: %w", catalog.ErrNoToken)
	}
	if s.Err != nil {
		return catalog.WantList{}, s.Err
	}
	if s.Want != nil {
		return *s.Want, nil
	}
	return catalog.WantList{Works: []catalog.Work{sampleWithSub, sampleNoMagnet}}, nil
}

// CollectedLists 实现 catalog.Source。
func (s *Source) CollectedLists(_ context.Context) (catalog.ListCollection, error) {
	if s.NoToken {
		return catalog.ListCollection{}, fmt.Errorf("取清单列表: %w", catalog.ErrNoToken)
	}
	if s.Err != nil {
		return catalog.ListCollection{}, s.Err
	}
	if s.Lists != nil {
		return *s.Lists, nil
	}
	return catalog.ListCollection{Lists: []catalog.MovieList{
		{ID: "k4EVE4", Name: "遥控跳弹", MoviesCount: 1, Privacy: "open"},
		{ID: "R9r77", Name: "default", MoviesCount: 6, IsDefault: true, Privacy: "own"},
	}}, nil
}

// List 实现 catalog.Source。
//
// 它复用同一套样例作品：清单 feed 与女优 feed 在选磁链、pin 上完全同形，
// 因此样例也应当同形 —— 否则离线跑出来的清单 feed 看着就「和别的不一样」，
// 而那正是最容易漏掉差异的地方。
func (s *Source) List(_ context.Context, id string, _ url.Values) ([]catalog.Work, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("清单 id 为空: %w", catalog.ErrBadRequest)
	}
	if s.ListWorks != nil {
		return s.ListWorks, nil
	}
	return []catalog.Work{sampleWithSub}, nil
}

// Browse 实现 catalog.Source。
//
// 复用同一套样例作品：全站订阅与女优订阅在选磁链、pin 上完全同形。
func (s *Source) Browse(_ context.Context, _ int, _ catalog.BrowseSelector, _ url.Values) ([]catalog.Work, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	if s.BrowseWorks != nil {
		return s.BrowseWorks, nil
	}
	return []catalog.Work{sampleNoSub}, nil
}

// ListName 实现 catalog.Source。
//
// 与 ActressName 共用 Names 表：两者都是「拿 id 换一个好看标题」，
// 而且测试里同时用到两份订阅时，一张表比两张更不容易写错。
func (s *Source) ListName(_ context.Context, id string) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	if n, ok := s.Names[id]; ok {
		return n, nil
	}
	return "", fmt.Errorf("清单 %s 没有可用的名字", id)
}
