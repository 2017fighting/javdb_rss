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
