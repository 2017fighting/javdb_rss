package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

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
// 只会给用户发一个他根本没订阅的作品。这是本服务最不该犯的一类错误。
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
// params 是原样透传的 App 女优页查询参数。**三个键例外** ——
// `page`、`limit`、`pages` 由本服务自己控制（页数、每页条数、总页数），
// 不会透传给上游。这一点必须在文档里说清楚，
// 否则用户设了 limit 却不生效会变成难以解释的行为。
func (c *Client) Actress(ctx context.Context, id string, params url.Values) ([]catalog.Work, error) {
	filterBy, err := buildEntityFilter(id, params)
	if err != nil {
		return nil, err
	}

	pages := pageCount(params)

	base := url.Values{"filter_by": {filterBy}}
	for k, vs := range params {
		// 自有参数一律不透传。清单定义在 catalog.OwnParams 里（唯一来源）——
		// 包括 pages：它已经在上面被 pageCount 读走了。
		if catalog.OwnParams[k] {
			continue
		}
		base[k] = vs
	}
	if base.Get("sort_by") == "" {
		base.Set("sort_by", "release")
	}
	if base.Get("order_by") == "" {
		base.Set("order_by", "desc")
	}
	base.Set("limit", strconv.Itoa(limitPerPage)) // 实测服务端上限就是 50

	// 逐页拉取。页数很少（默认 1），而且翻页是为了「从零建库」这类少见场景，
	// 因此这里不做跨页并行 —— 保持上游压力可预测，也避免同一订阅被并发拉扯。
	// 页内的磁链拉取仍然是并行的（见 hydrate）。
	var all []catalog.Work
	seen := make(map[string]bool) // 按作品 id 去重，防上游分页重叠
	for page := 1; page <= pages; page++ {
		q := url.Values{}
		for k, vs := range base {
			q[k] = vs
		}
		q.Set("page", strconv.Itoa(page))

		var env movieListEnvelope
		if err := c.GetJSON(ctx, "/api/v1/movies/tags", q, &env); err != nil {
			return nil, err
		}

		fresh := make([]movieSlim, 0, len(env.Movies))
		for _, m := range env.Movies {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			fresh = append(fresh, m)
		}

		works, err := c.hydrate(ctx, fresh)
		if err != nil {
			return nil, err
		}
		all = append(all, works...)

		// 这一页没满，说明已经到底，不再白打请求。
		if len(env.Movies) < limitPerPage {
			break
		}
	}
	return all, nil
}

// limitPerPage 是每页条数。实测服务端上限就是 50（传 100/200/500 都只给 50）。
const limitPerPage = 50

// maxPages 是 `?pages=N` 的上限，防止一个 URL 把上游拖死。
//
// 一个女优约 230 部作品，5 页就够建全库；给到 20 页是很宽松的余量。
const maxPages = 20

// pageCount 从透传参数里读页数，并夹到合理范围。
func pageCount(params url.Values) int {
	raw := strings.TrimSpace(params.Get("pages"))
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	if n > maxPages {
		return maxPages
	}
	return n
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
	raw := strings.TrimSpace(params.Get("filter_by"))
	if raw == "" {
		return "0:a:" + actorID, nil
	}
	if err := validateMask(raw); err != nil {
		return "", err
	}
	return raw, nil
}

// validateMask 只拦一类**可证明是笔误**的输入：主属性拼接。
//
// 实测（2026-09-30）发现上游对非法 `filter_by` 是**静默忽略**的：
//
//	0:a:EvkJ:c,m::   主属性逗号分隔 -> ✅ 只返回带中文字幕的作品
//	0:a:EvkJ:cm::    拼在一起     -> ❌ 静默忽略，返回该女优全部作品
//
// 这比报错危险得多：用户以为加了筛选，实际拿到全集，而且看不出来。
//
// 校验规则刻意保守 —— 只拒「长度 > 1 且不含逗号」的单段，因为：
//
//   - 单个主属性就是一个字母，多个用逗号连，因此拼接**不可能**是合法值；
//   - 因此这个检查不会误伤任何合法配置。
//
// 刻意**不**校验字母本身是否在已知集合里：那会在这张私有契约新增
// 一个筛选字母时把一个本来能用的配置判死，而那种新增是我们无法预知的。
func validateMask(mask string) error {
	parts := splitMask(mask)
	// 无主属性段（如 "0:a:EvkJ"）就没得可校验。
	if parts.mainSeg == "" {
		return nil
	}
	for _, seg := range strings.Split(parts.mainSeg, ",") {
		if len(seg) > 1 {
			return fmt.Errorf(
				"%w：filter_by 的主属性应当是**单个字母**，多个用逗号分隔（如 0:a:%s:c,m::），"+
					"而不是拼在一起（%q）。注意上游对拼错的掩码是静默忽略的 —— "+
					"拼在一起不会报错，只会静默返回全部作品",
				catalog.ErrBadRequest, parts.id, seg)
		}
	}
	return nil
}

