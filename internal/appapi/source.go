package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

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
	// 注意它与磁链级的 `cnsub` 不是一回事：作品级只是「该作品有中文字幕版」，
	// 而 feed 要选的是具体某条磁链，因此槽位规则用的是磁链级的那个。
	// 这里保留它是因为它能在不拉磁链的情况下先按中文字幕筛一遍。
	HasCNSub bool `json:"has_cnsub"`
	// MagnetsCount 是磁链数量。为 0 表示尚无磁链候选 —— 此时不必去拉磁链列表。
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

// resolveExact 把番号解析成**精确匹配**的作品。
//
// 返回完整的 movieSlim 而不是光秃秃的 id，因为搜索响应里已经带了
// number/title/release_date/magnets_count —— 丢掉它们就得再打一次
// `/api/v4/movies/{id}` 详情端点（之前就是这样：白花 200~400ms，
// 还丢掉了 magnets_count，因而无法对零做种的作品短路跳过磁链请求）。
func (c *Client) resolveExact(ctx context.Context, code string) ([]movieSlim, error) {
	want := normalizeCode(code)
	if want == "" {
		return nil, fmt.Errorf("番号为空")
	}

	// limit 必须显式给。这是一个**模糊/前缀搜索**，默认只返回 10 条，
	// 而精确匹配不一定排在前 10。实测（2026-09-30）limit 生效且上限就是 50，
	// 因此要满上限 —— 否则近似结果多于 10 条时真目标会被挤出，
	// 表现为一个莫名其妙的「没有精确匹配」。
	var env movieListEnvelope
	if err := c.GetJSON(ctx, "/api/v2/search",
		url.Values{"q": {code}, "limit": {strconv.Itoa(limitPerPage)}}, &env); err != nil {
		return nil, err
	}

	var matches []movieSlim
	for _, m := range env.Movies {
		if normalizeCode(m.Number) == want {
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 {
		// 找不到就**报错**，绝不退回近似结果。
		// 发错片是本服务最不该犯的一类错误；而这是个**可见的**失败，
		// 比静默发一个用户没订阅的番号安全得多。
		return nil, fmt.Errorf("番号 %q 没有精确匹配的作品（搜索返回 %d 条，均为近似结果）",
			code, len(env.Movies))
	}
	return matches, nil
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
	matches, err := c.resolveExact(ctx, code)
	if err != nil {
		return nil, err
	}
	// 一个番号对应多部作品是真实存在的（不同片商同名、不同版本等）。
	// 本服务不替用户猜，全部如实呈现。
	return c.hydrate(ctx, matches)
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
		if catalog.IsOwnParam(k) {
			continue
		}
		// ⚠️ filter_by 必须排除。buildEntityFilter 已经校验并 trim 过它，
		// 而这里再写一次会把**原始、未校验的值**盖回去 ——
		// 校验就白做了（例如 " 0:a:EvkJ " 的空白会重新出现）。
		if k == "filter_by" {
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
// 用户想加条件（只看中文字幕、只看单体作品）就自己传。
func buildEntityFilter(actressID string, params url.Values) (string, error) {
	if strings.TrimSpace(actressID) == "" {
		return "", fmt.Errorf("女优 id 为空")
	}
	raw := strings.TrimSpace(params.Get("filter_by"))
	if raw == "" {
		return "0:a:" + actressID, nil
	}
	if err := validateMask(raw, actressID); err != nil {
		return "", err
	}
	return raw, nil
}

// validateMask 拦住那些会让上游**静默返回错误内容**的 `filter_by`。
//
// 上游对非法 `filter_by` 不报错、只忽略（实测 2026-09-30），因此本地不拦的后果是：
// 用户以为加了条件，实际拿到的是**别的东西**，而且看不出来。两种真实形态：
//
//	缺骨架（如 "apmc"、"a"）  -> 上游当它无效，返回【全站最新作品】
//	id 与 URL 不符           -> 返回【别人的作品】，而 feed 标题写着这个女优
//
// 两类共五项校验，每一项都对应上面两种灾难的一种具体入口：
//
//  1. **三段骨架必须存在**（`zone:letter:id`）。
//     这是第一版漏掉的一维：当时只看主属性段（第 4 段），于是 "apmc" 被切成一段、
//     主属性为空，直接放行 —— 而 "apmc" 正是票里记录的那个陷阱。
//  2. zone 必须是数字（区域号）。
//  3. 实体字母必须是单个字符，且必须是 `a` —— 路由是女优页，
//     写成 `0:s:EvkJ` 是在要一个叫 EvkJ 的系列，几平总是笔误。
//  4. id 必须与 URL 里的女优一致 —— 否则会静默展示别人的作品。
//  5. 主属性必须是逗号分隔的单字母（如 `c,m`），不能拼在一起（如 `cm`）。
//
// 刻意**不**校验主属性字母本身是否在已知集合里：那会在这张私有契约新增
// 一个字母时把一个本来能用的配置判死，而那种新增是我们无法预知的。
// 上游对未知字母是忽略 —— 那个后果比误判死一个合法配置轻。
func validateMask(mask, actressID string) error {
	parts := strings.Split(mask, ":")

	// 1. 骨架
	if len(parts) < 3 {
		return badMask(mask, "它不是复合掩码。`filter_by` 的格式是 "+
			"{区域}:{实体字母}:{实体id}[:主属性::]，例如 0:a:EvkJ")
	}
	// 2. zone
	if parts[0] == "" || strings.IndexFunc(parts[0], func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return badMask(mask, "第一段应当是区域号数字（0=有码 1=无码 2=欧美 3=FC2）")
	}
	// 3. 实体字母
	if utf8.RuneCountInString(parts[1]) != 1 {
		return badMask(mask, "第二段应当是单个实体字母（a=女优 s=系列 m=片商 d=导演 c=番号 l=列表）")
	}
	if !strings.EqualFold(parts[1], "a") {
		return badMask(mask, fmt.Sprintf(
			"第二段的实体字母是 %q，但这个路由是**女优页**，应当用 `a`。"+
				"想订阅别的实体的作品，请用它自己的路由", parts[1]))
	}
	// 4. id
	id := strings.TrimSpace(parts[2])
	if id == "" {
		return badMask(mask, "第三段的实体 id 为空")
	}
	if !strings.EqualFold(id, actressID) {
		return badMask(mask, fmt.Sprintf(
			"第三段的实体 id 是 %q，但 URL 里的女优是 %q。"+
				"不相同会让 feed 标题写着 %s 却展示 %s 的作品", id, actressID, actressID, id))
	}
	// 5. 主属性（可缺省）
	if len(parts) > 3 {
		for _, seg := range strings.Split(parts[3], ",") {
			if strings.TrimSpace(seg) == "" {
				continue
			}
			if utf8.RuneCountInString(seg) > 1 {
				return badMask(mask, fmt.Sprintf(
					"主属性 %q 应当是**单个字母**，多个用逗号分隔（如 0:a:%s:c,m::），"+
						"而不是拼在一起", seg, actressID))
			}
		}
	}
	return nil
}

// badMask 统一包装成可判定的 ErrBadRequest，并附上可照拄的正确形式。
func badMask(mask, why string) error {
	return fmt.Errorf("%w：filter_by=%q 不合法 —— %s。\n"+
		"写成不合法的掩码不会报错，服务端只会**静默忽略**它，"+
		"结果是返回【全站最新作品】或【别人的作品】",
		catalog.ErrBadRequest, mask, why)
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
// `magnets_count == 0` 的作品直接跳过 —— 尚无磁链候选，拉也是空。
// 没有磁链的作品**仍保留在结果里**（由 feed.Build 跳过），
// 因为「该作品存在但尚无磁链候选」是有信息量的状态，不该在这一层抹掉。
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

	// 派生一个可取消的 ctx：第一个失败就让其余尽早放弃。
	//
	// 没有它的话，一个 401 已经注定整次调用要失败，剩下几十个请求
	// 还是会全部打完 —— 白耗上游配额与连接。
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var aborted atomic.Bool
	for _, idx := range pending {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// 拿信号量要**响应 ctx**。
			// 直接 `sem <- struct{}{}` 会让排队中的 goroutine 在客户端断开
			// 或已决定放弃时仍然死等，直到前面的请求跑完。
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				aborted.Store(true)
				return
			}
			defer func() { <-sem }()

			magnets, err := c.magnets(ctx, movies[idx].ID)
			if err != nil {
				cancel() // 首个失败即让其余尽早放弃
			}
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
	// 若一个错都没记到、却有人因为 ctx 被取消而放弃，那只能是**调用方**取消了。
	//
	// 这条不能省：不报的话就会返回一批「缺了磁链」的作品，
	// 而上层只会看到一次成功调用 —— 静默少给数据，是这个项目里最不愿出现的一类。
	if ctx.Err() != nil || aborted.Load() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("拉取磁链被中断")
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

// 编译期确认客户端满足领域端口。
var _ catalog.Source = (*Client)(nil)
