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

// ErrNoToken 表示这次操作需要用户从 App 导出的 token，但当前没有配置它。
//
// 单独成一个可判定的错误，是因为它的处置方式与别的失败都不同：
// 不是重试、不是改代码，而是**去 App 里导出一次**。
// 上层据此返回 503 并在文案里说清楚，而不是静默给一个空列表 ——
// 空列表会被理解成「你没收藏任何人」。
var ErrNoToken = errors.New("此操作需要 token：请从 App 导出后配置 app_api.token_file")

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
	// params 是**原样透传**给 App 演员页的查询参数（用户已选定这个做法）。
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
	CollectedActresses(ctx context.Context) ([]Actress, error)
}
