package dedupe

import (
	"context"
	"fmt"
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
	wantCalls    atomic.Int64
	tagCalls     atomic.Int64
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

func (c *countingSource) CollectedActresses(context.Context) (catalog.Collection, error) {
	c.actressCalls.Add(1)
	time.Sleep(c.delay)
	return catalog.Collection{Actresses: []catalog.Actress{{ID: "EvkJ", Name: "河北彩花"}}}, nil
}

func (c *countingSource) Actress(_ context.Context, _ string, params url.Values) ([]catalog.Work, error) {
	c.actressCalls.Add(1)
	c.mu.Lock()
	c.gotParams = append(c.gotParams, params.Encode())
	c.mu.Unlock()
	time.Sleep(c.delay)
	return []catalog.Work{{Number: "X-1"}}, nil
}

func (c *countingSource) WantToWatch(context.Context) (catalog.WantList, error) {
	c.wantCalls.Add(1)
	time.Sleep(c.delay)
	return catalog.WantList{Works: []catalog.Work{{Number: "W-1"}}}, nil
}

func (c *countingSource) TagVocabulary(_ context.Context, zone int) (catalog.TagVocabulary, error) {
	c.tagCalls.Add(1)
	time.Sleep(c.delay)
	return catalog.TagVocabulary{Groups: []catalog.TagGroup{{
		CategoryID: fmt.Sprintf("zone-%d", zone),
		Category:   "基本",
	}}}, nil
}

// TestTagVocabularyMergesPerZone 确认合并 key 里带上了片库号。
//
// 四个片库的词表**各不相同**，把 zone 漏出 key 就等于让一个片库的词表冒充
// 另一个 —— 与上游对非法 type 的静默回落是同一个后果：不报错，只发错词表。
func TestTagVocabularyMergesPerZone(t *testing.T) {
	merged := &countingSource{delay: 50 * time.Millisecond}
	s := New(merged)

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.TagVocabulary(context.Background(), 0); err != nil {
				t.Errorf("TagVocabulary: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := merged.tagCalls.Load(); got != 1 {
		t.Errorf("同片库的 %d 次并发调用打出了 %d 次上游请求，want 1", n, got)
	}

	// 四个**不同**的片库必须各打一次，不能被合并成一次。
	distinct := &countingSource{delay: 50 * time.Millisecond}
	s2 := New(distinct)
	var wg2 sync.WaitGroup
	start2 := make(chan struct{})
	for _, zone := range []int{0, 1, 2, 3} {
		wg2.Add(1)
		go func(zone int) {
			defer wg2.Done()
			<-start2
			if _, err := s2.TagVocabulary(context.Background(), zone); err != nil {
				t.Errorf("TagVocabulary(%d): %v", zone, err)
			}
		}(zone)
	}
	close(start2)
	wg2.Wait()
	if got := distinct.tagCalls.Load(); got != 4 {
		t.Errorf("四个不同片库合并成了 %d 次上游请求，want 4（key 里漏了 zone？）", got)
	}
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
func (emptySource) CollectedActresses(context.Context) (catalog.Collection, error) {
	return catalog.Collection{}, nil
}
func (emptySource) WantToWatch(context.Context) (catalog.WantList, error) {
	return catalog.WantList{}, nil
}
func (emptySource) TagVocabulary(context.Context, int) (catalog.TagVocabulary, error) {
	return catalog.TagVocabulary{}, nil
}

// truncatedSource 总是报告截断。它复用 emptySource 的空实现，只覆盖需要的一条。
type truncatedSource struct{ emptySource }

func (truncatedSource) CollectedActresses(context.Context) (catalog.Collection, error) {
	return catalog.Collection{
		Actresses:    []catalog.Actress{{ID: "EvkJ"}},
		Truncated:    true,
		PagesFetched: 20,
		MaxPages:     20,
	}, nil
}

func (truncatedSource) WantToWatch(context.Context) (catalog.WantList, error) {
	return catalog.WantList{
		Works:        []catalog.Work{{Number: "W-1"}},
		Truncated:    true,
		PagesFetched: 20,
		MaxPages:     20,
	}, nil
}

// TestWantTruncationSurvivesDedupe 是上面那条测试在「想看」清单上的孪生条目。
//
// 它与 TestCollectedTruncationSurvivesDedupe 一样，盯的是**装饰器吞掉信号**：
// dedupe 只转发一部分字段的实现会在这里现形。
func TestWantTruncationSurvivesDedupe(t *testing.T) {
	s := New(truncatedSource{})
	got, err := s.WantToWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated {
		t.Fatal("dedupe 层把截断信号弄丢了")
	}
	if got.PagesFetched != 20 || got.MaxPages != 20 {
		t.Errorf("解释信号的两个字段也应当保留：%+v", got)
	}
}

// TestWantMergesConcurrentIdenticalRequests 确认这条无参清单调用也会被合并。
//
// 它比收藏列表更值得合并：这条路径要翻页 + 逐部拉磁链，而 qBittorrent
// 会周期性地拉它。
func TestWantMergesConcurrentIdenticalRequests(t *testing.T) {
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
			if _, err := s.WantToWatch(context.Background()); err != nil {
				t.Errorf("err = %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := inner.wantCalls.Load(); got != 1 {
		t.Errorf("上游被调了 %d 次，应当合并成 1 次", got)
	}
}

// TestCollectedTruncationSurvivesDedupe 钉住一个容易被装饰器吞掉的信号：
//
// 这一层把并发结果**共享**给多个调用者。若实现只转发 Actresses 而不转发
// Truncated，截断信号就会在这一层消失 —— 而它恰恰是本服务唯一要避免的静默失败。
func TestCollectedTruncationSurvivesDedupe(t *testing.T) {
	s := New(truncatedSource{})
	got, err := s.CollectedActresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated {
		t.Fatal("dedupe 层把截断信号弄丢了")
	}
	if got.PagesFetched != 20 || got.MaxPages != 20 {
		t.Errorf("解释信号的两个字段也应当保留：%+v", got)
	}
}

// 新增契约（清单）的测试存根：这些假实现只关心别的方法，清单那一套
// 一律返回空 —— 它们要的是「满足接口」，而不是「清单行为」。

func (c *countingSource) CollectedLists(context.Context) (catalog.ListCollection, error) {
	return catalog.ListCollection{}, nil
}
func (c *countingSource) List(context.Context, string, url.Values) ([]catalog.Work, error) {
	return nil, nil
}
func (c *countingSource) ListName(context.Context, string) (string, error) { return "", nil }

func (emptySource) CollectedLists(context.Context) (catalog.ListCollection, error) {
	return catalog.ListCollection{}, nil
}
func (emptySource) List(context.Context, string, url.Values) ([]catalog.Work, error) { return nil, nil }
func (emptySource) ListName(context.Context, string) (string, error)                 { return "", nil }

func (truncatedSource) CollectedLists(context.Context) (catalog.ListCollection, error) {
	return catalog.ListCollection{}, nil
}
func (truncatedSource) List(context.Context, string, url.Values) ([]catalog.Work, error) {
	return nil, nil
}
func (truncatedSource) ListName(context.Context, string) (string, error) { return "", nil }

func (c *countingSource) Browse(context.Context, int, catalog.BrowseSelector, url.Values) ([]catalog.Work, error) {
	return nil, nil
}

func (emptySource) Browse(context.Context, int, catalog.BrowseSelector, url.Values) ([]catalog.Work, error) {
	return nil, nil
}

func (truncatedSource) Browse(context.Context, int, catalog.BrowseSelector, url.Values) ([]catalog.Work, error) {
	return nil, nil
}
