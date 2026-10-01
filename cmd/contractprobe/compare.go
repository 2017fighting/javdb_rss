package main

import (
	"fmt"
	"strings"

	"encoding/json"
)

// 本文件是三个**对照实验**：同一批作品、只改一个参数，比较两次请求的 id 序列。
//
// 每个实验都遵循同一条方法论，写在这里一次：
//
//  1. **基线**是「该女优全部作品 + release/desc」，即服务实际会发的那个请求。
//  2. **正向对照**：实验里必须至少有一个已知会改变结果的输入（sort_by=score、
//     单独一个主属性字母）。没有正向对照的话，「全部同序」这个结论
//     无法与「探针根本没把参数发出去」区分开 —— 这正是本项目反复强调的
//     「不能有假通过的检查」。
//  3. **比较的是 id 序列而不是集合**：set 相同但顺序不同，与 set 不同，
//     是两种不同的结论，不能混成一个「变了/没变」。
//
// ============================ sort_by ============================

// sortCandidate 是一个要对照的 sort_by 取值，以及它从哪来。
//
// 标出来源是因为两类证据的分量不同：来自 APK UI 标签的词是
// 「App 自己会发的值」，而笔记里试过的猜测值只是猜测。
// 结论必须能区分这两者。
type sortCandidate struct {
	Value  string
	Origin string
}

// sortCandidates 是本次对照的取值表。
//
// 前一版笔记只试了若干**猜测值**，且无法区分「非法值」与「合法但恰好同序」。
// 这里把两个来源分开列：
//
//	(1) APK 的 UI 标签 "Sort by X"（v1.9.35 静态取证，sha256 见 notes §0）——
//	    这是 App 自己会发给上游的词表，比猜测值有分量。
//	(2) 先前笔记试过的猜测值 —— 留着是为了让结论能直接与前版对比。
var sortCandidates = []sortCandidate{
	{Value: "release", Origin: "基线（也是服务端默认）"},
	// (1) APK 的 UI 标签 "Sort by X" 里的词。
	{Value: "score", Origin: "app: Sort by score"},
	{Value: "hit", Origin: "app: Sort by hit"},
	{Value: "update", Origin: "app: Sort by update"},
	{Value: "watched", Origin: "app: Sort by watched count"},
	{Value: "want_watch", Origin: "app: Sort by want watch count"},
	{Value: "number", Origin: "app: Sort by number"},
	{Value: "create_date", Origin: "app: Sort by create date"},
	// (2) 上面那些标签的可能拼法 —— 上游的字段名与 UI 文案常常不字面相同
	// （例：实体 id 是 `id`，而 UI 叫番号）。
	{Value: "watched_count", Origin: "app 标签的字段名拼法"},
	{Value: "want_watch_count", Origin: "app 标签的字段名拼法"},
	{Value: "number_letter", Origin: "app 标签的字段名拼法"},
	{Value: "created_at", Origin: "app 标签的字段名拼法"},
	{Value: "create_at", Origin: "app 标签的字段名拼法"},
	{Value: "release_date", Origin: "app 标签的字段名拼法"},
	{Value: "updated_at", Origin: "update 的字段名拼法"},
	{Value: "views", Origin: "ranking 的字段名"},
	{Value: "view_count", Origin: "ranking 的字段名"},
	{Value: "collect", Origin: "ranking 的字段名"},
	{Value: "collect_count", Origin: "ranking 的字段名"},
	{Value: "magnets_count", Origin: "作品形态字段"},
	{Value: "duration", Origin: "作品形态字段"},
	{Value: "size", Origin: "磁链字段"},
	{Value: "random", Origin: "常见拼法"},
	// (2b) 第二批：把已见过的实体字段名全部试一遍。
	//
	// 这一批的由来是阶段一的发现：App 的 UI 文案与上游的字段名**并不字面相同**
	// （`Sort by watched count` 不认 `watched`，只认 `watched_count`）。
	// 因此正确的搜索空间不是「UI 可能怎么写」，而是「实体上可能有哪些字段名」——
	// 字段名从 /api/v4/movies/{id} 的响应当里取。
	{Value: "comments_count", Origin: "v4 详情字段"},
	{Value: "reviews_count", Origin: "v4 详情字段"},
	{Value: "videos_count", Origin: "女优/系列字段"},
	{Value: "views_count", Origin: "系列字段（/series/letters）"},
	{Value: "play_count", Origin: "猜：播放数"},
	{Value: "playback", Origin: "排名端点名（/rankings/playbackP...）"},
	{Value: "favorite", Origin: "猜：收藏数"},
	{Value: "favorite_count", Origin: "猜：收藏数"},
	{Value: "likes_count", Origin: "猜：点赞数"},
	{Value: "share_count", Origin: "猜：分享数"},
	{Value: "download_count", Origin: "猜：下载数"},
	{Value: "title", Origin: "v4 详情字段"},
	{Value: "id", Origin: "v4 详情字段"},
	{Value: "type", Origin: "v4 详情字段"},
	{Value: "can_play", Origin: "v4 详情字段"},
	{Value: "has_cnsub", Origin: "v4 详情字段"},
	{Value: "new_magnets", Origin: "v4 详情字段"},
	{Value: "play_subtitle", Origin: "v4 详情字段"},
	{Value: "publish_date", Origin: "猜：发布日期"},
	{Value: "made_date", Origin: "猜：制作日期"},
	{Value: "released_at", Origin: "猜：发布日期"},
	{Value: "rank", Origin: "猜：排名"},
	{Value: "top", Origin: "猜：排名"},
	{Value: "trending", Origin: "猜：热度"},
	{Value: "latest", Origin: "猜：最新"},
	// (3) 先前笔记试过的猜测值 —— 留着让结论能直接与前版对比。
	{Value: "date", Origin: "note: 先前试过"},
	{Value: "hot", Origin: "note: 先前试过"},
	{Value: "new", Origin: "note: 先前试过"},
	{Value: "rating", Origin: "note: 先前试过"},
	{Value: "popularity", Origin: "note: 先前试过"},
	{Value: "view", Origin: "note: 先前试过"},
	{Value: "download", Origin: "note: 先前试过"},
	{Value: "likes", Origin: "note: 先前试过"},
	{Value: "weekly", Origin: "note: 先前试过"},
	{Value: "monthly", Origin: "note: 先前试过"},
}

