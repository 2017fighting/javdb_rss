package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// durationIDs 是上游给的四档时长。值取自 `/api/v2/tags?type=0` 的 `duration` 组
// （id 与边界都不是我们定的）。
//
// ⚠️ 它们**必须与年份一起给**才生效：实测 `0:t:m:::90-120:` 返回的时长是
// 76–300（根本没筛），而 `0:t:m::2020:90-120:` 返回 93–120（15/15 全部在档内）。
// 因此 browseFilter 把「有时长、没年份」当作**用户写错**，而不是默默发出去 ——
// 一条看起来筛了、实际没筛的 feed 正是本项目最不能接受的东西。
var durationIDs = map[string]bool{
	"lt-45":  true,
	"45-90":  true,
	"90-120": true,
	"gt-120": true,
}

// browseLetter 是全站浏览的实体字母。
//
// 它与女优的 `a`、清单的 `l` 并列，但**没有实体 id** —— 参见 browseFilter。
const browseLetter = "t"

// Browse 实现 catalog.Source：全站浏览（不挂实体）。
//
// 掩码形态：`{zone}:t:{main}:{tags}:{year}:{duration}:{month}`。
// 这条通道的发现过程（抓包 → 逐槽位反推 → 用不存在的 id 做对照）记在
// notes/tag-vocabulary.md 第 7 节；它是本服务里唯一能把**标签 + 时间**
// 组合起来的通道。
func (c *Client) Browse(ctx context.Context, zone int, sel catalog.BrowseSelector, params url.Values) ([]catalog.Work, error) {
	mask, err := browseFilter(zone, sel)
	if err != nil {
		return nil, err
	}
	return c.worksByMask(ctx, mask, params)
}

// browseFilter 构造全站掩码，并把**上游会静默忽略**的几种写法拦在这里。
//
// 拦的三类都是「发了不报错、但结果不是用户要的」：
//
//	标签超过 MaxTags 个   第 6 个起被静默丢弃
//	有时长没年份          时长整段被静默忽略
//	月份/年份格式不对      同上（上游当它无效）
//
// 刻意**不**校验区域号与主属性字母的取值集合之外的东西 —— 但区域号必须校验，
// 因为它决定打哪个片库，写错就是另一个库的作品。
func browseFilter(zone int, sel catalog.BrowseSelector) (string, error) {
	if catalog.ZoneName(zone) == "" {
		return "", fmt.Errorf("%w：区域号 %d 不存在（实测有效的只有 0=有码 1=无码 2=欧美 3=FC2）",
			catalog.ErrBadRequest, zone)
	}

	main := strings.TrimSpace(sel.Main)
	if err := validateMainFlags(main); err != nil {
		return "", err
	}

	tags := strings.TrimSpace(sel.Tags)
	if tags != "" {
		ids := strings.Split(tags, ",")
		if len(ids) > catalog.MaxTags {
			return "", fmt.Errorf("%w：标签给了 %d 个，但上游**只认前 %d 个**（第 6 个会被静默丢弃，"+
				"把同一个 id 挪到前 5 位就生效）。请只给 %d 个，或拆成多条订阅",
				catalog.ErrBadRequest, len(ids), catalog.MaxTags, catalog.MaxTags)
		}
		for _, id := range ids {
			if !isDigits(strings.TrimSpace(id)) {
				return "", fmt.Errorf("%w：标签 id %q 不是数字。全站掩码的标签槽与 "+
					"filter_by_tags 是同一个 id 空间（实测不存在的 id 会筛成 0 条）",
					catalog.ErrBadRequest, id)
			}
		}
	}

	year := strings.TrimSpace(sel.Year)
	if year != "" && (!isDigits(year) || len(year) != 4) {
		return "", fmt.Errorf("%w：年份 %q 应当是四位数字（如 2020）", catalog.ErrBadRequest, year)
	}

	month := strings.TrimSpace(sel.Month)
	if month != "" {
		n, err := strconv.Atoi(month)
		if err != nil || n < 1 || n > 12 {
			return "", fmt.Errorf("%w：月份 %q 应当在 1–12 之间", catalog.ErrBadRequest, month)
		}
	}

	duration := strings.TrimSpace(sel.Duration)
	if duration != "" && !durationIDs[duration] {
		return "", fmt.Errorf("%w：时长档位 %q 不认识。上游给的四档是 lt-45 / 45-90 / 90-120 / gt-120",
			catalog.ErrBadRequest, duration)
	}
	if duration != "" && year == "" {
		return "", fmt.Errorf("%w：时长必须与年份一起给 —— 实测单独给时长会被上游**静默忽略**"+
			"（`0:t:m:::90-120:` 返回的时长是 76–300，根本没筛）", catalog.ErrBadRequest)
	}

	// 空槽也照写：实测尾部的空段是允许的（抓包里 App 自己就发 0:t:m::::）。
	return strings.Join([]string{
		strconv.Itoa(zone), browseLetter, main, tags, year, duration, month,
	}, ":"), nil
}

// mainFlags 是主属性允许的字母。
//
// **这份集合不是猜的**，它是上游自己给的词表：`GET /api/v2/tags?type=0` 的
// `main` 组正好这六个，每个都带中文名（可播放 / 可下載 / 含字幕 / 單體影片 /
// 含預覽圖 / 含預覽視頻）；四个 zone 的 `main` 组实测一致。
//
// ⚠️ 与 `sort_by` 的处理**刻意不同**（那个不校验）：`sort_by` 的合法取值
// 从外部**不可枚举**（黑盒只能证真不能证伪，见 notes/actress-params.md），
// 硬校验会把上游新增的合法值判死；而主属性有一份上游直接给出的词表，
// 未知字母只可能是笔误。
//
// 真出现第 7 个字母时的代价是**可见的 400**（不是静默少筛），
// 那时改这里一行即可。
var mainFlags = map[string]string{
	"p": "可播放",
	"m": "可下載（含磁鏈）",
	"c": "含字幕",
	"s": "單體影片",
	"i": "含預覽圖",
	"v": "含預覽視頻",
}

// validateMainFlags 校验主属性段：逗号分隔的单字母（`c` 或 `c,m`）。
//
// 三条规矩，每一条都对应一种**静默失败**：
//
//	拼在一起（`cm`）  上游静默忽略整段 → 用户以为筛了，实际没筛
//	未知字母（`x`）   同上（上游只认它词表里那几个）
//	空段（`c,,m`）    无害，跳过
func validateMainFlags(main string) error {
	if main == "" {
		return nil
	}
	for _, seg := range strings.Split(main, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if utf8.RuneCountInString(seg) > 1 {
			return fmt.Errorf("%w：主属性 %q 应当是**单个字母**，多个用逗号分隔（如 c,m），"+
				"而不是拼在一起", catalog.ErrBadRequest, seg)
		}
		if _, ok := mainFlags[seg]; !ok {
			return fmt.Errorf("%w：主属性 %q 不是上游认的字母。上游自己给的词表是 "+
				"p(可播放) m(含磁鏈) c(含字幕) s(單體影片) i(含預覽圖) v(含預覽視頻) —— "+
				"写别的字母不会报错，只会被静默忽略，于是 feed 看着筛了其实没筛",
				catalog.ErrBadRequest, seg)
		}
	}
	return nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
