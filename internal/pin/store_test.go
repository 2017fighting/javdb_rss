package pin

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain 把本包的日志静音 —— pin 的 INFO 日志在测试里只是噪声。
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// 本节测的是 Store 的**打开语义** —— ticket 08 的「fail fast」决定里
// 最容易写错、后果最隐蔽的那一半：
//
//	文件不存在          -> 空表，正常启动（首次运行是常态，不是错误）
//	文件损坏            -> 拒绝启动（绝不能用空表静默盖掉用户状态）
//	version 不认识      -> 拒绝启动（未来格式不能被当成今天这个读）
//	目录不可写          -> 拒绝启动（配置错误，不静默退化）
//	同一文件已被占用    -> 拒绝启动（flock 单写者）

// TestOpenMissingFileIsEmptyStore 确认首次运行是正常路径。
//
// 它**不**创建 pin.json —— 只有真正要写一条 pin 时才落盘。
//
// 但它仍然要求目录**可写**（会在那里建 pin.json.lock，并做一次临时写探测）—— 这是刻意的：
// 启动时就探测可写性，把一个只会在第一次响应时才暴露的失败提前到启动时
// （TestOpenProbesDirectoryWritability 钉住这一点）。一个目录只读的实例因此**不会**启动；
// 那是 ticket 08 选定的「宁可看见错误，不要静默降级」。
func TestOpenMissingFileIsEmptyStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("文件不存在时应当正常打开: %v", err)
	}
	defer st.Close()

	if st.Len() != 0 {
		t.Errorf("空表的 Len() = %d, want 0", st.Len())
	}
	if _, ok := st.Get("anything"); ok {
		t.Error("空表不该有命中")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("打开阶段不该创建 pin.json（err=%v）", err)
	}
}

// TestOpenRejectsCorruptFile 钉住「损坏就不启动」。
//
// 如果这里降级成空表，用户的 pin 就在下一次 Flush 时被**静默覆盖** ——
// 然后所有作品重新按纯函数选、guid 抖动、qBittorrent 重复下载，
// 而没有任何东西告诉过他。这是本票最要避免的一类失败。
func TestOpenRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); err == nil {
		t.Fatal("损坏的 pin 文件应当让 Open 失败，而不是静默当成空表")
	}
}

