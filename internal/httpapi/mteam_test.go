package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/mteam"
	"github.com/2017fighting/javdb_rss/internal/pin"
)

// 这份夹具与 internal/mteam/torrent_test.go 里的 `torrentCanonical` **逐字节相同**，
// 期望的 infohash 也是那边用 python 的 hashlib 独立算出来的同一个值。
//
// 两处各写一份是刻意的：这张测试要证明的是**跨包的接缝**
// （HTTP → M-Team → bencode → 判定表 → 再经 HTTP 端出），
// 而它断言的 infohash 必须是一个独立真值，不能是从 mteam 包里读出来的常量 ——
// 那样两个包就一起错了。
const e2eTorrent = "d8:announce47:https://tra1.m-team.cc/announce?credential=YWJj13:creation datei1791873283e4:infod6:lengthi1024e4:name5:a.mp412:piece lengthi16384e6:pieces20:012345678901234567897:privatei1eee"
const e2eInfohash = "f875b7bedb38e072dde96440fb79e5fc55699d68"

// TestWantFeedServesMTorrentEndToEnd 是本轮（票 02）的主路径，一次穿过五层：
//
//	配置 → M-Team 客户端（假服务器）→ bencode 算 infohash → 判定表 → HTTP 端点
//
// 它断言的是用户真正看到的两样东西：feed 里那个 `guid`，以及从 `<enclosure>`
// 取回来的字节。中间任何一层写错，这两样里至少有一个会不对。
func TestWantFeedServesMTorrentEndToEnd(t *testing.T) {
	// 假馒头：搜索返回一行命中，下载返回那份夹具。
	var base string
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/torrent/search":
			_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":{"data":[
				{"id":"467487","name":"SSIS-001 一ヶ月間の…","size":"6834984408",
				 "labelsNew":["中字"],"createdDate":"2021-02-22 15:19:51","infoHash":null}
			]}}`)
		case "/api/torrent/genDlToken":
			_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":"`+base+`/api/rss/dlv2"}`)
		case "/api/rss/dlv2":
			_, _ = io.WriteString(w, e2eTorrent)
		default:
			t.Errorf("假馒头收到意外路径 %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mt.Close()
	base = mt.URL

	// 真的判定表（落临时目录），真的 M-Team 装饰器，假的上游。
	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open 判定表: %v", err)
	}
	defer func() { _ = store.Close() }()

	// 钉住表也用一个真的（空的）—— 装饰器要问它「这部作品下发过没有」，
	// 而那个问题的答案就是「首发冻结」的全部依据。
	pins, err := pin.Open(filepath.Join(t.TempDir(), "pin.json"))
	if err != nil {
		t.Fatalf("Open 钉住表: %v", err)
	}
	defer func() { _ = pins.Close() }()

	inner := &recordingSource{want: &catalog.WantList{Works: []catalog.Work{
		wantWork("SSIS-001", catalog.Magnet{Infohash: "上游那条", Name: "上游那条"}),
	}}}
	client := &mteam.Client{BaseURL: mt.URL, APIKey: "k", HTTP: mt.Client()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	src := mteam.New(inner, store, pins, log)

	// 判定由**后台**做（票 04）—— 请求路径不再查 M-Team。
	loop := mteam.NewLoop(inner, client, store, pins,
		func() time.Duration { return time.Minute }, 0, log)
	loop.Once(context.Background())

	const baseURL = "http://javdb-rss.javdb-rss.svc.cluster.local:8080"
	h := newTestServerWithSource(t, "provider: stub\nfeed:\n  base_url: \""+baseURL+"\"\n", src, store)

	// 第一步：feed 里那一行。
	got := parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())
	if len(got.Channel.Items) != 1 {
		t.Fatalf("得到 %d 条条目，want 1", len(got.Channel.Items))
	}
	item := got.Channel.Items[0]

	// guid 必须是**从种子字节算出来**的 infohash —— 搜索接口根本不返回它。
	if item.GUID != e2eInfohash {
		t.Errorf("guid = %q, want %q（它应当是从馒头那份种子里算出来的）", item.GUID, e2eInfohash)
	}
	// enclosure 必须指向本服务，而不是馒头那个五分钟就过期的链接。
	wantURL := baseURL + "/torrent/" + e2eInfohash + ".torrent"
	if item.Enclosure.URL != wantURL {
		t.Errorf("enclosure = %q, want %q", item.Enclosure.URL, wantURL)
	}

	// 第二步：把那个地址取回来 —— 这正是 qBittorrent 会做的事。
	rec := do(t, h, "/torrent/"+e2eInfohash+".torrent")
	if rec.Code != http.StatusOK {
		t.Fatalf("取种子 = %d，body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("content-type"); ct != "application/x-bittorrent" {
		t.Errorf("content-type = %q —— qBittorrent 只认这个值", ct)
	}
	if body := rec.Body.String(); body != e2eTorrent {
		t.Errorf("取回的字节与馒头给的不一致（长度 %d vs %d）", len(body), len(e2eTorrent))
	}
}