// sortBy 对照一批 sort_by 取值，报告哪些与 release 产生不同的序列。
func (p *probe) sortBy(actressID string, limit int) error {
	actressID, err := requireActress(actressID)
	if err != nil {
		return err
	}

	base, err := p.moviePage(values("filter_by", actressFilter(actressID), "sort_by", "release", "order_by", "desc"), limit)
	if err != nil {
		return fmt.Errorf("取基线（release/desc）: %w", err)
	}
	baseIDs := idsOf(base)
	p.save("sort_release_desc", base)

	fmt.Printf("=== sort_by 对照（女优 %s，limit=%d）===\n", actressID, limit)
	fmt.Printf("基线 release/desc：%d 条\n\n", len(baseIDs))

	// 结果表：与原序列的关系，以及与基线共有的条数。
	//
	// 刻意**不**报「集合是否相同」：只翻第一页时，一个真实的重排
	// 必然会让前 50 条变成另一批作品 —— 于是「集合不同」对任何生效的排序
	// 都会为真，它反而会把读者引向错误的解释。
	type row struct {
		value     string
		origin    string
		count     int
		sameOrder bool
		common    int
		firstDiff int
		diffCount int
	}
	rows := make([]row, 0, len(sortCandidates))
	for _, c := range sortCandidates {
		ms, err := p.moviePage(values("filter_by", actressFilter(actressID), "sort_by", c.Value, "order_by", "desc"), limit)
		if err != nil {
			// 单个取值失败不应该让整张表丢掉 —— 报出来继续。
			fmt.Printf("  %-18s 请求失败：%v\n", c.Value, err)
			continue
		}
		ids := idsOf(ms)
		first, diffs, _ := diffStat(baseIDs, ids)
		rows = append(rows, row{
			value:     c.Value,
			origin:    c.Origin,
			count:     len(ids),
			sameOrder: first < 0,
			common:    commonCount(baseIDs, ids),
			firstDiff: first,
			diffCount: diffs,
		})
		p.save("sort_"+c.Value, ms)
	}

	fmt.Printf("  %-18s %6s %8s %9s %8s  %s\n", "sort_by", "条数", "同序", "交集", "错位数", "来源")
	for _, r := range rows {
		fmt.Printf("  %-18s %6d %8v %9d %8d  %s\n", r.value, r.count, r.sameOrder, r.common, r.diffCount, r.origin)
	}

	// 报告把结论直接写出来，免得读者自己去数表。
	var distinct []string
	for _, r := range rows {
		if !r.sameOrder {
			distinct = append(distinct, r.value)
		}
	}
	fmt.Println()
	if len(distinct) == 0 {
		// 只有 score 一个不同（或一个都不同）时，先说清楚正向对照成不成立。
		fmt.Println("⚠️ 正向对照不成立：没有任何取值与基线不同序。")
		fmt.Println("   这意味着「同序」这一列不能作为「非法值」的证据 ——")
		fmt.Println("   先确认参数真的发出去了（换一个已知有效的值试试）。")
		return nil
	}
	fmt.Printf("产生不同序列的取值：%s\n", strings.Join(distinct, " "))
	fmt.Println("（至少有一个正向对照成功：参数确实生效了，因此「同序」这一列是有信息量的。）")

	// ---- 阶段二：把「真的在排序」与「换了一批作品」分开 ----
	//
	// 阶段只看第一页，所以「不同」可能来自两种完全不同的机制：
	// 同一批作品重新排序，或服务端换了一批作品回来。两者对 feed 的影响完全不同
	// （前者只是顺序，后者是条目变化）。因此把参与者的**全部页**拉下来对比。
	fmt.Println("\n--- 阶段二：生效的取值，比较全集 ---")
	baseAll, basePages, err := p.allMovieIDs(values("filter_by", actressFilter(actressID)), limit)
	if err != nil {
		return fmt.Errorf("取基线全集: %w", err)
	}
	fmt.Printf("基线全集：%d 条 / %d 页\n\n", len(baseAll), basePages)
	fmt.Printf("  %-18s %6s %8s %9s %9s\n", "sort_by", "条数", "同集合", "同序", "首个错位")
	for _, v := range distinct {
		ids, pages, err := p.allMovieIDs(values("filter_by", actressFilter(actressID), "sort_by", v), limit)
		if err != nil {
			fmt.Printf("  %-18s 请求失败：%v\n", v, err)
			continue
		}
		first, _, sameSet := diffStat(baseAll, ids)
		fmt.Printf("  %-18s %6d %8v %9v %9d\n", v, len(ids), sameSet, first < 0, first)
		_ = pages
	}
	fmt.Println("\n「同集合 true + 同序 false」才是真正的排序；「同集合 false」意味着")
	fmt.Println("服务端换了一批作品回来（不只是顺序）—— 那对 feed 是条目变化，不只是顺序变化。")
	return nil
}

