package catalog

import (
	"context"
	"errors"
	"fmt"
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
	// Gender 是上游给的性别标记：**0 = 女优，1 = 男优**（实测）。
	//
	// 它存在的理由很具体：收藏列表里**确实有男优** —— 实测该账号 144 位里
	// 有 6 位（森林原人、小沢とおる…）。而页面要的是「只看女优」，
	// 前端没有这个字段就只能把男女一列列出来。
	//
	// ⚠️ 取值语义是**实测**的（2026-10-04，逐页对过 gender 与名字），
	// 但上游没有文档。实测该字段在 16 页 144 项里**一个不缺**，因此这里用
	// 普通 int 而不是指针。若上游哪天开始不给它，缺失会默认成 0 ——
	// 后果是男优被当成女优多列出来（**看得出来**，不会静默少给东西）。
	Gender int
}

// Gender 的已知取值。用常量而不是裸数字：上游没文档，而这些值是从 144 项
// 实测反推的（见 Actress.Gender），把数字散在代码里会让「0 到底是男是女」
// 每处都得重新确认。
const (
	// GenderFemale 是女优（实测 138/144）。
	GenderFemale = 0
	// GenderMale 是男优（实测 6/144）。
	GenderMale = 1
)

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

// MovieList 是用户在 App 里建的一份**清单**（片单）。
//
// 它与 Actress 并列，都是「一块作品集合」的发现产物 —— 用户看到这份清单后
// 自己决定把哪些 id 填进订阅 URL。
//
// ⚠️ 名单叫 List 而不叫 Collection 是因为 Collection 已经被「收藏女优」占了，
// 而两者是**不同**的东西：Collection 是收藏的**女优**，MovieList 是自建的**片单**。
type MovieList struct {
	// ID 是上游的清单标识（如 "k4EVE4"），也是 /rss/list/{id}.xml 里的那个 id。
	ID string
	// Name 是清单名，由用户自己起（实测有「遥控跳弹」这种）。
	Name string
	// MoviesCount 是上游声明的清单长度。
	//
	// 它**可以**用来对账：实测 filter_by=0:l:{id} 返回的条数与它逐位相同
	// （4 份清单实测 9/1/2/6 全对），因此它不只是参考值。
	MoviesCount int
	// IsDefault 表示这是账号自带的那份默认清单（名字就叫 "default"）。
	IsDefault bool
	// Privacy 是上游给的可见性（实测取值："open" / "own"）。
	Privacy string
}

// ListCollection 是「你在 App 里建的清单」这份清单的读取结果。
//
// 它与 Collection 同构，理由也相同：清单按页拉取，而翻页有一个上限。
// Truncated 为 false 时它是一份**完整的清单**。
type ListCollection struct {
	Lists []MovieList
	// Truncated 报告上游**确实还有数据而本次没读完**（超过翻页上限）。
	Truncated bool
	// PagesFetched 是实际请求的页数，MaxPages 是当时生效的翻页上限。
	PagesFetched int
	MaxPages     int
}

// Tag 是标签词表里的一个标签。
//
// 它对应上游 `GET /api/v2/tags` 里某一组 `tags[]` 的一项，字段名与线格式一致。
type Tag struct {
	// ID 是上游的标签标识。
	//
	// ⚠️ 它**不是全局唯一**的：同一个片库的词表里，月份 1–12 与真标签的 id
	// 全部撞号（实测 12 个，如 id=3 同时是「月份:3」与「服裝:眼鏡」）。
	// 因此把 id 单拿出去用之前必须先确定它属于哪个分组（或与名字一起核对），
	// 否则会把「月份」的筛选条件当成某个真标签发出去。
	ID string
	// Name 是显示名，**原样来自上游**（不提字、不翻译）。
	Name string
	// VideosCount 是上游给的该标签作品数。
	//
	// ⚠️ 词表（`/api/v2/tags`）实测**不返回**这个字段 —— 它是给女优自己的
	// `tags[]` 预留的（那边逐项都有）。因此 0 既可能是「上游没给」也可能是真的 0；
	// 序列化时应当把它整段省掉，而不是编一个 0 出来。
	VideosCount int
}

// TagGroup 是标签词表里的一个分组（如「年份」「服裝」）。
//
// 分组**不是我们编的**：名字与顺序都来自上游，页面「按上游分组挑标签」
// 依赖的就是它。
type TagGroup struct {
	// CategoryID 是上游的分组标识（main / year / month / subject / role / …）。
	CategoryID string
	// Category 是分组显示名，原样来自上游。
	Category string
	// Tags 是组内标签，**顺序即上游顺序**。
	Tags []Tag
}