// TestTorrentEndpointRejectsTraversal 钉住那个值会被拼进**文件路径**这件事。
//
// ⚠️ 两道防线，而**这条测试必须绕过 mux 才测得动第一道**：
//
//	第一道：net/http 的 ServeMux 会先清洗路径并 307 重定向（于是 `..` 根本到不了 handler）。
//	第二道：handler 自己的白名单（40 位十六进制）。
//
// 只走 mux 的话，第一道已经把脏路径拦了 —— 就算把第二道整行删掉，测试照样绿
// （因为 `/etc/passwd.torrent` 本来就不存在，os.ReadFile 也会 404）。
// 评审指出了这个假阳性，所以这里直接调 handler，并**放一个金丝雀文件**：
// 它在 `torrents/../canary.torrent`，也就是 `LoadTorrent("../canary")` 恰好能读到的地方。
// 白名单真在工作时，那个文件是拿不到的。
func TestTorrentEndpointRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := mteam.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	// 金丝雀：一份“如果穿越成立就会被端出去”的种子。
	canary := "金丝雀，不该被端出去"
	canaryPath := filepath.Join(dir, "canary.torrent")
	if err := os.WriteFile(canaryPath, []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newTestServerObj(t, "provider: stub\n", &recordingSource{}, store)

	tests := []struct {
		name string
		path string
	}{
		{"相对路径穿越到金丝雀", "/torrent/../canary.torrent"},
		{"编码过的穿越", "/torrent/..%2fcanary.torrent"},
		{"非十六进制", "/torrent/不是十六进制.torrent"},
		{"短一位", "/torrent/" + strings.Repeat("a", 39) + ".torrent"},
		{"长一位", "/torrent/" + strings.Repeat("a", 41) + ".torrent"},
		{"非十六进制字符", "/torrent/" + strings.Repeat("z", 40) + ".torrent"},
		{"忘了 .torrent 后缀", "/torrent/" + strings.Repeat("a", 40)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 直接构造请求并调 handler —— 不过 mux，因为 mux 会先清洗路径。
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			s.handleTorrent(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", tt.path, rec.Code)
			}
			if strings.Contains(rec.Body.String(), canary) {
				t.Errorf("GET %s 端出了金丝雀的内容 —— 目录穿越成立了", tt.path)
			}
		})
	}
}

// TestTorrentEndpointUnknownHashIs404 确认「格式对但没有这个种子」是 404，
// 而不是 500 —— 「确实不存在」与「坏了」在排查时是两件事。
func TestTorrentEndpointUnknownHashIs404(t *testing.T) {
	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	h := newTestServerWithSource(t, "provider: stub\n", &recordingSource{}, store)

	// 形态合法（40 位十六进制）但表里没有。
	rec := do(t, h, "/torrent/"+strings.Repeat("a", 40)+".torrent")
	if rec.Code != http.StatusNotFound {
		t.Errorf("= %d, want 404", rec.Code)
	}
}

