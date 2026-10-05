package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/health"
	"github.com/2017fighting/javdb_rss/internal/stub"
)

// scrape 抓一次 /metrics。
func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := do(t, h, "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200（正文=%s）", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// metricValue 取一条序列的值；第二个返回值表示它**在不在**正文里。
//
// 「在不在」与「值是多少」是两件要分开断言的事：本组指标里有一条
// （`last_check_timestamp_seconds`）刻意在「从未检查过」时不出现 ——
// 报 0 会让「1970 年检查过」看起来像一条证据。
//
// 刻意不引 `expfmt` 来解析：测试要断言的是**我们自己的判断**，而不是
// exposition 格式的细节（格式由 client_golang 负责）。
func metricValue(t *testing.T, body, series string) (float64, bool) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rest, ok := strings.CutPrefix(line, series)
		if !ok {
			continue
		}
		// 序列名后面要么是空白（无标签），要么是 `{`（有标签）。
		// 少了这一步，`..._ok` 会误匹配上 `..._okfoo` 这类别的序列。
		if rest != "" && !strings.HasPrefix(rest, "{") && !strings.HasPrefix(rest, " ") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			t.Fatalf("序列行没有值: %q", line)
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(line[i+1:]), 64)
		if err != nil {
			t.Fatalf("解析 %q 的值失败: %v", line, err)
		}
		return v, true
	}
	return 0, false
}

// mustMetric 断言一条序列存在并返回它的值。
func mustMetric(t *testing.T, body, series string) float64 {
	t.Helper()
	v, ok := metricValue(t, body, series)
	if !ok {
		t.Fatalf("正文里没有序列 %s", series)
	}
	return v
}

// TestMetricsBeforeFirstCheckNeverClaimsHealth 是本组测试里最要紧的一条。
//
// 「从未检查过」既不是健康也不是不健康，而指标层必须把这件事**说出来**：
// 把没有证据说成 ok=1 是错的，而光看 ok=0 又分不开「刚启动」与「真的坏了」。
// 这就是 `checked` 必须单独成一条的理由 —— 少了它，告警规则只能靠猜。
func TestMetricsBeforeFirstCheckNeverClaimsHealth(t *testing.T) {
	h, _ := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})
	body := scrape(t, h)

	if v := mustMetric(t, body, "javdb_rss_upstream_checked"); v != 0 {
		t.Errorf("checked = %v, want 0 —— 还没跑过检查", v)
	}
	if v := mustMetric(t, body, "javdb_rss_upstream_ok"); v != 0 {
		t.Errorf("ok = %v, want 0 —— 没有证据就不许说健康", v)
	}
	if v := mustMetric(t, body, "javdb_rss_upstream_signature_broken"); v != 0 {
		t.Errorf("signature_broken = %v, want 0", v)
	}
	if _, ok := metricValue(t, body, "javdb_rss_upstream_last_check_timestamp_seconds"); ok {
		t.Error("从未检查过时不该有 last_check_timestamp_seconds（报 0 等于说 1970 年检查过）")
	}
	if _, ok := metricValue(t, body, "javdb_rss_upstream_check_latency_seconds"); ok {
		t.Error("从未检查过时不该有 check_latency_seconds")
	}
}

// TestMetricsSignatureBrokenOnlyForSignatureActions：两种签名失败形态都算
// 「要改代码」，普通故障不算 —— 误报的代价是有人半夜被叫起来改代码。
func TestMetricsSignatureBrokenOnlyForSignatureActions(t *testing.T) {
	for _, action := range []string{"InvalidSignature", "ParameterInvalid"} {
		t.Run(action, func(t *testing.T) {
			h, tr := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})
			tr.Record(health.Status{OK: false, CheckedAt: time.Now(), Action: action, Err: "x"})

			if v := mustMetric(t, scrape(t, h), "javdb_rss_upstream_signature_broken"); v != 1 {
				t.Errorf("action=%s 时 signature_broken = %v, want 1", action, v)
			}
		})
	}
}