// TestOpenRejectsUnknownVersion 确认版本号是**真的**被检查的。
//
// 少了它，未来换格式时旧二进制会把新文件按老结构解析 ——
// 解析「成功」但字段是空的，于是每一条 pin 都读不出来。
func TestOpenRejectsUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")
	body := `{"version": 999, "pins": {"aBc": {"infohash": "x"}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Open(path)
	if err == nil {
		t.Fatal("version=999 应当让 Open 失败")
	}
	// 报错必须提到版本，否则运维看不出该改文件还是改二进制。
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("报错里应当出现版本号，实际: %v", err)
	}
}

// TestOpenRejectsUnwritableDirectory 钉住启动时的「不可写就拒绝启动」。
//
// 用「父路径是一个普通文件」来构造失败，而不是 chmod：测试常以 root 跑，
// 而 root 会绕过权限位 —— 那种测试在 CI 里会静默变成空转。
func TestOpenRejectsUnwritableDirectory(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "iam-a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(filepath.Join(notADir, "pin.json")); err == nil {
		t.Fatal("父路径不是目录时应当让 Open 失败")
	}
}

// TestSecondOpenIsRefused 钉住 flock 单写者。
//
// 两个实例各钉一套 -> 同一部作品两个 guid -> qBittorrent 重复下载。
// 这是「不会被发现」的失败，因此把隐含约束（单副本）变成强制的、可见的拒绝。
func TestSecondOpenIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("第一个实例应当打开成功: %v", err)
	}
	defer first.Close()

	if _, err := Open(path); err == nil {
		t.Fatal("已有实例占用时，第二个 Open 必须失败")
	}

	// 关掉第一个之后，第二个应当能拿到锁 —— 证明那把锁真的被释放了，
	// 而不是「永远锁着」这种看起来正确的假象。
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatalf("前一个实例关闭后应当能打开: %v", err)
	}
	defer second.Close()
}

// --- 写入与重读 -------------------------------------------------------------

// TestSetAndFlushRoundTrips 是持久性本身：写进去、换一个 Store 读出来。
//
// 这是 pin 存在的全部意义 —— 进程重启后还能认出「上次选的是哪条」。
func TestSetAndFlushRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Set("aBc123", Record{
		Infohash:  "0e8f4789",
		Name:      "KV-328",
		SizeMB:    3110,
		CNSub:     false,
		CreatedAt: "09/27/2026",
	})
	if err := st.Flush(FlushStats{Added: 1}); err != nil {
		t.Fatalf("落盘: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("重新打开: %v", err)
	}
	defer st2.Close()

	got, ok := st2.Get("aBc123")
	if !ok {
		t.Fatal("重新打开后应当能读到那条 pin")
	}
	if got.Infohash != "0e8f4789" || got.SizeMB != 3110 || got.Name != "KV-328" {
		t.Errorf("快照字段丢了: %+v", got)
	}
	if got.PinnedAt.IsZero() {
		t.Error("pinned_at 应当被自动填上")
	}
}

// TestFlushWithoutChangesWritesNothing 钉住热路径不碰磁盘。
//
// qBittorrent 每 15 分钟轮询一次，而正常情况所有作品都已钉住 ——
// 每次轮询都重写一遍 pin.json 是没必要的磁盘写。
//
// 用「把文件改成只读内容后不动它」来验证：如果 Flush 真写了，内容会变。
func TestFlushWithoutChangesWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	st.Set("aBc", Record{Infohash: "aaa"})
	if err := st.Flush(FlushStats{Added: 1}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// 一次「全部命中」的轮询：只读、不 Set。
	if err := st.Flush(FlushStats{}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("无变更时 Flush 不该重写文件")
	}
	// 更直接的一条：把文件换成哨兵内容，无变更的 Flush 不该动它。
	if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Flush(FlushStats{}); err != nil {
		t.Fatal(err)
	}
	now, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(now) != "sentinel" {
		t.Error("无变更时 Flush 覆盖了文件 —— 热路径上不该有磁盘写")
	}
}

// TestFlushRejectsUnwritableTarget 是「运行中写失败 = 致命」的机制面。
//
// Flush 必须返回错误（而不是吞掉），因为 main 要据此退出：
// 悄悄继续用内存表服务，会让重启后的行为与本次运行不一致，而那种差异没人能解释。
func TestFlushRejectsUnwritableTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Set("aBc", Record{Infohash: "aaa"})

	// 把 tmp 的目标位置变成一个目录 —— WriteFile 必然失败。
	// 用这种构造而不是 chmod，因为测试可能以 root 跑（root 会绕过权限位）。
	if err := os.MkdirAll(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}

	if err := st.Flush(FlushStats{Added: 1}); err == nil {
		t.Fatal("写失败时 Flush 必须返回错误")
	}
}

// TestReloadPicksUpExternalEdits 钉住 SIGHUP 重读（运维手工删 pin 是合理操作）。
func TestReloadPicksUpExternalEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Set("aBc", Record{Infohash: "aaa"})
	st.Set("xYz", Record{Infohash: "bbb"})
	if err := st.Flush(FlushStats{Added: 2}); err != nil {
		t.Fatal(err)
	}

	// 运维手工把 xYz 删掉（「这个作品想重选」）。
	hand := `{"version":1,"pins":{"aBc":{"infohash":"aaa","pinned_at":"2026-09-30T00:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := st.Reload(); err != nil {
		t.Fatalf("重读手工编辑过的文件应当成功: %v", err)
	}
	if _, ok := st.Get("xYz"); ok {
		t.Error("重读后不该还留着已被删掉的 pin")
	}
	if _, ok := st.Get("aBc"); !ok {
		t.Error("重读后应保留仍在文件里的 pin")
	}
}

// TestReloadKeepsOldTableOnError 确认重读失败不毁掉内存里的表。
//
// 与配置热重载同一套语义：一个手滑改坏的文件不该让正在服务的实例失去状态。
func TestReloadKeepsOldTableOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Set("aBc", Record{Infohash: "aaa"})
	if err := st.Flush(FlushStats{Added: 1}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("{ broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Reload(); err == nil {
		t.Fatal("损坏的文件应当让 Reload 报错")
	}
	if _, ok := st.Get("aBc"); !ok {
		t.Error("重读失败后必须保留旧表")
	}
}

// TestFlushWritesVersionAndOwnerOnly 钉住线格式的两条硬要求：
// 文件里带 version（未来可迁移），权限是 0600（它含用户的观看偏好）。
func TestFlushWritesVersionAndOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Set("aBc", Record{Infohash: "aaa"})
	if err := st.Flush(FlushStats{Added: 1}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version": 1`) {
		t.Errorf("文件里应当有 version: 1，实际: %s", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("pin.json 权限 = %o, want 600", perm)
	}
}

// TestReloadFlushesPendingWritesFirst 钉住一个**静默丢 pin** 的窗口：
// 一个刚做出选择、还没落盘的请求，若与 SIGHUP 重读撞在一起，
// 会被刚读进来的文件内容覆盖 —— 而那些 pin 从未写盘，于是永久丢失。
//
// 丢 pin 就是丢 guid，所以重读前必须先刷盘。
func TestReloadFlushesPendingWritesFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 一条已落盘的旧 pin，与一条刚选出、**尚未**落盘的新 pin。
	st.Set("old", Record{Infohash: "aaa"})
	if err := st.Flush(FlushStats{Added: 1}); err != nil {
		t.Fatal(err)
	}
	st.Set("fresh", Record{Infohash: "bbb"}) // 故意不 Flush

	if err := st.Reload(); err != nil {
		t.Fatalf("重读: %v", err)
	}
	if _, ok := st.Get("fresh"); !ok {
		t.Fatal("重读把尚未落盘的 pin 弄丢了 —— 那会让 guid 抖动")
	}
	if _, ok := st.Get("old"); !ok {
		t.Error("重读丢了已有的 pin")
	}

	// 也确认它真的到了磁盘上，而不只是留在内存。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "bbb") {
		t.Errorf("刷盘后文件里应当有 fresh 那条: %s", raw)
	}
}

