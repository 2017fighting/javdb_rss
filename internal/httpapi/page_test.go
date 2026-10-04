package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2017fighting/javdb_rss/internal/stub"
)

// 这一组测试守的是**页面端点的接线**，不是页面的长相：
// 它在精确的根路径上、资产走 /assets/、以及最重要的 ——
// 它**没有**把打错的 feed 路径变成 HTML。

func TestPageIsServedAtRoot(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	rec := do(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("content-type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q，want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!doctype html>") {
		t.Errorf("响应不是 HTML 文档（%d 字节）", len(body))
	}
	// 资产同源、无第三方：页面只能引用自己的 /assets/。
	for _, ref := range []string{"/assets/app.css", "/assets/app.js"} {
		if !strings.Contains(body, ref) {
			t.Errorf("页面没有引用 %s", ref)
		}
	}
	for _, bad := range []string{"//cdn.", "https://", "http://"} {
		if strings.Contains(body, bad) {
			t.Errorf("页面里出现了外部引用 %q —— 资产必须内嵌、同源", bad)
		}
	}
}

func TestPageAssetsAreServed(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	tests := []struct {
		path     string
		wantType string
		marker   string
	}{
		{"/assets/app.css", "text/css", "--background"},
		// 页面靠这个键把服务地址记进 localStorage，marker 用它顺带钉住文件没被换错。
		{"/assets/app.js", "javascript", "javdb-rss-base"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := do(t, h, tt.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tt.path, rec.Code)
			}
			if ct := rec.Header().Get("content-type"); !strings.Contains(ct, tt.wantType) {
				t.Errorf("content-type = %q，want 含 %q", ct, tt.wantType)
			}
			if !strings.Contains(rec.Body.String(), tt.marker) {
				t.Errorf("%s 的内容里没有 %q —— 资产可能没被 embed 进来", tt.path, tt.marker)
			}
		})
	}
}

// TestTypoedFeedPathStays404 是本票最要紧的一条回归守卫。
//
// 页面如果用 `GET /` 当通配注册，`/rss/want`（少写 .xml）会返回一张 HTML
// 页面：qBittorrent 只会说「这不是一个 feed」，用户看到的是「服务坏了」，
// 而不是「我的 URL 打错了」。打错的 feed 必须仍是干净的 404。
func TestTypoedFeedPathStays404(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	for _, target := range []string{
		"/rss/want",               // 少写 .xml
		"/rss/actres/EvkJ.xml",    // 打错的实体段
		"/rss/collected.xml",      // 发现端点伪装成 feed
		"/index.html",             // 页面只在根路径上，不按文件名服务
		"/nope",                   // 任意未知路径
		"/assets/",                // 资产根不给目录清单
		"/assets/no-such-file.js", // 不存在的资产
	} {
		t.Run(target, func(t *testing.T) {
			rec := do(t, h, target)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404", target, rec.Code)
			}
			if ct := rec.Header().Get("content-type"); strings.HasPrefix(ct, "text/html") {
				t.Errorf("打错的路径返回了 HTML 页面（%s），feed 客户端只会说「这不是一个 feed」", target)
			}
		})
	}
}

func TestPageRejectsNonGET(t *testing.T) {
	h := newTestServer(t, "provider: stub\n", &stub.Source{})

	for _, target := range []string{"/", "/assets/app.js"} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", target, rec.Code)
		}
	}
}
