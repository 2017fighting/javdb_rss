package catalog

import (
	"context"
	"errors"
	"net/url"
)

// Actress 是用户在 App 里收藏的一位女优。
//
// 它是**发现的产物**，不是订阅 —— 用户看到这份列表后自己决定把哪些 id
// 填进配置或 URL。本服务不会因为它在列表里就自动为它建 feed。
type Actress struct {
	// ID 是上游的女优标识（如 "EvkJ"），也是 /rss/actress/{id}.xml 里的那个 id。
	ID string
	// Name 是显示名。
	//
	// 语言由请求的 accept-language 决定（即配置里的 app_api.lang）：
	// 实测 lang=en 得到 "Kawakita Saika"，lang=zh-CN 得到 "河北彩花"。
	//
	// 注意**不要**去读上游的 name_zht 字段 —— 实测它在新版服务端上恒为空，
	// 与 lang 无关。（先例项目的测试夹具给它填了值，照抄会写错。）
	Name string
	// VideosCount 是该女优的作品数。
	VideosCount int
}

// Collection 是「用户在 App 里收藏的女优」这份清单的读取结果。
//
// 它刻意不是裸的 []Actress：收藏列表按页拉取，而翻页有一个上限。
// 一旦收藏数超过上限，裸切片无法区分「我就收藏了这么多」与「服务只读到了
// 这么多」—— 那正是本服务反复要避免的静默少给数据。Truncated 就是为这条
// 区分而存在的信号。
//
// Truncated 为 false 时它是一份**完整的清单**。
type Collection struct {
	// Actresses 是读到的收藏女优。
	Actresses []Actress
	// Truncated 报告上游**确实还有数据而本次没读完**（超过翻页上限）。
	// 为 true 时 Actresses 是一份**已知不完整的**清单。
	//
	// 注意它不是「达到上限」而是「上限之外还有数据」：一个恰好收藏了上限
	// 位数量的用户不应看到截断信号，因此实现方必须把两者分清。
	Truncated bool
	// PagesFetched 是实际请求的页数，MaxPages 是当时生效的翻页上限。
	//
	// 它们用于解释信号（读了多少、卡在哪）。PagesFetched 可能大于 MaxPages
	// —— 为分清「恰好读完」与「还有更多」，实现方会在到达上限后多探一页。
	PagesFetched int
	MaxPages     int
}

// WantList 是「用户在 App 里标记为想看（want_watch）的作品」这份清单的读取结果。
//
// 它与 Collection 同构，理由也相同：清单按页拉取，而翻页有一个上限。
// 一旦想看数超过上限，裸切片无法区分「我就标了这么多」与「服务只读到了
// 这么多」—— 那正是本服务反复要避免的静默少给数据。Truncated 就是为这条
// 区分而存在的信号。
//
// 与 Collection 的一处**实质区别**：这份清单里的作品可能**还没有磁链候选**。
// 「想看某部片，但它还没有种」是这份清单的常态，不是错误 —— 因此这里保留
// 这些作品（Magnets 为空），由上层决定「不渲染进 feed，但要说出来」。
// 在这一层就把它们丢掉的话，上层连「有几部在等磁力」都算不出来。
//
// Truncated 为 false 时它是一份**完整的清单**。
type WantList struct {
	// Works 是读到的作品，**顺序即上游给出的顺序**。
	Works []Work
	// Truncated 报告上游**确实还有数据而本次没读完**（超过翻页上限）。
	// 为 true 时 Works 是一份**已知不完整的**清单。
	//
	// 语义与 Collection.Truncated 完全一致（不是「达到上限」而是「上限之外
	// 还有数据」），因此实现方同样必须多探一页来分清两者。
	Truncated bool
	// PagesFetched 是实际请求的页数，MaxPages 是当时生效的翻页上限。
	PagesFetched int
	MaxPages     int
}

// ErrNoToken 表示这次操作需要用户从 App 导出的 token，但当前没有配置它。
//
// 单独成一个可判定的错误，是因为它的处置方式与别的失败都不同：
// 不是重试、不是改代码，而是**去 App 里导出一次**。
// 上层据此返回 503 并在文案里说清楚，而不是静默给一个空列表 ——
// 空列表会被理解成「你没收藏任何人」/「你没标过任何想看」。
var ErrNoToken = errors.New("此操作需要 token：请从 App 导出后配置 app_api.token_file")

