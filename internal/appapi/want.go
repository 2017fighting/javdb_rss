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
// 触顶时仍然不静默（报 WantList.Truncated + WARN + feed 标题），那时再上调本常量。
// 它与收藏女优的上限取了同一个量级，但这**不是**共享常量：两者的增长速度
// 与触顶后果都不同，将来各自调整时不应当互相牵动。
const maxWantPages = 40

// reviewMoviesEnvelope 是 `/api/v2/users/review_movies` 的负载形态。
//
// 契约来源：先例项目 javdb-cli 的 `internal/javdb/appapi/endpoint/user/user.go`
// （ReviewMoviesPage）：`GET /api/v2/users/review_movies`，参数 `status` + `page`，
// 列表在 `data.movies`；`status` 取 `want_watch` | `watched`。
//
// ✅ **2026-10-04 已用真 token 实测确认**：`data.movies[]` 的元素确实带着
// id/number/title/release_date/has_cnsub/**magnets_count**（实测值里就有一个 0），
// 每页 10 条 + `current_page` —— 与 `movieSlim` 一致，
// 因此「magnets_count==0 就不发磁链请求」这个短路是安全的（见 hydrate）；
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
// # 三个刻意的行为
//
//  1. **没有 token 时直接返回 ErrNoToken，不发请求。** 发了也必然被拒
//     （实测返回 JWTVerificationError），而返回空清单会被理解成「我没标过任何想看」。
//  2. **翻页到底、按 movie id 去重、触顶时报 Truncated** —— 与收藏女优完全同形，
//     由 readAllPages 一处实现。
//  3. **首次选定走既有的槽位规则**（`hydrate` → `catalog.Select`），
//     因此 feed 的 guid 与别的 feed 同一套；`pin` 装饰器在更外层把它钉住。
//
// 尚无磁链的作品**保留在结果里**（Magnets 为空）—— 「标了想看但还没有种」
// 是这份清单的常态，不是错误；是否呈现、怎么呈现由上层决定。
func (c *Client) WantToWatch(ctx context.Context) (catalog.WantList, error) {
	if strings.TrimSpace(c.Token) == "" {
		return catalog.WantList{}, fmt.Errorf("取想看清单: %w", catalog.ErrNoToken)
	}

	movies, pg, err := readAllPages(maxWantPages,
		func(m movieSlim) string { return strings.TrimSpace(m.ID) },
		func(page int) ([]movieSlim, error) { return c.reviewMoviesPage(ctx, page) })
	if err != nil {
		return catalog.WantList{}, err
	}

	works, err := c.hydrate(ctx, movies)
	if err != nil {
		return catalog.WantList{}, err
	}
	return catalog.WantList{
		Works:        works,
		Truncated:    pg.truncated,
		PagesFetched: pg.pagesFetched,
		MaxPages:     pg.maxPages,
	}, nil
}

// reviewMoviesPage 拉「想看」清单的第 page 页（从 1 开始）。
//
// status 写死为 want_watch，而不是做成参数：这条端点同时服务 `watched`
// （见 reviewMoviesEnvelope 的契约注释），但本服务当前只读这一种清单，
// 为另一种预留参数等于提前写一段没人走的路。
func (c *Client) reviewMoviesPage(ctx context.Context, page int) ([]movieSlim, error) {
	var env reviewMoviesEnvelope
	if err := c.GetJSON(ctx, "/api/v2/users/review_movies",
		url.Values{"status": {"want_watch"}, "page": {strconv.Itoa(page)}}, &env); err != nil {
		return nil, fmt.Errorf("取想看清单第 %d 页: %w", page, err)
	}
	return env.Movies, nil
}
