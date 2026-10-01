package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// actor 转储 /api/v1/actors/{id}。
//
// 这条命令回答的是：**主属性字母 `i` / `v` 的含义**。
// 先前笔记只能确定 `p` `m` `c` `s`，而 `i` `v` 被记成「来自上游的 filter_tags，
// 含义未确认」。filter_tags 就是上游自己对这组字母的说明，因此直接读它即可 ——
// 不需要猜，也不需要解析 APK。
//
// 同时它给出 `filter_by_tags` 的候选标签 id（tags[]），供 tags 命令使用。
//
// 输出刻意保留**完整原始 JSON**：这是复勘工具，形状未知时不该由工具替读者
// 决定哪些字段有用。
func (p *probe) actor(id string) error {
	id, err := requireActress(id)
	if err != nil {
		return err
	}

	var raw json.RawMessage
	if err := p.client.GetJSON(p.ctx, "/api/v1/actors/"+url.PathEscape(id), nil, &raw); err != nil {
		return fmt.Errorf("取女优 %s 资料: %w", id, err)
	}
	p.save("actor_"+id, raw)

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return fmt.Errorf("缩进 %s 的响应: %w", id, err)
	}
	fmt.Printf("=== /api/v1/actors/%s ===\n%s\n", id, pretty.String())

	// 把本票关心的字段单独挑出来，免得在一大段 JSON 里翻找。
	var tree any
	if err := json.Unmarshal(raw, &tree); err == nil {
		fmt.Println("\n=== 本票关心的字段 ===")
		interesting := map[string]bool{
			"id": true, "name": true, "name_zht": true, "videos_count": true,
			"filter_tags": true, "tags": true, "tag": true,
		}
		var lines []string
		walkFind(tree, "", interesting, &lines)
		sort.Strings(lines)
		for _, l := range lines {
			fmt.Println("  " + l)
		}
		if len(lines) == 0 {
			fmt.Println("  （一个都没找到 —— 上游改过字段名？以上面完整 JSON 为准）")
		}
	}
	return nil
}

// walkFind 遍历任意 JSON 结构，把**键名命中 want** 的节点按路径收集起来。
//
// 存在的理由：复勘面对的是形状未确认的响应。写死字段名的话，
// 一旦上游改名，探针会静默打印空 —— 而「什么都没找到」正是最需要看见的信号。
// 因此这里按路径依次下探，命中就整段取值。
//
// 输出形如：
//
//	actors[0].filter_tags = [{"letter":"p","name":"Playable"}, ...]
//	actors[0].tags[3].name = "单体作品"
func walkFind(v any, path string, want map[string]bool, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if want[k] {
				body, err := json.Marshal(child)
				if err != nil {
					continue
				}
				*out = append(*out, fmt.Sprintf("%s = %s", childPath, truncateJSON(body, 600)))
				continue
			}
			walkFind(child, childPath, want, out)
		}
	case []any:
		for i, child := range t {
			walkFind(child, fmt.Sprintf("%s[%d]", path, i), want, out)
		}
	}
}

// truncateJSON 把过长的取值截断，只为人眼阅读；完整内容在落盘的证据文件里。
func truncateJSON(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…（完整值见落盘证据）"
}

// raw 转储任意端点，作为复勘时的逃生口。
//
// 存在的理由：本票只能预设「现在已知要问的问题」。下一次复勘一定会有
// 新的端点要看（上游加字段、加端点），而那时不该为了看一眼响应
// 再写一个命令。参数按 k=v 给，重复给同一个 k 就是多值。
func (p *probe) raw(path string, kv []string) error {
	q := url.Values{}
	for _, pair := range kv {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("参数 %q 不是 k=v 形式", pair)
		}
		q.Add(k, v)
	}

	var raw json.RawMessage
	if err := p.client.GetJSON(p.ctx, path, q, &raw); err != nil {
		return err
	}
	p.save("raw_"+sanitizeName(path), raw)

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return fmt.Errorf("缩进响应: %w", err)
	}
	fmt.Printf("=== %s %v ===\n%s\n", path, kv, pretty.String())
	return nil
}

// sanitizeName 把端点路径变成能当文件名的字符串。
func sanitizeName(path string) string {
	r := strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "-")
	return strings.Trim(r.Replace(path), "_")
}

