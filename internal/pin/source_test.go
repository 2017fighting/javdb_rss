package pin

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	// 纯函数对同样的输入必然得到同样的 infohash，
	// 就算 pin 表根本没被共享（甚至没被用上）也会「通过」。
	code    []catalog.Work
	actress []catalog.Work
	// want 可单独覆盖「想看」清单的返回。
	want []catalog.Work
	err  error
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

func (f *fakeSource) WantToWatch(context.Context) (catalog.WantList, error) {
	f.mu.Lock()
	f.calls++
	works := cloneWorks(f.pick(f.want))
	f.mu.Unlock()
	return catalog.WantList{Works: works}, f.err
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

// --- 日志捕获：让「切换 / pin 消失」这类事件可被断言 -------------------------

type captureHandler struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.recs = append(h.recs, r.Clone())
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) find(msgSubstr string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.recs {
		if strings.Contains(r.Message, msgSubstr) {
			return r, true
		}
	}
	return slog.Record{}, false
}

// withCapturedLogs 把默认 logger 换成捕获器。必须在 Open store **之前**调用 ——
// Source 会从 store 拿到当时的 slog.Default()。
func withCapturedLogs(t *testing.T) *captureHandler {
	t.Helper()
	h := &captureHandler{}
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	return h
}

func attrsOf(r slog.Record) map[string]string {
	out := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
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
	src := New(inner, st, DefaultPolicy{})

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

// TestPinnedChoiceSurvivesPlainCandidateChanges 钉住本票的意义：
// 上游多出/重排**普通**候选时，我们下发的那条不变。
//
// 这里第二次调用故意给出一条「按纯函数会赢」的更新的普通候选 ——
// 若装饰器每次重新选，guid 就会变、qBittorrent 会重下。
//
// 「出现更新的 cnsub 要不要切」由 ticket 09 定：**会切**，见 TestSwitchesToNewerCNSub。
func TestPinnedChoiceSurvivesPlainCandidateChanges(t *testing.T) {
	st := newStore(t)
	first := []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnetAt("old", false, "2026-01-01")},
	}}
	inner := &fakeSource{works: first}
	src := New(inner, st, DefaultPolicy{})

	if _, err := src.Code(context.Background(), "A-1"); err != nil {
		t.Fatal(err)
	}

	// 上游后来了更新的普通候选，且按纯函数它会赢。
	inner.mu.Lock()
	inner.works = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{
			magnetAt("old", false, "2026-01-01"),
			magnetAt("brand-new-plain", false, "2026-09-01"),
		},
	}}
	inner.mu.Unlock()

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := works[0].Magnets[0].Infohash; got != "old" {
		t.Errorf("普通候选变化不该换磁链，得到 %q —— guid 会抖", got)
	}
}

// TestSwitchesToNewerCNSub 是 ticket 09 的切换规则在装饰器层的行为：
// 已经钉了一条普通候选，上游出现 cnsub → 切过去并更新 pin。
func TestSwitchesToNewerCNSub(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnetAt("plain", false, "2026-01-01")},
	}}}
	src := New(inner, st, DefaultPolicy{})

	if _, err := src.Code(context.Background(), "A-1"); err != nil {
		t.Fatal(err)
	}

	inner.mu.Lock()
	inner.works = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{
			magnetAt("plain", false, "2026-01-01"),
			magnetAt("sub", true, "2026-06-01"),
		},
	}}
	inner.mu.Unlock()

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := works[0].Magnets[0].Infohash; got != "sub" {
		t.Errorf("出现 cnsub 后应当切换，得到 %q", got)
	}
	rec, _ := st.Get("m1")
	if rec.Infohash != "sub" || !rec.CNSub {
		t.Errorf("切换后 pin 应当更新成 cnsub 快照，得到 %+v", rec)
	}
}

