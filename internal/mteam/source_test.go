package mteam

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/pin"
)

// --- 纯函数：命中判定与选一条 --------------------------------------------------

func TestMatchCode(t *testing.T) {
	tests := []struct {
		name string
		code string
		hit  string
		want bool
	}{
		{"标准形态", "SONE-168", "SONE-168 激イキ191回！ 痙攣4702回！", true},
		// ⚠️ 这条是那个真陷阱的证据：按「非字母数字切词再比词元」的实现
		// 会把 SONE-168 切成 SONE + 168，于是这里返回 false —— 表现是「馒头没有」。
		{"日文汉字紧跟在番号后面", "SONE-168", "SONE-168 交わる体液", true},
		{"连字符被省略", "SONE-168", "SONE168 4K", true},
		{"空格代替连字符", "SONE-168", "SONE 168 4K", true},
		{"大小写不敏感", "SONE-168", "sone-168 xxx", true},
		{"带后缀（中字/无码破解）", "SSIS-001", "SSIS-001-C 無碼破解", true},
		{"前面还有别的前缀", "SONE-168", "[4K] SONE-168 xxx", true},
		{"更长番号不算命中", "SONE-168", "SONE-1680 xxx", false},
		{"番号前面粘着字母不算命中", "SONE-168", "XSONE-168 xxx", false},
		{"番号后面粘着字母不算命中", "SONE-168", "SONE-168X xxx", false},
		{"完全不相干", "SONE-168", "ABP-123 别的片", false},
		{"番号为空", "", "随便什么", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchCode([]SearchHit{{Name: tt.hit}}, tt.code)
			if (len(got) == 1) != tt.want {
				t.Errorf("matchCode(%q, %q) = %v, want %v", tt.code, tt.hit, len(got) == 1, tt.want)
			}
		})
	}
}

