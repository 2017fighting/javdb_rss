package mteam

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// Loop 是后台的**判定循环**：它把「这部作品该走哪个来源」查出来并落盘。
//
// # 为什么判定不能留在请求路径里
//
// `/rss/want.xml` 的一次渲染已经是 ~20 秒（24 页清单 + 222 次磁链请求），
// 而 qBittorrent 在 **30 秒无字节传输**时就掐断请求 —— 而 feed 是渲染完才写第一个字节。
// 一次判定要「搜索 + 下载种子」，234 部就是分钟级。
//
// 所以形状只能是：**判定在后台做，feed 只读结果**。
//
// # 三条约束（都写在票面上）
//
//   - **限速**：种子下载 ≤ 100 个/小时（站点自标的建议值）。它是**唯一的瓶颈** ——
//     搜索配额是 1000/24h，富余得多。
//   - **可续跑**：判定表**就是**进度。重启后从「没有判定的作品」继续，不重头来。
//   - **不退出进程**：读清单失败（没 token / 上游坏）⇒ 跳过本轮 + WARN。
//     退出会把一次上游抖动变成一次重启循环，而它并没有那么严重。
type Loop struct {
	// src 是**内层**的上游（拿清单用）。它不能是包着本层的那个 Source ——
	// 那会让循环去读「已经扣住未判定作品」的清单，于是永远看不到它们。
	src    catalog.Source
	client *Client
	store  *Store
	pins   Pins
	log    *slog.Logger

	// interval 由配置给、SIGHUP 可改。≤ 0 表示关闭（照 health.Run 的语义：
	// 关掉是**真正的空操作**，连启动时那一次都不做）。
	interval func() time.Duration

	// now 可注入，让限速窗口在测试里可确定。
	now func() time.Time

	// downloads 是种子下载的闸门（≤ 100 个/小时）。
	downloads *rateGate

	// OnFatal 在判定表落盘失败时被调用。它应当让进程退出。
	OnFatal func(error)
}

// NewLoop 造一个判定循环。
//
// maxDownloadsPerHour ≤ 0 时用 DefaultDownloadsPerHour。
func NewLoop(src catalog.Source, client *Client, store *Store, pins Pins,
	interval func() time.Duration, maxDownloadsPerHour int, log *slog.Logger) *Loop {
	if log == nil {
		log = slog.Default()
	}
	if maxDownloadsPerHour <= 0 {
		maxDownloadsPerHour = DefaultDownloadsPerHour
	}
	now := time.Now
	return &Loop{
		src: src, client: client, store: store, pins: pins, log: log,
		interval:  interval,
		now:       now,
		downloads: &rateGate{max: maxDownloadsPerHour, window: time.Hour, now: now},
	}
}

// DefaultDownloadsPerHour 是站点自标的建议值（见 notes/mteam-api.md §3）。
//
// 它是**唯一的瓶颈**：搜索 1000 次/24 小时，而下载只有 100 个/小时 ——
// 一个 234 部的清单若多数命中，回填要两个多小时。那是渐进的，不是故障。
const DefaultDownloadsPerHour = 100

// Run 常驻跑，直到 ctx 结束。
//
// 形状照 internal/health 的 Run：先把配置读进来（interval 是 func，
// 因此 SIGHUP 改了配置它下一轮就生效），然后按周期跑。
func (l *Loop) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			d := l.interval()
			if d <= 0 {
				// 关着的时候**什么都不做**，包括这一轮 —— 连清单都不去读。
				// 否则「关掉」会先偷偷打一发上游请求（health.Run 的同一条判断）。
				timer.Reset(time.Minute)
				continue
			}
			l.Once(ctx)
			timer.Reset(d)
		}
	}
}

