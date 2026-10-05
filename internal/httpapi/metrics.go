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
//	镜像 tag（home-ops 里钉的）   1.0.0    ← 声明要跑什么，由 Renovate 改
//	/version 与这里报的            v1.0.0   ← 实际在跑什么（tag 原文）
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
