// Package singleflight 把并发的相同工作合并成一次执行。
//
// 它是 Go 标准库之外最常见的一个小工具（golang.org/x/sync/singleflight 的同形物），
// 这里自己实现而不是引入依赖，是因为本项目的依赖只有 yaml.v3 ——
// 而这个逻辑只有几十行，且能独立测住。
//
// 典型用途：多个客户端同时请求同一份上游数据时，只打一次上游，其余等待并共享结果。
package singleflight

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Group 合并相同 key 的并发调用。零值即可用。
//
// 不同 key 之间完全独立；同一个 key 在第一次调用返回之前的所有调用都会等待它。
type Group struct {
	mu sync.Mutex
	m  map[string]*call
}

type call struct {
	wg  sync.WaitGroup
	val any
	err error
	// dups 记下有多少个调用者共享了这次执行（即有多少人加入了而不是自己执行）。
	//
	// 它不参与 Do 的语义，只供观测：测试用它**确定性地**判断
	// 「另一個调用者已经加入」，从而不必用 time.Sleep 盲等。
	// （Go 官方的 x/sync/singleflight 出于同样理由也维护了这样的计数器。）
	dups atomic.Int64
}

// Do 执行 fn，并把结果共享给同一 key 上的并发调用者。
//
// 返回值：
//
//	v       fn 的返回值（通过 any 传递，调用方需自行断言）
//	err     fn 的错误
//	shared  本次调用是否**共享了别人的结果**（false 表示它就是那个真正执行的人）
//
// shared 是为测试与观测留的：生产代码通常忽略它。
//
// # 两条调用方约定
//
//  1. **返回值是共享的，调用方不得修改它。** 多个调用者会拿到同一个对象
//     （例如同一个切片）。要修改请先复制。
//  2. **只有第一次调用的 ctx 生效。** 如果它被取消，所有等待者一起失败。
//     对只读 GET 而言这可以接受：失败者下一轮轮询会重试。
func (g *Group) Do(key string, fn func() (any, error)) (v any, err error, shared bool) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call)
	}
	if c, ok := g.m[key]; ok {
		c.dups.Add(1)
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err, true
	}
	c := new(call)
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	g.doCall(c, key, fn)
	return c.val, c.err, false
}

// doCall 执行真正的调用，并保证无论如何都从表里摘掉、唤醒等待者。
//
// panic 的处理值得说明：如果直接让它向上冒，那么等待者会永远挂在 wg.Wait() 上 ——
// 一个编程错误就会变成整个服务的死锁。所以这里把它转成 error 分发出去，
// 让所有调用者都拿到一个明确的失败，而不是卡住。
func (g *Group) doCall(c *call, key string, fn func() (any, error)) {
	defer func() {
		if r := recover(); r != nil {
			c.err = fmt.Errorf("singleflight: 被调用函数 panic: %v", r)
		}
		g.mu.Lock()
		delete(g.m, key)
		g.mu.Unlock()
		c.wg.Done()
	}()
	c.val, c.err = fn()
}