func TestPickBest(t *testing.T) {
	tests := []struct {
		name string
		hits []SearchHit
		want string
	}{
		{
			"中文字幕优先，与时间无关",
			[]SearchHit{
				{ID: "1", CNSub: false, CreatedAt: "2026-01-01 00:00:00"},
				{ID: "2", CNSub: true, CreatedAt: "2021-01-01 00:00:00"},
			},
			"2",
		},
		{
			"都没有中字时取最新的",
			[]SearchHit{
				{ID: "1", CreatedAt: "2021-02-18 21:23:47"},
				{ID: "2", CreatedAt: "2021-02-22 15:19:51"},
				{ID: "3", CreatedAt: "2021-02-20 23:00:14"},
			},
			"2",
		},
		{
			// 定宽零填充的线格式，因此字典序与时间序一致 —— 这条钉住那个前提。
			"时间相同时按 id 定序（确定性，可重放）",
			[]SearchHit{
				{ID: "b", CreatedAt: "2021-01-01 00:00:00"},
				{ID: "a", CreatedAt: "2021-01-01 00:00:00"},
			},
			"a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickBest(tt.hits).ID; got != tt.want {
				t.Errorf("pickBest = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRateGateIsASlidingWindow 钉住限速闸门的形状。
//
// ⚠️ 它必须是**滑动**的一小时，而不是「整点清零」—— 后者会在整点前后各放行 100 个，
// 也就是一小时里 200 个，而站点的建议值是 100 个/小时。
func TestRateGateIsASlidingWindow(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	g := &rateGate{max: 2, window: time.Hour, now: func() time.Time { return now }}

	if !g.allow() {
		t.Fatal("第一次应当放行")
	}
	// 把第二次放到半小时后，这样两个记录不会同时滑出窗口。
	now = now.Add(30 * time.Minute)
	if !g.allow() {
		t.Fatal("第二次应当放行")
	}
	if g.allow() {
		t.Error("第三次不该放行（上限 2）")
	}

	// 再推 31 分钟：第一次（12:00）滑出窗口，第二次（12:30）还在。
	now = now.Add(31 * time.Minute)
	if !g.allow() {
		t.Error("最早那次滑出窗口之后应当放行")
	}
	if g.allow() {
		t.Error("现在窗口里又满了（12:30 那次 + 新那次），不该放行")
	}
}

// --- 夹具 ---------------------------------------------------------------------

// fakePins 是一个可编程的钉住表。
type fakePins struct{ pinned map[string]bool }

func (f fakePins) Get(id string) (pin.Record, bool) {
	if f.pinned[id] {
		return pin.Record{Infohash: "pinned"}, true
	}
	return pin.Record{}, false
}

// fakeInner 是一个可编程的上游。
type fakeInner struct {
	want      []catalog.Work
	wantErr   error
	wantCalls atomic.Int64
	codeCalls atomic.Int64
}

func (f *fakeInner) WantToWatch(context.Context) (catalog.WantList, error) {
	f.wantCalls.Add(1)
	if f.wantErr != nil {
		return catalog.WantList{}, f.wantErr
	}
	// 每次都返回一份**新的**切片。
	works := make([]catalog.Work, len(f.want))
	copy(works, f.want)
	return catalog.WantList{Works: works}, nil
}

func (f *fakeInner) Code(context.Context, string) ([]catalog.Work, error) {
	f.codeCalls.Add(1)
	return nil, nil
}

func (f *fakeInner) Actress(context.Context, string, url.Values) ([]catalog.Work, error) {
	return nil, nil
}
func (f *fakeInner) ActressName(context.Context, string) (string, error) { return "", nil }
func (f *fakeInner) CollectedActresses(context.Context) (catalog.Collection, error) {
	return catalog.Collection{}, nil
}
func (f *fakeInner) CollectedLists(context.Context) (catalog.ListCollection, error) {
	return catalog.ListCollection{}, nil
}
func (f *fakeInner) List(context.Context, string, url.Values) ([]catalog.Work, error) {
	return nil, nil
}
func (f *fakeInner) ListName(context.Context, string) (string, error) { return "", nil }
func (f *fakeInner) TagVocabulary(context.Context, int) (catalog.TagVocabulary, error) {
	return catalog.TagVocabulary{}, nil
}
func (f *fakeInner) ActressTags(context.Context, string) (catalog.ActressTags, error) {
	return catalog.ActressTags{}, nil
}
func (f *fakeInner) Browse(context.Context, int, catalog.BrowseSelector, url.Values) ([]catalog.Work, error) {
	return nil, nil
}

// fakeMTeam 是一个假馒头，数着它被打了几次。
type fakeMTeam struct {
	searchCalls atomic.Int64
	// rows 是搜索返回的原始行。
	rows string
	// errBody 非空时，搜索返回一个失败信封。
	errBody string
	// torrent 是下载返回的字节。
	torrent string
	// base 是这个假服务器的地址，由夹具在起服务之后填 ——
	// 下载链接必须是**绝对**地址（真的馒头给的就是绝对的）。
	base string
	// downloadPath 非空时覆盖下载路径（用来模拟「取种子失败」）。
	downloadPath string
}

func (f *fakeMTeam) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/torrent/search":
			f.searchCalls.Add(1)
			if f.errBody != "" {
				_, _ = io.WriteString(w, f.errBody)
				return
			}
			_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":{"data":[`+f.rows+`]}}`)
		case "/api/torrent/genDlToken":
			link := f.base + "/api/rss/dlv2"
			if f.downloadPath != "" {
				link = f.base + f.downloadPath
			}
			_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":"`+link+`"}`)
		case "/api/rss/dlv2":
			_, _ = io.WriteString(w, f.torrent)
		case "/api/torrent/gone":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("假馒头收到意外路径 %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// fixture 把一次测试要用的两半凑齐：请求路径的 Source（只读）与后台的 Loop（写者）。
type fixture struct {
	src   *Source
	loop  *Loop
	store *Store
	inner *fakeInner
	mt    *fakeMTeam
	log   *bytes.Buffer
}

// newFixture 造一个夹具。maxDownloadsPerHour ≤ 0 用默认值。
func newFixture(t *testing.T, inner *fakeInner, mt *fakeMTeam, pinned map[string]bool) *fixture {
	t.Helper()
	return newFixtureLimited(t, inner, mt, pinned, 0)
}

func newFixtureLimited(t *testing.T, inner *fakeInner, mt *fakeMTeam, pinned map[string]bool, maxPerHour int) *fixture {
	t.Helper()
	srv := httptest.NewServer(mt.handler(t))
	t.Cleanup(srv.Close)
	mt.base = srv.URL

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	log, buf := captureLogs()
	client := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	pins := fakePins{pinned: pinned}
	return &fixture{
		src:   New(inner, store, pins, log),
		loop:  NewLoop(inner, client, store, pins, func() time.Duration { return time.Minute }, maxPerHour, log),
		store: store,
		inner: inner,
		mt:    mt,
		log:   buf,
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func captureLogs() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// decide 让后台循环跑 n 轮。
func (f *fixture) decide(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		f.loop.Once(context.Background())
	}
}

// hitRow 造一行搜索命中。infohash 从缺 —— 线格式里它恒为 null。
func hitRow(id, name string, cnsub bool, created string) string {
	labels := `[]`
	if cnsub {
		labels = `["中字"]`
	}
	return `{"id":"` + id + `","name":"` + name + `","size":"1073741824",` +
		`"labelsNew":` + labels + `,"createdDate":"` + created + `","infoHash":null}`
}

func wantWork(number string, magnets ...catalog.Magnet) catalog.Work {
	return catalog.Work{ID: "id-" + number, Number: number, Title: "标题 " + number, Magnets: magnets}
}

// upstreamMagnet 是「上游那条」—— 断言里反复要用它。
func upstreamMagnet() catalog.Magnet {
	return catalog.Magnet{Infohash: "upstream-hash", Name: "上游那条"}
}

// --- 请求路径：只读，而且绝不查 -----------------------------------------------

// TestRequestPathNeverCallsMTeam 是票 04 最要紧的一条。
//
// 判定**不在请求路径里做**。理由不是审美：`/rss/want.xml` 的一次渲染已经要 ~20 秒
// （24 页清单 + 222 次磁链请求），而 qBittorrent 在 30 秒无字节传输时就掐断请求 ——
// 而 feed 是渲染完才写第一个字节。判定是「搜索 + 下载种子」，234 部就是分钟级。
//
// 所以：**未判定的作品被扣住不发，而不是当场去查**。
func TestRequestPathNeverCallsMTeam(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{rows: hitRow("1", "SSIS-001 x", true, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	f := newFixture(t, inner, mt, nil)

	for i := 0; i < 3; i++ {
		list, err := f.src.WantToWatch(context.Background())
		if err != nil {
			t.Fatalf("第 %d 次: %v", i, err)
		}
		// 扣住：一个条目都不发……
		if len(list.Works) != 0 {
			t.Errorf("未判定的作品应当被扣住，实际发了 %d 条", len(list.Works))
		}
		// ……但**不是静默**：计数要交出去，好让标题说出来。
		if list.PendingSource != 1 {
			t.Errorf("PendingSource = %d, want 1", list.PendingSource)
		}
	}
	if got := mt.searchCalls.Load(); got != 0 {
		t.Errorf("请求路径打了 M-Team %d 次 —— 它一次都不该打", got)
	}
}

// TestHitVerdictReplacesThePool 是主路径：后台判定命中之后，feed 交出馒头那份。
func TestHitVerdictReplacesThePool(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{rows: hitRow("467487", "SSIS-001 一ヶ月間の…", true, "2021-02-22 15:19:51"), torrent: torrentCanonical}
	f := newFixture(t, inner, mt, nil)

	f.decide(t, 1)

	list, err := f.src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if list.PendingSource != 0 {
		t.Errorf("判定已就绪，不该再扣住：%d", list.PendingSource)
	}
	if len(list.Works) != 1 || len(list.Works[0].Magnets) != 1 {
		t.Fatalf("候选池应当是单元素，实际 %+v", list.Works)
	}
	m := list.Works[0].Magnets[0]
	// 来源被换成 M-Team（这是「整体替换」的证据：上游那条没了）。
	if m.Source != catalog.SourceMTeam {
		t.Errorf("来源 = %v, want SourceMTeam", m.Source)
	}
	// infohash 是从**种子字节**算出来的 —— 搜索接口根本不返回它。
	if m.Infohash != "f875b7bedb38e072dde96440fb79e5fc55699d68" {
		t.Errorf("infohash = %q（应当从种子算出）", m.Infohash)
	}
	if !m.CNSub {
		t.Error("labelsNew 里有中字，快照里应当带上")
	}
	if m.SizeMiB != 1024 {
		t.Errorf("SizeMiB = %d, want 1024（线格式是字节）", m.SizeMiB)
	}
}

// TestMissAndFailurePassThroughAndAreNotRetried 钉住另外两种结果：
// 它们都**原样发上游那条**，而且都不再重问。
//
// 两者的区别是：负结果是**判决**（永久），失败不是判决但也不自动重试。
func TestMissAndFailurePassThroughAndAreNotRetried(t *testing.T) {
	tests := []struct {
		name string
		mt   *fakeMTeam
		want Outcome
	}{
		{"负结果（搜索有结果，但都不是这部作品）",
			&fakeMTeam{rows: hitRow("1", "ABP-123 别的片", false, "2021-01-01 00:00:00")}, OutcomeMiss},
		{"查询失败（M-Team 给了看不懂的响应）",
			&fakeMTeam{errBody: `这不是 JSON`}, OutcomeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
			f := newFixture(t, inner, tt.mt, nil)

			f.decide(t, 3) // 三轮：只有第一轮该查

			if got := tt.mt.searchCalls.Load(); got != 1 {
				t.Errorf("查了 %d 次 —— %s 不该被重问", got, tt.want)
			}
			v, ok := f.store.Get("id-SSIS-001")
			if !ok || v.Outcome != tt.want {
				t.Errorf("判定 = %+v（存在=%v），want %s", v, ok, tt.want)
			}

			list, err := f.src.WantToWatch(context.Background())
			if err != nil {
				t.Fatalf("WantToWatch: %v", err)
			}
			if list.PendingSource != 0 {
				t.Errorf("有判定就不该扣住：PendingSource=%d", list.PendingSource)
			}
			if m := list.Works[0].Magnets[0]; m.Infohash != "upstream-hash" {
				t.Errorf("应当原样发上游那条，实际 %+v", m)
			}
		})
	}
}

// TestPinnedIsNeverDecided 钉住「首发冻结」。
//
// 已下发的作品：后台**连问都不问**，feed 也**原样放行**。
// 这是「来源永不自动更换」的落点 —— 换来源 = 磁盘上多一份。
func TestPinnedIsNeverDecided(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{rows: hitRow("1", "SSIS-001 x", true, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	f := newFixture(t, inner, mt, map[string]bool{"id-SSIS-001": true})

	f.decide(t, 2)

	if got := mt.searchCalls.Load(); got != 0 {
		t.Errorf("已钉住的作品被查了 %d 次，应当一次都不查", got)
	}
	list, err := f.src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if list.PendingSource != 0 {
		t.Errorf("已钉住的不该被扣住（它早就下发过了）：%d", list.PendingSource)
	}
	if m := list.Works[0].Magnets[0]; m.Source != catalog.SourceUpstream {
		t.Errorf("已钉住的作品不该被换来源，实际 %v", m.Source)
	}
}

// TestLoopIsResumable 钉住「可续跑」：判定表**就是**进度。
//
// 第二轮的清单里同一部作品已经有判定 ⇒ 不再重问。
// 这也正是配额能撑住的原因：一个 234 部的清单只会在**第一次**被问一遍。
func TestLoopIsResumable(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{
		wantWork("SSIS-001", upstreamMagnet()),
		wantWork("SSIS-002", upstreamMagnet()),
	}}
	mt := &fakeMTeam{rows: hitRow("1", "SSIS-001 x", false, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	f := newFixture(t, inner, mt, nil)

	f.decide(t, 1)
	if got := mt.searchCalls.Load(); got != 2 {
		t.Errorf("第一轮查了 %d 次，want 2（两部都没判定）", got)
	}
	f.decide(t, 2)
	if got := mt.searchCalls.Load(); got != 2 {
		t.Errorf("后续轮次又查了 —— 判定表应当就是进度（总 %d 次）", got)
	}
}

// TestLoopRateLimitsDownloads 钉住那个唯一的瓶颈：**种子下载 ≤ N/小时**。
//
// ⚠️ 闸门关掉时**不写判定** —— 「还没取到种子」不是任何一种结果。
// 写进去就等于「永久不再试」，而它明明只是等下一轮。
func TestLoopRateLimitsDownloads(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{
		wantWork("SSIS-001", upstreamMagnet()),
		wantWork("SSIS-002", upstreamMagnet()),
	}}
	mt := &fakeMTeam{rows: hitRow("1", "SSIS-001 x", false, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	// 搜索会命中两个番号（同一行匹配得上吗？匹配不上 —— 见下）。
	// 因此这里让两个作品共用同一个番号前缀，保证两条都能命中。
	inner.want[1] = wantWork("SSIS-001", upstreamMagnet())
	f := newFixtureLimited(t, inner, mt, nil, 1) // 每小时只放行 1 个下载

	f.decide(t, 3)

	// 一个下载配额 → 只有一部拿到判定；另一部**没有判定**（留到下一轮）。
	if got := f.store.Len(); got != 1 {
		t.Errorf("判定表里有 %d 条，want 1（闸门只放行了一个下载）", got)
	}
	if got := f.mt.searchCalls.Load(); got != 1 {
		t.Errorf("搜索了 %d 次 —— 闸门关了就该停下，而不是继续搜下一个", got)
	}
}

// TestLoopSurvivesWantListFailure 钉住「不退出进程」。
//
// 读清单失败（没 token / 上游坏）⇒ 跳过本轮 + WARN。退出会把一次上游抖动
// 变成一次重启循环，而它并没有那么严重。
func TestLoopSurvivesWantListFailure(t *testing.T) {
	inner := &fakeInner{wantErr: errors.New("上游炸了")}
	f := newFixture(t, inner, &fakeMTeam{}, nil)

	f.decide(t, 1) // 不 panic、不退出

	if got := f.store.Len(); got != 0 {
		t.Errorf("读不到清单时不该产生判定，实际 %d 条", got)
	}
	if !strings.Contains(f.log.String(), "跳过本轮") {
		t.Errorf("日志里应当说清楚跳过了这一轮：%s", f.log.String())
	}
}

// TestLoopOffWhenIntervalZero 钉住「0 表示关闭」，而且关闭是**真正的空操作**。
//
// ⚠️ 连清单都不去读 —— 否则「关掉」会先偷偷打一发上游请求
// （health.Run 有同一条判断）。
func TestLoopOffWhenIntervalZero(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{rows: hitRow("1", "SSIS-001 x", true, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	f := newFixture(t, inner, mt, nil)
	f.loop.interval = func() time.Duration { return 0 }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.loop.Run(ctx); close(done) }()
	time.Sleep(120 * time.Millisecond)
	cancel()
	<-done

	if got := inner.wantCalls.Load(); got != 0 {
		t.Errorf("关闭时读了 %d 次清单 —— 关闭应当是真正的空操作", got)
	}
	if got := mt.searchCalls.Load(); got != 0 {
		t.Errorf("关闭时打了 %d 次 M-Team", got)
	}
}

// TestDownloadFailureIsRecordedOnce 是票 03 评审那个 P1 的钉子（已随分工搬到 Loop）。
//
// 当时**搜索命中之后**的失败只打了一条日志就 return，什么都没记 ——
// 于是那部作品每轮都会重新搜索、重新下载，而下载配额是 100 个/小时。
func TestDownloadFailureIsRecordedOnce(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{
		rows:         hitRow("1", "SSIS-001 x", true, "2021-01-01 00:00:00"),
		downloadPath: "/api/torrent/gone", // 取种子会 404
	}
	f := newFixture(t, inner, mt, nil)

	f.decide(t, 3)

	if got := mt.searchCalls.Load(); got != 1 {
		t.Errorf("搜索了 %d 次 —— 取种子失败也不该每轮重来", got)
	}
	v, ok := f.store.Get("id-SSIS-001")
	if !ok || v.Outcome != OutcomeFailed {
		t.Errorf("应当留下一条失败记录，实际 %+v（存在=%v）", v, ok)
	}
}

// TestUnusableTorrentIsRecordedAsFailure 确认「种子认不出来」也降级 + 记失败，
// 而不是发出一个坏 guid，也不是每轮重下。
func TestUnusableTorrentIsRecordedAsFailure(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{
		rows:    hitRow("1", "SSIS-001 x", false, "2021-01-01 00:00:00"),
		torrent: torrentV2Only, // 认不出来的种子
	}
	f := newFixture(t, inner, mt, nil)
	f.decide(t, 2)

	v, ok := f.store.Get("id-SSIS-001")
	if !ok || v.Outcome != OutcomeFailed {
		t.Fatalf("认不出的种子应当留下失败记录，实际 %+v（存在=%v）", v, ok)
	}
	list, err := f.src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if m := list.Works[0].Magnets[0]; m.Infohash != "upstream-hash" {
		t.Errorf("应当降级成上游那条，实际 %+v", m)
	}
}

// TestCredentialFailureIsVisibleFromTheStore 钉住「凭据坏了」是**推导**出来的，
// 而不是额外存的一份状态 —— 因此重启之后仍然成立。
func TestCredentialFailureIsVisibleFromTheStore(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{errBody: `{"code":"1","message":"key無效"}`}
	f := newFixture(t, inner, mt, nil)

	if _, _, broken := f.store.CredentialFailure(); broken {
		t.Error("还没查过就不该报「凭据坏了」")
	}
	f.decide(t, 1)

	msg, at, broken := f.store.CredentialFailure()
	if !broken {
		t.Fatal("凭据失效应当能从判定表里读出来")
	}
	if msg == "" || at.IsZero() {
		t.Errorf("应当带上原因与时刻：msg=%q at=%v", msg, at)
	}

	// ⚠️ 关掉再打开一次 —— 这条测试的卖点就是「重启之后仍然成立」，
	// 不真的重启一遍的话，它只证明了内存里能读到（评审指出过这一点）。
	if err := f.store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := Open(f.store.dir)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if _, _, broken := reopened.CredentialFailure(); !broken {
		t.Error("重启之后「凭据坏了」就丢了 —— 那正是推导式设计要避免的")
	}
}

// TestThreeOutcomesAreDistinguishable 钉住三种结果在判定表里分得开。
//
// 它们的处置**完全相反**：命中就用它；负结果永不重问；失败也不自动重试，
// 但它是唯一一个「换掉 key 之后值得再试一次」的。
func TestThreeOutcomesAreDistinguishable(t *testing.T) {
	tests := []struct {
		name string
		mt   *fakeMTeam
		want Outcome
		kind FailureKind
	}{
		{"命中", &fakeMTeam{rows: hitRow("1", "SSIS-001 x", false, "2021-01-01 00:00:00"), torrent: torrentCanonical}, OutcomeHit, ""},
		{"负结果", &fakeMTeam{rows: hitRow("1", "ABP-123 别的片", false, "2021-01-01 00:00:00")}, OutcomeMiss, ""},
		{"查询失败（网络）", &fakeMTeam{errBody: `这不是 JSON`}, OutcomeFailed, FailureOther},
		{"查询失败（凭据）", &fakeMTeam{errBody: `{"code":"1","message":"key無效"}`}, OutcomeFailed, FailureCredential},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
			f := newFixture(t, inner, tt.mt, nil)
			f.decide(t, 1)

			v, ok := f.store.Get("id-SSIS-001")
			if !ok {
				t.Fatalf("没有留下判定")
			}
			if v.Outcome != tt.want {
				t.Errorf("outcome = %q, want %q", v.Outcome, tt.want)
			}
			if v.FailureKind != tt.kind {
				t.Errorf("failureKind = %q, want %q", v.FailureKind, tt.kind)
			}
		})
	}
}

// TestMissIsNotWordedAsFailure 钉住措辞。
//
// 「负结果」与「查询失败」的处置完全相反，把前者写成「失败」会让人去查一个
// 根本不存在的故障 —— 而这条日志是排障时唯一会读的那条。
func TestMissIsNotWordedAsFailure(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{rows: hitRow("1", "ABP-123 别的片", false, "2021-01-01 00:00:00")}
	f := newFixture(t, inner, mt, nil)

	f.decide(t, 1)

	out := f.log.String()
	if !strings.Contains(out, "负结果") {
		t.Errorf("日志里应当说清楚这是负结果：%s", out)
	}
	if strings.Contains(out, "失败") {
		t.Errorf("负结果不该被写成「失败」—— 那是另一回事：%s", out)
	}
}

// TestDropFailuresClearsOnlyFailures 钉住自愈那条路的两半：
// 失败会被清掉（换 key 之后它们该重查），而**负结果不会被清**（它是判决，永久）。
func TestDropFailuresClearsOnlyFailures(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	store.Put("a", Verdict{Outcome: OutcomeMiss})
	store.Put("b", Verdict{Outcome: OutcomeFailed, Failure: "网络", FailureKind: FailureOther})
	store.Put("c", Verdict{Outcome: OutcomeFailed, Failure: "key無效", FailureKind: FailureCredential})
	store.Put("d", Verdict{Outcome: OutcomeHit, Chosen: Candidate{Infohash: "h"}})

	if n := store.DropFailures(); n != 2 {
		t.Errorf("清掉了 %d 条，want 2（只有失败）", n)
	}
	for _, id := range []string{"a", "d"} {
		if _, ok := store.Get(id); !ok {
			t.Errorf("%q 被误删了 —— 负结果与命中都不是失败", id)
		}
	}
	for _, id := range []string{"b", "c"} {
		if _, ok := store.Get(id); ok {
			t.Errorf("%q 应当被清掉（失败不是判决，换 key 后它该重查）", id)
		}
	}
	if _, _, broken := store.CredentialFailure(); broken {
		t.Error("失败被清掉之后不该再报「凭据坏了」")
	}
}

// --- 装配与身份 ---------------------------------------------------------------

// TestWiringOrderMTorrentBeatsPin 是**装配顺序**的钉子。
//
// 正确的嵌套是 `pin.New(Source(inner))`：
//
//	Source 要看的输入是**上游刚返回的**候选；
//	而它要看的「下发过没有」是 pin 表**上一次**的状态。
//
// 反过来包的话（Source 在外），它第一件事就是调 pin，而 pin 会当场把未钉住的作品
// 全选一条上游磁链写进表 —— 等控制流回来，`pins.Get` 已经**恒为真**，
// 「首发冻结」于是变成「永远冻结在上游磁链」，整个功能静默失效。
// 这个 bug 被两轴评审抓到，而当时的单测没抓到（它们用的是裸假 source）。
func TestWiringOrderMTorrentBeatsPin(t *testing.T) {
	dir := t.TempDir()
	pinStore, err := pin.Open(filepath.Join(dir, "pin.json"))
	if err != nil {
		t.Fatalf("Open pin: %v", err)
	}
	defer func() { _ = pinStore.Close() }()

	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{rows: hitRow("467487", "SSIS-001 一ヶ月間の…", true, "2021-02-22 15:19:51"), torrent: torrentCanonical}
	f := newFixture(t, inner, mt, nil)
	f.decide(t, 1) // 先让后台把判定做出来

	// 按**真实装配**的顺序包：mteam 在内，pin 在外。
	src := pin.New(New(inner, f.store, pinStore, quietLogger()), pinStore, nil)

	works, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if len(works.Works) != 1 || len(works.Works[0].Magnets) != 1 {
		t.Fatalf("候选池应当是单元素，实际 %+v", works.Works)
	}

	// 一、M-Team 真的生效了（上游那条被换掉）。
	m := works.Works[0].Magnets[0]
	if m.Source != catalog.SourceMTeam {
		t.Fatalf("下发的是 %v 那条 —— M-Team 根本没生效（装配顺序错了？）", m.Source)
	}
	// 二、pin 钉住的是 **M-Team 那条**，而不是它先看到的上游那条。
	rec, ok := pinStore.Get("id-SSIS-001")
	if !ok {
		t.Fatal("pin 表里没有这部作品")
	}
	if rec.Infohash != m.Infohash {
		t.Errorf("pin 钉的是 %q，而下发的是 %q —— pin 与 feed 不一致", rec.Infohash, m.Infohash)
	}
}

// TestVerdictSurvivesRestart 确认判定真的落了盘（不是只活在内存里）。
func TestVerdictSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	mt := &fakeMTeam{rows: hitRow("1", "SSIS-001 x", true, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	srv := httptest.NewServer(mt.handler(t))
	defer srv.Close()
	mt.base = srv.URL

	run := func() {
		store, err := Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = store.Close() }()
		client := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
		loop := NewLoop(inner, client, store, fakePins{}, func() time.Duration { return time.Minute }, 0, quietLogger())
		loop.Once(context.Background())
	}
	run()
	run() // 第二次：判定应当来自磁盘，因此不再搜

	if got := mt.searchCalls.Load(); got != 1 {
		t.Errorf("搜索了 %d 次，重启后应当从判定表里读", got)
	}
}

// TestOtherRoutesAreUntouched 钉住「只改一条路由」。
func TestOtherRoutesAreUntouched(t *testing.T) {
	inner := &fakeInner{}
	mt := &fakeMTeam{rows: hitRow("1", "x", true, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	f := newFixture(t, inner, mt, nil)

	if _, err := f.src.Code(context.Background(), "SSIS-001"); err != nil {
		t.Fatalf("Code: %v", err)
	}
	if got := mt.searchCalls.Load(); got != 0 {
		t.Errorf("番号路由不该碰馒头，却查了 %d 次", got)
	}
	if got := inner.codeCalls.Load(); got != 1 {
		t.Errorf("番号路由应当原样透传，实际调用 %d 次", got)
	}
}

// TestUnknownOutcomeIsNotSilentlySkipped 钉住「认不出的结果要说一声」。
//
// 静默跳过会让那部作品永远拿不到 M-Team，而且看不出原因。
func TestUnknownOutcomeIsNotSilentlySkipped(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet())}}
	f := newFixture(t, inner, &fakeMTeam{}, nil)
	f.store.Put("id-SSIS-001", Verdict{Outcome: Outcome("手改的")})

	list, err := f.src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	// 按「未判定」处理：扣住，并计入待定来源。
	if list.PendingSource != 1 {
		t.Errorf("PendingSource = %d, want 1", list.PendingSource)
	}
	if !strings.Contains(f.log.String(), "认不出") {
		t.Errorf("日志里应当说一声：%s", f.log.String())
	}
}

// TestUnwritableStateDirRefusesToStart 钉住 fail fast：判定表目录不可写就拒绝启动。
func TestUnwritableStateDirRefusesToStart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("以 root 跑时目录权限拦不住写入")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(dir, "state")); err == nil {
		t.Fatal("不可写的状态目录必须拒绝启动")
	}
}
