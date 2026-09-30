package pin

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/feed"
)

// --- 测试脚手架 -------------------------------------------------------------

// fakeSource 是一个可编程的 catalog.Source，用来观察装饰器对它的改写。
type fakeSource struct {
	// works 是两个路由的默认返回。
	works []catalog.Work
	// code / actress 可单独覆盖某一路的返回。
	//
	// 跨 feed 共享的测试**必须**让两条路由看到不同的候选 —— 否则它是空转的：
	// 临时策略是纯函数，同样的输入必然得到同样的 infohash，
	// 就算 pin 表根本没被共享（甚至没被用上）也会「通过」。
	code    []catalog.Work
	actress []catalog.Work
	err     error
	// calls 记录被调用的次数，用来验证 dedupe 之外的行为。
	mu    sync.Mutex
	calls int
}

func (f *fakeSource) Code(context.Context, string) ([]catalog.Work, error) {
	f.mu.Lock()
	f.calls++
	works := cloneWorks(f.pick(f.code))
	f.mu.Unlock()
	return works, f.err
}

func (f *fakeSource) Actress(context.Context, string, url.Values) ([]catalog.Work, error) {
	f.mu.Lock()
	f.calls++
	works := cloneWorks(f.pick(f.actress))
	f.mu.Unlock()
	return works, f.err
}

// pick 返回某一路的候选，未设置时退回 works。
func (f *fakeSource) pick(override []catalog.Work) []catalog.Work {
	if override != nil {
		return override
	}
	return f.works
}

// cloneWorks 深拷贝作品及其磁链切片。
//
// 这不是洁癖：pin 装饰器会**原地改写** `Work.Magnets`（把它收敛成单元素）。
// 若夹具每次返回同一个底层切片，第二次调用拿到的就是已被改写的候选 ——
// 于是 TestCrossFeedSharesOnePin 这类测试会**假通过**：即使 pin 表根本没用上，
// 两次也会得到同一个 infohash（因为第二次根本看不到完整候选）。
func cloneWorks(in []catalog.Work) []catalog.Work {
	out := make([]catalog.Work, len(in))
	for i, w := range in {
		out[i] = w
		out[i].Magnets = append([]catalog.Magnet(nil), w.Magnets...)
	}
	return out
}

func (f *fakeSource) ActressName(context.Context, string) (string, error) { return "名字", nil }
func (f *fakeSource) CollectedActresses(context.Context) (catalog.Collection, error) {
	return catalog.Collection{}, nil
}

func magnet(hash string, cnsub bool) catalog.Magnet {
	return catalog.Magnet{Infohash: hash, Name: "N-" + hash, SizeMB: 100, CNSub: cnsub, CreatedAt: "09/01/2026"}
}

// newStore 打开一个落在临时目录里的 Store。
func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "pin.json"))
	if err != nil {
		t.Fatalf("打开 store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// --- 装饰器行为 -------------------------------------------------------------

// TestFirstSelectionIsPinned 是最基本的一条：首次见到一部作品时，
// 装饰器按纯函数选出应当下发的那条，**并把它钉住**。
//
// 钉住才是本票的重点 —— 只选不存的话，上游顺序一变 guid 就变。
func TestFirstSelectionIsPinned(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID:     "m1",
		Number: "A-1",
		Magnets: []catalog.Magnet{
			magnet("plain", false),
			magnet("sub", true),
		},
	}}}
	src := New(inner, st, TemporaryPolicy{})

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || len(works[0].Magnets) != 1 {
		t.Fatalf("装饰器应当把候选收敛成单元素: %+v", works)
	}
	if got := works[0].Magnets[0].Infohash; got != "sub" {
		t.Errorf("应当按纯函数选中中文字幕那条，得到 %q", got)
	}

	rec, ok := st.Get("m1")
	if !ok {
		t.Fatal("首次选择应当被钉住")
	}
	if rec.Infohash != "sub" {
		t.Errorf("pin 里存的是 %q, want sub", rec.Infohash)
	}
}

// TestPinnedChoiceSurvivesCandidateChanges 钉住本票的全部意义：
// 一旦选过，上游后来多出什么候选都不改变我们下发的那条。
//
// 这里第二次调用故意给出一条「按纯函数会赢」的新候选 ——
// 若装饰器每次重新选，guid 就会变、qBittorrent 会重下。
//
// 注：「有了更新的 cnsub 要不要切」是 ticket 09 的规则；
// 08 的临时策略是一律不切（见 TemporaryPolicy）。
func TestPinnedChoiceSurvivesCandidateChanges(t *testing.T) {
	st := newStore(t)
	first := []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnet("old", false)},
	}}
	inner := &fakeSource{works: first}
	src := New(inner, st, TemporaryPolicy{})

	if _, err := src.Code(context.Background(), "A-1"); err != nil {
		t.Fatal(err)
	}

	// 上游后来了新候选，且按纯函数它会赢。
	inner.mu.Lock()
	inner.works = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{
			magnet("old", false),
			magnet("brand-new-cnsub", true),
		},
	}}
	inner.mu.Unlock()

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := works[0].Magnets[0].Infohash; got != "old" {
		t.Errorf("已钉住的作品不该换磁链，得到 %q —— guid 会抖", got)
	}
}