// TestOpenProbesDirectoryWritability 钉住「启动时就拒绝」，而不是拖到第一次响应。
//
// 这条覆盖的是**只开锁文件探不出来**的那种情况：pin.json.lock 已经存在
// （上一次运行留下的），此时 O_CREATE|O_RDWR 只要文件本身能打开就成功，
// 目录只读它也不报错 —— 于是服务会正常启动，直到第一个请求要落盘时才炸。
//
// 这里用一个**预先存在的锁文件 + 只读目录**来构造。为了在 root 下也有效，
// 用「把目录的写权限去掉」只在非 root 时可靠；因此这里改用 chmod 后
// 直接断言 probeWritable 的契约。
func TestOpenProbesDirectoryWritability(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 会绕过权限位，这条测试在 root 下无意义")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")
	// 预先造一个锁文件，让 acquireLock 的探活失效。
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// 把目录设为不可写。
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := Open(path); err == nil {
		t.Fatal("目录只读时 Open 必须失败 —— 否则要到第一次落盘才发现")
	}
}

// TestOpenRejectsUnwritableDirectoryViaProbe 是上面那条的 root 安全版：
// 不改权限，直接验证 probeWritable 把一个「不是一个目录」的路径判死。
func TestOpenRejectsUnwritableDirectoryViaProbe(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := probeWritable(filepath.Join(file, "pin.json")); err == nil {
		t.Fatal("不可写的目录应当被 probeWritable 判死")
	}
}
