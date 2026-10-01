// Contractprobe 是 ticket 03（上游契约复勘）的可复跑探针。
//
// # 为什么把它入库
//
// notes/actress-params.md 与 notes/auth.md 里有一批标着「未验证」的断言，
// 它们与实测事实混在一起 —— 读的人分不清哪条能当依据。把复勘做成
// **可复跑的工具**而不是一次性脚本，是因为这些结论会被反复引用：
// 上游改一次契约，这里要能原地重测，而不是重写一遍探针。
//
// （对照：ticket 04 的 `cmd/magnetprobe/` 用完即删了。它的结论是
// 「顺序不可重放」这种一次性的取证；而本票的条目是**长期要能复核**的契约断言。）
//
// # 它测的就是服务真正会发的请求
//
// 探针复用 internal/appapi 的传输层与签名实现，因此它发出的请求
// 与服务发出的**逐字节同类**（同一套公共参数、同一个 jdsignature 算法）。
// 自己另写一份 HTTP 客户端的话，测通的东西未必是服务会走的路径。
//
// # 用法
//
//	contractprobe [-config config.yaml] [-out DIR] <命令> [参数...]
//
// 命令与它们对应的票面条目见 usage()。
// 原始响应会落盘到 -out 目录（默认 contractprobe-out/），作为可引用的证据。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "\n错误：%v\n", err)
		os.Exit(1)
	}
}

// probe 把一次复勘需要的全部上下文收在一起：上游客户端、证据落盘目录、
// 以及「结果写给人看，证据写给人查」这条约定。
type probe struct {
	ctx    context.Context
	client *appapi.Client
	// outDir 是原始响应落盘的位置。空字符串表示不落盘。
	outDir string
	// saved 记录本次落盘的证据文件名，收尾时列出来。
	saved []string
}

// flags 是全局命令行参数。
type flags struct {
	configPath string
	outDir     string
	actress    string
	limit      int
	wait       time.Duration
	zone       string
	sample     int
}

func run(args []string) error {
	fs := flag.NewFlagSet("contractprobe", flag.ContinueOnError)
	var f flags
	fs.StringVar(&f.configPath, "config", "", "配置文件路径（留空用内置默认值，与 config.Load(\"\") 一致）")
	fs.StringVar(&f.outDir, "out", "contractprobe-out", "原始证据落盘目录（留空则不落盘）")
	fs.StringVar(&f.actress, "actress", "", "女优 id（actor/sort/tags/combo 命令用，例如 EvkJ）")
	fs.IntVar(&f.limit, "limit", 50, "每页条数（实测服务端上限就是 50）")
	fs.DurationVar(&f.wait, "wait", 2*time.Minute, "session 命令观察旧 token 的最长时间")
	fs.StringVar(&f.zone, "zone", "censored", "letters 命令给排行榜的 filter_by（实测它不改变结果，保留是为了下次能验证）")
	fs.IntVar(&f.sample, "sample", 100, "letters 命令要扫的女优位数（排行榜每页 97 位，会翻页凑够）")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return flag.ErrHelp
	}

	holder, err := config.NewHolder(f.configPath)
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
	cl := &appapi.Client{
		Host:     ac.Host,
		Identity: identity,
		Signer:   appapi.NewSigner(),
		Lang:     ac.Lang,
		Token:    token,
	}

	p := &probe{
		ctx:    context.Background(),
		client: cl,
		outDir: f.outDir,
	}
	if p.outDir != "" {
		if err := os.MkdirAll(p.outDir, 0o755); err != nil {
			return fmt.Errorf("创建证据目录: %w", err)
		}
	}

	cmd, cmdArgs := rest[0], rest[1:]
	err = p.dispatch(cmd, cmdArgs, f)
	p.done()
	return err
}