// TestCrossFeedSharesOnePin 是 ticket 08 明写要求进测试的那条：
// 同一部作品同时出现在 /rss/code/... 与 /rss/actress/... 时，
// 必须共享同一个 pin，否则同一内容两个 guid、跨 feed 去重被破坏。
//
// ⚠️ 这条测试要真的能失败，两条路由就必须看到**不同的候选**。
// 两条路由返回同一份候选时，纯函数必然给出同一个 infohash，
// 哪怕 pin 表完全没被共享也会「通过」—— 那是假通过。
//
// 场景（两条路由都只有普通候选，因此 ticket 09 的 cnsub 切换不会介入）：
//
//	code    看得到 [x（旧）]              → 首次选定钉住 x
//	actress 看得到 [z（新）, x（旧）]     → 不共享 pin 的话纯函数会选 z（新 guid）
//
// 共享同一个 pin 时，actress 仍应下发 x。
func TestCrossFeedSharesOnePin(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{
		code: []catalog.Work{{
			ID: "m1", Number: "A-1",
			Magnets: []catalog.Magnet{magnetAt("x", false, "2026-01-01")},
		}},
		actress: []catalog.Work{{
			ID: "m1", Number: "A-1",
			Magnets: []catalog.Magnet{
				magnetAt("z", false, "2026-06-01"),
				magnetAt("x", false, "2026-01-01"),
			},
		}},
	}
	src := New(inner, st, DefaultPolicy{})

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
	src := New(inner, st, DefaultPolicy{})

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
	src := New(inner, st, DefaultPolicy{})

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

// TestWantListGoesThroughPinToo 确认「想看」 feed 与番号/女优 feed 走**同一套**钉住。
//
// 这条路径同样会被 qBittorrent 周期轮询，而 guid = 选中的 infohash：
// 不钉就是「上游新增一条普通候选 → 新 guid → 同一部片被下第二份」，
// 而且没有任何告警。
func TestWantListGoesThroughPinToo(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{want: []catalog.Work{{
		ID: "m1", Number: "A-1", Magnets: []catalog.Magnet{magnet("x", false)},
	}}}
	src := New(inner, st, DefaultPolicy{})

	first, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Works[0].Magnets[0].Infohash; got != "x" {
		t.Fatalf("首次选中 = %q, want x", got)
	}
	if st.Len() != 1 {
		t.Fatal("「想看」清单里选中的磁链应当落进 pin 表")
	}

	// 上游新增一条创建时间更新的**普通**候选 —— 按切换语义不该切。
	// 若不切换，说明这条路径确实走了 pin，而不是直接透传纯函数的选择。
	inner.mu.Lock()
	inner.want = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnet("x", false), magnetAt("newer", false, "09/30/2026")},
	}}
	inner.mu.Unlock()

	second, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Works[0].Magnets; len(got) != 1 || got[0].Infohash != "x" {
		t.Errorf("普通候选的变化不该动 pin，得到 %+v", got)
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
	}}}, st, DefaultPolicy{})
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

// TestPolicySeamAllowsSwitching 证明 Policy 这个接缝**真的**能承载不同策略，
// 而不是一个摆设：换一个会切换的策略，装饰器就切换。
func TestPolicySeamAllowsSwitching(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1", Magnets: []catalog.Magnet{magnet("old", false)},
	}}}
	// alwaysSwitch 模拟「找到不同的就切」的策略。
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

// TestDefaultPolicyIsSelect 确认默认策略的纯函数部分就是 catalog.Select
// （重排、新增普通候选都不该改变它）。
func TestDefaultPolicyIsSelect(t *testing.T) {
	in := []catalog.Magnet{magnet("a", false), magnet("b", true)}
	want, _ := catalog.Select(in)
	got, ok := DefaultPolicy{}.Desired(in)
	if !ok || got.Infohash != want.Infohash {
		t.Errorf("DefaultPolicy.Desired = %v/%v, want %v", got.Infohash, ok, want.Infohash)
	}
}

// alwaysSwitchPolicy 是一个只用于测试的 Policy：永远采用纯函数的结果。
type alwaysSwitchPolicy struct{}

func (alwaysSwitchPolicy) Desired(cands []catalog.Magnet) (catalog.Magnet, bool) {
	return catalog.Select(cands)
}

func (alwaysSwitchPolicy) KeepOrSwitch(pinned Record, pinnedOK bool, desired catalog.Magnet) (catalog.Magnet, bool) {
	return desired, pinnedOK && pinned.Infohash != desired.Infohash
}

// TestGUIDStableAcrossUpstreamChange 是本票的验收语言：
// 客户端看到的 guid 不因上游候选的**普通候选**变化而移动。
//
// 前几条测的是「pin 里的 infohash 不变」；这条把它接到真正的产物上 ——
// feed.Item.GUID() 就是 infohash，qBittorrent 按它去重。
// 上游重排/新增普通候选而 guid 不变 == 不会重复下载。
//
// 「新增 cnsub 会主动切 guid」是 ticket 09 的刻意行为，见 TestSwitchesToNewerCNSub。
func TestGUIDStableAcrossUpstreamChange(t *testing.T) {
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnetAt("first", false, "2026-01-01")},
	}}}
	src := New(inner, st, DefaultPolicy{})

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

	// 上游把顺序整个换掉，并加了一条更新的**普通**候选 —— 无状态实现会给新 guid。
	inner.mu.Lock()
	inner.works = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{
			magnetAt("newest-plain", false, "2026-09-01"),
			magnetAt("first", false, "2026-01-01"),
			magnetAt("middle-plain", false, "2026-05-01"),
		},
	}}
	inner.mu.Unlock()

	if after := guidOf(); after != before {
		t.Errorf("guid 变了：%q -> %q —— qBittorrent 会重复下载", before, after)
	}
}

