// Package dedupe 在 catalog.Source 之上合并并发的相同请求。
//
// 它是「Source 是唯一外部边界」这条设计的第一个实际收益：
// 加这一层完全不需要改动 httpapi、feed 或 appapi ——
// 只是换一个对象塞进装配处。
//
// 与缓存的关键区别：**它不存任何东西。**
// 只合并「此刻正在飞行中」的相同请求，一旦那次请求返回，记录立刻消失。
// 因此它不会返回陈旧数据、不占内存、重启没有任何影响。
package dedupe

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/singleflight"
)

// Source 是合并并发相同请求的 catalog.Source 装饰器。
type Source struct {
	inner catalog.Source
	group singleflight.Group
	// shared 统计有多少次调用共享了别人的结果，供观测与测试使用。
	shared atomic.Int64
}

// New 包装一个 Source。
func New(inner catalog.Source) *Source { return &Source{inner: inner} }

var _ catalog.Source = (*Source)(nil)

// Shared 返回被合并掉的调用次数。
func (s *Source) Shared() int64 { return s.shared.Load() }

// Code 实现 catalog.Source。
func (s *Source) Code(ctx context.Context, code string) ([]catalog.Work, error) {
	// key 用归一化后的番号：KV-328 与 kv-328 是同一个订阅，应当合并。
	key := "code:" + strings.ToUpper(strings.TrimSpace(code))
	return s.do(key, func() ([]catalog.Work, error) {
		return s.inner.Code(ctx, code)
	})
}

// Actress 实现 catalog.Source。
//
// key 必须涵盖**全部影响结果的输入**，否则参数不同的请求会被错误地合并 ——
// 那会让用户拿到别人那份订阅的内容。params 里已经包含了 pages，
// 因此不必单独处理它。
func (s *Source) Actress(ctx context.Context, id string, params url.Values) ([]catalog.Work, error) {
	key := "actress:" + id + "?" + canonicalParams(params)
	return s.do(key, func() ([]catalog.Work, error) {
		return s.inner.Actress(ctx, id, params)
	})
}

// ActressName 实现 catalog.Source。
//
// 它没有参数，因此按 id 合并即可。值得合并的理由：一个女优 feed 每次渲染
// 都会问一次名字，而 qBittorrent 可能同时拉同一个 feed 的多个连接。
func (s *Source) ActressName(ctx context.Context, id string) (string, error) {
	v, err, sharedCall := s.group.Do("name:"+id, func() (any, error) {
		return s.inner.ActressName(ctx, id)
	})
	if sharedCall {
		s.shared.Add(1)
	}
	if err != nil {
		return "", err
	}
	name, _ := v.(string)
	return name, nil
}

// ActressTags 实现 catalog.Source。
//
// 按 id 合并，理由与 ActressName 相同：页面切女优时会问一次，
// 而这个页面两个标签区可能同时请求同一位女优。它**不**与 ActressName 共享 key ——
// 两者返回的东西不同（singleflight 只存一个值），但底层打的是同一个上游端点
// （见 appapi 的 fetchActress）。
func (s *Source) ActressTags(ctx context.Context, id string) (catalog.ActressTags, error) {
	v, err, sharedCall := s.group.Do("actress_tags:"+id, func() (any, error) {
		return s.inner.ActressTags(ctx, id)
	})
	if sharedCall {
		s.shared.Add(1)
	}
	if err != nil {
		return catalog.ActressTags{}, err
	}
	got, _ := v.(catalog.ActressTags)
	return got, nil
}

// CollectedActresses 实现 catalog.Source。
//
// 它没有参数，因此所有并发调用合并成一次 —— 这是最划算的一处合并：
// 收藏列表要翻好几页，而它只有在你打开 /collected 时才会被请求。
func (s *Source) CollectedActresses(ctx context.Context) (catalog.Collection, error) {
	v, err, sharedCall := s.group.Do("collected", func() (any, error) {
		return s.inner.CollectedActresses(ctx)
	})
	if sharedCall {
		s.shared.Add(1)
	}
	if err != nil {
		return catalog.Collection{}, err
	}
	got, _ := v.(catalog.Collection)
	return got, nil
}

// List 实现 catalog.Source。
//
// key 与 Actress 同形（实体 + 全部参数）：参数不同的请求合并到一起
// 会让用户拿到别人那份订阅的内容。
func (s *Source) List(ctx context.Context, id string, params url.Values) ([]catalog.Work, error) {
	key := "list:" + id + "?" + canonicalParams(params)
	return s.do(key, func() ([]catalog.Work, error) {
		return s.inner.List(ctx, id, params)
	})
}