// dispatch 把命令名分发到对应的实验。
//
// 单独抽出来是为了让 run() 负责「准备上下文 + 收尾列证据」，
// 让这里只管「哪个命令对应哪个实验」。
func (p *probe) dispatch(cmd string, cmdArgs []string, f flags) error {
	switch cmd {
	case "actor":
		return p.actor(cmdArgOrFlag(cmdArgs, f.actress, "actress"))
	case "sort":
		return p.sortBy(cmdArgOrFlag(cmdArgs, f.actress, "actress"), f.limit)
	case "tags":
		id := cmdArgOrFlag(cmdArgs, f.actress, "actress")
		var explicit []string
		if len(cmdArgs) > 1 {
			explicit = cmdArgs[1:]
		}
		return p.filterByTags(id, f.limit, explicit)
	case "combo":
		id := cmdArgOrFlag(cmdArgs, f.actress, "actress")
		var letters []string
		if len(cmdArgs) > 1 {
			letters = cmdArgs[1:]
		}
		return p.combo(id, f.limit, letters)
	case "props":
		return p.props(cmdArgOrFlag(cmdArgs, f.actress, "actress"), f.limit)
	case "magnets":
		if len(cmdArgs) == 0 {
			return fmt.Errorf("magnets 需要至少一个作品 id 作为参数")
		}
		return p.magnetDump(cmdArgs)
	case "letters":
		return p.letters(cmdArgs, f.sample, f.zone)
	case "raw":
		if len(cmdArgs) == 0 {
			return fmt.Errorf("raw 需要一个端点路径，例如 `raw /api/v1/rankings/actors page=1`")
		}
		return p.raw(cmdArgs[0], cmdArgs[1:])
	case "collected":
		return p.collected(0)
	case "session":
		// 刻意**不**需要预先存在 token：这个实验自己登录两次，
		// 而它要的就是「新登录会不会踢掉旧会话」。要求先有一个 token
		// 反而会把实验前提搞混（那个 token 是哪次登录发的？）。
		// 它需要的是**凭据**（JAVDB_USERNAME / JAVDB_PASSWORD）。
		return p.session(f.wait)
	default:
		return fmt.Errorf("未知命令 %q（用法见 contractprobe -h）", cmd)
	}
}

// cmdArgOrFlag 允许把女优 id 写成位置参数或 -actress 标志。
//
// 两者都支持是因为复勘时常常连着换 id 重跑（位置参数更顺手），
// 而把 id 写进一长串命令里当默认值（标志）也同样常见。
func cmdArgOrFlag(args []string, flagValue, name string) string {
	if len(args) > 0 && args[0] != "" {
		return args[0]
	}
	if flagValue != "" {
		return flagValue
	}
	return ""
}

const usage = `contractprobe —— 上游契约复勘探针（ticket 03）

用法：
  contractprobe [标志] <命令> [女优 id | 作品 id ...]

命令（括号里是它回答的票面条目）：
  actor <女优id>        转储 /api/v1/actors/{id} —— filter_tags 给了主属性字母及其含义
                        （i/v 的含义），tags[] 给了 filter_by_tags 的候选标签
  sort  <女优id>        对照 sort_by 的一组候选值，报告哪些与 release 产生不同顺序
  tags  <女优id>        filter_by_tags 是否真的改变结果集（逐个标签对照全集）
  combo <女优id>        主属性逗号列表是否等于各单属性的交集、是否与顺序无关
  props <女优id>        推导每个主属性字母的含义：它对应列表响应里的哪个字段
  magnets <作品id...>   转储磁链（size / name / cnsub / created_at）—— 供 size 单位核对
  letters [女优id...]   扫一批女优的 filter_tags，汇总主属性字母表及其含义（i/v）
  raw <路径> [k=v...]   转储任意端点 —— 下次复勘要看新端点时的逃生口
  collected            复勘 /users/collected_actors 的分页契约（需要 token）
  session              需要 token：device_uuid 是否影响已发的 token、单会话「挤掉」的时序

标志：
`

// save 把一份证据落盘，返回文件名。
//
// 证据落盘是这个工具的**主要产物**：notes 里的每条结论都要能指回一份原始响应。
// 落盘失败只警告不中断 —— 拿不到证据文件比拿不到结论轻。
func (p *probe) save(name string, v any) {
	if p.outDir == "" {
		return
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告：证据 %s 序列化失败：%v\n", name, err)
		return
	}
	path := filepath.Join(p.outDir, name+".json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "警告：证据 %s 落盘失败：%v\n", name, err)
		return
	}
	p.saved = append(p.saved, path)
}

// done 在收尾时把落盘的证据列出来，免得读者去猜有哪些。
func (p *probe) done() {
	if len(p.saved) == 0 {
		return
	}
	fmt.Println("\n--- 原始证据 ---")
	for _, s := range p.saved {
		fmt.Println("  " + s)
	}
}