func TestMetricsTransientFailureIsNotSignatureBroken(t *testing.T) {
	h, tr := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})
	tr.Record(health.Status{OK: false, CheckedAt: time.Now(),
		Err: "请求 /api/v1/startup: dial tcp: connection refused"})
	body := scrape(t, h)

	if v := mustMetric(t, body, "javdb_rss_upstream_signature_broken"); v != 0 {
		t.Errorf("普通网络故障时 signature_broken = %v, want 0", v)
	}
	// 但它是**检查过**且失败的：checked=1 与 ok=0 一起才说得出「真的坏了」。
	if v := mustMetric(t, body, "javdb_rss_upstream_checked"); v != 1 {
		t.Errorf("检查过了 checked = %v, want 1", v)
	}
	if v := mustMetric(t, body, "javdb_rss_upstream_ok"); v != 0 {
		t.Errorf("失败时 ok = %v, want 0", v)
	}
}

// TestMetricsCarriesCheckTimeAndLatency 把两个「只在检查过之后才有意义」的
// 字段钉住：成功的检查也必须留下时刻与耗时（staleness 规则与 Grafana 都用它）。
func TestMetricsCarriesCheckTimeAndLatency(t *testing.T) {
	h, tr := newTestServerWithHealth(t, "provider: stub\n", &stub.Source{})
	checkedAt := time.Date(2026, 10, 5, 4, 4, 35, 0, time.UTC)
	tr.Record(health.Status{OK: true, CheckedAt: checkedAt, Latency: 444 * time.Millisecond})
	body := scrape(t, h)

	if v := mustMetric(t, body, "javdb_rss_upstream_ok"); v != 1 {
		t.Errorf("成功时 ok = %v, want 1", v)
	}
	if v := mustMetric(t, body, "javdb_rss_upstream_last_check_timestamp_seconds"); v != float64(checkedAt.Unix()) {
		t.Errorf("last_check = %v, want %v", v, checkedAt.Unix())
	}
	if v := mustMetric(t, body, "javdb_rss_upstream_check_latency_seconds"); math.Abs(v-0.444) > 1e-9 {
		t.Errorf("latency = %v, want 0.444", v)
	}
}

// TestMetricsWithoutUpstreamHasNoUpstreamSeries：provider=stub 时没有可坏的
// 依赖，就不该凭空造一条「上游正常」的证据（与 /readyz 恒就绪同一个判断）。
func TestMetricsWithoutUpstreamHasNoUpstreamSeries(t *testing.T) {
	// 这个 helper 不挂 Tracker —— 与 provider=stub 的装配一致。
	body := scrape(t, newTestServer(t, "provider: stub\n", &stub.Source{}))

	if _, ok := metricValue(t, body, "javdb_rss_upstream_checked"); ok {
		t.Error("没有上游时不该出现 upstream 系列")
	}
	// 但服务自己的东西仍在：build_info 与上游无关。
	if _, ok := metricValue(t, body, "javdb_rss_build_info"); !ok {
		t.Error("build_info 与上游无关，应当在")
	}
}

// TestMetricsBuildInfoAgreesWithVersion 把两个端点钉在同一个事实上。
//
// 两处报不一样的版本比不报更坏：Grafana 里那张图会被当成「现在跑的是什么」，
// 而 /version 是同一个问题的另一个答案。
func TestMetricsBuildInfoAgreesWithVersion(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	fromVersion := decodeVersion(t, do(t, h, "/version").Body.Bytes()).Version
	want := `javdb_rss_build_info{version="` + fromVersion + `"} 1`
	if body := scrape(t, h); !strings.Contains(body, want) {
		t.Errorf("正文里没有 %q —— build_info 必须与 /version 同源", want)
	}
}

// TestMetricsExposesRuntimeCollectors 钉住「默认 registry 那批收集器也在这」。
//
// 这是有意的（见 metricsHandler 的注释）：本服务是「挂上就不管」的那一类，
// goroutine、内存、GC 正是白拿的排查材料。要它们就得自己注册 ——
// 我们用的是每个 Server 自己的 registry，不是全局那个。
func TestMetricsExposesRuntimeCollectors(t *testing.T) {
	body := scrape(t, newTestServer(t, "provider: stub\n", &stub.Source{}))

	for _, series := range []string{"go_goroutines", "process_resident_memory_bytes"} {
		if _, ok := metricValue(t, body, series); !ok {
			t.Errorf("正文里没有 %s —— 运行时收集器没注册进来", series)
		}
	}
}
