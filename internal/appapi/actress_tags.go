package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// actressEnvelope 是 `/api/v1/actors/{id}` 里我们用到的部分。
//
// 契约来源：**实测**（2026-10-04，匿名可用）。见
// `.scratch/javdb-rss-ui/notes/tag-vocabulary.md` 第 4 节与 `evidence/actor_EvkJ.json`。
//
// ⚠️ `filter_tags` 与 `tags` 都在**顶层**，不在 `actor` 里 ——
// `actor` 只有 id/name/videos_count 这些基本信息。
type actressEnvelope struct {
	Actor struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		VideosCount int    `json:"videos_count"`
	} `json:"actor"`
	// FilterTags 是这位女优**支持的主属性**（feed URL 里 `main=` 的取值集合，
	// EvkJ 是 p/s/m/c），而不是标签。上游叫它 filter_tags，本服务对外一律叫 main。
	FilterTags []actressMainWire `json:"filter_tags"`
	// Tags 是她自己的标签，逐项带 videos_count，**上游不给分组**。
	Tags []actressTagWire `json:"tags"`
}

// actressMainWire 是顶层 `filter_tags` 的一项：只有 id/name。
type actressMainWire struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// actressTagWire 是顶层 `tags[]` 的一项。
type actressTagWire struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VideosCount int    `json:"videos_count"`
}

// fetchActress 是取女优详情的**唯一**上游读取点。
//
// 「feed 标题要名字」与「/actress_tags 要名字 + 主属性 + 标签」打的是同一个端点，
// 因此共用这一次读取：两份 envelope 各写一遍的话，上游字段或路径一变就会各漂各的
// （一处还会继续「能用」，只是慢慢给错东西）。
func (c *Client) fetchActress(ctx context.Context, id string) (actressEnvelope, error) {
	if strings.TrimSpace(id) == "" {
		return actressEnvelope{}, fmt.Errorf("女优 id 为空: %w", catalog.ErrBadRequest)
	}
	var env actressEnvelope
	if err := c.GetJSON(ctx, "/api/v1/actors/"+url.PathEscape(id), nil, &env); err != nil {
		return actressEnvelope{}, err
	}
	return env, nil
}

// ActressTags 实现 catalog.Source：一次读取交出显示名、她支持的主属性、她自己的标签。
//
// # 为什么叫 main 而不是 filter_tags
//
// 上游顶层那个键叫 `filter_tags`，装的却是**主属性**（`main=` 的取值集合）。
// 沿用那个名字会让下一个读它的人以为里面是标签，于是照着它去筛标签 ——
// 而那是**另一条通道**（`filter_by_tags`），两者不通用。名字纠正只在本服务内部做，
// 上游线格式照收不误。
//
// # 不需要 token
//
// 该端点匿名可用（实测），因此没配 token 时页面照样能取到某位女优的标签 ——
// 标签筛选本身是匿名的，不该被「收藏读不到」连坐。
//
// # 刻意不替调用方做的事
//
// 标签**不带分组**（上游不给），分组由页面拿词表按 id 反查 —— 反查时必须先按名字
// 消歧，因为月份 1–12 与真标签的 id 全部撞号。这个规则属于页面，不在这里假装有分组。
func (c *Client) ActressTags(ctx context.Context, id string) (catalog.ActressTags, error) {
	env, err := c.fetchActress(ctx, id)
	if err != nil {
		return catalog.ActressTags{}, err
	}

	out := catalog.ActressTags{
		// 用 URL 里那个 id，而不是上游回的 actor.id：白名单与页面都认前者，
		// 而它们必须永远是同一个标识（上游若改 id 就是另一回事了）。
		ID:          strings.TrimSpace(id),
		Name:        strings.TrimSpace(env.Actor.Name),
		VideosCount: env.Actor.VideosCount,
		Main:        make([]catalog.MainAttribute, 0, len(env.FilterTags)),
		Tags:        make([]catalog.Tag, 0, len(env.Tags)),
	}
	for _, m := range env.FilterTags {
		out.Main = append(out.Main, catalog.MainAttribute{
			ID:   strings.TrimSpace(m.ID),
			Name: m.Name,
		})
	}
	for _, t := range env.Tags {
		out.Tags = append(out.Tags, catalog.Tag{
			ID:          strings.TrimSpace(t.ID),
			Name:        t.Name,
			VideosCount: t.VideosCount,
		})
	}
	return out, nil
}

// ActressName 实现 catalog.Source：取女优显示名，用于 feed 标题。
//
// 它与 ActressTags 共用 fetchActress 的那一次读取（见 fetchActress 的注释）。
//
// # 为什么读 name 而不是 name_zht
//
// 实测（2026-09-30）：上游的 `name_zht` 字段在新版服务端上**恒为空**，
// 与 lang 无关；随 lang 变化的是 `name` 本身：
//
//	accept-language: en     -> name = "Kawakita Saika"
//	accept-language: zh-CN  -> name = "河北彩花"
//
// 也就是说想要中文名，正确做法是配 `app_api.lang: zh-CN`，
// 而不是去读 name_zht。（先例项目的测试夹具给 name_zht 填了值，
// 照抄它会写出一个永远落到 fallback 的实现。）
//
// # 为什么不需要 token
//
// 这个端点匿名可用，因此即使没配 token，feed 标题也能比光秃秃的 id 好看。
// 这也让本服务的「需求 1/2/3 不需要 token」这条性质不被破坏。
func (c *Client) ActressName(ctx context.Context, id string) (string, error) {
	env, err := c.fetchActress(ctx, id)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(env.Actor.Name)
	if name == "" {
		return "", fmt.Errorf("女优 %s 没有可用的名字", id)
	}
	return name, nil
}
