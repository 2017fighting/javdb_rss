// Package config 负责读取与热重载本服务的配置。
//
// 配置是单个 YAML 文件。选择 YAML 而不是环境变量，是因为订阅列表
// （番号、女优、透传参数）本质上是结构化数据，摊进环境变量会变得难以阅读和转义。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是整个配置文件。
type Config struct {
	// Listen 是 HTTP 监听地址。
	//
	// 默认值为 127.0.0.1:8080，**刻意不是 0.0.0.0**：本服务不做鉴权
	// （ticket 04 的决定），因此默认不应该对局域网可见。要暴露出去
	// 必须在配置里显式改这一行 —— 让「暴露」是一个需要动手的决定。
	Listen string `yaml:"listen"`

	// Provider 选择数据源实现。当前只支持 "stub"。
	//
	// 真实实现（"appapi"）要等 ticket 02（签名实装的取舍）与
	// ticket 06（番号解析规则）落地；提前把名字占好，是为了让
	// 「换数据源」这件事在配置里可见，而不是藏在代码里。
	Provider string `yaml:"provider"`

	Feed   FeedConfig   `yaml:"feed"`
	AppAPI AppAPIConfig `yaml:"app_api"`

	// Feeds 是可选的白名单。
	//
	// 为 nil（配置里没有 feeds: 段）时，URL 本身就是订阅声明，任何番号/女优
	// 都会被接受 —— 这与「每个订阅一个 feed URL」的形态一致。
	// 一旦出现 feeds: 段，就变成白名单：只有列出的订阅会被服务。
	//
	// 用指针而非空切片，是为了区分「没写」和「写了但为空」这两种意图。
	Feeds *FeedsConfig `yaml:"feeds"`
}

// FeedConfig 是 channel 级信息。
type FeedConfig struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Language    string `yaml:"language"`
	// BaseURL 是本服务的对外地址，用于 channel 的 <link>。
	BaseURL string `yaml:"base_url"`
}

// AppAPIConfig 是 App 私有 API 的接入参数。
type AppAPIConfig struct {
	// Host 形如 https://jdforrepam.com。
	Host string `yaml:"host"`
	// TokenFile 是手工从 App 导出的 token 存放路径。
	// 留空表示匿名访问 —— 需求 1/2/3 不需要 token。
	TokenFile string `yaml:"token_file"`
	// Lang 是 accept-language，影响服务端返回的文案语言。
	Lang string `yaml:"lang"`
	// DeviceUUID 覆盖默认的设备标识，让一台实例长期保持同一身份。
	DeviceUUID string `yaml:"device_uuid"`
	// MagnetConcurrency 是并行拉取磁链的并发上限（默认 8）。
	//
	// 调高会更快，但会给上游更大压力；调成 1 则完全串行。
	MagnetConcurrency int `yaml:"magnet_concurrency"`

	// ProbeInterval 是上游健康检查的间隔，如 "15m"。
	//
	// 0 或未设表示关闭探针。打开后：
	//   - /readyz 会随上游状态变成 503
	//   - /healthz/upstream 会返回机读详情
	//
	// 它检查的是「签名常量是否还跟服务端兼容」（本服务唯一已知会失效的输入），
	// 与 provider 是否已实现无关 —— 因此可以在 provider=stub 时就打开，
	// 先拿到早期告警能力。
	ProbeInterval time.Duration `yaml:"probe_interval"`
}

// FeedsConfig 是订阅白名单。
type FeedsConfig struct {
	Codes     []string     `yaml:"codes"`
	Actresses []ActressSub `yaml:"actresses"`
}

// ActressSub 是一条女优订阅。
type ActressSub struct {
	ID string `yaml:"id"`
	// Params 是**原样透传**给 App 演员页的查询参数。
	// 本服务不解释这些键值，只负责搬运（用户已选定这个做法）。
	Params map[string]string `yaml:"params"`
	// Since 是「只追新」的起始日期。
	//
	// TODO(ticket-09): 用它和哪个字段比较、开区间还是闭区间尚未定，
	// 因此这里只负责记录与透传，不做任何过滤。
	Since string `yaml:"since"`
}