// letters 回答：**主属性字母 `i` / `v` 的含义**。
//
// 判据是女优资料里的 `filter_tags` —— 上游自己给出的「这个女优支持哪些主属性」
// 清单，每项带 id（字母）与 name（含义）。先前笔记只能确定 p/m/c/s，
// 并把 i/v 记成「来自上游 filter_tags，含义未确认」。这里就直接把它读出来。
//
// 但**只查一个女优是不够的**：filter_tags 是**按女优**给出的可用属性
// （EvkJ 只有 p/s/m/c 四个），所以 i/v 可能只在别的女优身上出现。
// 因此本命令扫一批女优（默认从排行榜取），汇总整张词表。
//
// 顺带产出：「哪些字母存在」是 combo 实验的输入 —— 拿不存在的字母去试
// 只会得到「与基线同序」，那与「该字母被静默忽略」在输出上无法区分。
func (p *probe) letters(ids []string, sample int, zone string) error {
	if len(ids) == 0 {
		var err error
		ids, err = p.actressIDPool(sample, zone)
		if err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("没拿到任何女优 id")
	}

	// 字母 → 名称集合，以及每个字母在哪些女优身上出现过。
	names := map[string]map[string]bool{}
	sources := map[string][]string{}
	scan := map[string][]tagRef{}
	var failed []string

	for _, id := range ids {
		var raw json.RawMessage
		if err := p.client.GetJSON(p.ctx, "/api/v1/actors/"+url.PathEscape(id), nil, &raw); err != nil {
			failed = append(failed, id)
			continue
		}
		ft := extractFilterTags(raw)
		scan[id] = ft
		for _, t := range ft {
			if names[t.ID] == nil {
				names[t.ID] = map[string]bool{}
			}
			if t.Name != "" {
				names[t.ID][t.Name] = true
			}
			sources[t.ID] = append(sources[t.ID], id)
		}
	}
	p.save("letters_scan_"+zone, scan)

	fmt.Printf("=== filter_tags 字母表（zone=%s，扫了 %d 位女优）===\n\n", zone, len(ids))
	fmt.Printf("  %-6s %-24s %6s  %s\n", "字母", "上游给的名字", "出现数", "其中一位女优")
	var keys []string
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var ns []string
		for n := range names[k] {
			ns = append(ns, n)
		}
		sort.Strings(ns)
		src := ""
		if len(sources[k]) > 0 {
			src = sources[k][0]
		}
		fmt.Printf("  %-6s %-24s %6d  %s\n", k, strings.Join(ns, "/"), len(sources[k]), src)
	}
	if len(failed) > 0 {
		fmt.Printf("\n（%d 位女优请求失败：%s）\n", len(failed), strings.Join(failed, " "))
	}

	// 把「哪些字母扫到了、哪些没扫到」说清楚。
	//
	// ⚠️ 这段提示很重要，因为不加它就会得出一个**错的结论**：
	// 「i/v 没出现 ⇒ i/v 不存在」。实测已经推翻了这个推论 ——
	// i/v **确实有效**（`filter_by=0:a:<id>:v::` 真的筛出了东西），
	// 只是上游不在 filter_tags 里给它们名字。
	// 因此把「filter_tags 这份清单不完整」这一点直接写在输出里，
	// 并把读者导向真正能回答含义的命令（props）。
	missing := 0
	for _, k := range []string{"p", "m", "c", "s", "i", "v"} {
		if _, ok := names[k]; !ok {
			missing++
		}
	}
	if missing > 0 {
		fmt.Println()
		fmt.Printf("  %d 个主属性字母本轮没在 filter_tags 里出现。\n", missing)
		fmt.Println("  ⚠️ 这**不等于**它们不存在 —— 实测 `i`/`v` 都是有效的，")
		fmt.Println("     上游只是不在 filter_tags 里列它们。这份清单是「上游给名字的部分」，不是全部。")
		fmt.Println("     要确定含义用 `contractprobe props <女优id>`（用字段反推，不依赖上游文案）。")
	}
	return nil
}

// actressIDPool 收集一批女优 id，用于扫 filter_tags。
//
// 两个来源，都不需要 token：
//
//	/api/v1/actors            可分页（limit 上限 50），每页 50 位
//	/api/v1/rankings/actors   不分页（实测 limit/type/filter_by 都不改变结果，固定 97 位）
//
// 之所以要多来源、要翻页：`filter_tags` 是**按女优**给出的可用属性，
// 因此「某个字母是否存在」这个问题只有把样本做够大才能回答。
// 97 位就说「i/v 不存在」是不够的 —— 那一开始正是先前笔记犯的错
// （把一次小样本观察写成了断言）。
//
// 同时这个函数本身就是一条次要实证：`/api/v1/actors` 真的翻页、
// 而 `/api/v1/rankings/actors` 不翻（第二页无新 id 就停）。
func (p *probe) actressIDPool(sample int, zone string) ([]string, error) {
	if sample <= 0 {
		sample = 100
	}
	seen := map[string]bool{}
	var out []string
	add := func(ids []string) (fresh int) {
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
			fresh++
		}
		return fresh
	}

	// pageIDs 逐页拉一个「返回 id 列表」的端点，直到没有新 id、或到页数上限。
	//
	// 两个来源的分页形状完全一样，差别只在路径与参数 —— 写成两份的话，
	// 将来改一处（比如加退避）必然漏掉另一处。
	pageIDs := func(path, savePrefix string, q url.Values, maxPages int) error {
		for page := 1; page <= maxPages && len(out) < sample; page++ {
			query := url.Values{}
			for k, vs := range q {
				query[k] = append([]string(nil), vs...)
			}
			query.Set("page", fmt.Sprint(page))

			var raw json.RawMessage
			if err := p.client.GetJSON(p.ctx, path, query, &raw); err != nil {
				// 第一页就失败 = 这个来源完全不可用，直接报错；
				// 后面某页失败 = 翻到底了（或上游偶发），已收到的样本仍然有用。
				if page == 1 {
					return fmt.Errorf("取 %s: %w", path, err)
				}
				return nil
			}
			p.save(fmt.Sprintf("%s_p%d", savePrefix, page), raw)
			if add(extractIDs(raw)) == 0 {
				return nil
			}
		}
		return nil
	}

	// 来源一：/api/v1/actors，真翻页（limit 上限 50，实测每页 50 位）。
	if err := pageIDs("/api/v1/actors", "actors_pool",
		url.Values{"type": {"hot"}, "limit": {"50"}}, 40); err != nil {
		return nil, err
	}

	// 来源二：排行榜。实测**不翻页**（第二页没有新 id），因此上限给小一点。
	if err := pageIDs("/api/v1/rankings/actors", "rankings_actors_"+zone,
		url.Values{"type": {"monthly"}, "filter_by": {zone}, "limit": {"50"}}, 3); err != nil {
		return nil, err
	}
	return out, nil
}