// TestTorrentEndpointWithoutMTeamIs404 确认「没配第二个来源」也是 404 而不是 500 ——
// 「确实不存在」与「坏了」在排查时是两件事。
func TestTorrentEndpointWithoutMTeamIs404(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &recordingSource{})
	if rec := do(t, h, "/torrent/"+e2eInfohash+".torrent"); rec.Code != http.StatusNotFound {
		t.Errorf("= %d, want 404", rec.Code)
	}
}

// TestTorrentEndpointRejectsNonGET 确认路由只认 GET。
func TestTorrentEndpointRejectsNonGET(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &recordingSource{})
	req := httptest.NewRequest(http.MethodPost, "/torrent/"+e2eInfohash+".torrent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
}

// storeStatus 是 MTeamStatus 的替身：凭据那条**转发给真的判定表**
// （那条状态是从表里推导出来的，用假的会把这条测试的卖点测没），
// 而进度那几个读数由测试直接给。
type storeStatus struct {
	*mteam.Store
	prog mteam.Progress
}

func (s storeStatus) Progress() mteam.Progress {
	// 计数那部分**转发给判定表**（真实的 Loop 也是这么填的）——
	// 否则这个替身会把「判定表里有多少条」这件事测没。
	p := s.prog
	p.Stats = s.Store.Stats()
	return p
}

// TestUpstreamDetailReportsMTeamCredential 钉住第二个来源的凭据信号。
//
// 它必须与「上游坏了」**分得开**：一个重试有用，一个要人去控制台换 key。
// 而它**不改变状态码** —— M-Team 坏了不该让本实例被摘出 Service 端点。
func TestUpstreamDetailReportsMTeamCredential(t *testing.T) {
	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	s := newTestServerObj(t, "provider: stub\n", &recordingSource{}, store)
	s.mteamStatus = storeStatus{Store: store}

	// 还没查过：字段在，但为假（与 checked 那条既有判断同构 ——
	// 「从未判定过」与「判定过且没坏」不是一回事，这里都表现为 false，
	// 而真正的区分在 /metrics 的判定计数里，见票 06）。
	var body map[string]any
	rec := do(t, s.Handler(), "/healthz/upstream")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("不是 JSON: %v", err)
	}
	m, ok := body["mteam"].(map[string]any)
	if !ok {
		t.Fatalf("响应里应当有一个单独命名的 mteam 段：%v", body)
	}
	if m["credential_broken"] != false {
		t.Errorf("credential_broken = %v, want false", m["credential_broken"])
	}

	// 记一条凭据失败。
	store.Put("w1", mteam.Verdict{
		Outcome:     mteam.OutcomeFailed,
		Failure:     "M-Team 凭据无效：key無效",
		FailureKind: mteam.FailureCredential,
		At:          time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC),
	})

	rec = do(t, s.Handler(), "/healthz/upstream")
	body = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("不是 JSON: %v", err)
	}
	m = body["mteam"].(map[string]any)
	if m["credential_broken"] != true {
		t.Errorf("credential_broken = %v, want true", m["credential_broken"])
	}
	// ⚠️ 断言的是**文案里的关键词**，不是 `mteam.CredentialGuidance` 那个常量 ——
	// 拿被测实现自己的常量当期望值是同义反复：那个常量被改成空字符串也照样绿。
	// （本文件开头就写着这条标准，而它当时被我自己违反了。）
	nextStep, _ := m["next_step"].(string)
	for _, want := range []string{"控制台", "存取令牌", "重试无用"} {
		if !strings.Contains(nextStep, want) {
			t.Errorf("next_step 里应当有 %q，实际是 %q", want, nextStep)
		}
	}
	if !strings.Contains(m["error"].(string), "key無效") {
		t.Errorf("error = %v，应当带上上游的原话", m["error"])
	}

	// ⚠️ 状态码不受它影响：M-Team 坏了不该把本实例摘出 Service 端点。
	if rec.Code != http.StatusOK {
		t.Errorf("状态码 = %d —— M-Team 的凭据问题不该改变它", rec.Code)
	}
	// 而 /readyz 也不该因此变 503。
	if r := do(t, s.Handler(), "/readyz"); r.Code != http.StatusOK {
		t.Errorf("/readyz = %d，M-Team 的凭据问题不该影响就绪", r.Code)
	}
}

