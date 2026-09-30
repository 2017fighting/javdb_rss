// Package pin 持久化「每个作品已选中的磁链」。
//
// 这是本服务**唯一**的持久状态。它存在的理由很具体（ticket 04/08）：
// feed 条目的身份是 infohash（guid），而 infohash 由「选中哪条磁链」决定。
// 上游的磁链顺序既不是时间序、也不可用任何字段重放，因此**丢状态就是丢 guid** ——
// 进程重启后重新按规则选，会让约 1/4 的作品换磁链，qBittorrent 各多下一份，
// 而且没有任何告警。pin 就是为消除这类不可见失败而存在的。
//
// 本包只负责「存哪、怎么活」（ticket 08）。「什么时候把 pin 从旧值换成新值」
// 由 Source 装饰器与 Policy 决定（ticket 09）。
package pin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// CurrentVersion 是 pin 文件的结构版本。
//
// 它必须被**真的检查**：没有它，将来换格式时旧二进制会把新文件按老结构解析，
// 解析「成功」但字段全空，于是每一条 pin 都读不出来 —— 又一种静默失败。
const CurrentVersion = 1

// Record 是一条被钉住的磁链。
//
// 它刻意不只是 infohash，而是把选择时看到的那条磁链**快照**下来
// （name/size/cnsub/created_at）。理由：pin 指向上游消失时，「继续返回它」
// 需要足够渲染出一条 feed item 的信息；只存 infohash 会让那条路走不通
// （见 ticket 09 的「pin 指向上游消失」）。快照约 150 字节/条，代价可忽略。
type Record struct {
	Infohash  string    `json:"infohash"`
	Name      string    `json:"name,omitempty"`
	SizeMB    int       `json:"size_mb,omitempty"`
	CNSub     bool      `json:"cnsub,omitempty"`
	CreatedAt string    `json:"created_at,omitempty"`
	PinnedAt  time.Time `json:"pinned_at"`
}

// file 是 pin.json 的线格式。
type file struct {
	Version int               `json:"version"`
	Pins    map[string]Record `json:"pins"`
}

// Store 是 pin 表的持有者。
//
// 内存里保留整张表（几千条小键值，全量重写足够快），写入时整体落盘。
// 同一进程内并发安全；跨进程由 flock 保证只有一个写者。
type Store struct {
	path string
	// lock 是持锁的句柄，同时充当启动时的「目录可写」探针 ——
	// 创建它就需要在目标目录里写文件。
	lock *os.File

	mu    sync.Mutex
	pins  map[string]Record
	dirty bool

	log *slog.Logger
}

// Open 读取 pin 文件并取得独占锁。
//
// 返回错误的情形都是**拒绝启动**（ticket 08 的 fail fast 决定）：
//
//	目录不可写 / 不存在且建不出来 -> 配置错误，不静默退化
//	文件损坏 / version 不认识     -> 绝不能用空表静默盖掉用户状态
//	已被另一个实例占用            -> 两个写者会导致同一作品两个 guid
//
// 文件**不存在**不是错误：首次运行是常态。此时不创建文件 ——
// 只有真正要写一条 pin 时才落盘（那一次失败会当场致命，见 Flush）。
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("pin 文件路径为空")
	}
	log := slog.Default()

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("创建 pin 目录 %s: %w", dir, err)
		}
	}

	// 先拿锁再读文件。顺序反过来的话，两个实例会同时读到旧表、
	// 然后后写的那个覆盖前一个 —— 锁要在读之前。
	lock, err := acquireLock(path)
	if err != nil {
		return nil, err
	}

	// 再**真的写一次**探测目录可写性。
	//
	// 不能只靠上面那次「建锁文件」探活：锁文件已存在时，
	// O_CREATE|O_RDWR 只要求文件本身可写，而不管目录是否可写（目录只读时
	// 它照样成功）。那种情况下的失败会推到第一次响应时才暴露 ——
	// 而 ticket 08 要的是**启动时**就拒绝。这里建一个临时文件再删掉，
	// 用的是与真正落盘（pin.json.tmp + rename）同一条路径。
	if err := probeWritable(path); err != nil {
		_ = lock.Close()
		return nil, err
	}

	s := &Store{path: path, lock: lock, pins: map[string]Record{}, log: log}
	if err := s.load(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	log.Info("pin 表已加载", "path", path, "条数", len(s.pins))
	return s, nil
}

// acquireLock 在 pin 文件旁取一把独占、非阻塞的锁。
func acquireLock(path string) (*os.File, error) {
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// 这里失败通常就是「目录不可写」—— 正是启动时该拦下的配置错误。
		// 报错里直接给出处置办法：升级上来的实例最可能就是撞在这里
		// （旧版本的配置目录可能一直是只读的）。
		return nil, fmt.Errorf(
			"打开 pin 锁文件 %s: %w —— pin 需要可写目录。"+
				"请把 app_api.pin_file 指到一个可写路径（裸二进制下它默认在配置文件旁边），"+
				"或把该目录设为可写",
			lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf(
			"pin 文件 %s 已被另一个实例占用（%w）—— 本服务只支持单副本："+
				"两个实例会各钉一套磁链，同一部作品产生两个 guid，qBittorrent 会重复下载",
			path, err)
	}
	return f, nil
}

