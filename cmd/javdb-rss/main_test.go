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
	"github.com/2017fighting/javdb_rss/internal/pin"
)

// oldToken 是初始配置文件里的 token。
//
// 它从未被假上游签发过，因此一开始就是失效的 —— 这正是每条测试要的起点。
const oldToken = "old-token"

// fakeAPI 模拟上游。
//
// 两个刻意的地方：
//
//  1. **每次登录发一个不同的 token**。真实上游就是这样。第一版夹具每次发同一个，
//     于是 TestReloginWorksMoreThanOnce 分不清「第二次登录没生效」与
//     「第二次登录拿到了一个刚好也被作废的 token」—— 夹具反而成了障碍。
//  2. **登录次数被暴露出来**。自动续期最容易犯的错是并发下登 N 次，
//     而每次登录都会把上一次的 token 作废（单会话账号）。
type fakeAPI struct {
	logins    atomic.Int64
	staleHits atomic.Int64
	freshHits atomic.Int64
	seq       atomic.Int64
	slog      *slog.Logger

	mu      sync.Mutex
	issued  map[string]bool
	revoked map[string]bool
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{slog: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// issue 签发一个新 token 并记为有效。
func (f *fakeAPI) issue() string {
	tok := fmt.Sprintf("tok-%d", f.seq.Add(1))
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.issued == nil {
		f.issued = map[string]bool{}
	}
	f.issued[tok] = true
	return tok
}

// invalidate 作废一个 token，模拟「用户在手机 App 上登录把服务挤下线」。
func (f *fakeAPI) invalidate(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revoked == nil {
		f.revoked = map[string]bool{}
	}
	f.revoked[token] = true
}

// valid 报告一个 token 是否被承认。
func (f *fakeAPI) valid(token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issued[token] && !f.revoked[token]
}

func (f *fakeAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" {
			f.logins.Add(1)
			time.Sleep(30 * time.Millisecond) // 让并发窗口真实存在
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"token":"` + f.issue() + `"}}`))
			return
		}

		tok := strings.TrimPrefix(r.Header.Get("authorization"), "Bearer ")
		if !f.valid(tok) {
			f.staleHits.Add(1)
			_, _ = w.Write([]byte(`{"success":0,"action":"TokenExpired","message":"token 已過期","data":null}`))
			return
		}
		f.freshHits.Add(1)

		switch {
		case strings.HasSuffix(r.URL.Path, "/magnets"):
			// **两条**候选（一中文字幕、一普通）—— 这样「选哪条」才真的有效，
			// 而 pin 也才有东西可钉。只给一条的话，选与不选结果一样，
			// 会把 pin 层的测试变成空转。
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"magnets":[
				{"name":"X","hash":"abc","size":1,"cnsub":false,"hd":true,"files_count":1,"created_at":"09/01/2026"},
				{"name":"X","hash":"def","size":2,"cnsub":true,"hd":true,"files_count":2,"created_at":"09/02/2026"}]}}`))
		case strings.HasPrefix(r.URL.Path, "/api/v2/users/review_movies"):
			// 「想看」清单：第一页一条、第二页空（空页 = 到底）。
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"movies":[]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"movies":[` +
				`{"id":"m1","number":"KV-328","title":"T","release_date":"2026-09-01","magnets_count":1}]}}`))
		case strings.HasPrefix(r.URL.Path, "/api/v2/search"):
			// number 必须与查询的番号**精确匹配** ——
			// resolveExact 的正确行为就是「匹配不上就报错」。
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{"movies":[{"id":"m1","number":"KV-328","magnets_count":1}]}}`))
		default:
			_, _ = w.Write([]byte(`{"success":1,"action":null,"data":{}}`))
		}
	})
}