// TestTypedNilStoreBehavesLikeNoStore 钉住一个 Go 的陷阱，而它**真的崩过**。
//
// 没配 M-Team 时 main 传下来的是 `var st *mteam.Store`（nil）——
// 但把它交给一个**接口**参数，得到的接口**不是 nil**。于是
// `s.torrents != nil` 为真，handler 会去调一个 nil 接收者的方法 → SIGSEGV，
// 每一次 `/torrent/` 请求都崩。
//
// 修法是两层的：装配层不再挂 nil（`WithSecondSource`），而 mteam.Store 的读方法
// 对 nil 接收者返回「没有」而不是崩。这条测试钉的是**第二层** ——
// 因为第一层随时可能被下一个改装配的人绕过。
func TestTypedNilStoreBehavesLikeNoStore(t *testing.T) {
	var nilStore *mteam.Store
	s := newTestServerObj(t, "provider: stub\n", &recordingSource{}, nilStore)
	// ⚠️ 这里**故意**把一个类型化 nil 塞进接口字段（而不是走 WithSecondSource）——
	// 那正是当初 SIGSEGV 的形状，而这一条钉的是**第二层**兜底。
	s.mteamStatus = storeStatus{Store: nilStore}

	if rec := do(t, s.Handler(), "/torrent/"+e2eInfohash+".torrent"); rec.Code != http.StatusNotFound {
		t.Errorf("/torrent/ = %d, want 404（nil 的判定表就是「没有第二个来源」）", rec.Code)
	}
	rec := do(t, s.Handler(), "/healthz/upstream")
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz/upstream = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("不是 JSON: %v", err)
	}
	if m, ok := body["mteam"].(map[string]any); ok && m["credential_broken"] != false {
		t.Errorf("nil 的判定表不该报「凭据坏了」：%v", m)
	}
}

// TestWithSecondSourceIgnoresNil 钉住类型化 nil 那件事的**第一层**。
//
// `TestTypedNilStoreBehavesLikeNoStore` 钉的是第二层（Store 的读方法对 nil 接收者
// 安全）—— 但装配层那个 `if st == nil { return s }` 从来没被调用过（评审指出）。
// 第一层被下一个改装配的人绕过时，第二层还得兜着；两层都该有自己的钉子。
func TestWithSecondSourceIgnoresNil(t *testing.T) {
	s := newTestServerObj(t, "provider: stub\n", &recordingSource{}, nil)
	if s.torrents != nil || s.mteamStatus != nil {
		t.Fatal("前置条件：刚构造的 Server 不该挂第二个来源")
	}

	var nilStore *mteam.Store
	s.WithSecondSource(nilStore, nil)

	// ⚠️ 这里断言的是「**没有挂上**」。若 WithSecondSource 少写那个 nil 判断，
	// 这两个接口会变成非 nil（Go 的类型化 nil），于是 /torrent/ 会去调
	// nil 接收者的方法 —— 而那正是当初 SIGSEGV 的形状。
	if s.torrents != nil {
		t.Error("nil 的判定表不该被挂成种子来源（类型化 nil 会让接口非 nil）")
	}
	if s.mteamStatus != nil {
		t.Error("nil 的判定表不该被挂成状态来源")
	}
}

