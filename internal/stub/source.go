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
	// Names 覆盖 ActressName 的返回值。不在表里的 id 返回错误
	// （模拟上游没有这个名字），调用方据此退回 id。
	Names map[string]string
	// Collected 是 CollectedActresses 的返回值；为 nil 时用内置样例。
	// 它是一个完整的 catalog.Collection，因此需要构造截断场景的测试
	// 可以同时给出 Truncated / PagesFetched / MaxPages，而不是只给一部分。
	Collected *catalog.Collection
	// NoToken 为 true 时 CollectedActresses 返回 catalog.ErrNoToken，
	// 用来验证上层对「没配 token」的处置。
	NoToken bool
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
	if name, ok := s.Names[id]; ok && name != "" {
		return name, nil
	}
	if s.Names == nil && id == "EvkJ" {
		return "河北彩花", nil
	}
	return "", fmt.Errorf("女优 %s 没有名字", id)
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
	return catalog.Collection{Actresses: []catalog.Actress{
		{ID: "EvkJ", Name: "河北彩花", VideosCount: 229},
		{ID: "xyz1", Name: "Another Name", VideosCount: 57},
	}}, nil
}