// TestCrossFeedSharesOnePin 是 ticket 08 明写要求进测试的那条：
// 同一部作品同时出现在 /rss/code/... 与 /rss/actress/... 时，
// 必须共享同一个 pin，否则同一内容两个 guid、跨 feed 去重被破坏。
//
// ⚠️ 这条测试要真的能失败，两条路由就必须看到**不同的候选**。
// 若两条路由返回同一份候选，临时策略（纯函数）必然给出同一个 infohash，
// 哪怕 pin 表完全没被共享也会「通过」—— 那是假通过。
//
// 场景：
//
//	code    只看得到 [x（普通）]            → 首次选定钉住 x
//	actress 看得到 [y（中文字幕）, x]       → 不共享 pin 的话纯函数会选 y（新 guid）
//
// 共享同一个 pin 时，actress 仍应下发 x。
func TestCrossFeedSharesOnePin(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{
		code: []catalog.Work{{
			ID: "m1", Number: "A-1",
			Magnets: []catalog.Magnet{magnet("x", false)},
		}},
		actress: []catalog.Work{{
			ID: "m1", Number: "A-1",
			Magnets: []catalog.Magnet{magnet("y", true), magnet("x", false)},
		}},
	}
	src := New(inner, st, TemporaryPolicy{})

	byCode, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	byActress, err := src.Actress(context.Background(), "EvkJ", url.Values{})
	if err != nil {
		t.Fatal(err)
	}

	h1 := byCode[0].Magnets[0].Infohash
	h2 := byActress[0].Magnets[0].Infohash
	if h1 != "x" {
		t.Fatalf("code 首次选定应当是 x，得到 %q", h1)
	}
	if h2 != h1 {
		t.Errorf("跨 feed 必须共享同一个 pin（否则两个 guid）: code=%q actress=%q —— "+
			"actress 拿到了完整候选却没用上已有 pin，说明两条路由各钉一套", h1, h2)
	}
	if st.Len() != 1 {
		t.Errorf("同一部作品只该有一条 pin，实际 %d 条", st.Len())
	}
}

// TestWorksWithoutIDAreNotPinned 处理一个真实的边界：stub 数据源与某些
// 测试夹具没有 movie id。没有 id 就没法做键，只能退回纯函数。（不能拿番号当键 ——
// 番号不唯一，那会让两部不同作品互相钉死。）
func TestWorksWithoutIDAreNotPinned(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		Number:  "A-1",
		Magnets: []catalog.Magnet{magnet("x", false), magnet("y", true)},
	}}}
	src := New(inner, st, TemporaryPolicy{})

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := works[0].Magnets[0].Infohash; got != "y" {
		t.Errorf("无 id 时仍应按纯函数选，得到 %q", got)
	}
	if st.Len() != 0 {
		t.Error("没有 movie id 的作品不该被钉住")
	}
}

// TestWorksWithoutMagnetsPassThrough 确认「有作品但尚无磁链」是常见状态，
// 不该被装饰器弄坏（feed.Build 会跳过它）。
func TestWorksWithoutMagnetsPassThrough(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{ID: "m1", Number: "A-1"}}}
	src := New(inner, st, TemporaryPolicy{})

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || len(works[0].Magnets) != 0 {
		t.Errorf("无磁链的作品应当原样通过: %+v", works)
	}
	if st.Len() != 0 {
		t.Error("无磁链的作品不该被钉住")
	}
}

// TestFatalOnFlushError 钉住「运行中写失败 = 致命」。
//
// 装饰器不能只是把错误返回给这一个请求：那会让进程带着「内存里已改、磁盘上没改」
// 的状态继续服务。它必须通过 OnFatal 让 main 退出，把失败变成可见的重启。
func TestFatalOnFlushError(t *testing.T) {
	st := newStore(t)
	// 构造写失败：把 tmp 目标位置变成一个目录。
	if err := os.MkdirAll(st.Path()+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var fatalErr error
	src := New(&fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1", Magnets: []catalog.Magnet{magnet("x", false)},
	}}}, st, TemporaryPolicy{})
	src.OnFatal = func(err error) {
		mu.Lock()
		fatalErr = err
		mu.Unlock()
	}

	if _, err := src.Code(context.Background(), "A-1"); err == nil {
		t.Error("写失败时该次调用应当报错")
	}
	mu.Lock()
	defer mu.Unlock()
	if fatalErr == nil {
		t.Fatal("写失败必须触发 OnFatal —— 否则失败是不可见的")
	}
}