// TestWantFeedWithholdsPendingSourceAndSaysSo 钉住票 04 的扣住语义。
//
// 判定未就绪的作品**不发条目**，但**不是静默**：计数要进 channel 标题 ——
// 因为实测 qBittorrent 的 RSS API 响应里根本没有 description 字段，
// 而它正是这条 feed 的主要消费者。
//
// 它同时是「首屏 < 30 秒」的根据：handler 一次 M-Team 都不打。
func TestWantFeedWithholdsPendingSourceAndSaysSo(t *testing.T) {
	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	// 三个作品：一个已判定（命中），两个没有判定。
	store.Put("id-KV-1", mteam.Verdict{
		Outcome: mteam.OutcomeHit,
		Chosen:  mteam.Candidate{ID: "1", Infohash: e2eInfohash, Name: "KV-1", SizeMiB: 1024},
	})

	src := &recordingSource{want: &catalog.WantList{Works: []catalog.Work{
		wantWork("KV-1", catalog.Magnet{Infohash: "u1"}),
		wantWork("KV-2", catalog.Magnet{Infohash: "u2"}),
		wantWork("KV-3", catalog.Magnet{Infohash: "u3"}),
	}}}
	const base = "http://x"
	// 用真的装饰器，但判定表里只有一条命中。
	h := newTestServerWithSource(t, "provider: stub\nfeed:\n  base_url: \""+base+"\"\n",
		mteam.New(src, store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))), store)

	got := parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())

	// 只发一条（那条已判定的）。
	if len(got.Channel.Items) != 1 {
		t.Fatalf("得到 %d 条条目，want 1（未判定的两条应当被扣住）", len(got.Channel.Items))
	}
	if got.Channel.Items[0].GUID != e2eInfohash {
		t.Errorf("guid = %q", got.Channel.Items[0].GUID)
	}
	// ⚠️ 而扣住**必须说出来**：总数仍是 3 部（用户看到的是他在 App 里标了几部），
	// 另加「2 部待定来源」。
	if !strings.Contains(got.Channel.Title, "3 部") {
		t.Errorf("标题里应当有全部 3 部：%q", got.Channel.Title)
	}
	if !strings.Contains(got.Channel.Title, "2 部待定来源") {
		t.Errorf("标题里应当有「2 部待定来源」：%q", got.Channel.Title)
	}
	if !strings.Contains(got.Channel.Description, "2 部还在判定用哪个来源") {
		t.Errorf("描述里应当给完整句子：%q", got.Channel.Description)
	}
}

// TestWantFeedStaysFastWithALargePendingList 钉住「首屏仍在 30 秒内」这条硬约束。
//
// 一个 200+ 部的清单、且**一部都还没判定** —— 如果判定还在请求路径里，
// 这里会是「200 次搜索 + 最多 200 次种子下载」，分钟级，而 qBittorrent
// 在 30 秒无字节传输时就掐断了。
//
// 这条测试**故意不设超时断言**（墙钟断言在 CI 上很脆）：它断言的是
// **handler 一次 M-Team 都没打**，而那才是首屏快的原因。
func TestWantFeedStaysFastWithALargePendingList(t *testing.T) {
	var calls atomic.Int64
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":{"data":[]}}`)
	}))
	defer mt.Close()

	works := make([]catalog.Work, 0, 240)
	for i := 0; i < 240; i++ {
		works = append(works, wantWork(fmt.Sprintf("KV-%03d", i), catalog.Magnet{Infohash: fmt.Sprintf("h%03d", i)}))
	}
	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	src := &recordingSource{want: &catalog.WantList{Works: works}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newTestServerWithSource(t, "provider: stub\n", mteam.New(src, store, nil, log), store)

	got := parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())
	if len(got.Channel.Items) != 0 {
		t.Errorf("一部都没判定，不该发条目，实际 %d 条", len(got.Channel.Items))
	}
	if !strings.Contains(got.Channel.Title, "240 部待定来源") {
		t.Errorf("标题里应当有「240 部待定来源」：%q", got.Channel.Title)
	}
	// ⚠️ 这条断言现在是**结构性**的：`mteam.Source` 里根本没有客户端，
	// 它发不出 M-Team 请求。它的价值不在「跑出 0」，而在**钉住这条设计** ——
	// 哪天有人为了「顺手补一下」把客户端塞回 Source，它就会变成非零。
	// （评审指出原版还挂了一个没人用的 Loop，那是仪式性的，已删。）
	if got := calls.Load(); got != 0 {
		t.Errorf("请求路径打了 M-Team %d 次 —— 一次都不该打", got)
	}
}