// ============================ filter_by_tags ============================

// tagRef 是从女优资料里挖出来的标签。
//
// VideosCount 是**上游声明**的「该女优带这个标签的作品数」。
// 把它一起带着，是因为「实际筛出多少条」与这个数字**可能对不上** ——
// 实测 VR 标签：声明 48、筛出 27。把两者并排打出来，差异才会被看见，
// 而不是躺在一个只有作者知道的观察里。
type tagRef struct {
	ID          string
	Name        string
	VideosCount int
}

// extractTags 尽力从 /api/v1/actors/{id} 的响应里挖出标签。
//
// 形状未经确认，因此走查得宽容：只在顶层（以及 data 下）找名为 tags 的数组，
// 取每个元素的 id（字符串或数字）与 name。挖不到就返回空 ——
// 调用方会提示改用位置参数显式给 tag id，而不是静默地「测了个空」。
func extractTags(raw json.RawMessage) []tagRef {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}
	var out []tagRef
	var visit func(v any, depth int)
	visit = func(v any, depth int) {
		if depth > 3 {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				if k == "tags" {
					if arr, ok := child.([]any); ok {
						for _, el := range arr {
							if m, ok := el.(map[string]any); ok {
								out = append(out, tagRef{
									ID:          anyToString(m["id"]),
									Name:        anyToString(m["name"]),
									VideosCount: anyToInt(m["videos_count"]),
								})
							}
						}
					}
					continue
				}
				visit(child, depth+1)
			}
		}
	}
	visit(tree, 0)
	return out
}

// anyToInt 处理「json 数字都是 float64」这件事；非数字返回 0。
func anyToInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n := 0
		for _, r := range t {
			if r < '0' || r > '9' {
				return 0
			}
			n = n*10 + int(r-'0')
		}
		return n
	default:
		return 0
	}
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// 上游的标签 id 是数字；json 解出来是 float64，去掉小数点。
		return fmt.Sprintf("%d", int64(t))
	default:
		return ""
	}
}

