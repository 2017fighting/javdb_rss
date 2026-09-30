package singleflight

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDoMergesConcurrentCalls 是核心行为：同一 key 的并发调用只执行一次。
func TestDoMergesConcurrentCalls(t *testing.T) {
	var g Group
	var calls atomic.Int64

	const n = 50
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]string, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // 尽量让所有 goroutine 同时冲进去
			v, err, _ := g.Do("same", func() (any, error) {
				calls.Add(1)
				// 等其余 n-1 个调用者全部加入，再返回 —— 而不是睡一个固定时长撑窗口。
				// 后者只是把失败概率降低；前者是同步。
				waitForWaiters(t, &g, "same", n-1)
				return "result", nil
			})
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			results[i], _ = v.(string)
		}(i)
	}

	close(start)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("函数被执行了 %d 次，应当合并成 1 次", got)
	}
	for i, r := range results {
		if r != "result" {
			t.Errorf("结果 %d = %q，全部调用者都应当拿到同一个结果", i, r)
		}
	}
}

// TestDoSeparatesDifferentKeys 确认不同 key 不会被错误合并 ——
// 合并错了意味着一个用户拿到别人的订阅内容。
func TestDoSeparatesDifferentKeys(t *testing.T) {
	var g Group
	var calls atomic.Int64

	keys := []string{"a", "b", "c"}
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			_, _, _ = g.Do(k, func() (any, error) {
				calls.Add(1)
				return k, nil
			})
		}(k)
	}
	wg.Wait()

	if got := calls.Load(); got != 3 {
		t.Errorf("三个不同 key 应当各执行一次，实际 %d 次", got)
	}
}

// TestDoDoesNotCache 钉住它与缓存的根本区别：
// 第一次返回之后，同样的 key **会再次执行**。
//
// 这正是我们选它而不是缓存的理由 —— 没有陈旧数据的可能。
func TestDoDoesNotCache(t *testing.T) {
	var g Group
	var calls atomic.Int64

	for i := 0; i < 3; i++ {
		_, _, _ = g.Do("k", func() (any, error) {
			calls.Add(1)
			return i, nil
		})
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("串行的三次调用应当各执行一次，实际 %d 次 —— 说明它变成了缓存", got)
	}
}

// TestDoPropagatesError 确认错误被分发给所有等待者。
func TestDoPropagatesError(t *testing.T) {
	var g Group
	wantErr := errors.New("boom")

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err, _ := g.Do("k", func() (any, error) {
				// 等其余 n-1 个调用者加入。这样测的才是「错误被分发给等待者」，
				// 而不是「各跑各的、各自报错」—— 没有这一步，
				// 迟到的 goroutine 会自己成为执行者，测试就不再验证共享语义了。
				waitForWaiters(t, &g, "k", n-1)
				return nil, wantErr
			})
			if !errors.Is(err, wantErr) {
				t.Errorf("err = %v, want %v", err, wantErr)
			}
		}()
	}
	wg.Wait()
}

// TestDoReleasesKeyAfterCompletion 确认记录被清掉，不会永远占着内存。
func TestDoReleasesKeyAfterCompletion(t *testing.T) {
	var g Group
	_, _, _ = g.Do("k", func() (any, error) { return 1, nil })

	g.mu.Lock()
	n := len(g.m)
	g.mu.Unlock()
	if n != 0 {
		t.Errorf("完成之后表里还剩 %d 项，内存会一直涨", n)
	}
}

// TestDoRecoversFromPanic 确认 panic 不会让等待者永久挂起。
//
// 没有这个保护的话，一个编程错误就会变成整个服务的死锁 ——
// 所有等待 wg.Wait() 的请求永远不会返回。
func TestDoRecoversFromPanic(t *testing.T) {
	var g Group

	done := make(chan error, 1)
	go func() {
		_, err, _ := g.Do("k", func() (any, error) {
			panic("故意炸")
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("panic 应当被转成 error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("panic 后调用没有返回 —— 等待者会永久挂起")
	}

	// key 必须被释放，否则这个 key 永远不会再被执行。
	g.mu.Lock()
	n := len(g.m)
	g.mu.Unlock()
	if n != 0 {
		t.Errorf("panic 后表里还剩 %d 项", n)
	}
}

// TestDoReportsShared 确认第三个返回值能区分「执行者」与「共享者」。
//
// ⚠️ 这个测试写错过两次，错误都值得记下来。
//
// 错误一（**死锁**）：最初在主 goroutine 里直接调第二个 Do，然后在它**之后**
// close(release)。但第二个 Do 会阻塞在 c.wg.Wait() 上等第一个调用完成，
// 而第一个调用正阻塞在 <-release —— release 永远关不上，整个包死锁。
// 教训：任何会等待另一个调用的调用，都不能和那个调用的放行语句待在同一个 goroutine。
//
// 错误二（**时序脆弱**）：修死锁时改用了 time.Sleep(50ms) 等第二个 goroutine 就位。
// 那不是同步，是猜测 —— CI 调度延迟或高负载下 50ms 可能不够，
// 于是第二个调用会变成执行者，测试随机失败。
//
// 现在的做法：**等条件，不等时间**。`call.dups` 在共享者加入时自增，
// 因此这里可以轮询一个确定的状态而不是猜一个时长。
func TestDoReportsShared(t *testing.T) {
	var g Group
	release := make(chan struct{})
	started := make(chan struct{})

	// 第一个调用：登记进表后卡在 fn 里，成为一个「飞行中」的调用。
	firstShared := make(chan bool, 1)
	go func() {
		_, _, shared := g.Do("k", func() (any, error) {
			close(started)
			<-release
			return "v", nil
		})
		firstShared <- shared
	}()

	<-started // 确认第一个调用已在飞行中

	// 第二个调用：应当加入第一个而不是执行自己的 fn。
	secondShared := make(chan bool, 1)
	go func() {
		_, _, shared := g.Do("k", func() (any, error) {
			t.Error("第二个调用不该执行 fn —— 它应当共享第一个的结果")
			return "", nil
		})
		secondShared <- shared
	}()

	// 等第二个调用**确实已加入**（而不是猜一个时长），再放行第一个。
	waitForWaiters(t, &g, "k", 1)
	close(release)

	if shared := <-firstShared; shared {
		t.Error("执行者不该被标记为 shared")
	}
	if shared := <-secondShared; !shared {
		t.Error("第二个调用应当被标记为 shared")
	}
}

// waitForWaiters 等 key 上出现至少 n 个共享者。超时则让测试失败。
//
// 它是**轮询一个条件**而不是睡眠 —— 后者只能降低概率，前者能消除不确定性。
func waitForWaiters(t *testing.T, g *Group, key string, n int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		g.mu.Lock()
		c := g.m[key]
		g.mu.Unlock()

		if c != nil && c.dups.Load() >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 key=%q 上出现 %d 个共享者超时", key, n)
		}
		runtime.Gosched()
	}
}