// TestPendingSourceCountDropsAsTheLoopProgresses 钉住票面那条「计数随时间下降」。
//
// 它是「扣住」这套设计的**可观察收尾**：未判定的作品先被扣住、计数写在标题里，
// 后台推进一轮之后它们变成有判定的，于是计数降下来、条目发出去。
// 没有这条，前一条测试只证明了「空判定表下扣住 240 部」——
// 而扣住本身可以是**永久**的（那才是真正的故障）。
func TestPendingSourceCountDropsAsTheLoopProgresses(t *testing.T) {
	// 假馒头：搜索**没有**任何命中 ⇒ 两部都会拿到「负结果」。
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":{"data":[]}}`)
	}))
	defer mt.Close()

	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	src := &recordingSource{want: &catalog.WantList{Works: []catalog.Work{
		wantWork("KV-1", catalog.Magnet{Infohash: "u1"}),
		wantWork("KV-2", catalog.Magnet{Infohash: "u2"}),
	}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dec := mteam.New(src, store, nil, log)
	h := newTestServerWithSource(t, "provider: stub\n", dec, store)

	// 一、还没判定：两部都被扣住，标题里说出来。
	got := parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())
	if len(got.Channel.Items) != 0 {
		t.Fatalf("还没判定就不该发条目，实际 %d 条", len(got.Channel.Items))
	}
	if !strings.Contains(got.Channel.Title, "2 部待定来源") {
		t.Fatalf("标题里应当有「2 部待定来源」：%q", got.Channel.Title)
	}

	// 二、后台跑一轮（两部都会被判成负结果）。
	loop := mteam.NewLoop(src, &mteam.Client{BaseURL: mt.URL, APIKey: "k"}, store, nil,
		func() time.Duration { return time.Minute }, 0, log)
	loop.Once(context.Background())

	// 三、计数降下来了，而条目发出来了（负结果按上游下发）。
	got = parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())
	if strings.Contains(got.Channel.Title, "待定来源") {
		t.Errorf("判定完成之后不该再有待定来源：%q", got.Channel.Title)
	}
	if len(got.Channel.Items) != 2 {
		t.Errorf("得到 %d 条条目，want 2（负结果照常发上游那条）", len(got.Channel.Items))
	}
	if !strings.Contains(got.Channel.Title, "2 部") {
		t.Errorf("总数仍是 2 部：%q", got.Channel.Title)
	}
}

// --- 票 06：判定状态要看得见 --------------------------------------------------

// metricsBody 抓一次 /metrics 的正文。
func metricsBody(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := do(t, h, "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d", rec.Code)
	}
	return rec.Body.String()
}

// TestMetricsHasNoMTeamSeriesWithoutSecondSource 钉住「没配就没有序列」。
//
// 与 upstream 那条判断同构：没有可报的东西，就不该凭空造一条「一切正常」的证据 ——
// 一条恒为 0 的 credential_broken 会让「没配 M-Team」看起来像「配了且凭据正常」。
func TestMetricsHasNoMTeamSeriesWithoutSecondSource(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &recordingSource{})
	if body := metricsBody(t, h); strings.Contains(body, "javdb_rss_mteam_") {
		t.Errorf("没配第二个来源时不该有 mteam 序列：\n%s", body)
	}
}

