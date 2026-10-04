// Command markwatched 把 App 里「想看」中**已有磁链**的作品批量标成「看过」。
//
// # 它为什么是一次性工具，而不是服务的一部分
//
// 这与被砍掉的「主动推送模式」的**写回那一步**是同一件事（见
// .scratch/javdb-rss/notes/want-push-deferred.md），但这里没有轮询、没有 qbt、
// 没有队列：它是一次对真实账号的批量操作。做成常驻功能需要先回答
// 「什么时候该写回」「写回失败怎么重试」这类问题，那些问题还没答。
//
// # 默认 dry-run
//
// 它会先读一遍「想看」、打印出**打算改哪些**，然后停住。要真写必须显式加 -apply。
// 理由是它对**真实账号**做写操作，而写端点（POST /api/v1/movies/{id}/reviews）
// 在本仓库从未实测过 —— 契约来自先例项目 javdb-cli（它的 `mark --watched` 命令）：
//
//	status=watched|want_watch & score=<int> & content=<string>
//
// # 幂等与续跑
//
// 标成「看过」会让作品**离开**「想看」，因此重跑本工具自然只会处理剩下的 ——
// 中途失败（它会在第一处错误上停下）之后直接再跑一次即可，不必记录进度。
//
// # 用法
//
//	markwatched -config config.yaml              # 只看，不改
//	markwatched -config config.yaml -limit 1     # 只改一部（试水）
//	markwatched -config config.yaml -apply       # 真改（有磁链的全部）
//	markwatched -config config.yaml -undo        # 按记录文件改回「想看」
//
// # 退路
//
// 每次真写都会把改过的 movie id 记进一份记录文件（默认在配置文件旁边）。
// 没有它，一次 200 多部的批量写就没有撤销路径 —— 只能回 App 里一部部点回来。
// 那正是本仓库一直在避免的东西：一个不可逆的批量动作应该留下凭证。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/catalog"
	"github.com/2017fighting/javdb_rss/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "\n错误:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath = flag.String("config", "config.yaml", "配置文件路径")
		apply      = flag.Bool("apply", false, "真的写。不加这条只读一遍并打印计划（dry-run）")
		limit      = flag.Int("limit", 0, "最多处理几部（0 = 全部）。试水时用 1")
		delay      = flag.Duration("delay", 300*time.Millisecond, "每部之间的间隔，别把上游打急")
		score      = flag.Int("score", 0, "写进 review 的评分（默认 0）")
		content    = flag.String("content", "", "写进 review 的内容（默认空）")
		record     = flag.String("record", "", "记录文件的路径（留空 = 配置文件旁边的 markwatched-record.json）")
		undo       = flag.Bool("undo", false, "反向：按记录文件把那些作品改回「想看」")
	)
	flag.Parse()

	holder, err := config.NewHolder(*configPath)
	if err != nil {
		return err
	}
	ac := holder.Current().AppAPI

	identity := appapi.DefaultIdentity()
	if ac.DeviceUUID != "" {
		identity.DeviceUUID = ac.DeviceUUID
	}
	token, err := config.LoadToken(holder.TokenPath())
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("没有 token —— 这条路径读的是 App 里的私有清单，必须登录（javdb-rss login）")
	}
	cl := &appapi.Client{
		Host:              ac.Host,
		Identity:          identity,
		Signer:            appapi.NewSigner(),
		Lang:              ac.Lang,
		Token:             token,
		MagnetConcurrency: ac.MagnetConcurrency,
	}

	ctx := context.Background()
	recordPath := *record
	if recordPath == "" {
		recordPath = filepath.Join(filepath.Dir(holder.Path()), "markwatched-record.json")
	}

	if *undo {
		return runUndo(ctx, cl, recordPath, *delay)
	}

	fmt.Printf("读取「想看」清单（%s）…\n", ac.Host)
	list, err := cl.WantToWatch(ctx)
	if err != nil {
		return fmt.Errorf("读想看清单: %w", err)
	}
	if list.Truncated {
		// 这一条很要紧：看不全就会漏改，而漏改是**静默**的（用户以为改完了）。
		return fmt.Errorf("想看清单在 %d 页触顶（读了 %d 部），这份清单已知不完整 —— "+
			"拒绝在看不全的情况下做批量写。请上调 internal/appapi/want.go 的 maxWantPages",
			list.MaxPages, len(list.Works))
	}

	var pending, ready []catalog.Work
	for _, w := range list.Works {
		if len(w.Magnets) > 0 {
			ready = append(ready, w)
			continue
		}
		pending = append(pending, w)
	}

	fmt.Printf("\n想看共 %d 部：有磁链 %d 部（将被标成「看过」），尚无磁链 %d 部（不动）\n",
		len(list.Works), len(ready), len(pending))
	if len(pending) > 0 {
		fmt.Printf("留在「想看」的（等磁力）：")
		for i, w := range pending {
			if i > 0 {
				fmt.Print("、")
			}
			fmt.Print(w.Number)
		}
		fmt.Println()
	}

	targets := ready
	if *limit > 0 && *limit < len(targets) {
		targets = targets[:*limit]
		fmt.Printf("（-limit %d：本次只处理前 %d 部）\n", *limit, len(targets))
	}

	if !*apply {
		fmt.Printf("\n=== DRY RUN：以下 %d 部会被标成「看过」===\n", len(targets))
		for _, w := range targets {
			fmt.Printf("  %-12s %s\n", w.Number, w.ID)
		}
		fmt.Printf("\n什么都没写。要真做：加 -apply\n")
		return nil
	}

	fmt.Printf("\n=== 开始写：%d 部 ===\n", len(targets))
	var ok, failed int
	marked := make([]markedWork, 0, len(targets))
	for i, w := range targets {
		if err := mark(ctx, cl, w, "watched", *score, *content); err != nil {
			// 在第一处错误上停下，而不是硬着头皮写完 ——
			// 继续写只会在上游已经开始拒绝的时候扩大影响面。
			// 已经改成功的仍然要先落盘，否则那几部就成了没有凭证的改动。
			if len(marked) > 0 {
				_ = saveRecord(recordPath, marked)
			}
			return fmt.Errorf("第 %d/%d 部失败（%s %s）：%w\n"+
				"已成功 %d 部（已记进 %s）；修好原因后重跑本命令即可"+
				"（已改的会自然从「想看」消失）",
				i+1, len(targets), w.Number, w.ID, err, ok, recordPath)
		}
		ok++
		marked = append(marked, markedWork{ID: w.ID, Number: w.Number})
		fmt.Printf("  [%d/%d] %s → 看过\n", i+1, len(targets), w.Number)
		if i < len(targets)-1 {
			time.Sleep(*delay)
		}
	}
	if err := saveRecord(recordPath, marked); err != nil {
		// 写操作**已经发生**了，记录失败不能假装没事 —— 这是用户唯一的退路。
		return fmt.Errorf("改完了 %d 部，但写记录文件失败（退路丢失）：%w", ok, err)
	}
	failed = len(targets) - ok
	fmt.Printf("\n完成：成功 %d，失败 %d\n记录：%s（用它 -undo 可改回）\n", ok, failed, recordPath)
	return nil
}