// TagVocabulary 是某个片库（zone）的标签分组词表。
//
// 它是发现端点的产物：让「这个片库有哪些标签」不再依赖一份冻结快照。
type TagVocabulary struct {
	// Groups 是分组，**顺序即上游顺序**。
	//
	// 分组顺序与组内顺序都不是我们编的：一旦在中间排序，
	// 同样的选择就会在不同版本得到不同的 URL。
	Groups []TagGroup
}

// 区域号（filter_by 的第一段）。
//
// 它同时是全站浏览的「片库」选择：实测 0/1/2/3 返回**四个不同的集合**
// （0 有码 NMSL/FAYS…、1 无码 HEYZO…、2 欧美 Wifey/Blackedraw…、3 FC2-xxx）。
const (
	ZoneCensored   = 0 // 有码
	ZoneUncensored = 1 // 无码
	ZoneWestern    = 2 // 欧美
	ZoneFC2        = 3 // FC2
)

// ZoneName 返回区域号的人读名字（feed 标题用）。未知区域号返回空串。
func ZoneName(zone int) string {
	switch zone {
	case ZoneCensored:
		return "有码"
	case ZoneUncensored:
		return "无码"
	case ZoneWestern:
		return "欧美"
	case ZoneFC2:
		return "FC2"
	}
	return ""
}

// ZoneOptions 返回四个片库号的取值说明（嵌进「有效取值」文案的一句话）。
//
// 抽成函数而不是各处写一遍：这句话会出现在好几条错误信息里，
// 而它们必须给出**同一份**取值集合 —— 各写一份就会漂。
func ZoneOptions() string { return "0=有码 1=无码 2=欧美 3=FC2" }

// ValidZone 报告 zone 是否是本服务认识的片库号（0–3）。
//
// 它必须与上游**实测**的有效集合一致，而不是与「看起来合理」一致：
// 上游对越界值是**静默回落**（`type=9` 与 `type=0` 的响应逐字节相同），
// 所以一个没被拦住的越界 zone 不会报错，只会把另一个片库的内容当成答案。
func ValidZone(zone int) bool { return ZoneName(zone) != "" }

// ErrUnknownZone 是「片库号不认识」这个用户错误的唯一构造点。
//
// 它被 appapi 与 stub 两处实现共用：两处各写一份 fmt.Errorf 就会有两份
// 可以各自跑偏的文案，而这条文案是用户盯着 URL 读的那一句。
// 返回的错误用 ErrBadRequest 包装，因此上层能把它翻成 400 而不是 502。
func ErrUnknownZone(zone int) error {
	return fmt.Errorf("%w：片库号 %d 不存在（实测有效的只有 %s）",
		ErrBadRequest, zone, ZoneOptions())
}

// MaxTags 是标签筛选的上限，**上游的硬限制**，不是 UI 约定。
//
// 实测（2026-10-04）：把 6 个 id 交给上游时，第 6 个会被**静默丢弃** ——
// 同一个 id 挪到前 5 位就立刻生效（同一批请求的结果从 1 条变成 0 条）。
// 所以上层必须自己拦住「超过 5 个」，否则用户会以为筛了 6 个。
const MaxTags = 5