// newTestSource 起一个假上游 + 一份指向它的配置，返回可直接调用的 source。
func newTestSource(t *testing.T, initialToken string, withCreds bool) (*appapiSource, *fakeAPI) {
	t.Helper()

	fake := newFakeAPI()
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
	cfg := fmt.Sprintf(
		"provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  lang: zh-CN\n  probe_interval: \"0\"\n",
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
	return &appapiSource{holder: holder, log: fake.slog}, fake
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
	if fake.staleHits.Load() == 0 {
		t.Error("应当先用旧 token 试过一次（否则测试没走到续期路径）")
	}
	if fake.freshHits.Load() == 0 {
		t.Error("重试没有用上新 token")
	}

	// 新 token 应当落盘（重启后还能用），且不等于原来那个。
	tok, err := config.LoadToken(src.holder.TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" || tok == oldToken {
		t.Errorf("落盘的 token = %q，应当是续期后的新 token", tok)
	}
}

// TestReloginWorksMoreThanOnce 是**第二次**续期。
//
// 前一条只验证了「第一次失效能续期」，于是漏掉了一个真 bug：
//
//	tryRelogin 的双重检查原本是
//	    if cur, _ := LoadToken(...); cur == s.lastRelogin { 复用并返回 true }
//	第一次续期成功后，文件里就是新 token，而 s.lastRelogin 也是它 ——
//	**两者从此永远相等**。于是第二次失效时它误判成「别人已经登过了」，
//	把刚失效的 token 塞回去并报成功，重试必然再失败。
//	结果是：进程生命周期内**只能续期一次**。
//
// 「第二次失效」恰恰是最常见的场景 —— 用户打开手机 App（单会话账号）
// 把服务端的 token 挤掉。所以这个 bug 会让自动续期在最需要它的时候失效。
func TestReloginWorksMoreThanOnce(t *testing.T) {
	src, fake := newTestSource(t, oldToken, true)

	// 第一次失效 → 续期 → 成功
	if _, err := src.Code(context.Background(), "KV-328"); err != nil {
		t.Fatalf("第一次续期应当成功: %v", err)
	}
	if got := fake.logins.Load(); got != 1 {
		t.Fatalf("第一次续期应当只登一次，实际 %d", got)
	}

	// 把刚拿到的 token 也作废 —— 等价于「用户打开了手机 App」。
	cur, err := config.LoadToken(src.holder.TokenPath())
	if err != nil || cur == "" {
		t.Fatalf("续期后应当有 token 落盘: %q %v", cur, err)
	}
	fake.invalidate(cur)

	if _, err := src.Code(context.Background(), "KV-328"); err != nil {
		t.Fatalf("第二次续期应当也成功（旧实现只能续期一次）: %v", err)
	}
	if got := fake.logins.Load(); got != 2 {
		t.Errorf("登录次数 = %d，第二次失效后应当再登一次（共 2 次）", got)
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

	if got := fake.logins.Load(); got != 1 {
		t.Errorf("登录次数 = %d，%d 个并发请求应当只登一次（登多次会互相挤掉 token）", got, n)
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

	if _, err := src.Code(context.Background(), "KV-328"); err == nil {
		t.Fatal("token 失效且没凭据时应当失败")
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

// TestAuthErrorStillReturnedWhenReloginUnavailable 确认「配了凭据但续期失败」时
// 返回的是**原始错误**而不是一个误导性的成功。
func TestAuthErrorStillReturnedWhenReloginUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" {
			// 续期也失败（例如密码改了）
			_, _ = w.Write([]byte(`{"success":0,"action":"IncorrentUsernameOrPassword","message":"錯誤的用戶名或密碼","data":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":0,"action":"TokenExpired","message":"token 已過期","data":null}`))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.yaml")
	_ = os.WriteFile(cfgPath, []byte(fmt.Sprintf(
		"provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  probe_interval: \"0\"\n",
		srv.URL, filepath.Join(dir, "t.json"))), 0o600)
	t.Setenv(config.EnvUsername, "alice")
	t.Setenv(config.EnvPassword, "wrong")
	t.Setenv(config.EnvToken, "")

	holder, err := config.NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	src := &appapiSource{holder: holder, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	_, err = src.Code(context.Background(), "X")
	if err == nil {
		t.Fatal("续期也失败时应当报错")
	}
	if !errors.Is(err, catalog.ErrNoToken) && !strings.Contains(err.Error(), "token") {
		t.Errorf("错误应当能看出是 token 问题: %v", err)
	}
}

// ---------------------------------------------------------------------------
// login 子命令
// ---------------------------------------------------------------------------

// TestLoginSkipsSaveWhenEnvTokenSet 钉住一个**曾经是假分支**的行为。
//
// 原实现是：
//
//	if envToken != "" && !force { 打印「本次不会写 token 文件」 }
//	...然后无条件 SaveToken(...)
//
// 也就是说提示在撒谎，输出自相矛盾：
//
//	提示：JAVDB_TOKEN 已经设置了…所以本次不会写 token 文件。
//	✓ 已写入 /tmp/v5/token.json（权限 0600）
//
// 而我当时的「验证」也没抓到它 —— 我用 `login ... | head -9` 看输出，
// head 打满就关管道、进程在跑到 SaveToken 之前被 SIGPIPE 杀了，
// 于是「文件没创建」这个观察来自错误的因果。
//
// 这条测试**不看提示文字**，直接检查文件是否存在 —— 提示可以撒谎，文件不会。
func TestLoginSkipsSaveWhenEnvTokenSet(t *testing.T) {
	fake := newFakeAPI()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	newCfg := func(t *testing.T) (cfgPath, tokenPath string) {
		dir := t.TempDir()
		tokenPath = filepath.Join(dir, "token.json")
		cfgPath = filepath.Join(dir, "config.yaml")
		body := fmt.Sprintf(
			"provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  probe_interval: \"0\"\n",
			srv.URL, tokenPath)
		if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return cfgPath, tokenPath
	}

	t.Setenv(config.EnvToken, "already-set-from-env")
	t.Setenv(config.EnvUsername, "alice")
	t.Setenv(config.EnvPassword, "s3cret")

	t.Run("无 -force 时不写文件", func(t *testing.T) {
		cfgPath, tokenPath := newCfg(t)
		if err := runLogin([]string{"-config", cfgPath}); err != nil {
			t.Fatalf("runLogin: %v", err)
		}
		if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
			t.Error("JAVDB_TOKEN 已设置且没给 -force 时**不该**创建文件 —— 提示与行为必须一致")
		}
	})

	t.Run("给了 -force 时照常写", func(t *testing.T) {
		cfgPath, tokenPath := newCfg(t)
		if err := runLogin([]string{"-config", cfgPath, "-force"}); err != nil {
			t.Fatalf("runLogin: %v", err)
		}
		if _, err := os.Stat(tokenPath); err != nil {
			t.Errorf("-force 应当照常写文件: %v", err)
		}
	})
}

// TestLoginWritesTokenFile 确认正常路径真的落了盘（对照上面那条）。
func TestLoginWritesTokenFile(t *testing.T) {
	fake := newFakeAPI()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	cfgPath := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf("provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  probe_interval: \"0\"\n",
		srv.URL, tokenPath)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvUsername, "alice")
	t.Setenv(config.EnvPassword, "s3cret")

	if err := runLogin([]string{"-config", cfgPath}); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	tok, err := config.LoadToken(tokenPath)
	if err != nil || tok == "" {
		t.Fatalf("应当写入 token: %q %v", tok, err)
	}
	fi, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("权限 = %o, want 600", perm)
	}
}

// TestBuildSourceWiringIsUsable 覆盖**真实的装配路径**。
//
// 这条测试的存在理由：之前 buildSource 里漏传了 log，
// 于是 tryRelogin 一触发就 s.log.Warn(...) 造成 nil panic。
// 而当时所有测试都自己构造 appapiSource 并显式传 log ——
// **从没碰过 main 里那段真正被执行的装配代码**。
//
// 所以这条测试刻意走 buildSource，而不是手搓结构体。
func TestBuildSourceWiringIsUsable(t *testing.T) {
	fake := newFakeAPI()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	if err := config.SaveToken(tokenPath, oldToken); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf("provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  probe_interval: \"0\"\n",
		srv.URL, tokenPath)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvUsername, "alice")
	t.Setenv(config.EnvPassword, "s3cret")

	holder, err := config.NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := pin.Open(holder.PinPath())
	if err != nil {
		t.Fatalf("打开 pin store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	src, err := buildSource(holder.Current(), holder, st)
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}

	// 这一步会走到自动续期 —— 也就会走 s.log.Info/Warn。
	// 若装配漏了 log，这里会 panic（而不是返回错误）。
	if _, err := src.Code(context.Background(), "KV-328"); err != nil {
		t.Fatalf("装配出的 source 应当能用: %v", err)
	}
	if fake.logins.Load() == 0 {
		t.Fatal("应当触发过一次自动续期（否则这条测试没覆盖到装配缺陷）")
	}
}

// TestBuildSourcePinsSelections 是**装配路径**上的端到端验收（ticket 08）。
//
// 前一条测试只证明「装配出来的 source 能用」。它会通过，即使 pin 那一层
// 根本没被接进去 —— 因为 fakeAPI 的 magnets 只有一条，选与不选都一样。
//
// 这条让上游返回两条候选，然后**直接检查 pin 文件**：
// 装配正确时里面应当有一条记录。这正是「走真实装配代码而不是手搓结构体」
// 的教训（见上一条测试的注释）在 pin 上的复现。
func TestBuildSourcePinsSelections(t *testing.T) {
	fake := newFakeAPI()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	if err := config.SaveToken(tokenPath, oldToken); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf("provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  probe_interval: \"0\"\n",
		srv.URL, tokenPath)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvUsername, "alice")
	t.Setenv(config.EnvPassword, "s3cret")

	holder, err := config.NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := pin.Open(holder.PinPath())
	if err != nil {
		t.Fatalf("打开 pin store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	src, err := buildSource(holder.Current(), holder, st)
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}

	if _, err := src.Code(context.Background(), "KV-328"); err != nil {
		t.Fatalf("Code: %v", err)
	}

	// 直接读文件，而不是只读内存 —— **落盘**才是本票的交付物。
	raw, err := os.ReadFile(holder.PinPath())
	if err != nil {
		t.Fatalf("pin 文件应当已被写入: %v", err)
	}
	rec, ok := st.Get("m1")
	if !ok {
		t.Fatalf("装配路径应当把 m1 钉住，实际 pin 文件: %s", raw)
	}
	if rec.Infohash != "def" {
		t.Errorf("pin 的 infohash = %q, want def（中文字幕那条）", rec.Infohash)
	}
	if !strings.Contains(string(raw), "\"m1\"") {
		t.Errorf("磁盘上的 pin 文件应当包含 m1: %s", raw)
	}
}

// TestBuildSourceWantListIsWiredAndPinned 是「想看」 feed 在**装配路径**上的验收。
//
// 两条断言各有理由：
//
//  1. 装配出来的 source 能真的读出这份清单 —— 走的必须是 buildSource 接出来的那条
//     链（appapi → pin → dedupe），而不是测试里手搋的结构体。这个仓库为此吃过一次
//     亏：装配漏传 log 时所有单元测试都通过，而线上第一个请求就 panic。
//  2. 选中的磁链**落进了 pin 文件** —— 这条 feed 会被 qBittorrent 周期轮询，
//     不钉就是 guid 抖动（同一部片被多下一份，而且没有任何告警）。
func TestBuildSourceWantListIsWiredAndPinned(t *testing.T) {
	fake := newFakeAPI()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	if err := config.SaveToken(tokenPath, oldToken); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf("provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  probe_interval: \"0\"\n",
		srv.URL, tokenPath)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvUsername, "alice")
	t.Setenv(config.EnvPassword, "s3cret")

	holder, err := config.NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := pin.Open(holder.PinPath())
	if err != nil {
		t.Fatalf("打开 pin store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	src, err := buildSource(holder.Current(), holder, st)
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}

	list, err := src.WantToWatch(context.Background())
	if err != nil {
		t.Fatalf("WantToWatch: %v", err)
	}
	if len(list.Works) != 1 {
		t.Fatalf("得到 %d 部作品，want 1", len(list.Works))
	}
	if got := list.Works[0].Magnets; len(got) != 1 || got[0].Infohash != "def" {
		t.Fatalf("选中 = %+v，应当是那条中文字幕磁链 def", got)
	}
	rec, ok := st.Get("m1")
	if !ok {
		t.Fatal("「想看」 feed 的选中磁链也应当落进 pin 表")
	}
	if rec.Infohash != "def" {
		t.Errorf("pin 的 infohash = %q, want def", rec.Infohash)
	}
}

// TestBuildSourceHandlesConcurrentIdenticalRequests 钉住一件很容易搞错的事：
// **装配顺序**。
//
// dedupe 会把**同一个切片**返回给所有共享同一次上游调用的调用者（见 dedupe 的
// 注释：返回值在调用者之间共享，调用方不得修改）。而 pin 会原地改写
// `works[i].Magnets`。
//
// 因此 pin 必须在 dedupe **里面**：
//
//	dedupe.New(pin.New(client))   ✅ 合并成一次，那一次里改完再交给所有调用者（只读）
//	pin.New(dedupe.New(client))   ❌ 多个调用者拿到同一个切片并**并发原地改写** → 数据竞争
//
// 这条测试用 -race 跑才有意义（CI 的 make race 会跑到）。
func TestBuildSourceHandlesConcurrentIdenticalRequests(t *testing.T) {
	fake := newFakeAPI()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	if err := config.SaveToken(tokenPath, oldToken); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf("provider: appapi\napp_api:\n  host: %q\n  token_file: %q\n  probe_interval: \"0\"\n",
		srv.URL, tokenPath)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvUsername, "alice")
	t.Setenv(config.EnvPassword, "s3cret")

	holder, err := config.NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := pin.Open(holder.PinPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	src, err := buildSource(holder.Current(), holder, st)
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}

	const n = 16
	var wg sync.WaitGroup
	guids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			works, err := src.Code(context.Background(), "KV-328")
			if err != nil {
				return
			}
			// 读一下被改写过的切片 —— 与别的 goroutine 的改写撞在一起就是竞争。
			if len(works) > 0 && len(works[0].Magnets) > 0 {
				guids[i] = works[0].Magnets[0].Infohash
			}
		}(i)
	}
	wg.Wait()

	// 所有调用者都应当拿到同一个被钉住的 infohash。
	for i, g := range guids {
		if g != "" && g != "def" {
			t.Errorf("goroutine %d 拿到 %q, want def", i, g)
		}
	}
}