// Once 跑一轮：读清单 → 对没有判定的作品查 M-Team → 落盘。
//
// 单独抽出来是为了让测试能精确地跑「一轮」，而不必跟时钟赛跑。
func (l *Loop) Once(ctx context.Context) {
	list, err := l.src.WantToWatch(ctx)
	if err != nil {
		// 跳过本轮，**不退出进程**：一次上游抖动不该变成一次重启循环。
		l.log.WarnContext(ctx, "判定循环读不到「想看」清单，跳过本轮", "err", err)
		return
	}

	var decided, hits, pending int
	for i := range list.Works {
		w := &list.Works[i]
		if ctx.Err() != nil {
			break
		}
		// 首发冻结：下发过的不动，连问都不问。
		if l.pins != nil {
			if _, ok := l.pins.Get(w.ID); ok {
				continue
			}
		}
		// 可续跑：判定表就是进度。
		if _, ok := l.store.Get(w.ID); ok {
			continue
		}
		pending++
		outcome, err := l.decide(ctx, w)
		if err != nil {
			// decide 已经把失败写进表了（或按致命路径退出），这里只记账。
			l.log.WarnContext(ctx, "判定失败，本轮到此为止", "作品", w.Number, "err", err)
			break
		}
		decided++
		if outcome == OutcomeHit {
			hits++
		}
	}

	if err := l.store.Flush(); err != nil {
		if l.OnFatal != nil {
			l.OnFatal(err)
		} else {
			l.log.Error("判定表落盘失败，无法继续安全服务", "err", err)
		}
		return
	}

	// 只在**有进展或还有欠账**时打一条 INFO —— 常态下（全部已判定）这一轮是空的，
	// 而每 15 分钟刷一条「本轮 0 部」正是让人学会忽略日志的那种噪音。
	if decided > 0 || pending > 0 {
		l.log.InfoContext(ctx, "判定循环跑完一轮",
			"清单", len(list.Works), "新判定", decided, "命中", hits,
			"待判定", pending-decided, "判定表总数", l.store.Len())
	}
}

// decide 对一部作品做一次判定并落盘。返回结果（命中 / 负结果）。
//
// ⚠️ 它返回错误只有一种情形：**种子下载的闸门满了**（那是「稍后再来」，
// 不是失败）。真正的查询失败会被记进判定表并返回 nil —— 因为那是一条**结果**。
func (l *Loop) decide(ctx context.Context, w *catalog.Work) (Outcome, error) {
	hits, err := l.client.Search(ctx, w.Number)
	if err != nil {
		l.recordFailure(ctx, w, err)
		return OutcomeFailed, nil
	}

	matches := matchCode(hits, w.Number)
	if len(matches) == 0 {
		// 负结果：**永久**。措辞刻意不叫「失败」—— 那是另一回事，处置完全相反。
		l.store.Put(w.ID, Verdict{Outcome: OutcomeMiss, At: l.now()})
		l.log.DebugContext(ctx, "M-Team 上没有这部作品（负结果，永久）",
			"作品", w.Number, "候选", len(hits))
		return OutcomeMiss, nil
	}

	chosen := pickBest(matches)

	// ⚠️ 闸门在这里，而不是在循环外面：搜索是便宜的（1000/24h），
	// 下载才是瓶颈（100/小时）。闸门关了就把这部作品留到下一轮 ——
	// **不写判定**，因为「还没取到种子」不是任何一种结果。
	if !l.downloads.allow() {
		return "", errDownloadGate
	}

	data, err := l.client.Download(ctx, chosen.ID)
	if err != nil {
		// ⚠️ **也要记失败**，不能只打日志就走。不记的话，那部作品下一轮会
		// 重新搜索、重新下载 —— 而下载配额是 100 个/小时。
		l.recordFailure(ctx, w, err)
		l.log.WarnContext(ctx, "取 M-Team 的种子失败", "作品", w.Number, "种子", chosen.ID, "err", err)
		return OutcomeFailed, nil
	}
	infohash, err := Infohash(data)
	if err != nil {
		// 这一条值得 WARN 而不是 Debug：它是**M-Team 的契约变了**的信号
		// （种子形状不再是我们认得的），而不是某一次网络抖动。
		l.recordFailure(ctx, w, err)
		l.log.WarnContext(ctx, "M-Team 的种子认不出来", "作品", w.Number, "种子", chosen.ID, "err", err)
		return OutcomeFailed, nil
	}
	if err := l.store.SaveTorrent(infohash, data); err != nil {
		l.recordFailure(ctx, w, err)
		l.log.ErrorContext(ctx, "存 M-Team 的种子失败", "作品", w.Number, "infohash", infohash, "err", err)
		return OutcomeFailed, nil
	}

	l.store.Put(w.ID, Verdict{
		Outcome: OutcomeHit,
		Chosen: Candidate{
			ID:        chosen.ID,
			Infohash:  infohash,
			Name:      chosen.Name,
			SizeMiB:   int(chosen.SizeBytes / (1 << 20)),
			CNSub:     chosen.CNSub,
			CreatedAt: chosen.CreatedAt,
		},
		At: l.now(),
	})
	l.log.InfoContext(ctx, "M-Team 命中", "作品", w.Number, "种子", chosen.ID,
		"infohash", infohash, "中字", chosen.CNSub, "候选", len(matches))
	return OutcomeHit, nil
}