// markedWork 是记录文件里的一条。
type markedWork struct {
	ID     string `json:"id"`
	Number string `json:"number"`
}

// saveRecord 把本次改过的作品**合并**进记录文件。
//
// # 两条立场，都是从仓库里已有东西上拄来的
//
//  1. **合并而不是覆盖**：试水（-limit 1）之后再跑全量是很自然的顺序，
//     覆盖会把试水那部的凭证冲掉。
//  2. **读不动就拒绝写**：这份文件是那些写操作唯一的撤销凭证，
//     用一个空表静默覆盖它，等于把之前的退路一起毁掉。
//     与 pin 表对损坏文件的处理一模一样（宁可拒绝，也不要静默丢状态）。
//
// 落盘用临时文件 + rename、权限 0600 —— 与 pin/store、config.SaveToken
// 同一套做法（半截文件比没有文件更坏）。内容按 id 排序：
// 同一份集合总得到同一份字节，diff 与测试才可依赖。
func saveRecord(path string, batch []markedWork) error {
	existing := map[string]markedWork{}
	switch raw, err := os.ReadFile(path); {
	case err == nil:
		var prev []markedWork
		if err := json.Unmarshal(raw, &prev); err != nil {
			return fmt.Errorf("记录文件 %s 解析失败：%w"+
				"\n拒绝用一份空记录覆盖它 —— 它是已改作品唯一的撤销凭证", path, err)
		}
		for _, m := range prev {
			existing[m.ID] = m
		}
	case os.IsNotExist(err):
		// 第一次写，正常。
	default:
		return fmt.Errorf("读记录文件 %s: %w", path, err)
	}

	for _, m := range batch {
		existing[m.ID] = m
	}
	all := make([]markedWork, 0, len(existing))
	for _, m := range existing {
		all = append(all, m)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换 %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}

// runUndo 按记录文件把作品改回「想看」。
func runUndo(ctx context.Context, cl *appapi.Client, path string, delay time.Duration) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读记录文件 %s: %w", path, err)
	}
	var marked []markedWork
	if err := json.Unmarshal(raw, &marked); err != nil {
		return fmt.Errorf("解析记录文件 %s: %w", path, err)
	}
	fmt.Printf("按 %s 把 %d 部改回「想看」\n", path, len(marked))
	for i, m := range marked {
		if err := mark(ctx, cl, catalog.Work{ID: m.ID, Number: m.Number}, "want_watch", 0, ""); err != nil {
			return fmt.Errorf("第 %d/%d 部改回失败（%s %s）：%w", i+1, len(marked), m.Number, m.ID, err)
		}
		fmt.Printf("  [%d/%d] %s → 想看\n", i+1, len(marked), m.Number)
		if i < len(marked)-1 {
			time.Sleep(delay)
		}
	}
	fmt.Printf("\n完成：%d 部已改回「想看」。记录文件没有删除 —— 确认无误后自己删。\n", len(marked))
	return nil
}

// mark 给一部作品写一条标记。
//
// 字段与先例项目一致（status/score/content）。score 与 content 允许空 ——
// 我们要的是「标记」，不是一条评论。
func mark(ctx context.Context, cl *appapi.Client, w catalog.Work, status string, score int, content string) error {
	form := url.Values{
		"status":  {status},
		"score":   {strconv.Itoa(score)},
		"content": {content},
	}
	var out map[string]any
	return cl.PostFormJSON(ctx, "/api/v1/movies/"+url.PathEscape(w.ID)+"/reviews", form, &out)
}