// filterByTags 回答：`filter_by_tags` 是否真的改变结果集。
//
// 判据是逐个标签对照基线：只要**有一个**标签改变了 id 序列（无论是筛掉了
// 作品还是只是换了顺序），这条就成立。全部同序则说明它在本样本上无效，
// 或者我们给的标签 id 不是这个端点在意的那些。
//
// 这条测试最怕的失败方式是「测了个空」：挖不到标签、或者 filter_by_tags
// 被服务端静默忽略，两者在输出上都是「全都同序」。因此报告里把
// 「用了哪些标签 id」明确打出来，并区分「集合变了」与「只是顺序变了」。
func (p *probe) filterByTags(actressID string, limit int, explicit []string) error {
	actressID, err := requireActress(actressID)
	if err != nil {
		return err
	}

	base, err := p.moviePage(values("filter_by", actressFilter(actressID), "sort_by", "release", "order_by", "desc"), limit)
	if err != nil {
		return fmt.Errorf("取基线: %w", err)
	}
	baseIDs := idsOf(base)
	p.save("tags_baseline", base)

	tags := make([]tagRef, 0, len(explicit))
	for _, id := range explicit {
		tags = append(tags, tagRef{ID: id, Name: "(命令行给出)"})
	}
	if len(tags) == 0 {
		var raw json.RawMessage
		if err := p.client.GetJSON(p.ctx, "/api/v1/actors/"+actressID, nil, &raw); err != nil {
			return fmt.Errorf("取女优 %s 资料以挖标签: %w", actressID, err)
		}
		p.save("tags_actor_"+actressID, raw)
		tags = extractTags(raw)
		if len(tags) == 0 {
			return fmt.Errorf(
				"没能从女优 %s 的资料里挖出 tags[]（形状变了？）。\n"+
					"请先跑 `contractprobe actor %s` 看完整响应，再把标签 id 作为位置参数传进来",
				actressID, actressID)
		}
	}

	fmt.Printf("=== filter_by_tags 对照（女优 %s，limit=%d）===\n", actressID, limit)
	fmt.Printf("基线（不带 filter_by_tags）：%d 条\n", len(baseIDs))
	fmt.Printf("候选标签 %d 个\n\n", len(tags))
	// 注意：这里的「条数」是**第一页**的条数（limit 通常 50），
	// 而「声明」是上游 tags[] 里给的 videos_count。两者不是同一个口径 ——
	// 一页装不下时条数会卡在 limit 上，读表时别把它当成全集大小。
	fmt.Printf("  %-8s %-18s %7s %7s %8s %9s %8s\n",
		"tag id", "name", "声明数", "条数", "同序", "同集合", "错位数")
	changed := 0
	gotCount := map[string]int{} // 每个标签实得多少条，供下面的口径核对
	for _, t := range tags {
		ms, err := p.moviePage(values(
			"filter_by", actressFilter(actressID),
			"filter_by_tags", t.ID,
			"sort_by", "release", "order_by", "desc",
		), limit)
		if err != nil {
			fmt.Printf("  %-8s %-18s 请求失败：%v\n", t.ID, clip(t.Name, 18), err)
			continue
		}
		ids := idsOf(ms)
		first, diffs, sameSet := diffStat(baseIDs, ids)
		p.save("tags_"+t.ID, ms)
		declared := "-"
		if t.VideosCount > 0 {
			declared = fmt.Sprint(t.VideosCount)
		}
		fmt.Printf("  %-8s %-18s %7s %7d %8v %9v %8d\n",
			t.ID, clip(t.Name, 18), declared, len(ids), first < 0, sameSet, diffs)
		if first >= 0 {
			changed++
		}
		gotCount[t.ID] = len(ids)
	}

	// 把「声明数 ≠ 实际条数」的地方点出来。默认它不该发生，
	// 发生了就是我们对 filter_by_tags 的口径理解还有缺口。
	//
	// 只在**实得小于 limit** 时才报：恰好等于 limit 说明是被分页截断了，
	// 那不是口径差异，只是第一页装不下。
	var mismatched []string
	for _, t := range tags {
		got, ok := gotCount[t.ID]
		if !ok || t.VideosCount == 0 || got >= limit {
			continue
		}
		if got != t.VideosCount {
			mismatched = append(mismatched, fmt.Sprintf("%s(%s) 声明 %d、实得 %d", t.ID, t.Name, t.VideosCount, got))
		}
	}
	if len(mismatched) > 0 {
		fmt.Printf("\n⚠️ 声明数与实得数不一致的标签 %d 个（实得 < limit，因此不是被分页截断）：\n", len(mismatched))
		for _, m := range mismatched {
			fmt.Println("   " + m)
		}
		fmt.Println("   这表示 videos_count 与 filter_by_tags 的口径不同（例如前者跨区域统计）。")
	}
	fmt.Println()
	if changed == 0 {
		fmt.Println("结论指向：本样本上 filter_by_tags 不改变结果集 ——")
		fmt.Println("要么它无效，要么这些标签 id 不是它认的。两种解释都无法从外部区分，")
		fmt.Println("因此它只能继续**当作未验证功能**使用（与先前笔记一致）。")
	} else {
		fmt.Printf("结论：%d 个标签改变了结果集 —— filter_by_tags **确实生效**。\n", changed)
	}
	return nil
}