// Default 返回带默认值的配置。
func Default() *Config {
	return &Config{
		Listen:   "127.0.0.1:8080",
		Provider: ProviderAppAPI,
		Feed: FeedConfig{
			Title:       "JavDB RSS",
			Description: "由 JavDB 官方 App 私有 API 生成的订阅源",
			Language:    "zh-CN",
			BaseURL:     "http://127.0.0.1:8080",
		},
		AppAPI: AppAPIConfig{
			Host: "https://jdforrepam.com",
			Lang: "en",
			// 默认打开探针。k8s 下这是 /readyz 的数据来源；
			// 单纯跑 go run 时它只是一次每 15 分钟的轻请求，代价可忽略。
			ProbeInterval: 15 * time.Minute,
		},
	}
}

// Provider 取值。
const (
	// ProviderAppAPI 使用真实的 JavDB App 私有 API。
	ProviderAppAPI = "appapi"
	// ProviderStub 使用固定数据，不访问网络，用于跑通链路或离线调试。
	ProviderStub = "stub"
)

// Load 读取并校验配置文件。path 为空时返回纯默认配置。
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取配置 %s: %w", path, err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true) // 拼错的键应当报错，而不是被静默忽略
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("解析配置 %s: %w", path, err)
		}
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("配置 %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen 不能为空")
	}
	switch c.Provider {
	case ProviderStub, ProviderAppAPI:
	default:
		return fmt.Errorf("未知的 provider: %q", c.Provider)
	}
	if c.Feeds != nil {
		for _, a := range c.Feeds.Actresses {
			if strings.TrimSpace(a.ID) == "" {
				return fmt.Errorf("feeds.actresses 里有条目缺少 id")
			}
		}
	}
	return nil
}

// AllowsCode 报告白名单是否放行某个番号。
func (c *Config) AllowsCode(code string) bool {
	if c.Feeds == nil {
		return true
	}
	for _, x := range c.Feeds.Codes {
		if strings.EqualFold(strings.TrimSpace(x), code) {
			return true
		}
	}
	return false
}

// ActressSub 查询某个女优的白名单条目。
//
// 白名单不存在时返回一个空的订阅（放行，参数由 URL 决定）；
// 白名单存在但没列出这个 id 时第二个返回值为 false。
func (c *Config) ActressSub(id string) (ActressSub, bool) {
	if c.Feeds == nil {
		return ActressSub{ID: id}, true
	}
	for _, a := range c.Feeds.Actresses {
		if strings.EqualFold(strings.TrimSpace(a.ID), id) {
			return a, true
		}
	}
	return ActressSub{}, false
}

// Holder 持有一份配置并支持热重载。
//
// 用 atomic.Pointer 而不是 RWMutex：读路径（每个请求）完全无锁，
// 而写路径（SIGHUP）罕见。重载失败时**保留旧配置**并返回错误 ——
// 一个手滑写坏的 YAML 不应该让正在服务的实例失去配置。
type Holder struct {
	path string
	cur  atomic.Pointer[Config]
}

// NewHolder 加载配置并构造 Holder。path 为空时使用纯默认配置且重载为空操作。
func NewHolder(path string) (*Holder, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	h := &Holder{path: path}
	h.cur.Store(cfg)
	return h, nil
}

// Current 返回当前生效的配置。永不为 nil。
func (h *Holder) Current() *Config { return h.cur.Load() }

// Path 返回配置文件路径。
func (h *Holder) Path() string { return h.path }

// Reload 重新读取配置文件。失败时当前配置保持不变。
func (h *Holder) Reload() error {
	if h.path == "" {
		return nil
	}
	cfg, err := Load(h.path)
	if err != nil {
		return err
	}
	h.cur.Store(cfg)
	return nil
}

// LoadToken 从文件读取手工导出的 token。
//
// 接受两种形态，因为导出方式尚未固定（ticket 05 待定）：
//
//	裸 JWT                  eyJhbGciOi...
//	JSON 对象               {"token": "eyJhbGciOi..."}
//
// 文件不存在不算错误 —— 返回空字符串表示匿名访问，这是需求 1/2/3 的正常形态。
func LoadToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取 token 文件 %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var obj struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			return "", fmt.Errorf("解析 token 文件 %s: %w", path, err)
		}
		return strings.TrimSpace(obj.Token), nil
	}
	return trimmed, nil
}