// slimMovie 是列表端点返回的作品精简形态。
//
// 字段不是随便挑的：它们是**用来推导主属性字母含义**的候选谓词来源。
// 一个字母的含义不应该靠猜，而应该看「筛出来的集合」与「哪个字段恰好
// 把全集分成这两半」完全重合 —— 见 props 命令。
type slimMovie struct {
	ID           string `json:"id"`
	Number       string `json:"number"`
	ReleaseDate  string `json:"release_date"`
	HasCNSub     bool   `json:"has_cnsub"`
	MagnetsCount int    `json:"magnets_count"`
	CanPlay      bool   `json:"can_play"`
	PlaySubtitle int    `json:"play_subtitle"`
	// HasPreviewVideo / HasPreviewImages 是作品是否带预览视频/预览图。
	// 它们就是 `v` / `i` 两个字母的候选解释（实测结论见 notes）。
	HasPreviewVideo  bool `json:"has_preview_video"`
	HasPreviewImages bool `json:"has_preview_images"`
	NewMagnets       bool `json:"new_magnets"`
	Duration         int  `json:"duration"`
}

// moviePage 是 /api/v1/movies/tags 的负载。
type moviePage struct {
	Movies      []slimMovie `json:"movies"`
	CurrentPage int         `json:"current_page"`
}

// moviePage 拉一页作品列表。//
// 对照实验的核心是**比较 id 序列**：set 相同但顺序不同，与 set 不同，
// 是两种不同的结论（前者说明参数只影响排序，后者说明真的筛掉了东西），
// 因此这里保留上游给出的顺序原样返回，不排序、不去重。
//
// limit/page 由本方法统一补上，避免每个实验各写一遍而写出不一致的请求。
func (p *probe) moviePage(q url.Values, limit int) ([]slimMovie, error) {
	vals := url.Values{}
	for k, vs := range q {
		vals[k] = append([]string(nil), vs...)
	}
	vals.Set("limit", fmt.Sprint(limit))
	if vals.Get("page") == "" {
		vals.Set("page", "1")
	}

	var page moviePage
	if err := p.client.GetJSON(p.ctx, "/api/v1/movies/tags", vals, &page); err != nil {
		return nil, err
	}
	return page.Movies, nil
}

// actressFilter 是「某女优全部作品」的 filter_by 掩码，即对照实验的基线。
func actressFilter(id string) string { return "0:a:" + id }

// allMovies 把一个查询的**全部页**拉下来，返回去重后的作品记录与页数。
//
// 单独需要它，是因为「只翻第一页」时无法区分两件不同的事：
// 服务端把同一批作品重新排序，还是换了一批作品回来。前者对 feed 只是顺序，
// 后者是**条目本身变了**。
//
// 去重是按 id 做的，与 internal/appapi 里跨页去重的做法一致 ——
// 上游分页实测无重叠，但「无重叠」是观察而不是承诺，重复会让对比结果看起来
// 像是真的变了。
func (p *probe) allMovies(q url.Values, limit int) ([]slimMovie, int, error) {
	seen := map[string]bool{}
	var out []slimMovie
	pages := 0
	for page := 1; page <= 40; page++ {
		vals := url.Values{}
		for k, vs := range q {
			vals[k] = append([]string(nil), vs...)
		}
		vals.Set("page", fmt.Sprint(page))
		ms, err := p.moviePage(vals, limit)
		if err != nil {
			if page == 1 {
				return nil, 0, err
			}
			break
		}
		pages++
		for _, m := range ms {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			out = append(out, m)
		}
		if len(ms) < limit {
			break
		}
	}
	return out, pages, nil
}

// allMovieIDs 与 allMovies 相同，但只返回 id 序列。
func (p *probe) allMovieIDs(q url.Values, limit int) ([]string, int, error) {
	ms, pages, err := p.allMovies(q, limit)
	return idsOf(ms), pages, err
}

// idsOf 抽出 id 序列。
func idsOf(movies []slimMovie) []string {
	out := make([]string, len(movies))
	for i, m := range movies {
		out[i] = m.ID
	}
	return out
}

// requireActress 校验女优 id 非空并给出可照抄的用法。
func requireActress(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("需要女优 id（位置参数或 -actress），例如：contractprobe actor EvkJ")
	}
	return strings.TrimSpace(id), nil
}
