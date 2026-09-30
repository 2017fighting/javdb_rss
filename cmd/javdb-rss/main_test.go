package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/config"
)

// fakeAPI 模拟上游：旧 token 会被拒，登录后发新 token。
//
// 它刻意把「登录次数」暴露出来 —— 自动续期最容易犯的错就是
// 并发下登了 N 次，而每次登录都会把上一次的 token 作废（单会话账号）。
type fakeAPI struct {
	logins      atomic.Int64
	oldTokenHit atomic.Int64
	newTokenHit atomic.Int64
	*slog.Logger
}

const (
	oldToken = "old-token"
	newToken = "new-token"
)

func (f *fakeAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" {
			f.logins.Add(1)
			// 模拟真实延迟，让并发窗口真实存在。
			time.Sleep(30 * time.Millisecond)
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"token":"` + newToken + `"}}`))
			return
		}
		auth := r.Header.Get("authorization")
		switch auth {
		case "Bearer " + newToken:
			f.newTokenHit.Add(1)
		case "Bearer " + oldToken:
			f.oldTokenHit.Add(1)
			_, _ = w.Write([]byte(`{"success":0,"action":"TokenExpired","message":"token 已過期","data":null}`))
			return
		default:
			_, _ = w.Write([]byte(`{"success":0,"action":"LoginRequired","message":"需要登入","data":null}`))
			return
		}

		// 各端点给一个最小可用的成功响应。
		switch {
		case strings.HasSuffix(r.URL.Path, "/magnets"):
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"magnets":[{"name":"X","hash":"abc","size":1,"cnsub":false,"hd":true,"files_count":1,"created_at":"09/01/2026"}]}}`))
		case strings.HasPrefix(r.URL.Path, "/api/v2/search"):
			// number 必须与查询的番号**精确匹配** ——
			// resolveExact 的正确行为就是「匹配不上就报错」。
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"movies":[{"id":"m1","number":"KV-328"}]}}`))
		default:
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{}}`))
		}
	})
}

// newTestSource 起一个假上游 + 一份指向它的配置，返回可直接调用的 source。
func newTestSource(t *testing.T, initialToken string, withCreds bool) (*appapiSource, *fakeAPI) {
	t.Helper()

	fake := &fakeAPI{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	if initialToken != "" {
		if err := config.SaveToken(tokenPath, initialToken); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := fmt.Sprintf("provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  lang: zh-CN\n  probe_interval: \"0\"\n",
		srv.URL, tokenPath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	if withCreds {
		t.Setenv(config.EnvUsername, "alice")
		t.Setenv(config.EnvPassword, "s3cret")
	} else {
		t.Setenv(config.EnvUsername, "")
		t.Setenv(config.EnvPassword, "")
	}
	t.Setenv(config.EnvToken, "") // 确保走文件，否则 env 会盖掉

	holder, err := config.NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return &appapiSource{holder: holder, log: fake.Logger}, fake
}

// TestAutoReloginOnExpiredToken 是主路径：token 失效 → 自动续期 → 重试成功。
func TestAutoReloginOnExpiredToken(t *testing.T) {
	src, fake := newTestSource(t, oldToken, true)

	if _, err := src.Code(context.Background(), "KV-328"); err != nil {
		t.Fatalf("自动续期后应当成功: %v", err)
	}
	if got := fake.logins.Load(); got != 1 {
		t.Errorf("登录次数 = %d, want 1", got)
	}
	if got := fake.oldTokenHit.Load(); got == 0 {
		t.Error("应当先用旧 token 试过一次（否则测试没测到续期路径）")
	}
	if got := fake.newTokenHit.Load(); got == 0 {
		t.Error("重试没有用上新 token")
	}

	// 新 token 应当落盘，让重启后还能用。
	tok, err := config.LoadToken(src.holder.Current().AppAPI.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if tok != newToken {
		t.Errorf("落盘的 token = %q, want %q", tok, newToken)
	}
}

// TestConcurrentAuthFailuresReloginOnlyOnce 是最要紧的一条。
//
// 单会话账号下，多个并发请求各登各的会**互相把对方刚拿到的 token 挤掉** ——
// 结果是永远在登录，而且总有一个请求拿着刚被作废的 token 失败。
// 因此必须串行化，并且拿到锁后重新确认是否已经有人登过。
func TestConcurrentAuthFailuresReloginOnlyOnce(t *testing.T) {
	src, fake := newTestSource(t, oldToken, true)

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = src.Code(context.Background(), "KV-328")
		}(i)
	}
	close(start)
	wg.Wait()

	// 关键断言：只登了一次。
	if got := fake.logins.Load(); got != 1 {
		t.Errorf("登录次数 = %d，%d 个并发请求应当只登一次"+
			"（登多次会互相挤掉 token）", got, n)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("第 %d 个请求失败: %v", i, err)
		}
	}
}

// TestNoReloginWithoutCredentials 确认没配账号密码时**不**尝试登录。
//
// 这是「不踢用户手机」的默认状态：不设 JAVDB_PASSWORD 就永不自动登录。
func TestNoReloginWithoutCredentials(t *testing.T) {
	src, fake := newTestSource(t, oldToken, false)

	_, err := src.Code(context.Background(), "KV-328")
	if err == nil {
		t.Fatal("token 失效且没凭据时应当失败")
	}
	if !errors.Is(err, catalog.ErrNoToken) && err.Error() == "" {
		t.Errorf("错误应当可读: %v", err)
	}
	if got := fake.logins.Load(); got != 0 {
		t.Errorf("没有凭据时不该登录，实际登了 %d 次 —— 那会踢掉用户手机", got)
	}
}

// TestNoReloginOnNonAuthError 确认只有**凭据类**错误才触发续期。
//
// 网络抖动、上游 5xx、参数写错都不该登录一次 —— 那会白白踢掉用户手机，
// 而且对真正的问题毫无帮助。
func TestNoReloginOnNonAuthError(t *testing.T) {
	var logins atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" {
			logins.Add(1)
			_, _ = w.Write([]byte(`{"success":1,"data":{"token":"x"}}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502</html>"))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.yaml")
	_ = os.WriteFile(cfgPath, []byte(fmt.Sprintf(
		"provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  probe_interval: \"0\"\n",
		srv.URL, filepath.Join(dir, "t.json"))), 0o600)
	t.Setenv(config.EnvUsername, "alice")
	t.Setenv(config.EnvPassword, "s3cret")
	t.Setenv(config.EnvToken, "")

	holder, err := config.NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	src := &appapiSource{holder: holder, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if _, err := src.Code(context.Background(), "X"); err == nil {
		t.Fatal("上游 502 时应当失败")
	}
	if got := logins.Load(); got != 0 {
		t.Errorf("非凭据类错误不该触发登录，实际 %d 次", got)
	}
}
