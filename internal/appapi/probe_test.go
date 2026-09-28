package appapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// 服务端在两种签名问题下返回的**真实响应**。这两条是从 2026-09-28 的实测里
// 原样抄下来的，包括它们分别用了不同的 HTTP 状态码 —— 这正是本项目踩过的坑：
//
//	早期只处理了 ParameterInvalid（HTTP 200），于是「签名值无效」这条路径
//	被 GetJSON 按状态码提前拦成一个字符串错误，action 丢失，
//	告警在唯一该响的时候报 signature_broken: false。
const (
	bodySignatureMissing = `{"success":0,"action":"ParameterInvalid","message":"參數不能爲空: jdsignature","data":null}`
	bodySignatureInvalid = `{"success":0,"action":"InvalidSignature","message":"無效的簽名","data":null}`
	bodyAuthExpired      = `{"success":0,"action":"TokenExpired","message":"token 已過期","data":null}`
	bodyOK               = `{"success":1,"action":null,"message":null,"data":{"hello":"world"}}`
)

func clientFor(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		Host:     srv.URL,
		Identity: DefaultIdentity(),
		Signer:   NewSigner(),
		Lang:     "en",
	}
}

// TestCheckSucceeds 是健康路径。
func TestCheckSucceeds(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("jdsignature") == "" {
			t.Error("请求没有带 jdsignature 头")
		}
		// 8 个公共参数必须都在 query 上。
		q := r.URL.Query()
		for _, k := range []string{"platform", "app_version", "device_uuid"} {
			if q.Get(k) == "" {
				t.Errorf("缺公共参数 %q", k)
			}
		}
		_, _ = w.Write([]byte(bodyOK))
	})

	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check 应当成功: %v", err)
	}
}

// TestCheckClassifiesBothSignatureFailureShapes 是本文件的核心。
//
// 两种签名失败必须都被认出 —— 它们的 HTTP 状态码不同，早期实现只认了其中一种。
func TestCheckClassifiesBothSignatureFailureShapes(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantAction string
	}{
		// 签名值无效：HTTP 400。这是 Prefix 变更后的真实症状。
		{"签名无效（HTTP 400）", http.StatusBadRequest, bodySignatureInvalid, "InvalidSignature"},
		// 签名缺失：HTTP 200 + success:0。
		{"签名缺失（HTTP 200）", http.StatusOK, bodySignatureMissing, "ParameterInvalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})

			err := c.Check(context.Background())
			if err == nil {
				t.Fatal("Check 应当失败")
			}
			if got := ActionOf(err); got != tt.wantAction {
				t.Errorf("ActionOf = %q, want %q", got, tt.wantAction)
			}
			if !IsSignatureError(err) {
				t.Errorf("IsSignatureError(%v) = false —— 告警会在该响的时候哑掉", err)
			}
		})
	}
}

// TestNonEnvelopeHTTPErrorIsNotSignatureError 确认普通网络/服务端故障
// **不会**被误报成签名问题。误报的代价是有人半夜被叫起来改代码，
// 而实际上只需要重试。
func TestNonEnvelopeHTTPErrorIsNotSignatureError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"网关错误", http.StatusBadGateway, "<html>502 Bad Gateway</html>"},
		{"服务不可用", http.StatusServiceUnavailable, "upstream down"},
		{"纯文本 400", http.StatusBadRequest, "bad request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})

			err := c.Check(context.Background())
			if err == nil {
				t.Fatal("Check 应当失败")
			}
			if IsSignatureError(err) {
				t.Errorf("非信封错误被误判为签名问题: %v", err)
			}
			if ActionOf(err) != "" {
				t.Errorf("非信封错误不该有 action: %q", ActionOf(err))
			}
		})
	}
}

// TestAuthErrorIsNotSignatureError 确认 token 过期与签名问题被分开。
//
// 两者都要「去改配置」，但改的是不同的东西 —— 混在一起会让人去翻签名常量，
// 而实际上只需要重新导出 token。
func TestAuthErrorIsNotSignatureError(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(bodyAuthExpired))
	})

	err := c.Check(context.Background())
	if err == nil {
		t.Fatal("Check 应当失败")
	}
	if !IsAuthError(err) {
		t.Errorf("应当被识别为凭据错误: %v", err)
	}
	if IsSignatureError(err) {
		t.Errorf("凭据错误不该被当成签名问题: %v", err)
	}
	if ActionOf(err) != "TokenExpired" {
		t.Errorf("ActionOf = %q", ActionOf(err))
	}
}

// TestTransportErrorIsNotSignatureError 网络层失败同样不该被误判。
func TestTransportErrorIsNotSignatureError(t *testing.T) {
	c := clientFor(t, func(http.ResponseWriter, *http.Request) {})
	c.Host = "http://127.0.0.1:1" // 必然连不上

	err := c.Check(context.Background())
	if err == nil {
		t.Fatal("Check 应当失败")
	}
	if IsSignatureError(err) {
		t.Errorf("网络故障被误判为签名问题: %v", err)
	}
}

// TestAPIErrorCarriesStatus 确认错误里保留了 HTTP 状态码 ——
// 诊断「签名缺失」与「签名无效」时，状态码是最快的区分线索。
func TestAPIErrorCarriesStatus(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(bodySignatureInvalid))
	})

	var ae *APIError
	if err := c.Check(context.Background()); !errors.As(err, &ae) {
		t.Fatalf("应当是 APIError: %v", err)
	}
	if ae.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", ae.Status)
	}
	if !strings.Contains(ae.Error(), "400") {
		t.Errorf("Error() 里应当带上状态码: %q", ae.Error())
	}
}

// TestQueryParamsAreMerged 确认调用方传的参数与公共参数合并，而不是覆盖掉公共参数。
func TestQueryParamsAreMerged(t *testing.T) {
	var got url.Values
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(bodyOK))
	})

	_ = c.GetJSON(context.Background(), "/api/v1/movies/tags",
		url.Values{"filter_by": {"apmc"}}, nil)

	if got.Get("filter_by") != "apmc" {
		t.Errorf("调用方参数丢了: %q", got.Get("filter_by"))
	}
	if got.Get("platform") == "" {
		t.Error("公共参数被覆盖掉了")
	}
}

// TestBearerTokenSentWhenPresent 确认 token 走 Authorization 头。
func TestBearerTokenSentWhenPresent(t *testing.T) {
	var auth string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("authorization")
		_, _ = w.Write([]byte(bodyOK))
	})
	c.Token = "abc.def.ghi"

	_ = c.Check(context.Background())
	if auth != "Bearer abc.def.ghi" {
		t.Errorf("authorization = %q", auth)
	}
}

// TestNoTokenSendsNoAuthHeader 确认匿名请求不带 Authorization ——
// 需求 1/2/3 全靠匿名访问，带一个空 Bearer 反而可能被服务端拒。
func TestNoTokenSendsNoAuthHeader(t *testing.T) {
	var hasAuth bool
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		_, hasAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(bodyOK))
	})

	_ = c.Check(context.Background())
	if hasAuth {
		t.Error("没有 token 时不该带 Authorization 头")
	}
}
