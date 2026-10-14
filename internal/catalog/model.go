// Package catalog 定义本服务的领域模型。
//
// 这里只放「作品 / 磁链」这一层的概念，不涉及任何 HTTP 或序列化细节 ——
// 上游的数据来源（App 私有 API）与下游的呈现（RSS）都不应该渗进来。
//
// 与 App API 线格式（wire format）的对应关系刻意留在 appapi 包里，
// 这样线格式改名时只需要改一处映射，领域模型不受影响。
package catalog

import "fmt"

// MagnetSource 是磁链候选的**数据来源**。
//
// 它存在的理由很具体：**交付物由来源决定**。上游的候选以磁力交付，
// 而 M-Team 的候选不能 —— 私种的裸磁力是死链（announce 里那份按次签发的凭据
// 只存在于种子文件里），所以后者交给客户端的是本服务端出的种子文件。
//
// ⚠️ 名字不能叫 Source：那个名字在本包里已经被 `catalog.Source`（端口接口）占了。
// 两个东西层次不同（一个是「候选从哪来」，一个是「数据从哪取」），但混用会很难读。
type MagnetSource uint8

const (
	// SourceUpstream 是 JavDB 官方 App 的私有 API —— 也是**零值**。
	//
	// 零值必须是它：Magnet 的既有构造点（appapi、stub、各层测试）
	// 一行都不用改，而「行为零变化」是引入第二个来源时的硬前提。
	SourceUpstream MagnetSource = iota
	// SourceMTeam 是 M-Team。
	SourceMTeam
)

// 名字用于序列化与日志。
const (
	sourceUpstreamName = "upstream"
	sourceMTeamName    = "mteam"
)

// String 让 %v / %s 打出名字而不是数字。
func (s MagnetSource) String() string {
	if s == SourceMTeam {
		return sourceMTeamName
	}
	return sourceUpstreamName
}

// MarshalText 让它在 JSON 里是**可读的名字**而不是数字。
//
// ⚠️ 这不是为了好看：`pin.json` 是一份**给人手工改的**文件（README 把「删掉某一条」
// 当成正式的运维手段），而一个数字位会让那件事变成猜谜 —— 猜错的后果是
// 一条 pin 的来源被读成上游，于是它的 enclosure 从种子文件退回**磁力**，
// 而私种的裸磁力是死链。
func (s MagnetSource) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText 读回名字。
//
// ⚠️ **认不出的名字报错**，不静默当上游：手滑写错一个字母就该当场看见，
// 而不是让那部作品的来源悄悄变成上游（那会让它的种子地址变成死链）。
func (s *MagnetSource) UnmarshalText(b []byte) error {
	switch string(b) {
	case "", sourceUpstreamName:
		*s = SourceUpstream
	case sourceMTeamName:
		*s = SourceMTeam
	default:
		return fmt.Errorf("认不出的来源 %q（只认 %q / %q）",
			string(b), sourceUpstreamName, sourceMTeamName)
	}
	return nil
}

// Magnet 是一条可下载的磁链候选。
//
// 它对应 App API `/api/v1/movies/{id}/magnets` 返回的 magnets[] 元素 ——
// 但自从有了第二个数据来源，它不再只对应那一个端点：
// M-Team 侧的候选由搜索接口投影而来（字段不同，语义要对齐）。
type Magnet struct {
	// Source 是这条候选来自哪个数据来源。零值 = 上游。
	//
	// 它**不参与身份**：feed 条目的 guid 永远是纯 infohash，与来源无关。
	// 它只决定交付物（磁力还是种子文件）。
	Source MagnetSource

	// Infohash 是 BitTorrent infohash（对应线格式的 hash 字段）。
	//
	// 它是本服务里唯一被信任的作品身份标识：feed item 的 guid 直接用它。
	// 理由（ticket 08）：内容相同即 guid 相同 → 跨 feed 自动去重；
	// 洗版会产生新 infohash → 自动重新下载；且它完全由内容决定，
	// 因此进程重启不会改变 guid，与「无状态」的设计相容。
	Infohash string

	// Name 是磁链的显示名（线格式 name），通常就是番号。
	Name string

	// SizeMiB 是体积，单位 **MiB（2^20 字节）**（线格式 size）。
	//
	// 这个单位是 ticket 03 复勘**确认**的，不是推定：
	// 同一条磁链在 javdb.com 的作品页上带 data-size 属性，其值与线格式的 size
	// **逐位相同**，而网页显示的文字是 size/1024 保留两位小数 ——
	//
	//	8570 -> 8.37GB   8050 -> 7.86GB   5530 -> 5.40GB
	//	8189 -> 8.00GB   6993 -> 6.83GB   8346 -> 8.15GB
	//
	// 除以 1000 对不上（8570 -> 8.57 ≠ 8.37），因此它不是十进制的 MB。
	// 证据：.scratch/javdb-rss/notes/actress-params.md 的「size 的单位」一节。
	SizeMiB int

	// CNSub 表示这条磁链带中文字幕（线格式 cnsub）。
	//
	// 这是全部「中文字幕判定」的依据 —— 服务端直接给，不需要解析文件名。
	CNSub bool

	// HD 表示这条磁链是高清（线格式 hd）。
	HD bool

	// FilesCount 是该磁链候选包含的文件数（线格式 files_count）。
	FilesCount int

	// CreatedAt 是线上的创建时间，**原样保留**（线格式形如 "09/27/2026"）。
	//
	// 刻意不在这里解析成 time.Time：格式尚未核实（ticket 06），
	// 且解析失败不该让整条 feed 挂掉。解析留给需要它的调用方。
	CreatedAt string
}

// Work 是一部作品及其磁链候选。
//
// Magnets 可能为空 —— 作品存在但尚无磁链候选是常见状态，调用方需要处理。
type Work struct {
	// ID 是上游的作品标识（线格式 `id`，如 "82J0Md"）。
	//
	// 它**不是**番号：番号在 App API 里可以重复（合集、不同片商同名等），
	// 而 id 唯一。因此持久状态（pin，ticket 08）按它键 —— 用番号做键会让
	// 两部不同作品互相钉死。
	//
	// 它对 feed 渲染没有用处（item 的身份是 infohash），只用于跨请求/跨 feed
	// 认出「这是同一部作品」。
	ID string

	// Number 是番号，如 "KV-328"。它在 App API 里是可以重复的
	// （合集、不同片商同名等），因此它不是唯一键。
	Number string

	// Title 是作品标题。
	Title string

	// ReleaseDate 是发行日期（线格式形如 "2026-08-28"）。
	//
	// 原样保留，理由同 Magnet.CreatedAt。
	//
	// `since` 就是拿它与下界比（闭区间，严格 `YYYY-MM-DD`）：比发行日期而不是
	// 磁链的 `created_at`，因为「只追新」要的是**新作品**，而磁链时间普遍早于
	// 发售日（实测，见 notes/api-recon.md §10.4.2）。形状不对的值由调用方
	// 一律保留，因此这里不需要替它解析。
	ReleaseDate string

	// Magnets 是磁链候选，**顺序即服务端返回的顺序**。
	//
	// 本服务不做任何重排 —— 排序规则属于上游（用户已选定「信任 App 顺序」）。
	Magnets []Magnet
}
