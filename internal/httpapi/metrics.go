package httpapi

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/health"
)

// /metrics 是同一份「上游健康」快照的**第三个**消费者。
//
// 三者读的是同一个 health.Tracker，只是对不同的读者说话：
//
//	/readyz            给编排器 —— 「把实例从端点里摘掉」
//	/healthz/upstream  给**人**   —— 「要改代码，不是重试」
//	/metrics           给**告警规则** —— 「它在 VM 里，可以被查询与取历史」
//
// 指标**不新增任何事实**：序列与 /healthz/upstream 的 JSON 字段一一对应。
// 这也是它刻意写成「投影」而不是「另一套状态」的原因 —— 两处各自维护一份
// 「上游还通不通」，迟早会漂，而漂的表现是告警与探针说不一样的话。

// upstreamCollector 把 Tracker 的最近一次结论投影成指标。
//
// ⚠️ 为什么签名判定在这里（httpapi）而不在 health 包里：`health` 刻意
// **不认识任何一个具体的错误名**（见 `health.Status.Action` 的注释），
// 而「哪些 action 算签名类」是 appapi 的领域知识。这个包是唯一同时知道
// 「上游状态」与「哪个 action 意味着要改代码」的地方 ——
// `/healthz/upstream` 的 `signature_broken` 字段本来就是在这里算的。
type upstreamCollector struct {
	tracker *health.Tracker

	checked         *prometheus.Desc
	ok              *prometheus.Desc
	signatureBroken *prometheus.Desc
	lastCheck       *prometheus.Desc
	latency         *prometheus.Desc
}

func newUpstreamCollector(t *health.Tracker) *upstreamCollector {
	const ns = "javdb_rss_upstream_"
	return &upstreamCollector{
		tracker: t,
		checked: prometheus.NewDesc(ns+"checked",
			"是否已经跑过至少一次上游检查：1 = 检查过，0 = 还没检查过（刚启动）。",
			nil, nil),
		ok: prometheus.NewDesc(ns+"ok",
			"最近一次上游检查是否成功：1 = 成功。⚠️ 「从未检查过」也报 0，因此不要用 ok==0 告警。",
			nil, nil),
		signatureBroken: prometheus.NewDesc(ns+"signature_broken",
			"最近一次失败是否属于「签名常量与服务端不兼容」：1 = 需要改代码，重试无用；普通网络故障为 0。",
			nil, nil),
		lastCheck: prometheus.NewDesc(ns+"last_check_timestamp_seconds",
			"最近一次上游检查的时刻（Unix 秒）。从未检查过时**不出现**这条序列。",
			nil, nil),
		latency: prometheus.NewDesc(ns+"check_latency_seconds",
			"最近一次上游检查的耗时（秒）。从未检查过时不出现。",
			nil, nil),
	}
}

func (c *upstreamCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.checked
	ch <- c.ok
	ch <- c.signatureBroken
	ch <- c.lastCheck
	ch <- c.latency
}

func (c *upstreamCollector) Collect(ch chan<- prometheus.Metric) {
	st, known := c.tracker.Snapshot()

	ch <- prometheus.MustNewConstMetric(c.checked, prometheus.GaugeValue, gaugeValue(known))
	// 「没检查过」报告 ok=0 而不是 1：把没有证据说成健康是错的
	// （与 /healthz/upstream 的 checked 字段同一个判断）。
	// checked 因此必须**单独成一条** —— 少了它，ok=0 的两种含义
	// （「刚启动」与「真的坏了」）在指标层就分不开了。
	ch <- prometheus.MustNewConstMetric(c.ok, prometheus.GaugeValue, gaugeValue(known && st.OK))
	ch <- prometheus.MustNewConstMetric(c.signatureBroken, prometheus.GaugeValue,
		gaugeValue(known && !st.OK && appapi.IsSignatureAction(st.Action)))

	if !known {
		// 时间戳与耗时只在真的检查过之后才有意义。报 0 会让「1970 年检查过」
		// 看起来像一条证据 —— 而 staleness 类的规则正是拿这个字段算的。
		return
	}
	ch <- prometheus.MustNewConstMetric(c.lastCheck, prometheus.GaugeValue,
		float64(st.CheckedAt.Unix()))
	ch <- prometheus.MustNewConstMetric(c.latency, prometheus.GaugeValue, st.Latency.Seconds())
}

// buildInfoCollector 报告**正在运行**的是哪个版本。
//
// 它是 `/version` 的机读副本（同一个事实，只是换个读者）。存在的理由是
// 版本在这套部署里有三种形状，而它们说的不是同一件事：
//
//	镜像 tag（部署侧钉的那个）   1.2.3    ← 声明要跑什么，由 Renovate 改
//	/version 与这里报的          v1.2.3   ← 实际在跑什么（tag 原文）
//
// 1.2.3 只是**形状**示例，不指某个具体版本 —— 免得这段注释变成又一处要跟着发版改的地方。
//
// 两种形状是有意的，理由见 docs/adr/0001。
//
// ⚠️ 它**不**推翻 ticket 15 那句「不做版本可见性」：那条讲的是「最新版本是多少」
// 这种**别人的**状态（本服务是拉取方，没有自更新需求），而这是**自己的**运行态。
type buildInfoCollector struct {
	desc *prometheus.Desc
}

func newBuildInfoCollector(version string) *buildInfoCollector {
	return &buildInfoCollector{desc: prometheus.NewDesc(
		"javdb_rss_build_info",
		"正在运行的版本（与 /version 同一个事实，tag 原文形状）。",
		nil, prometheus.Labels{"version": version},
	)}
}

func (c *buildInfoCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *buildInfoCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, 1)
}

