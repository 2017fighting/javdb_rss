package health

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestTrackerDistinguishesNeverCheckedFromChecked(t *testing.T) {
	tr := NewTracker()

	// 刚启动：既不能说健康（还没证据），也不能说不健康（会误判）。
	if _, known := tr.Snapshot(); known {
		t.Fatal("新 Tracker 不该报告「已检查过」")
	}

	tr.Record(Status{OK: false, CheckedAt: time.Now(), Err: "boom", Action: "InvalidSignature"})
	st, known := tr.Snapshot()
	if !known {
		t.Fatal("记录后应当报告「已检查过」")
	}
	if st.OK {
		t.Error("应当记录为失败")
	}
	if st.Action != "InvalidSignature" {
		t.Errorf("Action = %q", st.Action)
	}
}

// fakeChecker 记录调用次数并返回可变的结论。
type fakeChecker struct {
	calls  atomic.Int64
	result atomic.Pointer[Result]
}

func newFakeChecker(r Result) *fakeChecker {
	f := &fakeChecker{}
	f.result.Store(&r)
	return f
}

func (f *fakeChecker) Check(context.Context) Result {
	f.calls.Add(1)
	return *f.result.Load()
}

// TestRunChecksImmediately 确认首次检查不等一个间隔。
//
// 否则服务启动后的头一个 interval 里 /readyz 会一直处于「尚未检查过」，
// k8s 在滚动发布时会拿不到结论。
func TestRunChecksImmediately(t *testing.T) {
	f := newFakeChecker(Result{OK: true})
	tr := NewTracker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, f, tr, func() time.Duration { return time.Hour }, quietLogger())

	deadline := time.After(2 * time.Second)
	for {
		if _, known := tr.Snapshot(); known {
			break
		}
		select {
		case <-deadline:
			t.Fatal("启动后没有立即检查")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if got := f.calls.Load(); got != 1 {
		t.Errorf("首次调用次数 = %d, want 1", got)
	}
}

// TestRunDisabledDoesNothing 确认「关掉探针」是真正的空操作 ——
// 包括启动时那一次，而不是偷偷先打一发。
func TestRunDisabledDoesNothing(t *testing.T) {
	f := newFakeChecker(Result{OK: true})
	tr := NewTracker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, f, tr, func() time.Duration { return 0 }, quietLogger())

	// 给足机会去犯错。
	time.Sleep(150 * time.Millisecond)
	if got := f.calls.Load(); got != 0 {
		t.Errorf("探针关闭时不该发请求，但调用了 %d 次", got)
	}
	if _, known := tr.Snapshot(); known {
		t.Error("探针关闭时不该产生检查记录")
	}
}

// TestRunRereadsIntervalEachCycle 确认间隔是每轮重读的，因此热重载能改变探针频率。
//
// 注意它验证的是「下一轮用新值」，不是「立即生效」—— 后者是刻意的设计取舍，
// 见 Run 的注释。因此这里用短间隔来测，而不是拿一个 1 小时的值去等。
func TestRunRereadsIntervalEachCycle(t *testing.T) {
	f := newFakeChecker(Result{OK: true})
	tr := NewTracker()

	// 第一轮故意用很长的间隔，改小后应当很快看到下一次检查。
	var interval atomic.Int64
	interval.Store(int64(50 * time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, f, tr, func() time.Duration { return time.Duration(interval.Load()) }, quietLogger())

	// 等首次检查完成（它不走间隔）。
	waitForCalls(t, f, 1)

	// 缩小间隔，缩短下一轮的等待。
	interval.Store(int64(5 * time.Millisecond))

	// 第二轮应当在「旧间隔」结束后到来，但用的是新值之后就一直很快了。
	waitForCalls(t, f, 3)
}

// waitForCalls 等检查次数达到 n，超时则失败。
func waitForCalls(t *testing.T, f *fakeChecker, n int64) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for f.calls.Load() < n {
		select {
		case <-deadline:
			t.Fatalf("等待 %d 次检查超时（当前 %d 次）", n, f.calls.Load())
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// TestRunStopsOnContextCancel 确认退出时不泄漏 goroutine（也顺带钉住探针会停）。
func TestRunStopsOnContextCancel(t *testing.T) {
	f := newFakeChecker(Result{OK: true})
	tr := NewTracker()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, f, tr, func() time.Duration { return 5 * time.Millisecond }, quietLogger())
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Run 没有返回")
	}

	// 确认真的停了。
	n := f.calls.Load()
	time.Sleep(60 * time.Millisecond)
	if f.calls.Load() != n {
		t.Error("ctx 取消后仍在检查")
	}
}

// TestFirstCheckFailureCarriesActionableHint 钉住一个我引入过的回归。
//
// 为了让冷启动「成功时保持静默」，我把首次检查单独分了一支；
// 结果首次**失败**那条也走了简化分支 —— 只记 action/err/latency，
// **没有那句「下一步」**。而后续轮次因为 prev.OK == st.OK == false
// 再也不会打日志。
//
// 于是「服务一启动时签名就已经失效」这个常见场景（Prefix 在你重启前刚失效）
// 会得到：唯一一条日志，且不带任何处置指引。
func TestFirstCheckFailureCarriesActionableHint(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	f := newFakeChecker(Result{OK: false, Action: "InvalidSignature", Err: "無效的簽名"})
	tr := NewTracker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, f, tr, func() time.Duration { return time.Hour }, log)

	deadline := time.After(2 * time.Second)
	for {
		if _, known := tr.Snapshot(); known {
			break
		}
		select {
		case <-deadline:
			t.Fatal("首次检查没发生")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// 给日志落盘一点时间（Run 里 Record 之后才打日志）。
	time.Sleep(50 * time.Millisecond)

	out := buf.String()
	if !strings.Contains(out, "下一步") {
		t.Errorf("首次失败的日志必须带上处置动作 —— 它可能是唯一的一条：\n%s", out)
	}
	if !strings.Contains(out, "InvalidSignature") {
		t.Errorf("日志应当带上 action：\n%s", out)
	}
}

// TestFirstCheckSuccessIsSilent 确认「成功保持静默」这条没被上一条测试改坏。
func TestFirstCheckSuccessIsSilent(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	f := newFakeChecker(Result{OK: true})
	tr := NewTracker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, f, tr, func() time.Duration { return time.Hour }, log)

	deadline := time.After(2 * time.Second)
	for {
		if _, known := tr.Snapshot(); known {
			break
		}
		select {
		case <-deadline:
			t.Fatal("首次检查没发生")
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond)

	if out := buf.String(); strings.Contains(out, "恢复正常") {
		t.Errorf("冷启动成功不该打「恢复正常」—— 它没有恢复过任何东西：\n%s", out)
	}
}
