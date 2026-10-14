package mteam

import (
	"context"
	"errors"
	"log/slog"
	"sync"
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

	// mu 保护下面那几个「最近一轮」的读数（/metrics 会并发读）。
	mu sync.Mutex
	// cycles 是跑完的轮数（0 = 从未跑过）。
	cycles int
	// lastPending 是最近一轮看到的、还没判定的作品数。
	lastPending int
	// stalled 是「连续多少轮没有推进」。
	stalled int
	// lastCycleAt 是最近一轮结束的时刻。
	lastCycleAt time.Time
	// credSeen/credBroken 记住上一次对外报的凭据状态，用于「只在翻转时打日志」。
	credSeen, credBroken bool

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

// Progress 是判定循环对外要说的那几句话 —— /metrics 与运维读它。
//
// 它是一次**快照**，不是一个活的对象：调用方拿到的是一份拷贝，之后不会变。
type Progress struct {
	// Cycles 是跑完的轮数。0 表示**从未跑过** —— 那时下面几个读数都没有意义。
	Cycles int
	// Pending 是最近一轮看到的、还没判定的作品数。
	Pending int
	// Stalled 是「连续多少轮没有推进」。
	//
	// ⚠️ 它是「看起来在工作、实际没在工作」那类失败的信号：周期照跑、日志照打，
	// 而判定表一条都没多。上游一直失败、限速把自己锁死、清单读不到 —— 都会长这样。
	Stalled int
	// LastCycleAt 是最近一轮结束的时刻。
	LastCycleAt time.Time
	// Stats 是判定表当前的计数快照。
	Stats Stats
}

// Progress 返回最近一轮的读数（并发安全）。
func (l *Loop) Progress() Progress {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Progress{
		Cycles:      l.cycles,
		Pending:     l.lastPending,
		Stalled:     l.stalled,
		LastCycleAt: l.lastCycleAt,
		Stats:       l.store.Stats(),
	}
}

// CredentialFailure 转发给判定表 —— 循环与表说的是同一件事。
func (l *Loop) CredentialFailure() (string, time.Time, bool) {
	return l.store.CredentialFailure()
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

	// 先把「这一轮有几部需要判定」数出来。
	//
	// ⚠️ 不能拿「循环里数到几部」当余量：闸门满时会中途 break，于是那个数
	// 只能表达 0 或 1，与清单里真正还剩多少毫无关系（评审抓出来的）。
	todo := 0
	for i := range list.Works {
		if !l.skip(list.Works[i]) {
			todo++
		}
	}

	var decided, hits int
	for i := range list.Works {
		w := &list.Works[i]
		if ctx.Err() != nil {
			break
		}
		if l.skip(*w) {
			continue
		}
		outcome, err := l.decide(ctx, w)
		if err != nil {
			// decide **唯一**的错误是「闸门满了」—— 那是「稍后再来」，不是失败。
			// 剩下的留到下一轮；措辞与级别都要跟得上这个语义：
			// 常态的流控回退不该报成 WARN，那会让人去查一个不存在的故障。
			l.log.InfoContext(ctx, "种子下载已达本小时上限，剩下的留到下一轮",
				"本轮已完成", decided, "还剩", todo-decided, "err", err)
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

	// 记下这一轮的读数，供 /metrics 读。
	l.mu.Lock()
	l.cycles++
	l.lastPending = todo - decided
	l.lastCycleAt = l.now()
	if decided > 0 {
		l.stalled = 0
	} else if l.lastPending > 0 {
		// 还有欠账却一条都没推进 ⇒ 这一轮是**停滞**的。
		l.stalled++
	}
	stalled, cycles, pending := l.stalled, l.cycles, l.lastPending
	l.mu.Unlock()

	// 只在**有进展或还有欠账**时打一条 INFO —— 常态下（全部已判定）这一轮是空的，
	// 而每 15 分钟刷一条「本轮 0 部」正是让人学会忽略日志的那种噪音。
	if decided > 0 || pending > 0 {
		l.log.InfoContext(ctx, "判定循环跑完一轮",
			"清单", len(list.Works), "新判定", decided, "命中", hits,
			"待判定", pending, "判定表总数", l.store.Len(), "轮次", cycles)
	}
	if stalled > 0 && pending > 0 {
		// 停滞**必须**看得见：周期照跑、日志照打，而判定表一条都没多 ——
		// 那是「看起来在工作、实际没在工作」。
		l.log.WarnContext(ctx, "判定循环连续若干轮没有推进",
			"连续轮次", stalled, "待判定", pending)
	}
}

// skip 报告这部作品这一轮**不用管**：已经下发过（首发冻结），或者已经有判定（可续跑）。
func (l *Loop) skip(w catalog.Work) bool {
	if l.pins != nil {
		if _, ok := l.pins.Get(w.ID); ok {
			return true
		}
	}
	_, ok := l.store.Get(w.ID)
	return ok
}

// decide 对一部作品做一次判定并落盘。返回结果（命中 / 负结果）。
//
// ⚠️ 它返回错误只有两种情形，而两种都**不是失败**：
//
//	闸门满了（errDownloadGate）—— 稍后再来；
//	凭据失效（ErrCredential）   —— 整条路都走不通，停轮（重试无用）。
//
// 其余失败会被记进判定表并返回 nil —— 因为那是一条**结果**（只是不是判决）。
func (l *Loop) decide(ctx context.Context, w *catalog.Work) (Outcome, error) {
	hits, err := l.client.Search(ctx, w.Number)
	if err != nil {
		l.recordFailure(ctx, w, err)
		if errors.Is(err, ErrCredential) {
			// ⚠️ 凭据失效**立刻停轮**：继续下去只会把整张清单一条条撞同一个错 ——
			// 既白烧搜索配额，又会把日志刷满（而「只在翻转时打」那条规矩
			// 正是为了不让日志变成噪音）。
			return OutcomeFailed, err
		}
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
		// ⚠️ **只在状态翻转时打**（照 health 的做法）。
		// 一个坏 key 会命中清单里的每一部作品，逐条打 ERROR 会把日志变成噪音 ——
		// 而「换 key」这件事只需要说一次。
		l.mu.Lock()
		flip := !l.credSeen || !l.credBroken
		l.credSeen, l.credBroken = true, true
		l.mu.Unlock()
		if flip {
			l.log.ErrorContext(ctx, "M-Team 凭据失效 —— 重试无用，去控制台换一个存取令牌",
				"作品", w.Number, "err", err)
		}
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
