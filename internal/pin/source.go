package pin

import (
	"context"
	"log/slog"
	"net/url"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// Source 是在 catalog.Source 上钉住选择的装饰器。
//
// # 它怎么把「钉住的那条」交给下游
//
// 装饰器把 `Work.Magnets` **改写为单元素** —— 那条被钉住的磁链。
// 于是 feed.Build 仍按老规则取 `magnets[0]`，catalog.Select 一行不改，
// 纯函数与状态完全分开。这是 ticket 08 选定的接缝：
// 一张全局表（按 movie id 键）天然满足「跨 feed 共享同一个 pin」。
//
// # 写入时机
//
// 一次调用内先在内存里完成所有 pin 变更，收尾时**只落盘一次**（Flush）。
// 无变更则零写入 —— qBittorrent 的常态轮询不碰磁盘。
//
// # 失败语义
//
// 落盘失败是**致命**的：调用 OnFatal 让 main 退出，而不是带着
// 「内存已改、磁盘没改」的状态继续服务。那种状态导致的差异
// （重启后行为不一样）没人能解释，而退出会让 k8s/systemd 立刻可见。
type Source struct {
	inner  catalog.Source
	store  *Store
	policy Policy
	log    *slog.Logger

	// OnFatal 在落盘失败时被调用。它应当让进程退出。
	//
	// 用回调而不是直接 os.Exit：本包是可测的库代码，退出是 main 的决定。
	// 为 nil 时退化为只记日志。
	OnFatal func(error)
}

// New 包装一个 Source，把选中的磁链钉进 st。
//
// st 为 nil 时退化为「按纯函数选但不钉住」—— 装配层（main）不会这样用，
// 但一个库不该在那种情况下 panic。
//
// policy 为 nil 时用 TemporaryPolicy（ticket 08 的占位；09 会换成真实策略）。
func New(inner catalog.Source, st *Store, policy Policy) *Source {
	if policy == nil {
		policy = TemporaryPolicy{}
	}
	log := slog.Default()
	if st != nil && st.log != nil {
		log = st.log
	}
	return &Source{inner: inner, store: st, policy: policy, log: log}
}

var _ catalog.Source = (*Source)(nil)

// Code 实现 catalog.Source。
func (s *Source) Code(ctx context.Context, code string) ([]catalog.Work, error) {
	works, err := s.inner.Code(ctx, code)
	if err != nil {
		return nil, err
	}
	return s.pinAll(works)
}

// Actress 实现 catalog.Source。
func (s *Source) Actress(ctx context.Context, id string, params url.Values) ([]catalog.Work, error) {
	works, err := s.inner.Actress(ctx, id, params)
	if err != nil {
		return nil, err
	}
	return s.pinAll(works)
}

// ActressName 直接透传 —— 它不是 feed 内容，没有选择可言。
func (s *Source) ActressName(ctx context.Context, id string) (string, error) {
	return s.inner.ActressName(ctx, id)
}

// CollectedActresses 直接透传 —— 收藏列表不产生 guid。
func (s *Source) CollectedActresses(ctx context.Context) (catalog.Collection, error) {
	return s.inner.CollectedActresses(ctx)
}

// pinAll 对一批作品逐个钉住，并在收尾时**一次性**落盘。
func (s *Source) pinAll(works []catalog.Work) ([]catalog.Work, error) {
	var stats FlushStats
	for i := range works {
		added, switched := s.pinOne(&works[i])
		if added {
			stats.Added++
		}
		if switched {
			stats.Switched++
		}
	}
	// 没有 store（装配漏了）时 pinOne 已经退化为纯函数选择，这里就不必落盘。
	if s.store == nil {
		return works, nil
	}
	if err := s.store.Flush(stats); err != nil {
		// 致命：内存已改、磁盘没改。继续服务会让「重启后行为不同」变得无从解释。
		if s.OnFatal != nil {
			s.OnFatal(err)
		} else {
			s.log.Error("pin 落盘失败，无法继续安全服务", "err", err)
		}
		return nil, err
	}
	return works, nil
}

// pinOne 决定一部作品下发哪条磁链，把它改写成单元素，并更新 pin 表。
//
// 返回 (是否新增, 是否切换)。
func (s *Source) pinOne(w *catalog.Work) (added, switched bool) {
	if len(w.Magnets) == 0 {
		// 「有作品但尚无磁链」是常见状态，不该被钉住，也不该让调用失败。
		return false, false
	}
	if w.ID == "" || s.store == nil {
		// 没有 movie id 就没有可靠的键。退回纯函数 —— 绝不拿番号当键，
		// 番号不唯一，那会让两部不同作品互相钉死。
		if m, ok := s.policy.Desired(w.Magnets); ok {
			w.Magnets = []catalog.Magnet{m}
		}
		return false, false
	}

	desired, ok := s.policy.Desired(w.Magnets)
	if !ok {
		return false, false
	}

	pinned, hasPin := s.store.Get(w.ID)
	chosen, changed := s.policy.KeepOrSwitch(pinned, hasPin, w.Magnets, desired)

	// 只有当选择本身变化（或首次）时才写。
	if !hasPin || changed {
		s.store.Set(w.ID, recordOf(chosen))
		added = !hasPin
		switched = hasPin && changed
	}
	w.Magnets = []catalog.Magnet{chosen}
	return added, switched
}
