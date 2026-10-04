package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// maxCollectedPages 是收藏列表的翻页上限。
//
// 上游是「返回空即到底」，正常情况下十几页就结束了。设上限是防止一个异常的上游
// （永远返回满页）让我们无限翻下去 —— 那会变成一个自己打自己的循环。
//
// 取值依据（2026-09-30 实测，真实 token）：`/users/collected_actors`
// **每页固定 10 条**，与实际参数无关。实测账号有 144 位收藏 = 15 页
// （14 满页 + 1 页 4 条），第 16 页为空。20 页 = 200 位，是当前 144 位的约 **1.4 倍**
// （多出 56 位、5 页的余量）。
//
// ⚠️ 这个余量**并不宽裕**：收藏再涨 56 位就会触顶。触顶时不再静默，
// 而是报告 Collection.Truncated 并记 WARN 日志（见下），那时应当上调本常量。
const maxCollectedPages = 20

// collectedActress 是收藏列表里的一位女优。
type collectedActress struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VideosCount int    `json:"videos_count"`
}

// collectedEnvelope 是 `/api/v1/users/collected_actors` 的负载形态。
//
// 契约来源：先例项目 javdb-cli 的实现（`{path, key}` 表 + AllPages 翻页）
// 与它的测试夹具。本仓库 2026-09-30 实测确认该端点存在且需要 token。
//
// 注意上游**只保证 actors 这个 key**，不保证分页元信息 —— 因此「到底了」
// 只能靠「这一页是空的」来判断，不能靠总数。
type collectedEnvelope struct {
	Actors []collectedActress `json:"actors"`
}

// CollectedActresses 实现 catalog.Source：读取用户在 App 里收藏的女优。
//
// 两个刻意的行为：
//
//  1. **没有 token 时直接返回 ErrNoToken，不发请求。** 发了也必然被拒
//     （实测返回 JWTVerificationError），而且返回空列表会被用户理解成
//     「我没收藏任何人」—— 那是最难排查的一种错。
//  2. **翻页到底、按 id 去重、触顶时报 Truncated。** 这三件事与「想看」清单
//     完全同形（同一套上游翻页行为、同一套触顶语义），因此由 readAllPages
//     一处实现 —— 包括那次「多探一页」的探针，以及它为什么必须存在。
func (c *Client) CollectedActresses(ctx context.Context) (catalog.Collection, error) {
	if strings.TrimSpace(c.Token) == "" {
		return catalog.Collection{}, fmt.Errorf("取收藏女优: %w", catalog.ErrNoToken)
	}

	actors, pg, err := readAllPages(maxCollectedPages,
		func(a collectedActress) string { return strings.TrimSpace(a.ID) },
		func(page int) ([]collectedActress, error) { return c.collectedPage(ctx, page) })
	if err != nil {
		return catalog.Collection{}, err
	}

	out := make([]catalog.Actress, 0, len(actors))
	for _, a := range actors {
		out = append(out, catalog.Actress{
			ID:          strings.TrimSpace(a.ID),
			Name:        strings.TrimSpace(a.Name),
			VideosCount: a.VideosCount,
		})
	}
	return catalog.Collection{
		Actresses:    out,
		Truncated:    pg.truncated,
		PagesFetched: pg.pagesFetched,
		MaxPages:     pg.maxPages,
	}, nil
}

// collectedPage 拉收藏列表的第 page 页（从 1 开始）。
func (c *Client) collectedPage(ctx context.Context, page int) ([]collectedActress, error) {
	var env collectedEnvelope
	if err := c.GetJSON(ctx, "/api/v1/users/collected_actors",
		url.Values{"page": {strconv.Itoa(page)}}, &env); err != nil {
		return nil, fmt.Errorf("取收藏女优第 %d 页: %w", page, err)
	}
	return env.Actors, nil
}

// actressEnvelope 是 `/api/v1/actors/{id}` 里我们需要的部分。
type actressEnvelope struct {
	Actor struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"actor"`
}

// ActressName 实现 catalog.Source：取女优显示名，用于 feed 标题。
//
// # 为什么读 name 而不是 name_zht
//
// 实测（2026-09-30）：上游的 `name_zht` 字段在新版服务端上**恒为空**，
// 与 lang 无关；随 lang 变化的是 `name` 本身：
//
//	accept-language: en     -> name = "Kawakita Saika"
//	accept-language: zh-CN  -> name = "河北彩花"
//
// 也就是说想要中文名，正确做法是配 `app_api.lang: zh-CN`，
// 而不是去读 name_zht。（先例项目的测试夹具给 name_zht 填了值，
// 照抄它会写出一个永远落到 fallback 的实现。）
//
// # 为什么不需要 token
//
// 这个端点匿名可用，因此即使没配 token，feed 标题也能比光秃秃的 id 好看。
// 这也让本服务的「需求 1/2/3 不需要 token」这条性质不被破坏。
func (c *Client) ActressName(ctx context.Context, id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("女优 id 为空")
	}
	var env actressEnvelope
	if err := c.GetJSON(ctx, "/api/v1/actors/"+url.PathEscape(id), nil, &env); err != nil {
		return "", err
	}
	name := strings.TrimSpace(env.Actor.Name)
	if name == "" {
		return "", fmt.Errorf("女优 %s 没有可用的名字", id)
	}
	return name, nil
}