// TestMetricsDistinguishesNeverJudgedFromZeroVerdicts 钉住票面那条最容易做错的判据。
//
// 「一条判定都没有」与「判定过、三个 outcome 都是 0」在**计数**上长得一模一样 ——
// 所以必须有单独的一条（judged），而且时间戳在从未判定时**不出现**：
// 报 0 等于说「1970 年判定过」，而 staleness 类规则正是拿它算的。
func TestMetricsDistinguishesNeverJudgedFromZeroVerdicts(t *testing.T) {
	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	s := newTestServerObj(t, "provider: stub\n", &recordingSource{}, store)
	s.mteamStatus = storeStatus{Store: store}

	body := metricsBody(t, s.Handler())
	if !strings.Contains(body, `javdb_rss_mteam_judged 0`) {
		t.Errorf("从未判定过时 judged 应当是 0：\n%s", body)
	}
	if strings.Contains(body, "javdb_rss_mteam_last_decision_timestamp_seconds") {
		t.Errorf("从未判定过时那条时间戳**不该出现**（报 0 等于说 1970 年判定过）")
	}
	for _, outcome := range []string{"hit", "miss", "failed"} {
		if !strings.Contains(body, `javdb_rss_mteam_verdicts{outcome="`+outcome+`"} 0`) {
			t.Errorf("三个 outcome 都该报（哪怕是 0）：%s", outcome)
		}
	}

	// 记一条判定之后：judged 翻到 1，时间戳出现。
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	store.Put("w1", mteam.Verdict{Outcome: mteam.OutcomeHit, Chosen: mteam.Candidate{Infohash: "h"}, At: at})

	body = metricsBody(t, s.Handler())
	if !strings.Contains(body, `javdb_rss_mteam_judged 1`) {
		t.Errorf("判定过之后 judged 应当是 1：\n%s", body)
	}
	if !strings.Contains(body, `javdb_rss_mteam_verdicts{outcome="hit"} 1`) {
		t.Errorf("命中数应当是 1：\n%s", body)
	}
	// ⚠️ 比**数值**而不是比字符串：Prometheus 会把大数渲染成 `1.7918928e+09`。
	if got, ok := metricValue(t, body, "javdb_rss_mteam_last_decision_timestamp_seconds"); !ok || got != float64(at.Unix()) {
		t.Errorf("时间戳 = %v（存在=%v），want %d", got, ok, at.Unix())
	}
}

