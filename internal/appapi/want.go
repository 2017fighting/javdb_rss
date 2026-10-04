package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// maxWantPages 是「想看」清单的翻页上限。
//
// 上游是「返回空即到底」（与收藏列表同一套翻页行为）。设上限是防止一个异常的
// 上游（永远返回满页）让我们无限翻下去 —— 那会变成一个自己打自己的循环。
//
// ⚠️ 取 40 是被**真实数据**逼出来的，不是一开始就估的：
// 最初取 20（与收藏女优同一个数），首次拿真账号跑就触顶了 —— 实测
// **2026-10-04：想看共 234 部**（23 页满页 + 第 24 页 4 条，第 25 页空），
// 也就是说 20 页的上限会静默地截掉 34 部（14.5%）。当时日志里的 WARN 与 feed
// 描述里的「触顶」正确地把这件事喊了出来 —— 这正是不静默的意义。
// 40 页（上限 400 部）是当前 234 部的约 1.7 倍。
//
// 触顶时仍然不静默（报 WantList.Truncated + WARN + feed 描述），那时再上调本常量。
// 它与收藏女优的上限取了同一个量级，但这**不是**共享常量：两者的增长速度
// 与触顶后果都不同，将来各自调整时不应当互相牵动。
const maxWantPages = 40

// reviewMoviesEnvelope 是 `/api/v2/users/review_movies` 的负载形态。
//
// 契约来源：先例项目 javdb-cli 的 `internal/javdb/appapi/endpoint/user/user.go`
// （ReviewMoviesPage）：`GET /api/v2/users/review_movies`，参数 `status` + `page`，
// 列表在 `data.movies`；status 取 `watched`|`want_watch`。
//
// ⚠️ 这是**尚未对着真实响应实测过**的一条契约（本仓库只实测过
// `/users/collected_actors`）。它与已实测的端点共用同一个信封与 `data.<key>` 形状，
// 且先例项目用它实现了 `want` 命令。
//
// ✅ **2026-10-04 已用真 token 实测确认**：`data.movies[]` 的元素确实带着
// id/number/title/release_date/has_cnsub/**magnets_count**（本页实测值里就有一个 0），
// 每页 10 条 + `current_page` —— 与 `movieSlim` 一致，
// 因此「magnets_count==0 就不发磁链请求」这个短路是安全的（见 hydrate）。
// 翻页行为也与收藏列表一致（空页 = 到底）。
type reviewMoviesEnvelope struct {
	Movies []movieSlim `json:"movies"`
}

// WantToWatch 实现 catalog.Source：读取用户在 App 里标记为「想看」的作品。
//
// # 为什么用 /api/v2/users/review_movies 而不是 /users/collected_codes
//
// 它们是**两个不同的概念**：`collected_codes` 是「收藏的番号」，而「想看」
// 是 App 作品页上的一个独立标记（与 `watched`「看过」并列）。用户要的是后者 ——
// 他在 App 里点的是「想看」。
//
// # 三处刻意的行为（与 CollectedActresses 一致，理由相同）
//
//  1. **没有 token 时直接返回 ErrNoToken，不发请求。**
//  2. **翻页到底，不去猜总数**（上游不返回总数，只能靠空页判断）。
//  3. **按 movie id 去重**（上游分页在数据变动时可能重叠）。
//
// 另有一处不同：磁链是逐部作品单独拉的（一次额外请求/部），而
// `magnets_count == 0` 的作品直接跳过 —— 「标了想看但还没有种」是常态，
// 那些作品仍然保留在结果里（Magnets 为空），由上层决定怎么呈现。
func (c *Client) WantToWatch(ctx context.Context) (catalog.WantList, error) {
	if strings.TrimSpace(c.Token) == "" {
		return catalog.WantList{}, fmt.Errorf("取想看清单: %w", catalog.ErrNoToken)
	}

	var movies []movieSlim
	seen := make(map[string]bool)
	appendPage := func(page []movieSlim) {
		for _, m := range page {
			id := strings.TrimSpace(m.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			movies = append(movies, m)
		}
	}

	for page := 1; page <= maxWantPages; page++ {
		items, err := c.reviewMoviesPage(ctx, "want_watch", page)
		if err != nil {
			return catalog.WantList{}, err
		}
		if len(items) == 0 {
			// 空页 = 到底。这是一份**完整**的清单。
			works, err := c.hydrate(ctx, movies)
			if err != nil {
				return catalog.WantList{}, err
			}
			return catalog.WantList{
				Works:        works,
				PagesFetched: page,
				MaxPages:     maxWantPages,
			}, nil
		}
		appendPage(items)
	}

	// 到达上限仍未遇空页。但「满页」不等于「还有更多」—— 再探一页才能分清
	// 「恰好标到上限」与「上限之外确实还有」。探针的内容**不入清单**。
	probe, err := c.reviewMoviesPage(ctx, "want_watch", maxWantPages+1)
	if err != nil {
		return catalog.WantList{}, err
	}
	works, err := c.hydrate(ctx, movies)
	if err != nil {
		return catalog.WantList{}, err
	}
	if len(probe) == 0 {
		return catalog.WantList{
			Works:        works,
			PagesFetched: maxWantPages + 1,
			MaxPages:     maxWantPages,
		}, nil
	}
	return catalog.WantList{
		Works:        works,
		Truncated:    true,
		PagesFetched: maxWantPages,
		MaxPages:     maxWantPages,
	}, nil
}

// reviewMoviesPage 拉某个 status 清单的第 page 页（从 1 开始）。
//
// status 目前只用到 "want_watch"；"watched" 留给将来（它是推送模式要用的那一步，
// 已划出本次范围）—— 因此这里按 status 参数化而不是写死 want_watch。
func (c *Client) reviewMoviesPage(ctx context.Context, status string, page int) ([]movieSlim, error) {
	var env reviewMoviesEnvelope
	if err := c.GetJSON(ctx, "/api/v2/users/review_movies",
		url.Values{"status": {status}, "page": {strconv.Itoa(page)}}, &env); err != nil {
		return nil, fmt.Errorf("取 %s 清单第 %d 页: %w", status, page, err)
	}
	return env.Movies, nil
}
