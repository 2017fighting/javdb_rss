package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/2017fighting/javdb_rss/internal/config"
)

// collected 复勘 `/users/collected_actors` 的分页契约。
//
// 这张票的条目里，只有它在前一轮已经实测过（每页 10 条、上限 20 页）。
// 之所以仍然给它一个命令，是因为「已实测过」与「能复核」是两回事：
// 上游的分页行为会变，而这个端点又是本服务**唯一会少给数据**的地方
// （收藏超过上限时清单不完整），因此它的契约值得能原地重测。
//
// # 它同时走两条路，这是刻意的
//
//  1. **原始分页**：直接按 page=1,2,3… 拉，数每页几条。
//     这条回答「上游的分页行为是什么」。
//  2. **服务自己的路径**：调用 `appapi.CollectedActresses` —— 服务真正用的那段代码。
//     这条回答「我们的实现读到了多少、有没有触顶」。
//
// 只做第一条的话，测的是上游；只做第二条的话，看不到每页条数。
// 两者都要，才能把「上游契约」与「我们的实现」分开断言。
//
// ⚠️ 它需要 token。没有已配置的 token 时会用凭据登录一次 ——
// 那会把你手机 App 上的会话挤下线（与 `javdb-rss login` 一样）。
func (p *probe) collected(maxPages int) error {
	if maxPages <= 0 {
		maxPages = 21
	}

	// 优先用已经配置好的 token：那样不会碰用户的会话。
	if p.client.Token == "" {
		username, password, ok := config.LoadCredentials()
		if !ok {
			return fmt.Errorf(
				"这个命令需要 token：设 %s（或先跑 `javdb-rss login` 写 token 文件），"+
					"或者设 %s / %s 让探针自己登录一次（后者会把你手机上的会话挤下线）",
				config.EnvToken, config.EnvUsername, config.EnvPassword)
		}
		fmt.Println()
		fmt.Println("⚠️  没有已配置的 token，因此探针要登录一次 —— 这会把你手机 App 上的会话挤下线。")
		fmt.Println()
		token, err := p.login(username, password, p.client.Identity.DeviceUUID)
		if err != nil {
			return err
		}
		p.client.Token = token
		fmt.Println("✓ 已登录")
	} else {
		fmt.Println("使用已配置的 token（不会碰你的手机会话）。")
	}

	// ---------- ① 原始分页 ----------

	fmt.Printf("\n=== /api/v1/users/collected_actors 逐页（最多看 %d 页）===\n\n", maxPages)
	fmt.Printf("  %-6s %6s  %s\n", "page", "条数", "备注")

	type pageStat struct {
		page int
		n    int
	}
	var stats []pageStat
	total := 0
	emptyAt := -1
	for page := 1; page <= maxPages; page++ {
		var raw json.RawMessage
		if err := p.client.GetJSON(p.ctx, "/api/v1/users/collected_actors",
			url.Values{"page": {fmt.Sprint(page)}}, &raw); err != nil {
			return fmt.Errorf("取第 %d 页: %w", page, err)
		}
		// 用通用的走查数 id 个数，而不是再定义一份 actors 结构 ——
		// 那样就把传输层的线格式在探针里抄了第二遍，线格式一改两边就会不一致。
		n := len(extractIDs(raw))
		stats = append(stats, pageStat{page: page, n: n})
		note := ""
		switch {
		case n == 0:
			note = "空页 = 到底"
			emptyAt = page
		case page > 1 && n == stats[0].n:
			note = "满页"
		case page > 1:
			note = "不满页（最后一页）"
		}
		fmt.Printf("  %-6d %6d  %s\n", page, n, note)
		total += n
		if n == 0 {
			break
		}
	}
	p.save("collected_pages", stats)

	// ---------- ② 服务自己的路径 ----------

	coll, err := p.client.CollectedActresses(p.ctx)
	if err != nil {
		return fmt.Errorf("走服务自己的路径读收藏: %w", err)
	}
	p.save("collected_service_path", map[string]any{
		"actresses":     len(coll.Actresses),
		"pages_fetched": coll.PagesFetched,
		"max_pages":     coll.MaxPages,
		"truncated":     coll.Truncated,
	})

	fmt.Printf("\n=== 服务自己的路径（appapi.CollectedActresses）===\n\n")
	fmt.Printf("  读到的收藏：%d 位\n", len(coll.Actresses))
	fmt.Printf("  翻页次数：  %d\n", coll.PagesFetched)
	fmt.Printf("  翻页上限：  %d\n", coll.MaxPages)
	fmt.Printf("  触顶截断：  %v\n", coll.Truncated)

	// ---------- 判读 ----------

	fmt.Println()
	fullPages, partialPages := 0, 0
	firstPage := 0
	for _, st := range stats {
		if firstPage == 0 && st.n > 0 {
			firstPage = st.n
		}
		if st.n == 0 {
			continue
		}
		if st.n == firstPage {
			fullPages++
		} else {
			partialPages++
		}
	}

	if firstPage > 0 {
		fmt.Printf("每页条数：第 1 页 %d 条；", firstPage)
		switch {
		case emptyAt > 0:
			fmt.Printf("共 %d 个满页，第 %d 页为空（到底）。\n", fullPages, emptyAt)
		case partialPages > 0:
			fmt.Printf("%d 个不满页，且在 %d 页内没遇到空页。\n", partialPages, maxPages)
		default:
			fmt.Printf("在 %d 页内全是满页、没遇到空页 —— 可能收藏很多，也可能页数上限设小了。\n", maxPages)
		}
	}

	if coll.Truncated {
		fmt.Println()
		fmt.Println("⚠️ 服务报「触顶且还有更多」—— 收藏数已经接近或超过上限。")
		fmt.Println("   此时 /collected 返回的是一份**已知不完整**的清单（带 truncated: true）。")
		fmt.Println("   处置：上调 internal/appapi 里的 maxCollectedPages，并复核那里的注释与余量。")
		return nil
	}
	fmt.Println()
	fmt.Printf("余量：上限 %d 页 × 每页 %d 条 ≈ %d 位，当前读到 %d 位。\n",
		coll.MaxPages, firstPage, coll.MaxPages*firstPage, len(coll.Actresses))
	fmt.Println("（这条换算只用于判断余量是否宽裕；真正的判据是 truncated 是否为 true。）")
	return nil
}
