package mteam

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// CurrentVersion 是判定表文件的结构版本。
//
// 它必须被**真的检查**：没有它，将来换格式时旧二进制会把新文件按老结构解析，
// 解析「成功」但字段全空 —— 于是每一条判定都读不出来，又一种静默失败。
// （照 pin 的先例。）
const CurrentVersion = 1

// Outcome 是一次判定的结果。
//
// 三种结果的**语义完全不同**，这是本轮最容易做糊的地方：
//
//	命中     —— 馒头有。候选快照与种子字节都已取回，不再重问。
//	负结果   —— 查过，馒头没有。**永久**，不会因为「也许以后有了」而重查。
//	查询失败 —— **没查成**（网络 / 限流 / 凭据）。它不构成判决，只有「重置」会再试。
type Outcome string

const (
	OutcomeHit    Outcome = "hit"
	OutcomeMiss   Outcome = "miss"
	OutcomeFailed Outcome = "failed"
)

// Candidate 是**选中那条**候选的快照。
//
// `ID` 是种子在馒头上的 id，它在「重置」与重新取种子时要用
// （只有 infohash 是换不到下载链接的）。
type Candidate struct {
	ID        string `json:"id"`
	Infohash  string `json:"infohash"`
	Name      string `json:"name,omitempty"`
	SizeMiB   int    `json:"size_mib,omitempty"`
	CNSub     bool   `json:"cnsub,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// Verdict 是一部作品的判定结果。
//
// 它只存**选中的那一条**，不存整份候选列表 —— 因为 M-Team 的候选
// **要下载才知道 infohash**，而我们只下载选中的那条；存一份「除了一条之外
// 都没有 infohash」的候选表，只会让读它的人以为那些条目能用。
// 快照的用途与 pin.Record 的一样：判定冻结之后不再重查，因此渲染需要的字段
// 必须当时就存下来。
type Verdict struct {
	Outcome Outcome `json:"outcome"`
	// Chosen 是选中的那条（含 infohash）。Outcome 不是命中时它为空。
	Chosen Candidate `json:"chosen,omitempty"`
	// At 是判定落地的时刻（人看日志用；它不参与任何判据）。
	At time.Time `json:"at"`
}

// file 是判定表文件的线格式。
type file struct {
	Version  int                `json:"version"`
	Verdicts map[string]Verdict `json:"verdicts"`
}

// Store 是判定表的持有者：按**作品 id** 记住 M-Team 的判定。
//
// # 它与 pin 的区别（这条区别决定了它们不放在同一个文件里）
//
// 丢 pin 会改 `guid` —— 客户端**静默**重复下载，而上游重建不出同一个选择。
// 丢判定表**不会**改 `guid`（pin 还在），代价只是重新查一遍 ——
// 受 100 个种子/小时的限速约束，可能要几小时，但它是**可重建**的。
//
// 所以：判定表可以丢，pin 不可以。把两者放进同一个文件，等于让「可以丢的」
// 有机会带走「不可以丢的」。
type Store struct {
	dir  string
	lock *os.File

	mu       sync.Mutex
	verdicts map[string]Verdict
	dirty    bool

	log *slog.Logger
}

// Open 读取判定表并取得独占锁。
//
// 返回错误的情形都是**拒绝启动**（照 pin 的先例）：
//
//	目录不可写 / 建不出来     -> 配置错误，不静默退化
//	文件损坏 / version 不认识 -> 绝不能用空表静默盖掉已有判定
//	已被另一个实例占用        -> 两个写者会让同一部作品得到两套判定
//
// 文件**不存在**不是错误：首次运行是常态。
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("判定表目录为空")
	}
	log := slog.Default()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建判定表目录 %s: %w", dir, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, torrentDir), 0o700); err != nil {
		return nil, fmt.Errorf("创建种子目录 %s: %w", filepath.Join(dir, torrentDir), err)
	}

	// 先拿锁再读文件（顺序反过来的话两个实例会同时读到旧表，后写的覆盖前一个）。
	lock, err := acquireLock(filepath.Join(dir, fileName))
	if err != nil {
		return nil, err
	}
	if err := probeWritable(dir); err != nil {
		_ = lock.Close()
		return nil, err
	}

	s := &Store{dir: dir, lock: lock, verdicts: map[string]Verdict{}, log: log}
	if err := s.load(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	log.Info("M-Team 判定表已加载", "dir", dir, "条数", len(s.verdicts))
	return s, nil
}

const (
	// fileName 是判定表本身。
	fileName = "mteam.json"
	// torrentDir 是种子字节的落点（判定表目录下）。
	//
	// 它单独一层目录，是因为里面的东西**含账号凭据**：目录与文件都 0600/0700，
	// 而判定表本身不含凭据。分开也让人一眼看得出「哪些文件是敏感的」。
	torrentDir = "torrents"
)

// Path 是判定表文件的完整路径。
func (s *Store) Path() string { return filepath.Join(s.dir, fileName) }

// acquireLock 在判定表文件旁取一把独占、非阻塞的锁。
func acquireLock(path string) (*os.File, error) {
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf(
			"打开判定表锁文件 %s: %w —— 判定表需要可写目录。"+
				"请把 mteam.state_dir 指到一个可写路径", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf(
			"判定表 %s 已被另一个实例占用（%w）—— 本服务只支持单副本："+
				"两个实例会各查一套判定，同一部作品可能发出两个来源", path, err)
	}
	return f, nil
}

// probeWritable 确认目录真的可写（建临时文件再删，与真正的落盘同一条路径）。
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, "mteam.probe*")
	if err != nil {
		return fmt.Errorf("判定表目录 %s 不可写: %w —— 请把 mteam.state_dir 指到一个可写路径", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// load 从磁盘读入判定表。
func (s *Store) load() error {
	raw, err := os.ReadFile(s.Path())
	if os.IsNotExist(err) {
		s.verdicts = map[string]Verdict{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取判定表 %s: %w", s.Path(), err)
	}

	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf(
			"判定表 %s 已损坏（%w）—— 拒绝启动。"+
				"它不会改 guid（pin 还在），因此**可以**删掉它让服务重新查一遍；"+
				"但绝不会用空表静默覆盖：那会把「已经查过」变成「没查过」",
			s.Path(), err)
	}
	if f.Version != CurrentVersion {
		return fmt.Errorf(
			"判定表 %s 的版本是 %d，本程序只认识 %d —— 拒绝启动（旧二进制按老结构解析会得到一张空表，那是静默失败）",
			s.Path(), f.Version, CurrentVersion)
	}
	if f.Verdicts == nil {
		f.Verdicts = map[string]Verdict{}
	}
	s.verdicts = f.Verdicts
	return nil
}

// Reload 重新从磁盘读判定表（SIGHUP 用）。
//
// 失败时保留旧表：一个手滑改坏的文件不该让正在服务的实例失去状态。
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// Get 返回某部作品的判定。第二个返回值报告有没有。
func (s *Store) Get(workID string) (Verdict, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.verdicts[workID]
	return v, ok
}

// Put 记下一条判定。
func (s *Store) Put(workID string, v Verdict) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdicts[workID] = v
	s.dirty = true
}

// Len 是已判定的作品数。
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.verdicts)
}

// Flush 把判定表落盘。无变更时零写入（qBittorrent 的常态轮询不碰磁盘）。
//
// 写失败必须由调用方当作**致命**：带着「内存已改、磁盘没改」的状态继续服务，
// 会让重启后的行为与本次运行不一致，而那种差异没人能解释。
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}

	raw, err := json.MarshalIndent(file{Version: CurrentVersion, Verdicts: s.verdicts}, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化判定表: %w", err)
	}
	raw = append(raw, '\n')

	// 原子替换：写一半断电会留下半截文件，下次启动读到坏 JSON 而拒绝启动。
	path := s.Path()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写入 %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("设置 %s 权限: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换 %s: %w", path, err)
	}

	s.dirty = false
	s.log.Info("M-Team 判定表已更新", "path", path, "条数", len(s.verdicts))
	return nil
}

// SaveTorrent 把种子字节落盘，返回它在判定表里的相对位置。
//
// 权限 0600：种子里带着一份**账号维度的 tracker 凭据**，它不是公开数据。
func (s *Store) SaveTorrent(infohash string, data []byte) error {
	path := s.TorrentPath(infohash)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入种子 %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("设置种子 %s 权限: %w", path, err)
	}
	return nil
}

// TorrentPath 是某个 infohash 的种子落点。
func (s *Store) TorrentPath(infohash string) string {
	return filepath.Join(s.dir, torrentDir, infohash+".torrent")
}

// LoadTorrent 取回某个 infohash 的种子字节。
func (s *Store) LoadTorrent(infohash string) ([]byte, error) {
	return os.ReadFile(s.TorrentPath(infohash))
}

// Close 释放锁。它不负责落盘 —— Flush 是每个请求的收尾，Close 只是清理。
func (s *Store) Close() error {
	if s.lock == nil {
		return nil
	}
	err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	cerr := s.lock.Close()
	if err != nil {
		return err
	}
	return cerr
}