// errDownloadGate 表示「种子下载的闸门满了，这部作品留到下一轮」。
//
// 它是**唯一**一种 decide 会返回的错误 —— 因为它不是任何一种判定结果，
// 不能写进表（写进去就等于「永久不再试」，而它明明只是等一会儿）。
var errDownloadGate = errors.New("种子下载已达本小时上限")

// recordFailure 记下一次**没查成**，并按类别决定日志的措辞。
//
// 它落盘，因此不会每轮重问 —— 但那条记录与「负结果」是**两回事**：
// 负结果是「查过、没有」，失败是「没查成」。前者是判决，后者不是，
// 因此只有后者会被「重置」或 SIGHUP 再试一次（见 Store.DropFailures）。
func (l *Loop) recordFailure(ctx context.Context, w *catalog.Work, err error) {
	kind := FailureOther
	if errors.Is(err, ErrCredential) {
		kind = FailureCredential
	}
	l.store.Put(w.ID, Verdict{
		Outcome:     OutcomeFailed,
		Failure:     err.Error(),
		FailureKind: kind,
		At:          l.now(),
	})

	// 两类失败的日志**必须不同**：一类要人去换 key，另一类等一等就好。
	// 混成一条，告警就会把人指向错的方向。
	if kind == FailureCredential {
		l.log.ErrorContext(ctx, "M-Team 凭据失效 —— 重试无用，去控制台换一个存取令牌",
			"作品", w.Number, "err", err)
		return
	}
	l.log.WarnContext(ctx, "M-Team 查询失败（不自动重试）", "作品", w.Number, "err", err)
}

// rateGate 是「每 window 最多 max 次」的闸门。
//
// 手写而不是引 golang.org/x/time/rate：本仓库对依赖的态度是「为它写过一张 ADR」
// （见 docs/adr/0003），而这里要的只是「一小时不超过 100 次」——
// 一个环形时间戳队列就够了，不值得再引一棵依赖树。
//
// 它是**刻意保守**的：窗口是滑动的一小时，而不是「整点清零」——
// 后者会在整点前后各放行 100 个，也就是一小时内 200 个。
type rateGate struct {
	max    int
	window time.Duration
	now    func() time.Time

	// at 是窗口内已经发生的时刻，按时间递增（append 天然有序）。
	at []time.Time
}

// allow 报告现在能不能再来一次；能就记下这次。
func (g *rateGate) allow() bool {
	t := g.now()
	cut := t.Add(-g.window)

	// 丢掉滑出窗口的那些（前缀，因此一次切片就够了）。
	i := 0
	for i < len(g.at) && g.at[i].Before(cut) {
		i++
	}
	g.at = append(g.at[:0], g.at[i:]...)

	if len(g.at) >= g.max {
		return false
	}
	g.at = append(g.at, t)
	return true
}
