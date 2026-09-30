package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExampleConfigIsValid 把 config.example.yaml 当成一份必须长期可用的文档来测：
// 示例一旦跑不动，照着它配的人就会撞墙。
func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("示例配置无法加载: %v", err)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Errorf("listen = %q", cfg.Listen)
	}
	if cfg.Provider != ProviderAppAPI {
		t.Errorf("示例配置的 provider = %q，应当是 appapi（真实数据源已可用）", cfg.Provider)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDefaultListenIsLoopback 钉住一个安全默认：
// 服务不做鉴权，因此默认绝不能绑 0.0.0.0 —— 暴露必须是显式动作。
func TestDefaultListenIsLoopback(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Errorf("默认 listen = %q，应当是 127.0.0.1:8080", cfg.Listen)
	}
}

// TestAllowlist 确认「没写 feeds: 段」与「写了 feeds: 段」是两种不同意图。
func TestAllowlist(t *testing.T) {
	open, err := Load(writeConfig(t, "provider: stub\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !open.AllowsCode("任意番号") {
		t.Error("没有 feeds 段时应当放行任何番号")
	}
	if _, ok := open.ActressSub("任意id"); !ok {
		t.Error("没有 feeds 段时应当放行任何女优")
	}

	closed, err := Load(writeConfig(t, `
provider: stub
feeds:
  codes: [KV-328]
  actresses:
    - id: EvkJ
      params: {filter_by: apmc}
      since: "2026-01-01"
`))
	if err != nil {
		t.Fatal(err)
	}
	if !closed.AllowsCode("kv-328") {
		t.Error("白名单匹配应当忽略大小写")
	}
	if closed.AllowsCode("ABC-123") {
		t.Error("白名单外的番号不该放行")
	}
	sub, ok := closed.ActressSub("evkj")
	if !ok {
		t.Fatal("白名单内的女优应当放行（忽略大小写）")
	}
	if sub.Params["filter_by"] != "apmc" || sub.Since != "2026-01-01" {
		t.Errorf("订阅参数没读对: %+v", sub)
	}
	if _, ok := closed.ActressSub("nobody"); ok {
		t.Error("白名单外的女优不该放行")
	}
}

// TestUnknownKeyRejected 拼错的键必须报错。
// 静默忽略一个拼错的键，意味着用户改了配置却毫无效果。
func TestUnknownKeyRejected(t *testing.T) {
	_, err := Load(writeConfig(t, "provider: stub\nlissten: \"127.0.0.1:1\"\n"))
	if err == nil {
		t.Fatal("拼错的键应当导致加载失败")
	}
}

// TestAppAPIProviderAccepted 确认真实数据源现在能配了（ticket 06 打通后）。
func TestAppAPIProviderAccepted(t *testing.T) {
	cfg, err := Load(writeConfig(t, "provider: appapi\n"))
	if err != nil {
		t.Fatalf("provider=appapi 应当可用: %v", err)
	}
	if cfg.Provider != ProviderAppAPI {
		t.Errorf("provider = %q", cfg.Provider)
	}
}

// TestStubProviderStillAccepted 确认离线调试通道没被拆掉。
func TestStubProviderStillAccepted(t *testing.T) {
	if _, err := Load(writeConfig(t, "provider: stub\n")); err != nil {
		t.Errorf("provider=stub 应当仍可用: %v", err)
	}
}

func TestHolderReloadKeepsOldOnFailure(t *testing.T) {
	p := writeConfig(t, "provider: stub\nlisten: \"127.0.0.1:1234\"\n")
	h, err := NewHolder(p)
	if err != nil {
		t.Fatal(err)
	}
	if h.Current().Listen != "127.0.0.1:1234" {
		t.Fatalf("初始 listen = %q", h.Current().Listen)
	}

	// 写坏配置后重载：必须报错，但当前配置不能变。
	if err := os.WriteFile(p, []byte("provider: stub\nlisten: [这不是字符串\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err == nil {
		t.Fatal("坏配置应当导致重载失败")
	}
	if h.Current().Listen != "127.0.0.1:1234" {
		t.Errorf("重载失败后配置被改动了: %q", h.Current().Listen)
	}

	// 修好后重载应当生效。
	if err := os.WriteFile(p, []byte("provider: stub\nlisten: \"127.0.0.1:5678\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatalf("重载: %v", err)
	}
	if h.Current().Listen != "127.0.0.1:5678" {
		t.Errorf("重载后 listen = %q", h.Current().Listen)
	}
}

func TestLoadToken(t *testing.T) {
	dir := t.TempDir()

	// 文件不存在 = 匿名访问，不是错误。需求 1/2/3 的正常形态。
	if tok, err := LoadToken(filepath.Join(dir, "nope")); err != nil || tok != "" {
		t.Errorf("缺文件应返回空 token 且无错: %q %v", tok, err)
	}
	if tok, err := LoadToken(""); err != nil || tok != "" {
		t.Errorf("空路径应返回空 token 且无错: %q %v", tok, err)
	}

	bare := filepath.Join(dir, "bare.txt")
	if err := os.WriteFile(bare, []byte("  eyJhbGciOi.jwt.payload\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := LoadToken(bare); err != nil || tok != "eyJhbGciOi.jwt.payload" {
		t.Errorf("裸 JWT 解析结果 = %q, %v", tok, err)
	}

	js := filepath.Join(dir, "token.json")
	if err := os.WriteFile(js, []byte(`{"token":"abc.def.ghi"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := LoadToken(js); err != nil || tok != "abc.def.ghi" {
		t.Errorf("JSON token 解析结果 = %q, %v", tok, err)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"token":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(bad); err == nil {
		t.Error("坏 JSON 应当报错")
	}
}

// TestProbeIntervalParsesDuration 确认 "15m" 这类写法能解析成 time.Duration。
// yaml.v3 对 time.Duration 有特殊处理，这一点值得钉住 ——
// 它要是哪天变成只接受纳秒整数，配置会静默变成 0（探针被关掉）而不是报错。
func TestProbeIntervalParsesDuration(t *testing.T) {
	cfg, err := Load(writeConfig(t, "provider: stub\napp_api:\n  probe_interval: \"15m\"\n"))
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if cfg.AppAPI.ProbeInterval != 15*time.Minute {
		t.Errorf("ProbeInterval = %v, want 15m", cfg.AppAPI.ProbeInterval)
	}
}

// TestProbeIntervalDefaultsOn 确认默认是打开的 ——
// 忘配就等于没有最早期的失效告警，而这个告警正是探针的全部价值。
func TestProbeIntervalDefaultsOn(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppAPI.ProbeInterval <= 0 {
		t.Errorf("默认 ProbeInterval = %v，应当大于 0（默认开启探针）", cfg.AppAPI.ProbeInterval)
	}
}

// TestProbeIntervalCanBeDisabled 确认能显式关掉，且关掉就是 0 而不是某个默认值。
func TestProbeIntervalCanBeDisabled(t *testing.T) {
	cfg, err := Load(writeConfig(t, "provider: stub\napp_api:\n  probe_interval: \"0\"\n"))
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if cfg.AppAPI.ProbeInterval != 0 {
		t.Errorf("ProbeInterval = %v, want 0", cfg.AppAPI.ProbeInterval)
	}
}

// ---------------------------------------------------------------------------
// token 的来源与优先级
// ---------------------------------------------------------------------------

// TestTokenFromEnvWinsOverFile 钉住优先顺序：环境变量 > 文件。
//
// 环境变量是**正式部署**的通道（k8s Secret / docker env_file）——
// 容器里不方便挂一个可写文件，而且 env 更符合「凭据不进镜像」的做法。
// 文件则留给本地开发与 `javdb-rss login` 写入。
//
// 两者都用时环境变量赢，因为它是更明确的那一个。
func TestTokenFromEnvWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	if err := os.WriteFile(path, []byte(`{"token":"from-file"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("JAVDB_TOKEN", "from-env")
	tok, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "from-env" {
		t.Errorf("token = %q，环境变量应当优先于文件", tok)
	}
}

// TestTokenFromEnvWithNoFile 确认没有文件时环境变量单独可用 ——
// 这正是容器里的形态（只给 env，不挂文件）。
func TestTokenFromEnvWithNoFile(t *testing.T) {
	t.Setenv("JAVDB_TOKEN", "env-only")
	tok, err := LoadToken(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("没有文件不该报错: %v", err)
	}
	if tok != "env-only" {
		t.Errorf("token = %q", tok)
	}
}

// TestTokenEnvEmptyFallsBackToFile 确认空的环境变量不算「设置了」——
// 否则一个空的 env（比如 compose 里写了但没填）会让文件里的 token 失效。
func TestTokenEnvEmptyFallsBackToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	if err := os.WriteFile(path, []byte("from-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JAVDB_TOKEN", "   ")
	tok, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "from-file" {
		t.Errorf("token = %q，空白的环境变量应当被忽略", tok)
	}
}

// TestLoadCredentials 确认自动续期的凭据只从环境变量取，且两项齐全才算配置。
func TestLoadCredentials(t *testing.T) {
	// 未配置
	t.Setenv("JAVDB_USERNAME", "")
	t.Setenv("JAVDB_PASSWORD", "")
	if _, _, ok := LoadCredentials(); ok {
		t.Error("两个都没设时不该报告已配置")
	}
	// 只设一半 —— 不算配置完成，否则会拿空密码去打上游
	t.Setenv("JAVDB_USERNAME", "alice")
	if _, _, ok := LoadCredentials(); ok {
		t.Error("只设了用户名时不该报告已配置")
	}
	// 齐全
	t.Setenv("JAVDB_PASSWORD", "s3cret")
	u, p, ok := LoadCredentials()
	if !ok || u != "alice" || p != "s3cret" {
		t.Errorf("LoadCredentials = (%q,%q,%v)", u, p, ok)
	}
}

// TestSaveTokenWrites0600 确认写出来的 token 文件只有属主可读。
//
// 它不是可选项：token 等同于账号的登录态，而 0644 在多用户机器上
// 等于把账号交出去。测试直接断言权限位。
func TestSaveTokenWrites0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "token.json")
	if err := SaveToken(path, "eyJhbGciOi.test"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("权限 = %o，want 600", perm)
	}
	// 回读要能拿到同一个值（证明写的形态与 LoadToken 的解析对得上）。
	got, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "eyJhbGciOi.test" {
		t.Errorf("回读 = %q", got)
	}
}

// TestSaveTokenOverwritesTighterPerms 确认已存在的宽松权限文件会被收紧。
//
// 场景真实：用户手工创建过一个 0644 的 token 文件，然后跑 login 覆盖它。
// 如果只是写内容而不改权限，旧权限会留着。
func TestSaveTokenOverwritesTighterPerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveToken(path, "new"); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("覆盖后权限 = %o，want 600", perm)
	}
}

// TestSaveTokenRejectsEmpty 确认不写空 token —— 那会让下一次启动
// 静默变成匿名访问，而用户以为他登录过了。
func TestSaveTokenRejectsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	if err := SaveToken(path, "   "); err == nil {
		t.Error("空 token 应当报错")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("报错时不该留下文件")
	}
}

// TestTokenPathDefaultsNextToConfig 确认 token_file 留空时的落点。
//
// 这个 bug 真实发生过：示例配置里 token_file 是 ""（因为它是可选项），
// 而 login 直接拿 "" 当路径 —— 登录成功了、用户手机被踢下线了，
// 结果 `os.Rename(".tmp", "")` 失败，**token 丢了**，用户得再登一次。
//
// 默认放在配置文件旁边，是因为「配置在哪儿、它的凭据就在哪儿」最不容易搞错。
func TestTokenPathDefaultsNextToConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("provider: stub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "token.json")
	if got := h.TokenPath(); got != want {
		t.Errorf("TokenPath() = %q, want %q", got, want)
	}
}

// TestTokenPathHonoursExplicitValue 确认显式配了就照用。
func TestTokenPathHonoursExplicitValue(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("provider: stub\napp_api:\n  token_file: \"/tmp/custom-token.json\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := NewHolder(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.TokenPath(); got != "/tmp/custom-token.json" {
		t.Errorf("TokenPath() = %q", got)
	}
}

// TestSaveTokenRejectsEmptyPath 确认空路径报错而不是去写一个叫 ".tmp" 的文件。
//
// 上一层的默认值应该保证不会有空路径，但这里再挡一道 —— 静默写错位置
// 比明确报错难查得多。
func TestSaveTokenRejectsEmptyPath(t *testing.T) {
	if err := SaveToken("", "tok"); err == nil {
		t.Fatal("空路径应当报错")
	}
	// 不该留下任何垃圾文件。
	if _, err := os.Stat(".tmp"); err == nil {
		t.Error("空路径下不该写出 .tmp 文件")
	}
}
