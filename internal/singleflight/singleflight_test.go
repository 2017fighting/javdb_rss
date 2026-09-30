package singleflight

import (
	"errors"
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
				time.Sleep(20 * time.Millisecond) // 保证窗口足够宽
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
				time.Sleep(10 * time.Millisecond)
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
// ⚠️ 这个测试写错过一次，错误值得记下来：
//
// 最初的写法是在主 goroutine 里直接调第二个 Do（想用它拿到 shared），
// 然后在它**之后** close(release)。但第二个 Do 会阻塞在 c.wg.Wait() 上
// 等第一个调用完成，而第一个调用正阻塞在 <-release ——
// 于是 release 永远关不上，整个包死锁。
//
// 教训：**任何会等待另一个调用的调用，都不能和那个调用的放行语句
// 待在同一个 goroutine 里。** 所以这里第二个调用也必须另起 goroutine。
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

	// 给第二个调用一点时间真正进入等待，再放行第一个。
	// 用 sleep 而不是更好的同步：Go 没有提供「观察者已阻塞在 Wait 上」的钩子，
	// 而这里的目标只是让第二个调用**有机会**加入；即使它晚了一步，
	// 上面的 t.Error 也会把它暴露出来。
	time.Sleep(50 * time.Millisecond)
	close(release)

	if shared := <-firstShared; shared {
		t.Error("执行者不该被标记为 shared")
	}
	if shared := <-secondShared; !shared {
		t.Error("第二个调用应当被标记为 shared")
	}
}