// TestSwitchIsVisible 钉住 ticket 09 的可见性要求：
// pin 从旧值换成新值时，必须留下一条能定位到作品与新旧值的 INFO。
func TestSwitchIsVisible(t *testing.T) {
	h := withCapturedLogs(t)
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnetAt("old", false, "2026-01-01")},
	}}}
	src := New(inner, st, DefaultPolicy{})
	if _, err := src.Code(context.Background(), "A-1"); err != nil {
		t.Fatal(err)
	}

	inner.mu.Lock()
	inner.works = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{
			magnetAt("old", false, "2026-01-01"),
			magnetAt("new-sub", true, "2026-06-01"),
		},
	}}
	inner.mu.Unlock()

	if _, err := src.Code(context.Background(), "A-1"); err != nil {
		t.Fatal(err)
	}

	r, ok := h.find("pin 切换")
	if !ok {
		t.Fatal("切换必须留下一条日志，否则「换了哪条磁链」不可见")
	}
	attrs := attrsOf(r)
	if attrs["旧磁链"] != "old" || attrs["新磁链"] != "new-sub" {
		t.Errorf("切换日志没有指出新旧值: %v", attrs)
	}
	if attrs["作品"] != "m1" {
		t.Errorf("切换日志没有指出作品: %v", attrs)
	}
}

// TestPinAbsentFromUpstreamKeepsSnapshot 钉住 ticket 09 对「pin 消失」的决定：
// 继续沿用快照（guid 稳定优先），但要 WARN 出来，不能静默发死链。
func TestPinAbsentFromUpstreamKeepsSnapshot(t *testing.T) {
	h := withCapturedLogs(t)
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnetAt("gone", false, "2026-01-01")},
	}}}
	src := New(inner, st, DefaultPolicy{})
	if _, err := src.Code(context.Background(), "A-1"); err != nil {
		t.Fatal(err)
	}

	// 上游只剩一条更新的普通候选：pin 指向的那条消失了，但没有新 cnsub。
	inner.mu.Lock()
	inner.works = []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnetAt("other", false, "2026-06-01")},
	}}
	inner.mu.Unlock()

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := works[0].Magnets[0].Infohash; got != "gone" {
		t.Errorf("pin 消失时应当继续沿用快照，得到 %q", got)
	}
	if rec, _ := st.Get("m1"); rec.Infohash != "gone" {
		t.Errorf("pin 不该被改掉，得到 %q", rec.Infohash)
	}

	r, ok := h.find("不在当前上游候选")
	if !ok {
		t.Fatal("pin 消失必须留下一条日志，否则「发了死链」不可见")
	}
	if r.Level != slog.LevelWarn {
		t.Errorf("pin 消失的日志级别 = %v, want WARN", r.Level)
	}
}

// TestPinAbsentWhenUpstreamReturnsNoMagnets 是一条容易漏的边界：上游把某部已钉住
// 作品的磁链**全部删掉**时，装饰器仍应返回快照并 WARN —— 否则该作品会静默地
// 从 feed 里消失（而 ticket 09 要的是稳定的死链 + 可见的告警）。
func TestPinAbsentWhenUpstreamReturnsNoMagnets(t *testing.T) {
	h := withCapturedLogs(t)
	st := newStore(t)
	inner := &fakeSource{works: []catalog.Work{{
		ID: "m1", Number: "A-1",
		Magnets: []catalog.Magnet{magnetAt("gone", false, "2026-01-01")},
	}}}
	src := New(inner, st, DefaultPolicy{})
	if _, err := src.Code(context.Background(), "A-1"); err != nil {
		t.Fatal(err)
	}

	// 上游这次对这部作品一条候选都不给。
	inner.mu.Lock()
	inner.works = []catalog.Work{{ID: "m1", Number: "A-1"}}
	inner.mu.Unlock()

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(works[0].Magnets) != 1 || works[0].Magnets[0].Infohash != "gone" {
		t.Errorf("已钉住的作品即使上游无候选也应返回快照，得到 %+v", works[0].Magnets)
	}
	if rec, _ := st.Get("m1"); rec.Infohash != "gone" {
		t.Errorf("pin 不该被改掉，得到 %q", rec.Infohash)
	}
	r, ok := h.find("不在当前上游候选")
	if !ok {
		t.Fatal("上游删光磁链时必须留下一条日志，否则作品会静默消失")
	}
	if attrs := attrsOf(r); attrs["候选数"] != "0" {
		t.Errorf("日志应说明候选数为 0: %v", attrs)
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
	}}}, nil, DefaultPolicy{})

	works, err := src.Code(context.Background(), "A-1")
	if err != nil {
		t.Fatalf("没有 store 时不该报错: %v", err)
	}
	if got := works[0].Magnets[0].Infohash; got != "y" {
		t.Errorf("没有 store 时应当退化为纯函数选择，得到 %q", got)
	}
}