// TestPolicySeamAllowsSwitching 证明 Policy 这个接缝**真的**能承载 ticket 09，
// 而不是一个摆设：换一个会切换的策略，装饰器就切换。
func TestPolicySeamAllowsSwitching(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1", Magnets: []catalog.Magnet{magnet("old", false)},
	}}}
	// alwaysSwitch 模拟「找到更好的就切」的策略。
	src := New(inner, st, alwaysSwitchPolicy{})

	if _, err := src.Code(context.Background(), "A-1"); err != nil {
		t.Fatal(err)
	}

	// 上游出现一条新候选，alwaysSwitch 会切过去。
	inner.mu.Lock()
	inner.works = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnet("old", false), magnet("new", true)},
	}}
	inner.mu.Unlock()

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := works[0].Magnets[0].Infohash; got != "new" {
		t.Errorf("策略要求切换时应当切到 %q，得到 %q", "new", got)
	}
	if rec, _ := st.Get("m1"); rec.Infohash != "new" {
		t.Errorf("切换后 pin 应当更新，得到 %q", rec.Infohash)
	}
}

// TestDefaultPolicyIsSelect 确认临时策略的纯函数部分就是 catalog.Select
// （04 的 created_at 规则是 ticket 09 的事，这里只钉住「08 没有偷偷改规则」）。
func TestDefaultPolicyIsSelect(t *testing.T) {
	in := []catalog.Magnet{magnet("a", false), magnet("b", true)}
	want, _ := catalog.Select(in)
	got, ok := TemporaryPolicy{}.Desired(in)
	if !ok || got.Infohash != want.Infohash {
		t.Errorf("TemporaryPolicy.Desired = %v/%v, want %v", got.Infohash, ok, want.Infohash)
	}
}

// alwaysSwitchPolicy 是一个只用于测试的 Policy：永远采用纯函数的结果。
type alwaysSwitchPolicy struct{}

func (alwaysSwitchPolicy) Desired(cands []catalog.Magnet) (catalog.Magnet, bool) {
	return catalog.Select(cands)
}

func (alwaysSwitchPolicy) KeepOrSwitch(pinned Record, pinnedOK bool, _ []catalog.Magnet, desired catalog.Magnet) (catalog.Magnet, bool) {
	return desired, pinnedOK && pinned.Infohash != desired.Infohash
}

// TestGUIDStableAcrossUpstreamChange 是本票的验收语言：
// 客户端看到的 guid 不因上游候选变化而移动。
//
// 前几条测的是「pin 里的 infohash 不变」；这条把它接到真正的产物上 ——
// feed.Item.GUID() 就是 infohash，qBittorrent 按它去重。
// 上游重排/新增候选而 guid 不变 == 不会重复下载。
func TestGUIDStableAcrossUpstreamChange(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnet("first", false)},
	}}}
	src := New(inner, st, TemporaryPolicy{})

	guidOf := func() string {
		t.Helper()
		works, err := src.Code(context.Background(), "A-1")
		if err != nil {
			t.Fatal(err)
		}
		items := feed.Build(works)
		if len(items) != 1 {
			t.Fatalf("得到 %d 条 item", len(items))
		}
		return items[0].GUID()
	}

	before := guidOf()

	// 上游把顺序整个换掉，并加了一条会赢的 cnsub —— 无状态实现会给新 guid。
	inner.mu.Lock()
	inner.works = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{
			magnet("newer-cnsub", true),
			magnet("first", false),
			magnet("newest", false),
		},
	}}
	inner.mu.Unlock()

	if after := guidOf(); after != before {
		t.Errorf("guid 变了：%q -> %q —— qBittorrent 会重复下载", before, after)
	}
}

// TestNilStoreDegradesInsteadOfPanicking 确认一个库不会因为装配漏了 store 而崩溃。
//
// main 不会这样装配（它在装配前就 Open 了 store 并 fail fast），
// 但「传 nil 就 panic」是库代码里最容易伤到下一个调用者的设计。
func TestNilStoreDegradesInsteadOfPanicking(t *testing.T) {
	src := New(&fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnet("x", false), magnet("y", true)},
	}}}, nil, TemporaryPolicy{})

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatalf("没有 store 时不该报错: %v", err)
	}
	if got := works[0].Magnets[0].Infohash; got != "y" {
		t.Errorf("没有 store 时应当退化为纯函数选择，得到 %q", got)
	}
}
