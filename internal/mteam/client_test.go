package mteam

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 这些测试打的是**契约**，不是实现细节：它们断言服务真正会发的那个请求
// （方法、路径、头、体的编码），以及三种「重试无用」的失败各自可判定。
// 每条断言都对应一个实测出来的坑，见 notes/mteam-api.md。

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, APIKey: "test-key", HTTP: srv.Client()}, srv
}

func TestSearchSendsTheContract(t *testing.T) {
	var got struct {
		method, path, key, ua, ct string
		body                      map[string]any
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		got.key, got.ua, got.ct = r.Header.Get("x-api-key"), r.Header.Get("User-Agent"), r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":{"data":[
			{"id":"467487","name":"SSIS-001 一ヶ月間の…","size":"6834984408",
			 "labelsNew":["中字"],"createdDate":"2021-02-22 15:19:51","infoHash":null,
			 "status":{"seeders":"122","leechers":"3"}}
		],"pageNumber":1,"pageSize":100,"total":1,"totalPages":1}}`)
	})

	hits, err := c.Search(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("方法 = %q，M-Team 的所有接口都是 POST", got.method)
	}
	if got.path != "/api/torrent/search" {
		t.Errorf("路径 = %q", got.path)
	}
	if got.key != "test-key" {
		t.Errorf("x-api-key = %q", got.key)
	}
	// ⚠️ 这条不是形式主义：实测 Cloudflare 会把**空 UA 与 `curl/*`** 302 到
	// www.google.com，而那个表现看起来像鉴权失败或网络问题。UA 只要非空且不含
	// `curl` 就能过，但「恰好能过」不是设计 —— 所以这里钉的是「我们显式设了一个」。
	if strings.TrimSpace(got.ua) == "" || strings.Contains(got.ua, "curl") {
		t.Errorf("User-Agent = %q —— 空或含 curl 会被 Cloudflare 拦掉", got.ua)
	}
	if !strings.Contains(got.ct, "application/json") {
		t.Errorf("搜索的 Content-Type = %q，它必须是 JSON（给 form 会返回「參數錯誤」）", got.ct)
	}
	if got.body["mode"] != "adult" {
		t.Errorf("mode = %v，本服务固定 adult", got.body["mode"])
	}
	if got.body["keyword"] != "SSIS-001" {
		t.Errorf("keyword = %v", got.body["keyword"])
	}
	if _, ok := got.body["pageNumber"]; !ok {
		t.Error("缺 pageNumber")
	}
	if _, ok := got.body["pageSize"]; !ok {
		t.Error("缺 pageSize")
	}

	if len(hits) != 1 {
		t.Fatalf("得到 %d 条命中", len(hits))
	}
	h := hits[0]
	if h.ID != "467487" || h.Name != "SSIS-001 一ヶ月間の…" {
		t.Errorf("id/name = %q/%q", h.ID, h.Name)
	}
	if h.SizeBytes != 6834984408 {
		t.Errorf("size = %d（线格式是字符串，要转成字节数）", h.SizeBytes)
	}
	if !h.CNSub {
		t.Error("labelsNew 里有「中字」，CNSub 应当是 true")
	}
	if h.CreatedAt != "2021-02-22 15:19:51" {
		t.Errorf("createdAt = %q（线格式原样保留）", h.CreatedAt)
	}
	if h.Seeders != 122 {
		t.Errorf("seeders = %d（线格式是字符串）", h.Seeders)
	}
}

// TestSearchCNSubOnlyFromLabelsNew 钉住那个**看起来权威、实际不填**的字段。
//
// `detail` 里有一个 `hasChineseSubtitle`，实测它在 labelsNew 明明写着「中字」时
// 仍然是 false。搜索响应里没有这个字段，但这条测试把「中字只认 labelsNew」
// 这条判据钉在它唯一的入口上 —— 将来有人「顺手也读一下 hasChineseSubtitle」时，
// 会在这里看到为什么不行。
func TestSearchCNSubOnlyFromLabelsNew(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":{"data":[
			{"id":"1","name":"A","labelsNew":["中字","4k"]},
			{"id":"2","name":"B","labelsNew":[]},
			{"id":"3","name":"C"}
		]}}`)
	})
	hits, err := c.Search(context.Background(), "x")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []bool{true, false, false}
	for i, w := range want {
		if hits[i].CNSub != w {
			t.Errorf("第 %d 条 CNSub = %v, want %v", i, hits[i].CNSub, w)
		}
	}
}

func TestDownloadUsesFormEncodingThenFetchesTheLink(t *testing.T) {
	const torrent = "d8:announce4:abce"
	var tokenCT, tokenBody string
	var c *Client
	c, _ = newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/torrent/genDlToken":
			tokenCT = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			tokenBody = string(b)
			_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":"`+c.BaseURL+`/api/rss/dlv2?sign=abc"}`)
		case "/api/rss/dlv2":
			// ⭐ 实测：这一跳**真的会 302 到一个 CDN**（fr1.halomt.com?playload=…），
			// 种子在那一跳之后。所以取种子必须跟随重定向。
			http.Redirect(w, r, c.BaseURL+"/cdn/torrent", http.StatusFound)
		case "/cdn/torrent":
			_, _ = io.WriteString(w, torrent)
		default:
			t.Errorf("意外的路径 %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := c.Download(context.Background(), "467487")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	// ⚠️ genDlToken 要 **form**，不是 JSON —— 给 JSON 会得到「參數錯誤」。
	// 站点自己的 wiki 就承认文档的 json/form 说明不可信。
	if strings.Contains(tokenCT, "application/json") {
		t.Errorf("genDlToken 的 Content-Type = %q —— 它要 form 编码", tokenCT)
	}
	if !strings.Contains(tokenBody, "id=467487") {
		t.Errorf("genDlToken 的体 = %q，应当带上 id", tokenBody)
	}
	if string(data) != torrent {
		t.Errorf("取到的字节 = %q, want %q", data, torrent)
	}
}

func TestCredentialErrorsAreDistinguishable(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			// 实测：key 无效时 HTTP 状态是 **200**，靠 code/message 才看得出来。
			// 只看状态码的实现会把它当成功，然后在解析 data 时莫名其妙地炸。
			name: "key 无效（HTTP 200 + code 1）",
			body: `{"code":"1","message":"key無效","data":null}`,
		},
		{
			name: "完全没有 key（code 401）",
			body: `{"code":"401","message":"Full authentication is required to access this resource"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				// 故意用 200：这正是实测的形状。
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.Search(context.Background(), "x")
			if !errors.Is(err, ErrCredential) {
				t.Errorf("err = %v，应当可用 errors.Is 判成 ErrCredential", err)
			}
		})
	}
}

// TestRedirectIsDistinguishable 钉住 Cloudflare 的 UA 拦截。
//
// 它的表现是 302 到 www.google.com —— 不像鉴权失败，也不像网络故障。
// 不单独成一类的话，排查会一路往「key 是不是过期了」的方向错下去。
//
// ⚠️ 它只对 **API 端点** 成立。下载链接恰好相反（它合法地 302 到 CDN），
// 见 TestDownloadUsesFormEncodingThenFetchesTheLink。
func TestRedirectIsDistinguishable(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://www.google.com/", http.StatusFound)
	})
	_, err := c.Search(context.Background(), "x")
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("err = %v，应当可用 errors.Is 判成 ErrBlocked", err)
	}
}

// TestExpiredLinkIsDistinguishable 钉住下载链接的寿命。
//
// 实测：链接生成后约 5 分钟就失效，返回 `{"code":"1","message":"連結不可用！ 超出有效期"}`。
// 正常情况下不会碰到（我们是生成完立刻取），碰到就说明流程出了问题 ——
// 所以它要和「普通上游故障」分开。
func TestExpiredLinkIsDistinguishable(t *testing.T) {
	var c *Client
	c, _ = newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/torrent/genDlToken" {
			_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":"`+c.BaseURL+`/api/rss/dlv2?sign=abc"}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":"1","message":"連結不可用！ 超出有效期","data":null}`)
	})
	_, err := c.Download(context.Background(), "467487")
	if !errors.Is(err, ErrLinkExpired) {
		t.Errorf("err = %v，应当可用 errors.Is 判成 ErrLinkExpired", err)
	}
}

// TestUnknownFailureIsAPlainError 确认**没见过的**失败不会被硬塞进上面三类。
//
// 猜错分类比不分类更坏：它会让告警说「凭据过期，去换 key」而真相是别的。
func TestUnknownFailureIsAPlainError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":"1","message":"某个我们没见过的错","data":null}`)
	})
	_, err := c.Search(context.Background(), "x")
	if err == nil {
		t.Fatal("期望报错")
	}
	for _, known := range []error{ErrCredential, ErrBlocked, ErrLinkExpired} {
		if errors.Is(err, known) {
			t.Errorf("err = %v 被错判成了 %v", err, known)
		}
	}
	if !strings.Contains(err.Error(), "没见过的错") {
		t.Errorf("报错里应当带上上游的原话，实际是 %q", err)
	}
}

// TestSearchHTTPErrorIsAnError 确认非 2xx 也算失败（除了那条 302 的拦截）。
func TestSearchHTTPErrorIsAnError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := c.Search(context.Background(), "x"); err == nil {
		t.Fatal("502 应当报错")
	}
}
