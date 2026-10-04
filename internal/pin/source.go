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
// policy 为 nil 时用 DefaultPolicy（ticket 09 的切换语义）。
func New(inner catalog.Source, st *Store, policy Policy) *Source {
	if policy == nil {
		policy = DefaultPolicy{}
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

// WantToWatch 实现 catalog.Source。
//
// 它**要**钉住 —— 与番号/女优 feed 同一个理由：这条 feed 会被 qBittorrent
// 当作订阅周期轮询，而「选中哪条磁链」决定 guid。不钉的话，上游候选一变
// 就会出现一条新 guid，客户端把同一部片再下一份。
//
// 尚无磁链的作品（Magnets 为空）在这里无害地穿过：pinOne 没什么可选，
// 也就不钉任何东西；等它有了磁链，那一次调用才会钉上。
func (s *Source) WantToWatch(ctx context.Context) (catalog.WantList, error) {
	list, err := s.inner.WantToWatch(ctx)
	if err != nil {
		return catalog.WantList{}, err
	}
	works, err := s.pinAll(list.Works)
	if err != nil {
		return catalog.WantList{}, err
	}
	list.Works = works
	return list, nil
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
	if s.store == nil || w.ID == "" {
		// 没有 movie id 就没有可靠的键。退回纯函数 —— 绝不拿番号当键，
		// 番号不唯一，那会让两部不同作品互相钉死。
		if m, ok := s.policy.Desired(w.Magnets); ok {
			w.Magnets = []catalog.Magnet{m}
		}
		return false, false
	}

	if len(w.Magnets) == 0 {
		// 「有作品但尚无磁链」对没钉住的作品是常见状态，原样跳过。
		// 但对**已钉住**的作品，上游把磁链全删了同样是「pin 消失」——
		// ticket 09 要的是继续返回快照（可能死链）并记 WARN，
		// 否则这个作品会静默地从 feed 里消失。
		if pinned, ok := s.store.Get(w.ID); ok {
			s.warnPinGone(w.ID, pinned.Infohash, 0)
			w.Magnets = []catalog.Magnet{magnetOf(pinned)}
		}
		return false, false
	}

	desired, ok := s.policy.Desired(w.Magnets)
	if !ok {
		return false, false
	}

	pinned, hasPin := s.store.Get(w.ID)
	chosen, changed := s.policy.KeepOrSwitch(pinned, hasPin, desired)

	// 只有当选择本身变化（或首次）时才写。
	if !hasPin || changed {
		s.store.Set(w.ID, recordOf(chosen))
		added = !hasPin
		switched = hasPin && changed
	}

	// 让「切换」本身可观察（ticket 09）：逐条 INFO，而不是只看落盘的聚合计数。
	// 这里读的 w.Magnets 还是**上游**给的候选，改写放在日志之后。
	switch {
	case switched:
		s.log.Info("pin 切换",
			"作品", w.ID,
			"旧磁链", pinned.Infohash, "新磁链", chosen.Infohash,
			"旧日期", pinned.CreatedAt, "新日期", chosen.CreatedAt)
	case hasPin && !containsInfohash(w.Magnets, pinned.Infohash):
		// pin 指向上游消失：按 ticket 09 继续沿用快照（guid 稳定优先），
		// 但必须留下痕迹，否则「发了一条死链」又一次不可见。
		s.warnPinGone(w.ID, pinned.Infohash, len(w.Magnets))
	}

	w.Magnets = []catalog.Magnet{chosen}
	return added, switched
}

// warnPinGone 记录一条「pin 指向上游消失、仍沿用快照」的警告。
func (s *Source) warnPinGone(id, infohash string, candidateCount int) {
	s.log.Warn("pin 不在当前上游候选里，继续沿用快照（可能是死链）",
		"作品", id, "磁链", infohash, "候选数", candidateCount)
}

// containsInfohash 判断候选里是否还有某条 infohash。
func containsInfohash(cands []catalog.Magnet, hash string) bool {
	for _, m := range cands {
		if m.Infohash == hash {
			return true
		}
	}
	return false
}