// maskParts 是 `filter_by` 拆开后的各段。
//
// 用一个具名类型而不是裸下标，是因为 `parts[3]` 这种写法读不出含义，
// 而且一旦掩码格式变了，所有魔数下标都会静默指错位置。
type maskParts struct {
	zone    string // 区域号
	entity  string // 实体类型字母
	id      string // 实体 id（如女优 id），用于错误文案给示例
	mainSeg string // 主属性逗号列表，空表示未指定
}

// splitMask 拆 `filter_by`。格式：{zone}:{letter}:{id}[:{main}:]:
func splitMask(mask string) maskParts {
	parts := strings.Split(mask, ":")
	var mp maskParts
	if len(parts) > 0 {
		mp.zone = parts[0]
	}
	if len(parts) > 1 {
		mp.entity = parts[1]
	}
	if len(parts) > 2 {
		mp.id = parts[2]
	}
	if len(parts) > 3 {
		mp.mainSeg = parts[3]
	}
	if mp.id == "" {
		// 错误文案要给一个能照抄的示例，没有 id 时用占位符。
		mp.id = "<女优id>"
	}
	return mp
}

// hydrate 把精简作品逐个补齐磁链。
//
// # 为什么要并行
//
// 这一步是 N+1 次请求（一次列表 + 每部一次磁链）。串行时实测一个 50 部的
// 女优页要 6.75 秒。但上游其实很快：
//
//	X-Runtime: 0.005328              ← 源站渲染 5ms
//	Server-Timing: cfOrigin;dur=198  ← 含 Cloudflare 边缘 218ms
//
// 也就是说那 6.75 秒**不是上游慢，是我们串行发请求**。
// 实测并发 8 把它降到 1.30 秒（并发 1→6.75s, 4→2.22s, 8→1.30s, 16→0.86s）。
// 并发 8 是收益递减的拐点附近，且对第三方上游比较克制。
//
// # 顺序与失败语义
//
//   - 结果**严格按输入顺序**返回（按下标回填），不受并发完成顺序影响。
//     feed 的呈现顺序属于上游，不能被并发打乱。
//   - 任何一个磁链请求失败都会让整次调用失败。这是刻意的：
//     静默漏掉几部作品会让用户以为「这几部没有新磁链」，
//     而那与「上游出了错」是完全不同的两回事。
//
// # 省请求的短路
//
// `magnets_count == 0` 的作品直接跳过 —— 还没人发种，拉也是空。
// 没有磁链的作品**仍保留在结果里**（由 feed.Build 跳过），
// 因为「这片存在但没种」是有信息量的状态，不该在这一层抹掉。
func (c *Client) hydrate(ctx context.Context, movies []movieSlim) ([]catalog.Work, error) {
	works := make([]catalog.Work, len(movies))
	// 记下哪些下标真的要去拉磁链。
	var pending []int
	for i, m := range movies {
		works[i] = catalog.Work{
			Number:      m.Number,
			Title:       m.Title,
			ReleaseDate: m.ReleaseDate,
		}
		if m.MagnetsCount > 0 {
			pending = append(pending, i)
		}
	}
	if len(pending) == 0 {
		return works, nil
	}

	conc := c.concurrency()
	if conc <= 1 || len(pending) == 1 {
		// 串行路径：仍然走同一段代码，只是 workers=1，避免两套逻辑分叉。
		conc = 1
	}

	type result struct {
		idx     int
		magnets []catalog.Magnet
		err     error
	}
	// 用带缓冲的 channel 而不是 WaitGroup + 锁：每个下标只被写入一次，
	// 收集时天然无竞争。
	sem := make(chan struct{}, conc)
	results := make(chan result, len(pending))

	var wg sync.WaitGroup
	for _, idx := range pending {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			magnets, err := c.magnets(ctx, movies[idx].ID)
			results <- result{idx: idx, magnets: magnets, err: err}
		}(idx)
	}

	// 收集必须在 Wait 之前完成，否则带缓冲的 channel 写满后 worker 会阻塞，
	// 而 Wait 又在等 worker —— 死锁。这里用一个独立的 goroutine 收尾。
	go func() {
		wg.Wait()
		close(results)
	}()

	var firstErr error
	for r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("取 %s (%s) 的磁链: %w",
					movies[r.idx].Number, movies[r.idx].ID, r.err)
			}
			continue
		}
		works[r.idx].Magnets = r.magnets
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return works, nil
}

// concurrency 返回磁链请求的并发上限。
func (c *Client) concurrency() int {
	if c.MagnetConcurrency <= 0 {
		return DefaultMagnetConcurrency
	}
	return c.MagnetConcurrency
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
