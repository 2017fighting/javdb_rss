package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// maxListPages 是清单列表的翻页上限。
//
// 与收藏女优/想看同一套翻页行为（「返回空即到底」），因此同样需要一个上限
// 来防一个异常的上游让我们无限翻下去。
//
// 取 20 与收藏女优一致：实测（2026-10-04）该账号共 **5 份**清单，
// 第 2 页就是空的。20 页是很宽的余量；触顶时仍然不静默
// （报 ListCollection.Truncated + WARN），那时再上调本常量。
const maxListPages = 20

// listEnvelope 是 `/api/v1/lists/simple` 的负载形态。
//
// 契约来源：**实测**（2026-10-04，真 token）。见 notes/lists.md。
//
//	{"lists":[{"id":"k4EVE4","name":"遥控跳弹","privacy":"open",
//	           "is_default":false,"movies_count":1,"has_movie":false}]}
//
// 「到底了」同样只能靠「这一页是空的」判断（上游不给总数）。
//
// 刻意**不解析** `has_movie`：它是「当前用户有没有把这部片收进清单」，
// 与本服务的用途（把清单渲染成 feed）无关。少解析一个字段就少一处理解错误。
type listEnvelope struct {
	Lists []listSimple `json:"lists"`
}

type listSimple struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Privacy     string `json:"privacy"`
	IsDefault   bool   `json:"is_default"`
	MoviesCount int    `json:"movies_count"`
}

// listDetailEnvelope 是 `/api/v1/lists/{id}` 里我们需要的部分（只取名字）。
type listDetailEnvelope struct {
	List struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"list"`
}

// CollectedLists 实现 catalog.Source：读取用户在 App 里建的清单。
//
// # ⚠️ 为什么不是 /api/v1/users/collected_lists
//
// 端点清单里那条最像的 —— `/api/v1/users/collected_lists` —— **实测返回
// HTTP 500**（GET/POST、带不带参数、带不带 token 全试过，见 notes/lists.md）。
// 也就是说「我**关注**的清单」这条路在上游是坏的，而
// `/api/v1/lists/simple` 是能用的那个：它返回**你自己建的**清单
// （实测有一份 `is_default: true`、`privacy: "own"` 的 default 清单，
// 那只有账号主人才看得到）。
//
// 两者不是一回事 —— 「我建的」与「我关注的」—— 因此这一点写在接口注释里，
// 而不只是写在笔记里：将来上游把 collected_lists 修好了，这是一处要回来看的地方。
//
// **需要 token**：匿名调用实测返回 JWTVerificationError。
func (c *Client) CollectedLists(ctx context.Context) (catalog.ListCollection, error) {
	if strings.TrimSpace(c.Token) == "" {
		return catalog.ListCollection{}, fmt.Errorf("取清单列表: %w", catalog.ErrNoToken)
	}

	rows, pg, err := readAllPages(maxListPages,
		func(l listSimple) string { return strings.TrimSpace(l.ID) },
		func(page int) ([]listSimple, error) { return c.listPage(ctx, page) })
	if err != nil {
		return catalog.ListCollection{}, err
	}

	out := make([]catalog.MovieList, 0, len(rows))
	for _, l := range rows {
		out = append(out, catalog.MovieList{
			ID:          strings.TrimSpace(l.ID),
			Name:        strings.TrimSpace(l.Name),
			MoviesCount: l.MoviesCount,
			IsDefault:   l.IsDefault,
			Privacy:     strings.TrimSpace(l.Privacy),
		})
	}
	return catalog.ListCollection{
		Lists:        out,
		Truncated:    pg.truncated,
		PagesFetched: pg.pagesFetched,
		MaxPages:     pg.maxPages,
	}, nil
}

// listPage 拉清单列表的第 page 页（从 1 开始）。
func (c *Client) listPage(ctx context.Context, page int) ([]listSimple, error) {
	var env listEnvelope
	if err := c.GetJSON(ctx, "/api/v1/lists/simple",
		url.Values{"page": {fmt.Sprint(page)}}, &env); err != nil {
		return nil, fmt.Errorf("取清单列表第 %d 页: %w", page, err)
	}
	return env.Lists, nil
}

// ListName 实现 catalog.Source：取清单名，用于 feed 标题。
//
// # 为什么不需要 token 也能用（有时）
//
// 实测：`privacy: "open"` 的清单匿名可读；`privacy: "own"` 的会返回
// NoPermission。也就是说没配 token 时，公开清单仍有好看标题，
// 私有清单退回 id —— 而**订阅本身不受影响**（清单作品走的是
// `/api/v1/movies/tags`，那条路不需要 token）。
//
// 与 ActressName 一样，这是条**非关键路径**：任何失败都退回 id，
// 绝不让取名失败把一个本来能用的 feed 弄挂。
func (c *Client) ListName(ctx context.Context, id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("清单 id 为空")
	}
	var env listDetailEnvelope
	if err := c.GetJSON(ctx, "/api/v1/lists/"+url.PathEscape(id), nil, &env); err != nil {
		return "", err
	}
	name := strings.TrimSpace(env.List.Name)
	if name == "" {
		return "", fmt.Errorf("清单 %s 没有可用的名字", id)
	}
	return name, nil
}