// extractFilterTags 从女优资料里取出 filter_tags。
//
// 形状是实测确认的：顶层 "filter_tags": [{id, name}, ...]。
// 但仍然走得宽容 —— 形状变了的话返回空，而不是 panic。
func extractFilterTags(raw json.RawMessage) []tagRef {
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}
	arr, ok := tree["filter_tags"].([]any)
	if !ok {
		return nil
	}
	var out []tagRef
	for _, el := range arr {
		m, ok := el.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, tagRef{ID: anyToString(m["id"]), Name: anyToString(m["name"])})
	}
	return out
}

// extractIDs 走查任意 JSON，收集所有名为 id 的字符串值。
//
// 宽容是刻意的：排行榜/列表端点的载荷形状各异（女优对象、作品对象、嵌套的
// actors 数组），而本命令只要 id。宁可多收几个（后面会被去重、被逐位复核），
// 也不要因为形状猜错而返回空 —— 空集会被误读成「上游没有数据」。
func extractIDs(raw json.RawMessage) []string {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}
	var out []string
	var visit func(v any)
	visit = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				if k == "id" {
					if s := anyToString(child); s != "" {
						out = append(out, s)
					}
					continue
				}
				visit(child)
			}
		case []any:
			for _, child := range t {
				visit(child)
			}
		}
	}
	visit(tree)
	return out
}

// magnetWire 是磁链的线格式。字段名与线上一致。
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

// magnetDump 转储若干作品的磁链，用来核对 `size` 字段的单位。
//
// 这条命令回答的是：**`size` 的单位**。
// 先前笔记假定它是兆字节（并据此填 RSS 的 enclosure length），但从未核实。
// 核对的判据是拿同一条磁链去 javdb.com 的网页看它标注的体积 ——
// 网页显示的是人名可读的 "3.11GB"，而这里拿到的是整数 3110。
// 因此本命令刻意把 name（线格式字段也叫 name）/ infohash / size 一起打出来，
// 好让两处能对上同一条记录。
func (p *probe) magnetDump(movieIDs []string) error {
	for _, id := range movieIDs {
		var env magnetsEnvelope
		if err := p.client.GetJSON(p.ctx, "/api/v1/movies/"+url.PathEscape(id)+"/magnets", nil, &env); err != nil {
			return fmt.Errorf("取作品 %s 的磁链: %w", id, err)
		}
		p.save("magnets_"+id, env)

		fmt.Printf("=== /api/v1/movies/%s/magnets （%d 条）===\n", id, len(env.Magnets))
		fmt.Printf("  %-42s %8s %6s %5s %5s  %s\n", "name", "size", "cnsub", "hd", "files", "created_at")
		for _, m := range env.Magnets {
			fmt.Printf("  %-42s %8d %6v %5v %5d  %s\n",
				clip(m.Name, 42), m.Size, m.CNSub, m.HD, m.FilesCount, m.CreatedAt)
		}
		fmt.Println()
	}
	fmt.Println("判据：拿同一条 name/infohash 去 javdb.com 的作品页看它标注的体积。")
	fmt.Println("      网页给人看的单位（如 3.11GB）与这里的整数对上，就能定下单位。")
	return nil
}

// clip 把字符串截到 n 个字符以内，保持表格不散架。
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// values 是 url.Values 的简写构造，让实验代码读起来像一张参数表。
func values(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

// joinIDs 把 id 序列拼成一行，用于报告中的人眼对照。
func joinIDs(ids []string) string {
	return strings.Join(ids, " ")
}
