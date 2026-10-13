package mteam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 判定表是本轮唯一新增的持久状态，因此这里测的全是**它在坏情况下怎么办** ——
// 正常的读写往返反而次要（那是必然成立的）。先例是 internal/pin/store_test.go。

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s.Len(); got != 0 {
		t.Fatalf("新表应当为空，得到 %d 条", got)
	}

	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	s.Put("work-1", Verdict{
		Outcome: OutcomeHit,
		Chosen:  Candidate{ID: "467487", Infohash: "abc", Name: "SSIS-001", SizeMiB: 6518, CNSub: true},
		At:      at,
	})
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// 先放锁再重开 —— 本服务是单写者，两个实例同时开着会被 flock 拒绝（那是刻意的）。
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 重新打开（模拟重启）：判定必须原样回来。
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	defer func() { _ = s2.Close() }()
	v, ok := s2.Get("work-1")
	if !ok {
		t.Fatal("重启后判定丢了")
	}
	if v.Outcome != OutcomeHit || v.Chosen.Infohash != "abc" {
		t.Errorf("判定内容不对: %+v", v)
	}
	if !v.Chosen.CNSub {
		t.Error("候选快照里的中文字幕标记丢了")
	}
}

// TestStoreRefusesToStartOnCorruptFile 钉住「绝不用空表静默覆盖」。
//
// 空表看起来「服务正常」，但它把「已经查过」变成了「没查过」——
// 表现是每轮都去问一遍馒头（烧配额），而且没有任何告警。
func TestStoreRefusesToStartOnCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{ 这不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil {
		t.Fatal("坏文件必须拒绝启动")
	}
	// 报错里要给出**处置办法**：这份状态可以删（它不改 guid）。
	if !strings.Contains(err.Error(), "可以") {
		t.Errorf("报错应当说明它可以删掉重建，实际是 %q", err)
	}
}

// TestStoreRefusesUnknownVersion 钉住版本检查真的执行。
func TestStoreRefusesUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"version":99,"verdicts":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("不认识的版本必须拒绝启动")
	}
}

// TestStoreRefusesSecondInstance 钉住单写者。
//
// 两个实例会各查一套判定，同一部作品可能发出两个来源 —— 而这在客户端是两份下载。
func TestStoreRefusesSecondInstance(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("第一个实例: %v", err)
	}
	defer func() { _ = first.Close() }()

	if _, err := Open(dir); err == nil {
		t.Fatal("第二个指向同一目录的实例必须拒绝启动")
	}
}

// TestStoreTorrentBytesRoundTripAndPermissions 钉住种子的落点与权限。
//
// 权限不是形式主义：种子里带着一份**账号维度的 tracker 凭据**，
// 而它决定「同机器上别的用户能不能拿走它」。
func TestStoreTorrentBytesRoundTripAndPermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	data := []byte("d8:announce4:abce")
	if err := s.SaveTorrent("abc123", data); err != nil {
		t.Fatalf("SaveTorrent: %v", err)
	}

	got, err := s.LoadTorrent("abc123")
	if err != nil {
		t.Fatalf("LoadTorrent: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("取回的字节 = %q, want %q", got, data)
	}

	// 落在判定表目录下的 torrents/ 里，而不是散在别处。
	want := filepath.Join(dir, torrentDir, "abc123.torrent")
	if s.TorrentPath("abc123") != want {
		t.Errorf("TorrentPath = %q, want %q", s.TorrentPath("abc123"), want)
	}
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("种子没落在预期位置: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("种子权限 = %o, want 600（它含账号凭据）", perm)
	}
}

// TestStoreTorrentMissingIsAnError 确认「没有那份种子」是可判定的错误，
// 而不是一个空切片 —— 端点要据此返回 404，而不是发一个空文件给客户端。
func TestStoreTorrentMissingIsAnError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.LoadTorrent("没有这个"); err == nil {
		t.Fatal("缺种子应当报错")
	}
}

// TestStoreFlushIsNoOpWhenClean 钉住「无变更零写入」。
//
// 热路径是 qBittorrent 每 15 分钟一次的轮询，而正常情况下没有任何新判定 ——
// 那时不该碰磁盘。
func TestStoreFlushIsNoOpWhenClean(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(err) {
		t.Error("没有变更时不该产生判定表文件")
	}
}