// ErrBadRequest 表示**用户提供的订阅参数不合法**。
//
// 它让上层能把「你写错了 URL」与「上游出错了」分开返回 4xx 与 5xx ——
// 前者重试无用，后者重试有用。
var ErrBadRequest = errors.New("订阅参数不合法")

// OwnParams 是本服务自有、**不透传**给上游的查询参数名。
//
// 定义在这里而不是散在各层，是因为「哪些参数是我们自己的」是一个领域事实；
// 分散写会让新增一个自有参数时漏改某一层，而那种漏改的表现是
// 悄悄把它当成透传参数发给了上游。
//
// 注意这四个的**流转并不相同**：
//
//	since —— httpapi 消费（按发行日期过滤）
//	pages —— appapi 消费（要翻几页），因此必须流到那一层
//	page  —— 无人消费。分页完全由 pages 控制，所以它被丢弃
//	limit —— 无人消费。固定为上游上限 50，所以它被丢弃
//
// 把它们统一剔除还有一个实际好处：这两条被丢弃的参数不再影响
// dedupe 装饰器的合并 key —— 否则 ?page=9 与不带它会被当成两个不同的请求。
var ownParams = map[string]bool{
	"since": true,
	"pages": true,
	"page":  true,
	"limit": true,
}

// IsOwnParam 报告某个 query 参数是否由本服务自有（**不透传**给上游）。
//
// 用访问器而不是把 map 导出：导出的可变 map 任何包都能改，
// 而它是一份跨层共享的事实 —— 被意外改写会让某个自有参数静默变成透传，
// 而那意味着用户设的 `pages` 之类会原样发给上游。
func IsOwnParam(name string) bool { return ownParams[name] }

// Source 产出 feed 与发现端点所需的数据。
//
// 它是本服务的**唯一边界端口**：HTTP 层只认这个接口，不知道背后是真实的
// App 私有 API、一个装饰器、还是一个测试用的假实现。
type Source interface {
	// Code 返回某个番号对应的作品。
	//
	// 之所以返回切片而不是单个 Work：番号在 App API 里**不是唯一键**
	// （合集、不同片商同名等情况），一个番号可能对应多部作品。
	// 如何从中消歧属于 ticket 06；在规则定下来之前，实现方应当原样返回候选，
	// 由上层如实呈现。
	Code(ctx context.Context, code string) ([]Work, error)

	// Actress 返回某个女优的作品列表。
	//
	// params 是**原样透传**给 App 女优页的查询参数（用户已选定这个做法）。
	// 本服务不解释、不改写、不校验这些参数 —— 它们是上游的私有契约，
	// 我们只负责搬运。`since`/`page`/`limit`/`pages` 一类的本服务自有参数
	// 应由调用方在进到这里之前摘除。
	Actress(ctx context.Context, id string, params url.Values) ([]Work, error)

	// ActressName 返回女优的显示名，用于 feed 标题（拿不到就用 id 当标题）。
	//
	// 它**不需要 token**（走匿名端点），因此即使没有登录态也能让标题好看些。
	// 实现方在拿不到时应当返回错误而不是空字符串 —— 调用方据此退回 id。
	ActressName(ctx context.Context, id string) (string, error)

	// CollectedActresses 返回用户在 App 里收藏的女优。
	//
	// **需要 token。** 没有配置 token 时，实现方必须返回包装了 ErrNoToken 的错误
	// （用 errors.Is 可判定），而不是返回空列表。
	//
	// 返回的是 Collection 而不是裸切片：翻页有上限，确属超过上限时
	// 实现方必须把 Truncated 置为 true，好让上层能给出「这份清单不完整」
	// 的信号，而不是静默少给数据。
	CollectedActresses(ctx context.Context) (Collection, error)

	// WantToWatch 返回用户在 App 里标记为「想看」（want_watch）的作品。
	//
	// **需要 token**，理由与处置方式与 CollectedActresses 完全相同：
	// 没有 token 时必须返回包装了 ErrNoToken 的错误，而不是空清单 ——
	// 空的想看清单会被理解成「我没标过任何片」。
	//
	// 与 CollectedActresses 的差别只有一处：它是 feed 的数据源（不是发现端点），
	// 因此实现方返回的作品**可能没有磁链候选**，而那不是错误（见 WantList）。
	WantToWatch(ctx context.Context) (WantList, error)
}
