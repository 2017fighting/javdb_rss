package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	src := mteam.New(inner, client, store, pins, slog.New(slog.NewTextHandler(io.Discard, nil)))

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
	s.mteamHealth = store

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
	if m["next_step"] != mteam.CredentialGuidance {
		t.Errorf("next_step = %v，应当是 mteam 包给出的处置", m["next_step"])
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
	s.mteamHealth = nilStore

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
