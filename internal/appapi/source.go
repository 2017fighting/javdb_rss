package appapi

import (
	"context"

	"fmt"
	"net/url"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// movieSlim 是列表类端点返回的作品精简形态。
//
// `/api/v2/search`、`/api/v1/movies/tags`、`/api/v1/movies/latest` 共用这个结构，
// 因此只定义一次。字段名与线上一致，映射到 catalog.Work 的责任在本包里 ——
// 线格式改名时只需要改这一处。
type movieSlim struct {
	ID          string `json:"id"`
	Number      string `json:"number"`
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date"`
	// HasCNSub 是**作品级**的中文字幕标记。
	//
	// 注意它与磁链级的 `cnsub` 不是一回事：作品级只是「这片有字幕版」，
	// 而 feed 要选的是具体某条磁链，因此槽位规则用的是磁链级的那个。
	// 这里保留它是因为它能在不拉磁链的情况下先做一次筛选。
	HasCNSub bool `json:"has_cnsub"`
	// MagnetsCount 是磁链数量。为 0 表示还没人发种 —— 此时不必去拉磁链列表。
	MagnetsCount int `json:"magnets_count"`
}

type movieListEnvelope struct {
	Movies      []movieSlim `json:"movies"`
	CurrentPage int         `json:"current_page"`
}

// magnetWire 是 `/api/v1/movies/{id}/magnets` 返回的磁链形态。
type magnetWire struct {
	Name       string `json:"name"`
	Hash       string `json:"hash"`
	Size       int    `json:"size"`
	CNSub      bool   `json:"cnsub"`
	HD         bool   `json:"hd"`
	FilesCount int    `json:"files_count"`
	CreatedAt  string `json:"created_at"`
}

type magnetsEnvelope struct {
	Magnets []magnetWire `json:"magnets"`
}

// resolveExact 把番号解析成作品 id，**只接受番号精确匹配的那一条**。
//
// 这是本包最容易写错、后果又最隐蔽的一处。实测（2026-09-28）发现：
//
//	GET /api/v2/search?q=KV-328
//	→ 8 部作品，番号分别是 KV-328 / KV-323 / KV-322 / KV-326 / KV-318 / KV-324 / KV-329 / KV-327
//
// 这是一个**模糊/前缀搜索**，不是精确查询。如果按位置取 movies[0]，
// 番号尾部稍有不同就会静默命中错误的作品 —— 而且不会报错，
// 只会给用户发一个他根本没订阅的种子。这是本服务最不该犯的一类错误。
//
// 所以这里显式按 `number` 字段精确比对（忽略大小写与首尾空白）。
// 找不到就返回错误，**绝不退回「取第一个」**。
func (c *Client) resolveExact(ctx context.Context, code string) (string, error) {
	want := normalizeCode(code)
	if want == "" {
		return "", fmt.Errorf("番号为空")
	}

	var env movieListEnvelope
	if err := c.GetJSON(ctx, "/api/v2/search", url.Values{"q": {code}}, &env); err != nil {
		return "", err
	}

	var matches []movieSlim
	for _, m := range env.Movies {
		if normalizeCode(m.Number) == want {
			matches = append(matches, m)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("番号 %q 没有精确匹配的作品（搜索返回 %d 条，均为近似结果）", code, len(env.Movies))
	case 1:
		return matches[0].ID, nil
	default:
		// 一个番号对应多部作品是真实存在的（不同片商同名、不同版本等）。
		// 本服务不替用户猜，因此保留全部候选 —— 由上层如实呈现。
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return strings.Join(ids, ","), nil
	}
}

// normalizeCode 做番号比较前的归一化。
//
// 只做大小写与空白处理，不做更激进的规整 ——
// 「看起来一样的番号」之间的差异（连字符、后缀字母）恰恰是我们不想抹掉的，
// 抹掉它们就等于把精确匹配退化成模糊匹配，正是 resolveExact 要防的事。
func normalizeCode(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// Code 实现 catalog.Source：把番号解析成作品，并补齐各自的磁链。
func (c *Client) Code(ctx context.Context, code string) ([]catalog.Work, error) {
	idField, err := c.resolveExact(ctx, code)
	if err != nil {
		return nil, err
	}

	ids := strings.Split(idField, ",")
	works := make([]catalog.Work, 0, len(ids))
	for _, id := range ids {
		w, err := c.workByID(ctx, id)
		if err != nil {
			return nil, err
		}
		works = append(works, w)
	}
	return works, nil
}

// Actress 实现 catalog.Source。
//
// params 是原样透传的 App 演员页查询参数。**唯一的例外是 page 与 limit** ——
// 本服务要自己控制分页（见 collectWorksByFilter），因此这两个键会被覆盖。
// 这一点必须在文档里说清楚，否则用户设了 limit 却不生效会变成难以解释的行为。
//
// TODO(ticket-09): 现在只取第一页（≤50 部）。一个女优可能有 200+ 部作品
// （实测 EvkJ 的 videos_count 是 229），要不要翻页、翻几页，
// 取决于 ticket 09 算出来的成本模型与缓存策略。在那之前刻意保守。
func (c *Client) Actress(ctx context.Context, id string, params url.Values) ([]catalog.Work, error) {
	filterBy, err := buildEntityFilter(id, params)
	if err != nil {
		return nil, err
	}

	query := url.Values{"filter_by": {filterBy}}
	for k, vs := range params {
		// page/limit 由本服务控制，不接受透传。
		if k == "page" || k == "limit" {
			continue
		}
		query[k] = vs
	}
	if query.Get("sort_by") == "" {
		query.Set("sort_by", "release")
	}
	if query.Get("order_by") == "" {
		query.Set("order_by", "desc")
	}
	query.Set("page", "1")
	query.Set("limit", "50") // 实测服务端上限就是 50

	return c.collectWorksByFilter(ctx, query)
}

// buildEntityFilter 构造 `filter_by` 复合掩码。
//
// 格式实测确认（2026-09-28）：
//
//	{zone}:{letter}:{id}[:{main}:]:
//
// 其中 zone 是区域号（censored=0 uncensored=1 western=2 fc2=3），
// letter 是实体类型字母（actor=a series=s maker=m director=d code=c list=l）。
//
// 用户透传的参数里可以带 `filter_by` 覆盖默认值 —— 这正是「按照 App 里的参数来」
// 的落点：我们提供一个能用的默认（该女优的全部作品），
// 用户想加筛选（只看有字幕、只看单体作品）就自己传。
func buildEntityFilter(actorID string, params url.Values) (string, error) {
	if strings.TrimSpace(actorID) == "" {
		return "", fmt.Errorf("女优 id 为空")
	}
	if raw := strings.TrimSpace(params.Get("filter_by")); raw != "" {
		return raw, nil
	}
	return "0:a:" + actorID, nil
}

// collectWorksByFilter 拉一页作品列表并补齐磁链。
func (c *Client) collectWorksByFilter(ctx context.Context, query url.Values) ([]catalog.Work, error) {
	var env movieListEnvelope
	if err := c.GetJSON(ctx, "/api/v1/movies/tags", query, &env); err != nil {
		return nil, err
	}
	return c.hydrate(ctx, env.Movies)
}

// hydrate 把精简作品逐个补齐磁链。
//
// 这一步是 N+1 次请求（一次列表 + 每部一次磁链）。这是本服务最贵的地方，
// 也是 ticket 09 要决定要不要加缓存的原因。
//
// 两条省请求的短路：
//
//  1. `magnets_count == 0` 时直接跳过 —— 还没人发种，拉也是空。
//  2. 磁链列表本身为空的作品被保留在结果里（无磁链的作品由 feed.Build 跳过），
//     因为「这片存在但没种」是有信息量的状态，不该在这一层抹掉。
func (c *Client) hydrate(ctx context.Context, movies []movieSlim) ([]catalog.Work, error) {
	works := make([]catalog.Work, 0, len(movies))
	for _, m := range movies {
		w := catalog.Work{
			Number:      m.Number,
			Title:       m.Title,
			ReleaseDate: m.ReleaseDate,
		}
		if m.MagnetsCount > 0 {
			magnets, err := c.magnets(ctx, m.ID)
			if err != nil {
				return nil, fmt.Errorf("取 %s (%s) 的磁链: %w", m.Number, m.ID, err)
			}
			w.Magnets = magnets
		}
		works = append(works, w)
	}
	return works, nil
}

// magnets 取一部作品的磁链候选，**保持服务端返回的顺序**。
func (c *Client) magnets(ctx context.Context, movieID string) ([]catalog.Magnet, error) {
	var env magnetsEnvelope
	if err := c.GetJSON(ctx, "/api/v1/movies/"+url.PathEscape(movieID)+"/magnets", nil, &env); err != nil {
		return nil, err
	}
	out := make([]catalog.Magnet, 0, len(env.Magnets))
	for _, m := range env.Magnets {
		out = append(out, catalog.Magnet{
			Infohash:   m.Hash,
			Name:       m.Name,
			SizeMB:     m.Size,
			CNSub:      m.CNSub,
			HD:         m.HD,
			FilesCount: m.FilesCount,
			CreatedAt:  m.CreatedAt,
		})
	}
	return out, nil
}

// workByID 取单部作品及其磁链。
func (c *Client) workByID(ctx context.Context, movieID string) (catalog.Work, error) {
	// 用 /api/v4/movies/{id} 而不是搜索结果里的精简形态：
	// 详情端点给的是权威字段，且不依赖搜索是否把它排在前面。
	var det struct {
		Movie struct {
			ID          string `json:"id"`
			Number      string `json:"number"`
			Title       string `json:"title"`
			ReleaseDate string `json:"release_date"`
		} `json:"movie"`
	}
	if err := c.GetJSON(ctx, "/api/v4/movies/"+url.PathEscape(movieID), nil, &det); err != nil {
		return catalog.Work{}, err
	}

	magnets, err := c.magnets(ctx, movieID)
	if err != nil {
		return catalog.Work{}, err
	}

	return catalog.Work{
		Number:      det.Movie.Number,
		Title:       det.Movie.Title,
		ReleaseDate: det.Movie.ReleaseDate,
		Magnets:     magnets,
	}, nil
}

// 编译期确认客户端满足领域端口。
var _ catalog.Source = (*Client)(nil)
