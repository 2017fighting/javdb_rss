package dedupe

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// countingSource 记录调用次数与收到的参数，并可以人为拖慢以制造并发窗口。
type countingSource struct {
	codeCalls    atomic.Int64
	actressCalls atomic.Int64
	delay        time.Duration
	gotParams    []string
	mu           sync.Mutex
}

func (c *countingSource) Code(context.Context, string) ([]catalog.Work, error) {
	c.codeCalls.Add(1)
	time.Sleep(c.delay)
	return []catalog.Work{{Number: "X-1"}}, nil
}

func (c *countingSource) ActressName(_ context.Context, id string) (string, error) {
	return "名字-" + id, nil
}

func (c *countingSource) CollectedActresses(context.Context) ([]catalog.Actress, error) {
	c.actressCalls.Add(1)
	time.Sleep(c.delay)
	return []catalog.Actress{{ID: "EvkJ", Name: "河北彩花"}}, nil
}

func (c *countingSource) Actress(_ context.Context, _ string, params url.Values) ([]catalog.Work, error) {
	c.actressCalls.Add(1)
	c.mu.Lock()
	c.gotParams = append(c.gotParams, params.Encode())
	c.mu.Unlock()
	time.Sleep(c.delay)
	return []catalog.Work{{Number: "X-1"}}, nil
}

// TestCodeMergesConcurrentIdenticalRequests 是这一层存在的全部理由：
// qBittorrent 同时拉同一个 feed 多次时，只打一次上游。
func TestCodeMergesConcurrentIdenticalRequests(t *testing.T) {
	inner := &countingSource{delay: 50 * time.Millisecond}
	s := New(inner)

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.Code(context.Background(), "KV-328"); err != nil {
				t.Errorf("Code: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := inner.codeCalls.Load(); got != 1 {
		t.Errorf("上游被调用了 %d 次，20 个并发相同请求应当合并成 1 次", got)
	}
	if got := s.Shared(); got != n-1 {
		t.Errorf("Shared() = %d, want %d", got, n-1)
	}
}

// TestCodeNormalizesKey 确认大小写与空白不同的同一个番号会被合并 ——
// 否则「同一个订阅」会因为写法不同而重复打上游。
func TestCodeNormalizesKey(t *testing.T) {
	inner := &countingSource{delay: 50 * time.Millisecond}
	s := New(inner)

	variants := []string{"KV-328", "kv-328", "  KV-328  "}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, v := range variants {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			<-start
			_, _ = s.Code(context.Background(), v)
		}(v)
	}
	close(start)
	wg.Wait()

	if got := inner.codeCalls.Load(); got != 1 {
		t.Errorf("大小写/空白不同的同一番号应当合并，上游被调用 %d 次", got)
	}
}

// TestActressDoesNotMergeDifferentParams 是**安全性**上的关键一条：
// 参数不同的请求绝不能被合并，否则用户会拿到别人那份订阅的内容。
func TestActressDoesNotMergeDifferentParams(t *testing.T) {
	inner := &countingSource{delay: 40 * time.Millisecond}
	s := New(inner)

	cases := []url.Values{
		{"filter_by": {"0:a:EvkJ"}},
		{"filter_by": {"0:a:EvkJ"}, "sort_by": {"release"}},
		{"filter_by": {"0:a:EvkJ"}, "sort_by": {"score"}},
		{"filter_by": {"0:a:EvkJ"}, "pages": {"2"}},
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, p := range cases {
		wg.Add(1)
		go func(p url.Values) {
			defer wg.Done()
			<-start
			_, _ = s.Actress(context.Background(), "EvkJ", p)
		}(p)
	}
	close(start)
	wg.Wait()

	if got := inner.actressCalls.Load(); got != int64(len(cases)) {
		t.Errorf("四组不同参数应当各打一次上游，实际 %d 次 —— 参数不同的请求被错误合并了", got)
	}
}

// TestActressMergesSameParamsDifferentOrder 确认参数书写顺序不影响 key。
//
// 这依赖 url.Values.Encode() 的排序行为；如果哪天换成手写拼接，
// 这条会抓到「同样的请求算不出同样的 key」这个 bug。
func TestActressMergesSameParamsDifferentOrder(t *testing.T) {
	inner := &countingSource{delay: 50 * time.Millisecond}
	s := New(inner)

	// 两个语义完全相同、但插入顺序不同的 url.Values。
	build := func(order []string) url.Values {
		v := url.Values{}
		m := map[string]string{"filter_by": "0:a:EvkJ", "sort_by": "release", "order_by": "desc"}
		for _, k := range order {
			v.Set(k, m[k])
		}
		return v
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, order := range [][]string{
		{"filter_by", "sort_by", "order_by"},
		{"order_by", "filter_by", "sort_by"},
	} {
		wg.Add(1)
		go func(o []string) {
			defer wg.Done()
			<-start
			_, _ = s.Actress(context.Background(), "EvkJ", build(o))
		}(order)
	}
	close(start)
	wg.Wait()

	if got := inner.actressCalls.Load(); got != 1 {
		t.Errorf("参数顺序不同的等价请求应当合并，上游被调用 %d 次", got)
	}
}

// TestActressSeparatesDifferentIDs 确认不同女优不会被合并。
func TestActressSeparatesDifferentIDs(t *testing.T) {
	inner := &countingSource{delay: 40 * time.Millisecond}
	s := New(inner)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, id := range []string{"EvkJ", "AbcD", "XyZ1"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, _ = s.Actress(context.Background(), id, nil)
		}(id)
	}
	close(start)
	wg.Wait()

	if got := inner.actressCalls.Load(); got != 3 {
		t.Errorf("三个不同女优应当各打一次上游，实际 %d 次", got)
	}
}

// TestSequentialCallsAreNotMerged 钉住它与缓存的区别：
// 先后发生的调用**不会**被合并，因此不存在陈旧数据。
func TestSequentialCallsAreNotMerged(t *testing.T) {
	inner := &countingSource{}
	s := New(inner)

	for i := 0; i < 3; i++ {
		if _, err := s.Code(context.Background(), "KV-328"); err != nil {
			t.Fatal(err)
		}
	}
	if got := inner.codeCalls.Load(); got != 3 {
		t.Errorf("三次顺序调用应当各打一次上游，实际 %d 次 —— 说明它变成了缓存", got)
	}
	if s.Shared() != 0 {
		t.Errorf("没有并发时不该有合并，Shared() = %d", s.Shared())
	}
}

// TestEmptyResultPassesThrough 确认空结果不会被误当成错误或被吞掉。
func TestEmptyResultPassesThrough(t *testing.T) {
	s := New(emptySource{})
	got, err := s.Code(context.Background(), "X")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("得到 %d 条，want 0", len(got))
	}
}

type emptySource struct{}

func (emptySource) Code(context.Context, string) ([]catalog.Work, error) { return nil, nil }
func (emptySource) Actress(context.Context, string, url.Values) ([]catalog.Work, error) {
	return nil, nil
}
func (emptySource) ActressName(context.Context, string) (string, error) { return "", nil }
func (emptySource) CollectedActresses(context.Context) ([]catalog.Actress, error) {
	return nil, nil
}
