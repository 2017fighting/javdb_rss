// Package config 负责读取与热重载本服务的配置。
//
// 配置是单个 YAML 文件。选择 YAML 而不是环境变量，是因为订阅列表
// （番号、女优、透传参数）本质上是结构化数据，摊进环境变量会变得难以阅读和转义。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/2017fighting/javdb_rss/internal/appapi"
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
	// Lang 是 accept-language。它决定了上游返回的**女优名字用哪种语言**：
	//
	//	en     -> "Kawakita Saika"
	//	zh-CN  -> "河北彩花"
	//
	// （实测 2026-09-30。注意不要指望 name_zht 字段 —— 它在新版服务端恒为空。）
	// 默认 zh-CN，因为本服务的用户与内容都是中文的。
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

	// PinFile 是 pin 状态文件（「每个作品已选中的磁链」）的路径。
	//
	// 这是本服务**唯一**的持久状态，也是「无状态」的一处有意例外：
	// 上游的磁链顺序不可重放，丢 pin 就是丢 guid（qBittorrent 会重下）。
	// 设计见 ticket 08。
	//
	// 留空时默认放在**配置文件旁边**的 pin.json（与 token_file 同一套规则）。
	//
	// ⚠️ 它**无法关闭**：留空是「用默认路径」，不是「禁用钉住」。
	// 默认关闭会让升级后静默退回有抖动的纯函数 —— 而那正是要消除的失败。
	// 文件不可写时本服务拒绝启动（fail fast）。
	PinFile string `yaml:"pin_file"`
}

// FeedsConfig 是订阅白名单。
type FeedsConfig struct {
	Codes     []string     `yaml:"codes"`
	Actresses []ActressSub `yaml:"actresses"`
	// Lists 是清单订阅的白名单（只列 id）。
	//
	// 它刻意比 Actresses **简单**（没有 params / since）：清单 feed 的
	// 透传参数直接写在 URL 上就够（`sort_by`/`order_by`/`pages` 都会被转发），
	// 而女优那套 params 是当初为了把「App 里的筛选」固化进配置才加的。
	// 两者不同是刻意的 —— 把没验证过的对称性加上去，只会多一处要同步维护的东西。
	//
	// zone **不在配置里**：清单的 filter_by 实测固定为 `0:l:{id}`（写错 zone
	// 不会报错，只会静默给别的作品），因此不把一个危险的旋钮交给配置。
	Lists []string `yaml:"lists"`
	// Zones 是全站标签订阅（/rss/tags/{zone}.xml）允许的片库号。
	//
	// 白名单的单位在这里是「片库」而不是「订阅」：全站订阅的组合空间是
	// 4 个 zone × 任意标签/年份组合，**枚举不出来**。可枚举且有意义的粒度
	// 是「你愿不愿意把哪个片库作为订阅放出去」。
	//
	// 不写这一段= 四个片库全放行；写了就只有列出的会被服务。
	Zones []string `yaml:"zones"`
}

// ActressSub 是一条女优订阅。
type ActressSub struct {
	ID string `yaml:"id"`
	// Params 是**原样透传**给 App 女优页的查询参数。
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
			// zh-CN 而不是 en：它决定女优名字的语言（见 Lang 字段的说明）。
			Lang: "zh-CN",
			// 显式写出来而不是依赖 appapi 内部的兜底：
			// 字段注释写了「默认 8」，那这里就该真的是 8，
			// 否则「配置里的默认值」与「实际生效的默认值」是两回事。
			MagnetConcurrency: appapi.DefaultMagnetConcurrency,
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

// AllowsList 报告白名单是否放行某份清单。
//
// 与 AllowsCode 同一套语义：白名单不存在时全部放行（URL 本身就是订阅声明），
// 存在但没列出时返回 false，由路由层变成 404。
func (c *Config) AllowsList(id string) bool {
	if c.Feeds == nil {
		return true
	}
	for _, x := range c.Feeds.Lists {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(id)) {
			return true
		}
	}
	return false
}