// TestMetricsReportsProgressAndCredential 钉住进度与凭据那几条。
func TestMetricsReportsProgressAndCredential(t *testing.T) {
	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	store.Put("w1", mteam.Verdict{
		Outcome: mteam.OutcomeFailed, Failure: "key無效", FailureKind: mteam.FailureCredential,
		At: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC),
	})

	s := newTestServerObj(t, "provider: stub\n", &recordingSource{}, store)
	// 循环跑过 5 轮、其中连续 3 轮没推进、还有 7 部欠账。
	s.mteamStatus = storeStatus{Store: store, prog: mteam.Progress{
		Cycles: 5, Pending: 7, Stalled: 3,
	}}

	body := metricsBody(t, s.Handler())
	for _, want := range []string{
		`javdb_rss_mteam_pending_works 7`,
		`javdb_rss_mteam_stalled_cycles 3`,
		`javdb_rss_mteam_credential_broken 1`,
		`javdb_rss_mteam_verdicts{outcome="failed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺 %q：\n%s", want, body)
		}
	}
}

// TestMetricsOmitsProgressWhenLoopNeverRan 钉住「循环从未跑过」时进度不出现。
//
// 与那条时间戳同一条判断：报 0 等于说「跑过、且没有欠账」。
func TestMetricsOmitsProgressWhenLoopNeverRan(t *testing.T) {
	store, err := mteam.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	s := newTestServerObj(t, "provider: stub\n", &recordingSource{}, store)
	s.mteamStatus = storeStatus{Store: store} // Cycles = 0

	if body := metricsBody(t, s.Handler()); strings.Contains(body, "javdb_rss_mteam_pending_works") {
		t.Errorf("循环从未跑过时不该报 pending：\n%s", body)
	}
}

// TestWantFeedRendersAllThreeOutcomesEndToEnd 是票 03 遗留的那条端到端
// （当时只在 `catalog.Source` 接缝上验过，没在 HTTP 层验）。
//
// 它一次穿过五层：假 M-Team → 后台循环判定 → 判定表 → feed 渲染 → XML。
// 三种结果各走一遍，并断言**同一件事**：无论哪一种，作品都照上游下发
// （差别只在判定表里那条记录，以及凭据那条要在 /healthz/upstream 里看得见）。
//
// 判据是「用户看到什么」：条数、标题里的计数、以及凭据信号 —— 不是内部字段。
func TestWantFeedRendersAllThreeOutcomesEndToEnd(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		wantCredBroken bool
		wantOutcome    string
	}{
		{
			name: "空结果（负结果：查过、没有）",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":{"data":[]}}`)
			},
			wantOutcome: "miss",
		},
		{
			name: "401（凭据失效 —— 重试无用）",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"code":"401","message":"Full authentication is required"}`)
			},
			wantCredBroken: true,
			wantOutcome:    "failed",
		},
		{
			name: "网络层坏法（M-Team 给了看不懂的响应）",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `<html>502 Bad Gateway</html>`)
			},
			wantOutcome: "failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := httptest.NewServer(tt.handler)
			defer mt.Close()

			store, err := mteam.Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = store.Close() }()

			src := &recordingSource{want: &catalog.WantList{Works: []catalog.Work{
				wantWork("KV-1", catalog.Magnet{Infohash: "u1"}),
			}}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			dec := mteam.New(src, store, nil, log)
			loop := mteam.NewLoop(src, &mteam.Client{BaseURL: mt.URL, APIKey: "k"}, store, nil,
				func() time.Duration { return time.Minute }, 0, log)
			s := newTestServerObj(t, "provider: stub\n", dec, store)
			s.mteamStatus = storeStatus{Store: store}
			h := s.Handler()

			// 一、判定之前：作品被**扣住**（不发条目，但计数说出来）。
			got := parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())
			if len(got.Channel.Items) != 0 || !strings.Contains(got.Channel.Title, "1 部待定来源") {
				t.Fatalf("判定之前应当扣住并计数，实际 %d 条 / %q",
					len(got.Channel.Items), got.Channel.Title)
			}

			// 二、后台跑一轮。
			loop.Once(context.Background())

			// 三、判定表里那条记录是**可区分的**（三种结果各是各的）。
			v, ok := store.Get("id-KV-1")
			if !ok {
				t.Fatalf("后台没留下判定")
			}
			if string(v.Outcome) != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", v.Outcome, tt.wantOutcome)
			}

			// 四、feed：三种结果**都照上游下发**（负结果与失败都不扣）。
			got = parseWantFeed(t, do(t, h, "/rss/want.xml").Body.String())
			if len(got.Channel.Items) != 1 {
				t.Fatalf("得到 %d 条条目，want 1（三种结果都不该扣住）", len(got.Channel.Items))
			}
			if strings.Contains(got.Channel.Title, "待定来源") {
				t.Errorf("判定已完成，不该再有待定来源：%q", got.Channel.Title)
			}
			if !strings.HasPrefix(got.Channel.Items[0].Enclosure.URL, "magnet:?xt=urn:btih:u1") {
				t.Errorf("应当发上游那条磁链，实际 %q", got.Channel.Items[0].Enclosure.URL)
			}

			// 五、凭据那条要在**人看的地方**也看得见（只有 401 那一种）。
			var body map[string]any
			if err := json.Unmarshal(do(t, h, "/healthz/upstream").Body.Bytes(), &body); err != nil {
				t.Fatalf("不是 JSON: %v", err)
			}
			m, _ := body["mteam"].(map[string]any)
			if got := m["credential_broken"]; got != tt.wantCredBroken {
				t.Errorf("credential_broken = %v, want %v", got, tt.wantCredBroken)
			}
		})
	}
}