// metricsHandler 返回 /metrics 的处理器。
//
// ⚠️ 用每个 Server **自己的** registry，而不是全局的 DefaultRegisterer。
//
// 理由是失败方式：全局注册表是进程级的，同一个测试二进制里构造第二个 Server 时，
// 重复注册会失败 —— 而忽略那个错误就等于让第二个 Server 的 /metrics **静默地**
// 出第一个 Server 的序列（本仓库的测试恰好就是那样反复构造 Server 的）。
// 这里宁可自己把「默认就有的那批」注册一遍，也不要一处会说谎的全局状态。
// （与默认 registry 同一批：Go 运行时 + 进程收集器。）
//
// go_* / process_* 一并端出来是**有意的**：本服务是「挂上就不管」的那一类，
// goroutine、内存、GC、fd 正是白拿的排查材料。
func (s *Server) metricsHandler() http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		newBuildInfoCollector(Version),
	)
	// provider=stub 时没有上游要报告 —— 与 /readyz 的语义一致
	// （没有可坏的依赖，就不该凭空造一条「上游正常」的证据）。
	if s.upstream != nil {
		reg.MustRegister(newUpstreamCollector(s.upstream))
	}
	// 第二个来源的状态（没配它就没有这些序列 —— 与上面 upstream 同一条判断：
	// 没有可报的东西，就不该凭空造一条「一切正常」的证据）。
	if s.mteamStatus != nil {
		reg.MustRegister(newMTeamCollector(s.mteamStatus))
	}
	// 用 HandlerFor 而不是 Handler()：后者绑在全局 registry 上（见上）。
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// gaugeValue 把布尔变成 0/1。Prometheus 没有布尔类型，而这两个取值
// 在序列里就是「假/真」的全集。
func gaugeValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// mteamCollector 把第二个来源（M-Team）的状态投影成指标。
//
// 它**不新增任何事实**：序列与 `/healthz/upstream` 的 `mteam` 段、以及后台循环
// 自己的读数一一对应。两处各自维护一份「判定推进到哪了」，迟早会漂，
// 而漂的表现是告警与人对不上话。
type mteamCollector struct {
	status MTeamStatus

	judged       *prometheus.Desc
	verdicts     *prometheus.Desc
	lastDecision *prometheus.Desc
	pending      *prometheus.Desc
	stalled      *prometheus.Desc
	credBroken   *prometheus.Desc
}

func newMTeamCollector(st MTeamStatus) *mteamCollector {
	const ns = "javdb_rss_mteam_"
	return &mteamCollector{
		status: st,
		judged: prometheus.NewDesc(ns+"judged",
			"是否已经有过至少一条判定：1 = 有。⚠️ 「一条判定都没有」也报 verdicts 全 0，"+
				"因此不要只看 verdicts 判断「刚启用」还是「判定过但没命中」。",
			nil, nil),
		verdicts: prometheus.NewDesc(ns+"verdicts",
			"判定表里各结果的条数。outcome = hit / miss / failed。",
			[]string{"outcome"}, nil),
		lastDecision: prometheus.NewDesc(ns+"last_decision_timestamp_seconds",
			"最近一条判定的时刻（Unix 秒）。**一条判定都没有时不出现这条序列** —— "+
				"报 0 等于说「1970 年判定过」，而 staleness 类规则正是拿它算的。",
			nil, nil),
		pending: prometheus.NewDesc(ns+"pending_works",
			"最近一轮看到的、还没判定的作品数。循环从未跑过时不出现。",
			nil, nil),
		stalled: prometheus.NewDesc(ns+"stalled_cycles",
			"连续多少轮没有推进（还有欠账却一条判定都没多）。"+
				"⚠️ 它是「看起来在工作、实际没在工作」的信号 —— 周期照跑、日志照打。",
			nil, nil),
		credBroken: prometheus.NewDesc(ns+"credential_broken",
			"存取令牌是否已失效：1 = 重试无用，去控制台换一个。与 upstream 的 signature_broken 同一条判断。",
			nil, nil),
	}
}

func (c *mteamCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.judged
	ch <- c.verdicts
	ch <- c.lastDecision
	ch <- c.pending
	ch <- c.stalled
	ch <- c.credBroken
}

func (c *mteamCollector) Collect(ch chan<- prometheus.Metric) {
	p := c.status.Progress()
	st := p.Stats

	judged := !st.LastAt.IsZero()
	ch <- prometheus.MustNewConstMetric(c.judged, prometheus.GaugeValue, gaugeValue(judged))
	// 三个 outcome 都报，哪怕是 0：告警规则要能对单个 outcome 写表达式，
	// 而「序列时有时无」会让那种表达式在有些实例上静默地匹配不到东西。
	ch <- prometheus.MustNewConstMetric(c.verdicts, prometheus.GaugeValue, float64(st.Hit), "hit")
	ch <- prometheus.MustNewConstMetric(c.verdicts, prometheus.GaugeValue, float64(st.Miss), "miss")
	ch <- prometheus.MustNewConstMetric(c.verdicts, prometheus.GaugeValue, float64(st.Failed), "failed")

	if judged {
		ch <- prometheus.MustNewConstMetric(c.lastDecision, prometheus.GaugeValue, float64(st.LastAt.Unix()))
	}
	// 进度只在循环真的跑过之后才有意义 —— 与上面 judged 同一条判断。
	if p.Cycles > 0 {
		ch <- prometheus.MustNewConstMetric(c.pending, prometheus.GaugeValue, float64(p.Pending))
	}
	ch <- prometheus.MustNewConstMetric(c.stalled, prometheus.GaugeValue, float64(p.Stalled))

	_, _, broken := c.status.CredentialFailure()
	ch <- prometheus.MustNewConstMetric(c.credBroken, prometheus.GaugeValue, gaugeValue(broken))
}