// AllowsZone 报告白名单是否放行某个片库（全站标签订阅用）。
//
// 与 AllowsCode / AllowsList 同一套语义：白名单不存在时全放行。
// zone 用字符串比较是为了让写错的值（如 "有码"）落在「不在白名单」而不是
// 被静默转成 0 —— 后者会让一个写错的配置放行**有码**这个片库。
func (c *Config) AllowsZone(zone string) bool {
	if c.Feeds == nil {
		return true
	}
	for _, x := range c.Feeds.Zones {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(zone)) {
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
//
// 路径会先解析成**绝对路径**。两个理由：
//
//  1. TokenPath() 由配置文件位置推导（留空时取同目录的 token.json）。
//     相对路径会让「登录写到哪儿」取决于你在哪个目录敲的命令 ——
//     一个很难查的不一致。
//  2. 出错时能直接打出完整路径，用户一眼看得出它去哪儿找了。
func NewHolder(path string) (*Holder, error) {
	if path != "" {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
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

// TokenPath 返回 token 文件的**实际**路径。
//
// app_api.token_file 留空时，默认放在**配置文件旁边**的 token.json ——
// 「配置在哪儿、它的凭据就在哪儿」最不容易搞错。
//
// 这个默认值是必须的，不是锦上添花：它曾经缺失过，后果是
// login 登录成功、用户手机被踢下线，然后 `os.Rename(".tmp", "")` 失败，
// **token 丢了**。而且运行中的服务也在读那个空路径，就算写成功也读不到。
//
// 因此 login 命令与服务端**必须都走这个方法**，否则两边会指向不同的文件。
func (h *Holder) TokenPath() string {
	return h.resolveAlongside(h.Current().AppAPI.TokenFile, "token.json")
}

// PinPath 返回 pin 状态文件的**实际**路径。
//
// app_api.pin_file 留空时，默认放在**配置文件旁边**的 pin.json ——
// 与 TokenPath 完全同一套规则（共用 resolveAlongside，因此两者不会走偏）。
//
// 与 token 的区别：pin 是**必需**的（不可关闭）。裸二进制因此要求配置目录可写，
// 而容器/k8s/systemd 会在部署清单里把它指到专门的可写卷。
func (h *Holder) PinPath() string {
	return h.resolveAlongside(h.Current().AppAPI.PinFile, "pin.json")
}

// resolveAlongside 实现「配置在哪儿，它的附属文件就在哪儿」这条规则。
//
// token.json 与 pin.json 都按它落地。抽出来是因为这两份持久状态**必须**
// 用同一套解析规则：它们各写一遍的话，任何一边改了默认值都会让「写进去的
// 和服务读到的不是同一个文件」—— 那个 bug 在 token 上真实发生过一次
// （见 TokenPath 的注释），而 pin 的后果是每个请求都静默换 guid。
func (h *Holder) resolveAlongside(explicit, fallback string) string {
	if p := strings.TrimSpace(explicit); p != "" {
		return p
	}
	if h.path == "" {
		return fallback
	}
	return filepath.Join(filepath.Dir(h.path), fallback)
}

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

// 环境变量名。
//
// 它们存在的理由是**正式部署**：容器里挂一个可写 token 文件不方便，
// 而 k8s Secret / docker env_file 是更自然的凭据通道。
// 本地开发仍然用 `javdb-rss login` 写文件。
const (
	// EnvToken 是 token 的环境变量名。它**优先于** token 文件。
	EnvToken = "JAVDB_TOKEN"
	// EnvUsername / EnvPassword 启用**自动续期**（见 LoadCredentials）。
	EnvUsername = "JAVDB_USERNAME"
	EnvPassword = "JAVDB_PASSWORD"
)

// LoadToken 解析 token，环境变量**优先于**文件。
//
// 文件接受两种形态，因为导出方式尚未固定：
//
//	裸 JWT                  eyJhbGciOi...
//	JSON 对象               {"token": "eyJhbGciOi..."}
//
// 文件不存在不算错误 —— 返回空字符串表示匿名访问，这是需求 1/2/3 的正常形态。
// 空白的值（空字符串或全空白）一律当作「没设置」—— 否则一个空的 env
// （比如 compose 里写了但没填）会把文件里本来可用的 token 顶掉。
func LoadToken(path string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(EnvToken)); v != "" {
		return v, nil
	}
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

// SaveToken 把 token 写入文件，权限 0600。
//
// 写的是 {"token": "..."} 形态 —— 与 LoadToken 兼容，且给将来加字段留了位置。
//
// 两处刻意的处理：
//
//  1. **先建后收紧权限**：如果文件已存在且是 0644（用户手工建过），
//     仅覆盖内容会把旧权限留着。因此写完显式 Chmod。
//  2. **临时文件 + rename**：避免写一半断电留下一个半截的 token 文件，
//     那会让下次启动读到一个坏 JSON 而报错。
func SaveToken(path, token string) error {
	if strings.TrimSpace(path) == "" {
		// 明确报错，而不是去写一个叫 ".tmp" 的文件再在 rename 时炸。
		// 调用方应当用 Holder.TokenPath() 拿到一个解析过的路径。
		return fmt.Errorf("token 文件路径为空（应当用 Holder.TokenPath() 解析默认值）")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("拒绝写入空 token —— 那会让下次启动静默变成匿名访问")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建 %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(struct {
		Token string `json:"token"`
	}{Token: token}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入 %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换 %s: %w", path, err)
	}
	// 目标文件若原本存在，rename 会继承临时文件的权限；但某些文件系统上
	// 不是这样，所以显式再收紧一次。幂等操作，代价可忽略。
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return nil
}

// LoadCredentials 返回用于**自动续期**的账号密码。
//
// # ⚠️ 开启它的代价：会挤掉用户手机上的 App 会话
//
// 用户实测报告：同一账号只能在一个地方登录，新登录会挤掉之前那个。
// 因此启用自动续期意味着：token 一失效，本服务就去重新登录，
// **而用户手机上的 App 会被踢下线**。
//
// 所以它是**由凭据是否存在来控制的开关**，而不是默认行为：
//
//	不设 JAVDB_PASSWORD  -> 永不自动登录，手机安全；token 失效时只能人工重登
//	设了 JAVDB_PASSWORD  -> 自动续期，但每次续期都会踢掉手机
//
// 两项必须齐全才算配置 —— 只设用户名会让我们拿空密码去打上游。
func LoadCredentials() (username, password string, ok bool) {
	u := strings.TrimSpace(os.Getenv(EnvUsername))
	p := os.Getenv(EnvPassword)
	if u == "" || strings.TrimSpace(p) == "" {
		return "", "", false
	}
	return u, p, true
}