// BrowseSelector 是「全站浏览」的几个筛选维度。
//
// 它们最终拼成上游的 filter_by 掩码：
//
//	{zone}:t:{Main}:{Tags}:{Year}:{Duration}:{Month}
//
// 各字段的空串表示不限。
//
// ⚠️ 它与实体掩码（女优/清单）在 letter 之后**整体错开一位**：实体掩码是
// {zone}:{letter}:{id}:{main}:…，而全站形式的 letter `t` 没有实体 id，
// 因此 main 落在实体掩码里 id 的位置。这不是猜测：是从 App 的真实抓包反推、
// 再逐槽位实测确认的（见 notes/tag-vocabulary.md 第 7 节）。
type BrowseSelector struct {
	// Main 是主属性字母的逗号列表（如 "c,m"）。空串为不限。
	Main string
	// Tags 是标签 id 的逗号列表，**最多 MaxTags 个**。空串为不限。
	Tags string
	// Year 是四位年份（如 "2020"）。空串为不限。
	Year string
	// Duration 是时长档位 id（lt-45 / 45-90 / 90-120 / gt-120）。空串为不限。
	//
	// ⚠️ 它**必须与 Year 一起给**：实测单独给时长会被上游静默忽略。
	Duration string
	// Month 是月份（1–12）。空串为不限。单独给也生效（实测）。
	Month string
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
	// 以下五个由 httpapi 消费，拼进**全站掩码**（见 BrowseSelector）。
	//
	// 它们列在这里的另一个作用是：女优/清单路由上如果有人传了它们，
	// 会被剔除而**不会**透传给上游 —— 而那只会上游忽略。
	// “被剔掉”与“被忽略”都会得到同一条未被筛的 feed，所以路由层还会额外
	// 把这种用法当作**用户写错**返回 400（见 handleActress 的注释）。
	"tags":     true,
	"year":     true,
	"month":    true,
	"duration": true,
	"main":     true,
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

	// CollectedLists 返回用户在 App 里建的清单（片单）。
	//
	// **需要 token。** 没有配置 token 时，实现方必须返回包装了 ErrNoToken 的错误
	// （用 errors.Is 可判定），而不是返回空列表 —— 空清单会被理解成
	// 「你没建过任何清单」。
	//
	// ⚠️ 它与 CollectedActresses 走的是**不同**的上游端点，而且不是
	// 看起来最像的那个：`/api/v1/users/collected_lists` 实测返回
	// **HTTP 500**（GET/POST、带不带参数都一样），能用的只有
	// `/api/v1/lists/simple`（需要 token）。详见 notes/tag-vocabulary.md 的姊妹篇
	// notes/lists.md。
	CollectedLists(ctx context.Context) (ListCollection, error)

	// List 返回某份清单里的作品列表。
	//
	// 它是 List 订阅的数据源：与 Actress 同形（一套透传参数、一套自有参数
	// pages），只是 filter_by 里的实体字母从 `a` 换成 `l`。
	//
	// ⚠️ zone **写死 0**：清单的 filter_by 实测是 `0:l:{id}`，而 zone 写错
	// （例如 2）不会报错，只会静默返回【别的作品】—— 实测 4 份清单在 zone=0
	// 下返回 9/1/2/6 条（与上游声明的 movies_count 逐位相同），换成 zone=2
	// 全部变成 50 条。上游的清单形态里**没有** zone 字段，因此没得选。
	List(ctx context.Context, id string, params url.Values) ([]Work, error)

	// ListName 返回清单名，用于 feed 标题（拿不到就用 id 当标题）。
	//
	// 与 ActressName 同一套非关键路径语义：失败退回 id，绝不让取名
	// 把一个本来能用的 feed 弄挂。
	//
	// 匿名只能读 `privacy: open` 的清单（实测），`privacy: own` 的会返回
	// NoPermission —— 那种情况下退回 id 就行。
	ListName(ctx context.Context, id string) (string, error)

	// TagVocabulary 返回某个片库的标签分组词表（分组名、分组顺序、组内标签
	// 与顺序一律原样）。
	//
	// **不需要 token**：实测该上游端点匿名可用。它也不引入任何服务端状态。
	//
	// zone 只接受 0–3，实现方必须把越界的 zone 判成错误（用 ErrBadRequest
	// 包装），而**不能**原样发给上游：上游对非法 `type` 是静默回落 ——
	// `type=9` 与 `type=0` 的响应逐字节相同。照原样透传等于把「另一个片库的
	// 词表」当成你要的答案交出去。
	TagVocabulary(ctx context.Context, zone int) (TagVocabulary, error)

	// Browse 返回**全站**（不挂任何实体）的作品列表，按 zone 分片库。
	//
	// 它对应 App 「浏览」页那条：掩码是
	// `{zone}:t:{main}:{tags}:{year}:{duration}:{month}` —— letter `t` 没有
	// 实体 id，因此标签、年份、月份、时长全都能直接用（详见 BrowseSelector）。
	//
	// 这是本服务里**唯一**能把「标签 + 时间」组合起来的通道：实测
	// `filter_by_tags` 作为独立参数只对女优实体生效，在清单/搜索/latest/top 上
	// 全被静默忽略（见 notes/tag-vocabulary.md 第 6 节）。
	//
	// 实现方必须自己拦住「标签超过 MaxTags 个」与「有时长无年份」——
	// 这两件事上游都不报错，只会静默少筛。
	Browse(ctx context.Context, zone int, sel BrowseSelector, params url.Values) ([]Work, error)
}