// ---------------------------------------------------------------------------
// 上游健康探针（ticket 05）
// ---------------------------------------------------------------------------

// newProbeHolder 造一个指向 srv 的配置，供 upstreamChecker 用。
func newProbeHolder(t *testing.T, host string) *config.Holder {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf("provider: appapi\napp_api:\n  host: %q\n  probe_interval: \"0\"\n", host)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	holder, err := config.NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return holder
}

// TestUpstreamCheckerCarriesGuidance 钉住本票的边界：
// 「下一步做什么」这段**上游特有**的知识属于检查方（upstreamChecker），
// 不再属于 health。因此：
//
//	签名类失败 → 必须给出「去改代码」的处置（指向先例项目/备灾文档）；
//	普通网络失败 → 必须给出「重试即可，别叫醒人」的处置，而不是签名那套。
//
// 这正是 health 里那条硬编码文案的替代品 —— 它搬到了这里。
func TestUpstreamCheckerCarriesGuidance(t *testing.T) {
	tests := []struct {
		name         string
		handler      http.HandlerFunc
		wantAction   string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name: "签名失效",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"success":0,"action":"InvalidSignature","message":"無效的簽名","data":null}`))
			},
			wantAction:   "InvalidSignature",
			wantContains: []string{"javdb-cli", "dart-toolchain-probe.md"},
		},
		{
			name: "普通网络故障",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("<html>502</html>"))
			},
			wantAction:   "",
			wantContains: []string{"重试"},
			// 网络抖动时不该把「去改代码」的处置端出来 ——
			// 那正是 health 里硬编码文案会犯的错。
			wantAbsent: []string{"javdb-cli", "dart-toolchain-probe.md"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			t.Cleanup(srv.Close)

			c := upstreamChecker{holder: newProbeHolder(t, srv.URL)}
			got := c.Check(context.Background())

			if got.OK {
				t.Fatal("应当报告失败")
			}
			if got.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", got.Action, tt.wantAction)
			}
			if got.Guidance == "" {
				t.Fatal("失败时必须给出处置动作 —— health 已经不再替检查方编造它了")
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(got.Guidance, want) {
					t.Errorf("Guidance 应当包含 %q，实际 %q", want, got.Guidance)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got.Guidance, absent) {
					t.Errorf("Guidance 不该包含 %q（那是签名类失败的处置），实际 %q", absent, got.Guidance)
				}
			}
		})
	}
}
