// Package health 跟踪「上游还能不能通」这件事，供 HTTP 探针读取。
//
// 它刻意不依赖任何其他内部包，也不自己发请求 —— 它只负责**记住**最近一次
// 检查的结果，让别人来问。把「检查」与「记录」分开的好处是：k8s 的探针、
// 后台定时器、以及真实请求失败时的回写，三者可以往同一个地方报告。
package health

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Status 是一次上游检查的结果。
type Status struct {
	// OK 表示最近一次检查是否成功。
	OK bool
	// CheckedAt 是最近一次检查发生的时刻。
	CheckedAt time.Time
	// Latency 是那次检查的耗时。
	Latency time.Duration
	// Action 是上游报告的错误名。
	//
	// 单独留着一个字段，是因为错误文本只能供人看，
	// 而告警规则需要能对一整个确定的名字做匹配。
	//
	// 它是**不透明**的：health 只负责原样记录与呈现，
	// 不认识任何一个具体的名字。名字的含义属于检查方。
	Action string
	// Guidance 是检查方给出的处置动作 —— 「下一步该做什么」。
	//
	// 它由 Checker 提供，health 只负责在失败日志里原样带出。
	// 之所以不由 health 编写：同样一句「去改代码」，对签名检查是对的，
	// 对一个语义完全不同的检查方（例如 SMTP、DNS）就是错的，
	// 而这条日志偏偏是排障时唯一会读的那条。
	Guidance string
	// Err 是失败原因，成功时为空。
	Err string
}

// Result 是 Checker 返回的检查结论。
type Result struct {
	OK bool
	// Action 与 Status.Action 同义，不透明。
	Action string
	// Guidance 是失败时可以写进日志的处置动作。成功时忽略。
	//
	// 检查方应当给一句**可操作**的话（去看哪个项目、查哪份文档、
	// 还是只需重试）。留空也可以，但那时日志里就不会有「下一步」——
	// 对「首次失败后长期安静」的场景，那可能是唯一一条日志。
	Guidance string
	Err      string
}

// Tracker 记录上游健康状态，并发安全。
//
// 之所以要区分「从未检查过」和「检查过且失败」：服务刚启动时探针还没跑过，
// 这时候既不应当报告不健康（会误判），也不应当报告健康（还没证据）。
type Tracker struct {
	mu    sync.RWMutex
	last  Status
	known bool
}

// NewTracker 构造一个「尚未检查过」的 Tracker。
func NewTracker() *Tracker { return &Tracker{} }

// Record 记下一次检查结果。
func (t *Tracker) Record(s Status) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = s
	t.known = true
}

// Snapshot 返回最近一次结果。第二个返回值表示是否**曾经**检查过。
func (t *Tracker) Snapshot() (Status, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.last, t.known
}

// Checker 是能被检查的上游。
//
// 它返回结构化的 Result 而不是 error，因为「失败的原因叫什么」和
// 「下一步该做什么」都是**领域知识**，应当由最懂上游的那个包
// 说出来，而不是让探针去猜错误文本、或者替它编一句可能错误的处置。
// 单独成接口则是为了让探针可以脱离真实上游测试。
type Checker interface {
	// Check 打一次最轻的请求，足以证明上游仍然可用。
	Check(ctx context.Context) Result
}

// Run 周期性地执行检查，直到 ctx 结束。
//
// interval 是一个函数而不是固定值，因此配置热重载能改变探针频率。
// 但要说清楚生效时机：**本轮结束后才重读**。
// 把 15m 改成 1m，最长要等满 15m 才生效；把它改小倒是很快见效。
// 这个延迟是刻意接受的 —— 为了「改配置立刻生效」而把唤醒粒度封顶，
// 意味着即使配的是 1 小时也要每几十秒醒一次，不值得。
//
// interval 返回 <= 0 时跳过本次检查（探针关闭）。
//
// 首次检查会**立即**跑，不等一个间隔 —— 否则服务启动后的头一个 interval 里，
// /readyz 会一直处于「尚未检查过」状态，k8s 拿不到结论。
func Run(ctx context.Context, c Checker, t *Tracker, interval func() time.Duration, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	check := func() {
		// 探针关着时不做任何请求 —— 包括启动时的那一次。
		// 这样「关掉探针」就是一个真正的空操作，而不会先偷偷打一发。
		if interval() <= 0 {
			return
		}
		start := time.Now()
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()

		r := c.Check(cctx)
		st := Status{
			CheckedAt: time.Now(),
			Latency:   time.Since(start),
			OK:        r.OK,
			Action:    r.Action,
			Guidance:  r.Guidance,
			Err:       r.Err,
		}

		// 只在状态翻转时打日志，避免每 15 分钟刷一行噪音。
		prev, known := t.Snapshot()
		t.Record(st)

		switch {
		case !known:
			// 首次检查：**成功保持静默**。
			//
			// （冷启动时打一句「恢复正常」是错的 —— 它没有「恢复」过任何东西，
			// 而且会让「正常签名保持静默」这条约定失效。）
			//
			// 但首次**失败**必须与后续失败说同样的话：服务一启动就是坏的场景很常见
			// （Prefix 在你重启前刚失效），而那时它是你唯一的一条日志。
			if !st.OK {
				logUpstreamFailure(log, st)
			}
		case prev.OK != st.OK:
			if st.OK {
				log.Info("上游检查恢复正常", "latency", st.Latency)
			} else {
				logUpstreamFailure(log, st)
			}
		}
	}

	check()

	// 用 Timer 而不是 Ticker：间隔每轮都要重新取，
	// 而 Ticker 的周期一旦建好就改不了。
	timer := time.NewTimer(nextInterval(interval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if d := interval(); d > 0 {
				check()
			}
			timer.Reset(nextInterval(interval))
		}
	}
}

// logUpstreamFailure 打一条上游失败日志，并**带上检查方给的处置动作**。
//
// 抽成一个函数而不是在两处各写一遍，是因为两处必须说同样的话：
// 首次检查失败与后续状态翻转失败，对读日志的人来说是同一件事。
// 曾经首次那条少了一句「下一步」，而它偏偏是「服务一启动就已经坏了」
// 这个常见场景下**唯一**的一条日志。
//
// 「下一步」的内容**不是** health 编的，而是原样透出 st.Guidance ——
// 这样换一个语义不同的检查方时，日志文案无需修改 health 包。
//
// 注意日志只在状态**翻转**时打一次（否则每 15 分钟刷一行噪音）。
// 「持续坏着」的可见性靠另外两条通道：/readyz 返回 503、
// 以及 k8s 里那个每小时跑一次的告警 CronJob。
func logUpstreamFailure(log *slog.Logger, st Status) {
	attrs := []any{"action", st.Action, "err", st.Err, "latency", st.Latency}
	if st.Guidance != "" {
		attrs = append(attrs, "下一步", st.Guidance)
	}
	log.Error("上游检查失败", attrs...)
}

// 探针关闭（interval <= 0）时用一个很长的退避值而不是 0 ——
// time.Timer 的 0 或负值会立即触发，变成忙循环。
// nextInterval 保证 Timer 拿到一个正数周期。
func nextInterval(interval func() time.Duration) time.Duration {
	const disabledPoll = time.Minute
	if d := interval(); d > 0 {
		return d
	}
	return disabledPoll
}