// probeWritable 确认 pin 文件所在目录真的可写。
//
// 用「建临时文件再删」而不是 access(2)：access 与真正的 open 在只读挂载、
// ACL、容器安全策略下并不总是一致，而这里要的就是「真写一次」。
func probeWritable(path string) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".probe*")
	if err != nil {
		return fmt.Errorf(
			"pin 目录 %s 不可写: %w —— pin 需要可写目录。"+
				"请把 app_api.pin_file 指到一个可写路径（裸二进制下它默认在配置文件旁边），"+
				"或把该目录设为可写", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// load 从磁盘读入 pin 表。
func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		s.pins = map[string]Record{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 pin 文件 %s: %w", s.path, err)
	}
	if len(raw) == 0 {
		s.pins = map[string]Record{}
		return nil
	}

	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf(
			"解析 pin 文件 %s: %w —— 文件已损坏。修好或删掉它再启动；"+
				"本服务不会用空表覆盖它", s.path, err)
	}
	if f.Version != CurrentVersion {
		return fmt.Errorf(
			"pin 文件 %s 的 version=%d 不被支持（本二进制支持 %d）—— "+
				"请用匹配的二进制，或手工迁移该文件", s.path, f.Version, CurrentVersion)
	}
	if f.Pins == nil {
		f.Pins = map[string]Record{}
	}
	s.pins = f.Pins
	return nil
}

// Reload 重新读取 pin 文件（SIGHUP）。
//
// 失败时**保留旧表**并返回错误 —— 与配置热重载同一套语义。运维手工删掉一条 pin
// 是合理操作（「这个作品想重选」），不重读就只能重启整个进程。
//
// ⚠️ 重读前会**先把待落盘的变更刷盘**。不这样做的话，一个正好在重读窗口里
// 完成选择的请求，其 pin 会被刚读进来的文件内容覆盖掉 —— 而那些 pin 还没写盘，
// 于是永久丢失（下次轮询重新按规则选 → guid 可能抖）。本票存在的理由就是
// 不让 pin 丢，所以这里不能省这一步。
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.flushLocked(FlushStats{}); err != nil {
		// 刷盘失败不覆盖旧表，也不继续重读：让运维先看见写盘的问题。
		return err
	}
	old := s.pins
	if err := s.load(); err != nil {
		s.pins = old
		return err
	}
	s.dirty = false
	return nil
}

// Get 查一条 pin。
func (s *Store) Get(id string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.pins[id]
	return r, ok
}

// Len 返回表里的条数，供可观测（不设上限，只把条数记进日志）。
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pins)
}

// Path 返回 pin 文件路径。
func (s *Store) Path() string { return s.path }

// Set 写入或更新一条 pin，并标记为待落盘。
//
// 它**不**立即写磁盘：一次 feed 渲染可能产生几十条新 pin，而写入是全量重写。
// 由调用方在请求结束时调一次 Flush（ticket 08：「每请求合并写一次」）。
func (s *Store) Set(id string, r Record) {
	if r.PinnedAt.IsZero() {
		r.PinnedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pins[id] = r
	s.dirty = true
}

// FlushStats 是本次落盘的变更计数，仅用于日志。
type FlushStats struct {
	// Added 是本请求新增的 pin 数，Switched 是改变了 infohash 的 pin 数。
	Added    int
	Switched int
}

// Flush 把内存表落盘 —— 但**只在真的有变更时**。
//
// 无变更零写入是热路径上的关键：qBittorrent 每 15 分钟轮询一次，
// 而正常情况下所有作品都已钉住，此时不该碰磁盘。
//
// 写失败必须由调用方当作**致命**：本函数返回错误，main 据此退出。
// 继续用内存表服务会让「重启后的行为」与「本次运行」不一致，
// 而那种差异没人能解释；退出则 k8s/systemd 立刻把它变成可见的重启。
func (s *Store) Flush(stats FlushStats) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked(stats)
}

// flushLocked 是 Flush 的实现，要求调用方已持有 s.mu。
// Reload 也用它（它需要「先刷盘再重读」，见那里的注释）。
func (s *Store) flushLocked(stats FlushStats) error {
	if !s.dirty {
		return nil
	}

	start := time.Now()
	raw, err := json.MarshalIndent(file{Version: CurrentVersion, Pins: s.pins}, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 pin 表: %w", err)
	}
	raw = append(raw, '\n')

	// 原子替换，与 config.SaveToken 同一套做法：
	// 写一半断电留下半截文件，下次启动会读到一个坏 JSON 而拒绝启动。
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写入 %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("设置 %s 权限: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换 %s: %w", s.path, err)
	}

	s.dirty = false
	s.log.Info("pin 表已更新",
		"path", s.path, "新增", stats.Added, "切换", stats.Switched,
		"总计", len(s.pins), "耗时", time.Since(start).String())
	return nil
}

// Close 释放锁。它不负责落盘 —— Flush 是每个请求的收尾，Close 只是清理。
func (s *Store) Close() error {
	if s.lock == nil {
		return nil
	}
	err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	cerr := s.lock.Close()
	s.lock = nil
	if err != nil {
		return err
	}
	return cerr
}