// Browse 实现 catalog.Source。
//
// key 要把 **zone 与五个筛选维度全都算进去**：全站订阅之间只差一个年份或
// 一个标签，合并错了就是拿别人那份内容给用户。
func (s *Source) Browse(ctx context.Context, zone int, sel catalog.BrowseSelector, params url.Values) ([]catalog.Work, error) {
	key := fmt.Sprintf("browse:%d:%s|%s|%s|%s|%s?%s",
		zone, sel.Main, sel.Tags, sel.Year, sel.Duration, sel.Month, canonicalParams(params))
	return s.do(key, func() ([]catalog.Work, error) {
		return s.inner.Browse(ctx, zone, sel, params)
	})
}

// ListName 实现 catalog.Source。
//
// 与 ActressName 同一个理由：一条清单 feed 每次渲染都会问一次名字，
// 而 qBittorrent 可能同时拉同一个 feed 的多个连接。
func (s *Source) ListName(ctx context.Context, id string) (string, error) {
	v, err, sharedCall := s.group.Do("listname:"+id, func() (any, error) {
		return s.inner.ListName(ctx, id)
	})
	if sharedCall {
		s.shared.Add(1)
	}
	if err != nil {
		return "", err
	}
	name, _ := v.(string)
	return name, nil
}

// CollectedLists 实现 catalog.Source。
//
// 与 CollectedActresses 同一个理由合并（无参数、要翻页、只在发现端点被请求）。
func (s *Source) CollectedLists(ctx context.Context) (catalog.ListCollection, error) {
	v, err, sharedCall := s.group.Do("collected_lists", func() (any, error) {
		return s.inner.CollectedLists(ctx)
	})
	if sharedCall {
		s.shared.Add(1)
	}
	if err != nil {
		return catalog.ListCollection{}, err
	}
	got, _ := v.(catalog.ListCollection)
	return got, nil
}

// TagVocabulary 实现 catalog.Source。
//
// key 里必须带 zone：四个片库的词表**各不相同**，合并错了就是把另一个库的
// 词表交给用户 —— 而那正是上游对非法 type 的静默回落造成的后果，
// 只是换了个入口。
func (s *Source) TagVocabulary(ctx context.Context, zone int) (catalog.TagVocabulary, error) {
	v, err, sharedCall := s.group.Do(fmt.Sprintf("tags:%d", zone), func() (any, error) {
		return s.inner.TagVocabulary(ctx, zone)
	})
	if sharedCall {
		s.shared.Add(1)
	}
	if err != nil {
		return catalog.TagVocabulary{}, err
	}
	got, _ := v.(catalog.TagVocabulary)
	return got, nil
}

// WantToWatch 实现 catalog.Source。
//
// 它没有参数，因此所有并发调用合并成一次 —— 与收藏列表同一个理由，而且更划算：
// 这条清单要翻页 + 逐部拉磁链，而它每 15 分钟就被 qBittorrent 拉一次。
func (s *Source) WantToWatch(ctx context.Context) (catalog.WantList, error) {
	v, err, sharedCall := s.group.Do("want", func() (any, error) {
		return s.inner.WantToWatch(ctx)
	})
	if sharedCall {
		s.shared.Add(1)
	}
	if err != nil {
		return catalog.WantList{}, err
	}
	got, _ := v.(catalog.WantList)
	return got, nil
}

// do 是两条路由共用的收尾，顺便统计命中情况。
func (s *Source) do(key string, fn func() ([]catalog.Work, error)) ([]catalog.Work, error) {
	v, err, sharedCall := s.group.Do(key, func() (any, error) {
		return fn()
	})
	if sharedCall {
		s.shared.Add(1)
	}
	if err != nil {
		return nil, err
	}
	// 断言只可能成功（只有本文件往这些 key 里放 []catalog.Work），
	// 但显式处理而不是直接 panic —— 一个类型错误不该让服务崩掉。
	works, ok := v.([]catalog.Work)
	if !ok {
		return nil, nil
	}
	// 返回值在调用者之间**共享**，因此调用方不得修改这个切片。
	// feed.Build 只读它；httpapi 的 since 过滤会另建一个新切片。
	return works, nil
}

// canonicalParams 把透传参数编成稳定的字符串，用于构造 key。
//
// url.Values.Encode() 按键排序，因此同一个参数集合总得到同一个字符串 ——
// 这正是合并所要求的确定性。缺少这一点会让「同样的请求」算不出同样的 key，
// singleflight 就永远不会命中。
func canonicalParams(p url.Values) string {
	if len(p) == 0 {
		return ""
	}
	return p.Encode()
}
