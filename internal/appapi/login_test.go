package appapi

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// TestLoginPostsCredentials 钉住登录的契约。
//
// 实测（2026-09-30）确认的形态：
//
//	POST /api/v1/sessions   form: username, password
//	→ {"success":1,"data":{"token":"eyJ..."}}
//
// 字段是 **username** 而不是 email —— 这一点容易被先例项目的文档误导
// （它一个地方写 email 一个地方写 username，而实测要求 username）。
func TestLoginPostsCredentials(t *testing.T) {
	var gotForm url.Values
	var gotAuth string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("content-type"); ct != "" {
			// 表单提交；这里只确认不是 JSON。
			if ct == "application/json" {
				t.Errorf("不该用 JSON 提交: %s", ct)
			}
		}
		gotAuth = r.Header.Get("authorization")
		_ = r.ParseForm()
		gotForm = r.PostForm
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"token":"eyJhbGciOi.test.sig"}}`))
	})

	tok, err := c.Login(context.Background(), "alice", "s3cret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok != "eyJhbGciOi.test.sig" {
		t.Errorf("token = %q", tok)
	}
	if gotForm.Get("username") != "alice" {
		t.Errorf("username = %q —— 实测该端点要的是 username 而不是 email", gotForm.Get("username"))
	}
	if gotForm.Get("password") != "s3cret" {
		t.Errorf("password = %q", gotForm.Get("password"))
	}
	// 登录请求本身不该带 Authorization —— 还没有 token。
	if gotAuth != "" {
		t.Errorf("登录请求不该带 Authorization: %q", gotAuth)
	}
}

// TestLoginAcceptsAccessTokenField 确认兼容 access_token 字段名。
// 先例项目就是两个都读，说明上游历史上用过不同的名字。
func TestLoginAcceptsAccessTokenField(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"access_token":"from-access-token"}}`))
	})
	tok, err := c.Login(context.Background(), "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "from-access-token" {
		t.Errorf("token = %q", tok)
	}
}

// TestLoginErrorsWhenNoTokenInResponse 确认响应里没有 token 时报错，
// 而不是返回一个空 token 让上层拿去用 —— 那会在下一个请求变成难懂的 401。
func TestLoginErrorsWhenNoTokenInResponse(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{}}`))
	})
	if _, err := c.Login(context.Background(), "u", "p"); err == nil {
		t.Fatal("响应没给 token 时应当报错")
	}
}

// TestLoginSurfacesWrongCredentials 确认密码错误是一个**明确的业务错误**，
// 而不是被吞掉。它的 action 是 IncorrentUsernameOrPassword（上游的拼写，不是我们打错）。
func TestLoginSurfacesWrongCredentials(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":0,"action":"IncorrentUsernameOrPassword","message":"錯誤的用戶名或密碼","data":null}`))
	})
	_, err := c.Login(context.Background(), "u", "wrong")
	if err == nil {
		t.Fatal("密码错误应当报错")
	}
	if ActionOf(err) != "IncorrentUsernameOrPassword" {
		t.Errorf("action = %q", ActionOf(err))
	}
	// 它不该被误判成「token 失效」—— 那是另一个方向的问题。
	if IsAuthError(err) {
		t.Error("登录失败不该被当成凭据过期")
	}
}

// TestLoginRequiresBothFields 确认缺任一项时在本地就拦下，不去白打一次上游。
func TestLoginRequiresBothFields(t *testing.T) {
	var hit bool
	c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"success":1,"data":{"token":"x"}}`))
	})

	for _, tc := range []struct{ user, pass string }{{"", "p"}, {"u", ""}, {"", ""}} {
		if _, err := c.Login(context.Background(), tc.user, tc.pass); err == nil {
			t.Errorf("user=%q pass=%q 应当在本地就报错", tc.user, tc.pass)
		}
	}
	if hit {
		t.Error("本地就能发现的问题不该去打上游")
	}
}
