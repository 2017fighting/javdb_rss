package mteam

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

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

// --- 装饰器 -------------------------------------------------------------------

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
	// 每次都返回一份**新的**切片：装饰器会原地改写它。
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
	// err 非空时，搜索返回一个失败信封。
	errBody string
	// torrent 是下载返回的字节。
	torrent string
	// base 是这个假服务器的地址，由 newFixture 在起服务之后填 ——
	// 下载链接必须是**绝对**地址（真的馒头给的就是绝对的）。
	base string
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
			_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":"`+f.base+`/api/rss/dlv2"}`)
		case "/api/rss/dlv2":
			_, _ = io.WriteString(w, f.torrent)
		default:
			t.Errorf("假馒头收到意外路径 %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func newFixture(t *testing.T, inner *fakeInner, mt *fakeMTeam, pinned map[string]bool) (*Source, *Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(mt.handler(t))
	t.Cleanup(srv.Close)
	mt.base = srv.URL

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	client := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	return New(inner, client, store, fakePins{pinned: pinned}, quietLogger()), store, srv
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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

// TestHitReplacesThePoolWithThisServicesTorrent 是本票的主路径。
func TestHitReplacesThePoolWithThisServicesTorrent(t *testing.T) {
	upstreamMagnet := catalog.Magnet{Infohash: "upstream-hash", Name: "上游那条"}
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", upstreamMagnet)}}
	mt := &fakeMTeam{
		rows:    hitRow("467487", "SSIS-001 一ヶ月間の…", true, "2021-02-22 15:19:51"),
		torrent: torrentCanonical,
	}
	src, _, _ := newFixture(t, inner, mt, nil)

	list, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
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

// TestHitIsCachedAcrossRenders 确认判定落地之后不再重问馒头。
//
// 这条不是优化：qBittorrent 每 15 分钟拉一次，而馒头的搜索配额是 1000 次/24 小时。
func TestHitIsCachedAcrossRenders(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", catalog.Magnet{Infohash: "u"})}}
	mt := &fakeMTeam{rows: hitRow("1", "SSIS-001 x", false, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	src, _, _ := newFixture(t, inner, mt, nil)

	for i := 0; i < 3; i++ {
		if _, err := src.WantToWatch(context.Background()); err != nil {
			t.Fatalf("第 %d 次: %v", i, err)
		}
	}
	if got := mt.searchCalls.Load(); got != 1 {
		t.Errorf("搜索了 %d 次，判定落地后应当只搜一次", got)
	}
}

// TestPinnedWorkIsNotEvenQueried 钉住「首发冻结」。
//
// 「有钉住 ⇒ 连问都不问」不只是省一次请求：它保证**已经下发过的作品不会换来源**，
// 而换来源 = 磁盘上多一份。
func TestPinnedWorkIsNotEvenQueried(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", catalog.Magnet{Infohash: "u"})}}
	mt := &fakeMTeam{rows: hitRow("1", "SSIS-001 x", true, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	src, _, _ := newFixture(t, inner, mt, map[string]bool{"id-SSIS-001": true})

	list, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if got := mt.searchCalls.Load(); got != 0 {
		t.Errorf("已钉住的作品被查了 %d 次，应当一次都不查", got)
	}
	if m := list.Works[0].Magnets[0]; m.Source != catalog.SourceUpstream {
		t.Errorf("已钉住的作品不该被换来源，实际 %v", m.Source)
	}
}

// TestNoMatchLeavesTheUpstreamMagnet 确认「馒头没有」不改变任何东西。
func TestNoMatchLeavesTheUpstreamMagnet(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", catalog.Magnet{Infohash: "u"})}}
	mt := &fakeMTeam{rows: hitRow("1", "ABP-123 别的片", true, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	src, _, _ := newFixture(t, inner, mt, nil)

	list, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if m := list.Works[0].Magnets[0]; m.Infohash != "u" || m.Source != catalog.SourceUpstream {
		t.Errorf("没有命中时应当原样发上游那条，实际 %+v", m)
	}
}

// TestMTeamFailureDoesNotBreakTheFeed 钉住「第二个来源出问题不该让整条 feed 失败」。
//
// 三种坏法都试：凭据无效、网络层失败、上游给了看不懂的响应。
func TestMTeamFailureDoesNotBreakTheFeed(t *testing.T) {
	tests := []struct {
		name string
		mt   *fakeMTeam
	}{
		{"凭据无效", &fakeMTeam{errBody: `{"code":"1","message":"key無效"}`}},
		{"上游给了看不懂的响应", &fakeMTeam{errBody: `这不是 JSON`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", catalog.Magnet{Infohash: "u"})}}
			tt.mt.torrent = torrentCanonical
			src, _, _ := newFixture(t, inner, tt.mt, nil)

			list, err := src.WantToWatch(context.Background())
			if err != nil {
				t.Fatalf("M-Team 失败不该让整条 feed 失败，却得到 %v", err)
			}
			if m := list.Works[0].Magnets[0]; m.Infohash != "u" {
				t.Errorf("失败时应当降级成上游那条，实际 %+v", m)
			}
		})
	}
}

// TestUnusableTorrentFallsBack 确认「种子认不出来」也降级，而不是发出一个坏 guid。
func TestUnusableTorrentFallsBack(t *testing.T) {
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", catalog.Magnet{Infohash: "u"})}}
	mt := &fakeMTeam{
		rows:    hitRow("1", "SSIS-001 x", false, "2021-01-01 00:00:00"),
		torrent: torrentV2Only, // 认不出来的种子
	}
	src, store, _ := newFixture(t, inner, mt, nil)

	list, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if m := list.Works[0].Magnets[0]; m.Infohash != "u" {
		t.Errorf("认不出的种子应当降级，实际 %+v", m)
	}
	// 而且**不该**留下一条判定 —— 否则它会被当成「查过、有」永久记下来。
	if _, ok := store.Get("id-SSIS-001"); ok {
		t.Error("认不出的种子不该留下判定")
	}
}

// TestInnerErrorPropagates 确认上游自己的错仍然照原样报出去。
func TestInnerErrorPropagates(t *testing.T) {
	inner := &fakeInner{wantErr: errors.New("上游炸了")}
	src, _, _ := newFixture(t, inner, &fakeMTeam{}, nil)
	if _, err := src.WantToWatch(context.Background()); err == nil {
		t.Fatal("上游的错应当原样报出去")
	}
}

// TestOtherRoutesAreUntouched 钉住「只改一条路由」。
func TestOtherRoutesAreUntouched(t *testing.T) {
	inner := &fakeInner{}
	mt := &fakeMTeam{rows: hitRow("1", "x", true, "2021-01-01 00:00:00"), torrent: torrentCanonical}
	src, _, _ := newFixture(t, inner, mt, nil)

	if _, err := src.Code(context.Background(), "SSIS-001"); err != nil {
		t.Fatalf("Code: %v", err)
	}
	if got := mt.searchCalls.Load(); got != 0 {
		t.Errorf("番号路由不该碰馒头，却查了 %d 次", got)
	}
	if got := inner.codeCalls.Load(); got != 1 {
		t.Errorf("番号路由应当原样透传，实际调用 %d 次", got)
	}
}

// TestVerdictSurvivesRestart 确认判定真的落了盘（不是只活在内存里）。
func TestVerdictSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	inner := &fakeInner{want: []catalog.Work{wantWork("SSIS-001", catalog.Magnet{Infohash: "u"})}}
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
		src := New(inner, client, store, fakePins{}, quietLogger())
		if _, err := src.WantToWatch(context.Background()); err != nil {
			t.Fatalf("WantToWatch: %v", err)
		}
	}
	run()
	run() // 第二次：判定应当来自磁盘，因此不再搜

	if got := mt.searchCalls.Load(); got != 1 {
		t.Errorf("搜索了 %d 次，重启后应当从判定表里读", got)
	}
}

// TestWiringOrderMTorrentBeatsPin 是**装配顺序**的钉子。
//
// 它存在的理由很具体：两轴评审在这里抓到一个 P0，而当时所有单测都是绿的 ——
// 因为它们用裸假 source 当 inner，正好绕过了「mteam 与 pin 谁包谁」这条链。
//
// 正确的嵌套是 `pin.New(mteam.New(inner))`：
//
//	M-Team 要看的输入是**上游刚返回的**候选；
//	而它要看的「下发过没有」是 pin 表**上一次**的状态。
//
// 反过来包（mteam 在外）的话，mteam 一上来就调 pin，而 pin 会当场把未钉住的作品
// 全选一条上游磁链写进表 —— 等控制流回到 mteam，`pins.Get` 已经**恒为真**，
// 「首发冻结」于是变成「永远冻结在上游磁链」，整个功能静默失效。
func TestWiringOrderMTorrentBeatsPin(t *testing.T) {
	dir := t.TempDir()
	pinStore, err := pin.Open(filepath.Join(dir, "pin.json"))
	if err != nil {
		t.Fatalf("Open pin: %v", err)
	}
	defer func() { _ = pinStore.Close() }()

	mtStore, err := Open(filepath.Join(dir, "mteam"))
	if err != nil {
		t.Fatalf("Open 判定表: %v", err)
	}
	defer func() { _ = mtStore.Close() }()

	mt := &fakeMTeam{
		rows:    hitRow("467487", "SSIS-001 一ヶ月間の…", true, "2021-02-22 15:19:51"),
		torrent: torrentCanonical,
	}
	srv := httptest.NewServer(mt.handler(t))
	defer srv.Close()
	mt.base = srv.URL

	inner := &fakeInner{want: []catalog.Work{
		wantWork("SSIS-001", catalog.Magnet{Infohash: "upstream-hash", Name: "上游那条"}),
	}}
	client := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}

	// 按**真实装配**的顺序包：mteam 在内，pin 在外。
	mtSrc := New(inner, client, mtStore, pinStore, quietLogger())
	src := pin.New(mtSrc, pinStore, nil) // 正确
	_ = mtSrc

	works, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if len(works.Works) != 1 || len(works.Works[0].Magnets) != 1 {
		t.Fatalf("候选池应当是单元素，实际 %+v", works.Works)
	}

	// 一、M-Team 真的被查了（上游那条被换掉）。
	m := works.Works[0].Magnets[0]
	if m.Source != catalog.SourceMTeam {
		t.Fatalf("下发的是 %v 那条 —— M-Team 根本没生效（装配顺序错了？）", m.Source)
	}
	if m.Infohash != "f875b7bedb38e072dde96440fb79e5fc55699d68" {
		t.Errorf("infohash = %q", m.Infohash)
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