// ============================ 主属性逗号列表 ============================

// defaultMainProps 是要试的主属性字母。
//
// `i` / `v` 来自先前笔记（「来自上游的 filter_tags，含义未确认」），
// 这里一并试：即使它们的含义仍然不明，**它们能不能用**是可测的。
var defaultMainProps = []string{"p", "m", "c", "s", "i", "v"}

// combo 回答：主属性的逗号列表是否等于各单属性的交集、是否与顺序无关。
//
// 这是把「逗号分隔多个主属性 —— 实测生效」这句笔记**精确化**：
// 「生效」到底指什么？三种可能的语义，靠对照能把它们分开：
//
//	AND（交集）  —— 组合的结果集 = 各单属性结果集的交集
//	OR（并集）   —— 组合的结果集 = 各单属性结果集的并集
//	忽略         —— 组合的结果集 = 基线（等于没筛）
//
// # 为什么必须比较**全集**而不是第一页
//
// 第一版这个实验只比 page=1，于是「组合 = 交集」这一列几乎全为 false ——
// 但那是**方法错的**，不是上游错的：筛完之后每页的 50 条都是各自集合的一个前缀，
// 两个不同筛选条件的前缀本来就不会相等，与语义无关。
// 要谈「集合相等」就必须把整个片单拉全（EvkJ 229 部 = 5 页）。
// 因此本命令一律用 allMovieIDs，并对同一个掩码做缓存，避免重复请求。
//
// 另一条同样重要的：**i / v 要单独看**。它们不出现在上游的 filter_tags 里
// （见 letters 命令），但「不在 filter_tags 里」不等于「传了没用」——
// 这里就把它们和 p/m/c/s 一起当成普通候选测。
//
// 实测结论（ticket 03）：六个字母**全部生效**，`i` 筛到 227/229、`v` 筛到 151/229
// —— 含义见 props 命令。这里曾经写着「实测 i 被忽略」，那是只看第一页得出的
// 错结论（基线与 i 的第一页恰好相同），完整片单一看就现形。
func (p *probe) combo(actressID string, limit int, letters []string) error {
	actressID, err := requireActress(actressID)
	if err != nil {
		return err
	}
	if len(letters) == 0 {
		letters = defaultMainProps
	}

	cache := map[string][]string{}
	// fetch 拉某个主属性列表（可以是 "c" 或 "c,m"）的全集，带缓存。
	fetch := func(main string) ([]string, error) {
		if ids, ok := cache[main]; ok {
			return ids, nil
		}
		mask := actressFilter(actressID)
		if main != "" {
			mask += ":" + main + "::"
		}
		ids, _, err := p.allMovieIDs(values("filter_by", mask), limit)
		if err != nil {
			return nil, fmt.Errorf("取 filter_by=%s 的全集: %w", mask, err)
		}
		cache[main] = ids
		p.save("combo_"+strings.ReplaceAll(main, ",", ""), ids)
		return ids, nil
	}

	base, err := fetch("")
	if err != nil {
		return err
	}

	// ---- 单属性 ----
	fmt.Printf("=== 单属性（女优 %s，全集）===\n", actressID)
	fmt.Printf("基线（无主属性）：%d 部\n\n", len(base))
	fmt.Printf("  %-4s %8s %9s %9s %9s  %s\n", "字母", "全集", "=基线", "⊂基线", "生效", "判断")
	effectiveLetters := make([]string, 0, len(letters))
	for _, l := range letters {
		ids, err := fetch(l)
		if err != nil {
			fmt.Printf("  %-4s 请求失败：%v\n", l, err)
			continue
		}
		sameSet := sameElements(base, ids)
		subset := isSubset(ids, base)
		effective := !sameSet
		if effective {
			effectiveLetters = append(effectiveLetters, l)
		}
		// 注意这里的措辞：与基线同一集合**不等于**「被忽略」——
		// 如果该属性对这部片单里的每一部都为真（例如所有作品都有预览图），
		// 筛出来的集合也会与基线相同。两种原因从外部**无法区分**，
		// 因此只能如实并列，不能断言其中一个。
		reason := "与基线同集（该属性可能全集为真，也可能被忽略 —— 外部不可区分）"
		if effective {
			reason = "结果集与基线不同"
		}
		fmt.Printf("  %-4s %8d %9v %9v %9v  %s\n", l, len(ids), sameSet, subset, effective, reason)
	}
	if len(effectiveLetters) == 0 {
		fmt.Println("\n一个字母都与基线同集 —— 先确认 filter_by 语法没写错（基线应当不为空），")
		fmt.Println("再确认这份片单不是「所有作品都满足全部属性」那种退化情形。")
		return nil
	}

	// ---- 组合：只对**真正生效**的字母做子集枚举 ----
	//
	// 对不生效的字母枚举组合没有信息量：交集里会混进一个恒等于基线的集合，
	// 于是「组合 = 交集」永远为真，把结论稀释掉。
	fmt.Printf("\n=== 组合（只枚举与基线不同集的字母：%s）===\n\n", strings.Join(effectiveLetters, " "))
	fmt.Printf("  %-12s %8s %9s %9s %9s %9s\n", "组合", "全集", "=交集", "交集同序", "=并集", "=基线")
	for _, c := range subsets(effectiveLetters) {
		if len(c) < 2 {
			continue
		}
		joined := strings.Join(c, ",")
		ids, err := fetch(joined)
		if err != nil {
			fmt.Printf("  %-12s 请求失败：%v\n", joined, err)
			continue
		}

		want := cache[c[0]]
		for _, l := range c[1:] {
			want = intersectSeq(want, cache[l])
		}
		firstInter, _, sameSetInter := diffStat(want, ids)
		union := unionSeq(cache, c)
		_, _, sameSetUnion := diffStat(union, ids)
		fmt.Printf("  %-12s %8d %9v %9v %9v %9v\n",
			joined, len(ids), sameSetInter, firstInter < 0, sameSetUnion, sameElements(base, ids))
	}
	fmt.Println()
	fmt.Println("判读：若「=交集」与「交集同序」两列都为 true，则逗号列表是 AND（交集）语义，")
	fmt.Println("      且组合结果与交集**同序** —— 合成一条规则的判据就齐了。")

	// ---- 顺序无关 ----
	fmt.Printf("\n=== 顺序无关（同一组字母，两种写法，全集对照）===\n\n")
	orderOK, orderBad := 0, 0
	for _, pr := range orderedPairs(effectiveLetters) {
		if pr[0] > pr[1] {
			continue // 每对只看一次
		}
		a, err := fetch(pr[0] + "," + pr[1])
		if err != nil {
			continue
		}
		b, err := fetch(pr[1] + "," + pr[0])
		if err != nil {
			continue
		}
		if sameSeq(a, b) {
			orderOK++
			continue
		}
		orderBad++
		fmt.Printf("  ✗ %s,%s vs %s,%s 不同（%d 条 vs %d 条）\n", pr[0], pr[1], pr[1], pr[0], len(a), len(b))
	}
	fmt.Printf("  %d 对逐位同序（顺序无关），%d 对不同序\n", orderOK, orderBad)
	return nil
}

// diffHint 给出「首个错位」的可读表述。
func diffHint(first int, ids []string) string {
	if first < 0 {
		return "—"
	}
	if first >= len(ids) {
		return fmt.Sprintf("第 %d 位（右侧更短）", first)
	}
	return fmt.Sprintf("第 %d 位", first)
}
