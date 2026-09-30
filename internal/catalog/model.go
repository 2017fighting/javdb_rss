// Package catalog 定义本服务的领域模型。
//
// 这里只放「作品 / 磁链」这一层的概念，不涉及任何 HTTP 或序列化细节 ——
// 上游的数据来源（App 私有 API）与下游的呈现（RSS）都不应该渗进来。
//
// 与 App API 线格式（wire format）的对应关系刻意留在 appapi 包里，
// 这样线格式改名时只需要改一处映射，领域模型不受影响。
package catalog

// Magnet 是一条可下载的磁链候选。
//
// 它对应 App API `/api/v1/movies/{id}/magnets` 返回的 magnets[] 元素。
type Magnet struct {
	// Infohash 是 BitTorrent infohash（对应线格式的 hash 字段）。
	//
	// 它是本服务里唯一被信任的作品身份标识：feed item 的 guid 直接用它。
	// 理由（ticket 08）：内容相同即 guid 相同 → 跨 feed 自动去重；
	// 洗版会产生新 infohash → 自动重新下载；且它完全由内容决定，
	// 因此进程重启不会改变 guid，与「无状态」的设计相容。
	Infohash string

	// Name 是磁链的显示名（线格式 name），通常就是番号。
	Name string

	// SizeMB 是体积，单位 兆字节（线格式 size）。
	SizeMB int

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
	// TODO(ticket-09): "since=<日期> 只追新" 到底该拿哪个字段比还没定
	// （发行日期 vs 上架时间），定了之后再决定要不要在这里解析。
	ReleaseDate string

	// Magnets 是磁链候选，**顺序即服务端返回的顺序**。
	//
	// 本服务不做任何重排 —— 排序规则属于上游（用户已选定「信任 App 顺序」）。
	Magnets []Magnet
}
