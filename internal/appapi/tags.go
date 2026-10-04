package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/2017fighting/javdb_rss/internal/catalog"
)

// tagVocabularyEnvelope 是 `/api/v2/tags?type={zone}` 的负载形态。
//
// 契约来源：**实测**（2026-10-04，匿名可用）。见 notes/tag-vocabulary.md 第 1 节。
//
//	{"tags":[{"category":"基本","category_id":"main",
//	          "tags":[{"id":"p","name":"可播放"}, …]}, …]}
//
// ⚠️ 顶层那个键就叫 `tags`，装的却是**分组** —— 这是上游的形状。
// 本服务在 HTTP 层把它归一成 `groups`，免得每个读的人先误解一次。
//
// `type` 是必填的（不给报 ParameterInvalid），取值范围实测 0–3，
// 而越界值**静默回落**（`type=9` 与 `type=0` 逐字节相同）—— 见 TagVocabulary。
type tagVocabularyEnvelope struct {
	Groups []tagGroupWire `json:"tags"`
}

type tagGroupWire struct {
	CategoryID string    `json:"category_id"`
	Category   string    `json:"category"`
	Tags       []tagWire `json:"tags"`
}

// tagWire 是词表里的一个标签。
//
// VideosCount 在词表里实测**不出现**（它是女优顶层 `tags[]` 的字段）。
// 照接不误：上游哪天给了就透出去，没给就保持零值（序列化时省掉）。
type tagWire struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VideosCount int    `json:"videos_count"`
}

// TagVocabulary 实现 catalog.Source：取某个片库的标签分组词表。
//
// 纯透传 + 归一：分组顺序、分组名、组内标签与顺序一律原样。
//
// # 为什么 zone 必须在这里也判一次
//
// 上游对非法的 `type` 是**静默回落**：实测 `type=9` 与 `type=0` 的响应逐字节
// 相同。也就是说越界不会报错，只会把【另一个片库的词表】当成你要的答案
// 交出去。HTTP 层已经判过一次（那里能把用户写的字符串判成 400），
// 这一层再判是因为本方法也可以被别的调用方直接用 —— 而 `zone int` 挡不住
// 越界。两处判的是同一个集合（catalog.ValidZone），因此不会互相漂。
func (c *Client) TagVocabulary(ctx context.Context, zone int) (catalog.TagVocabulary, error) {
	if !catalog.ValidZone(zone) {
		return catalog.TagVocabulary{}, catalog.ErrUnknownZone(zone)
	}

	var env tagVocabularyEnvelope
	if err := c.GetJSON(ctx, "/api/v2/tags",
		url.Values{"type": {strconv.Itoa(zone)}}, &env); err != nil {
		return catalog.TagVocabulary{}, fmt.Errorf("取片库 %d 的标签词表: %w", zone, err)
	}

	groups := make([]catalog.TagGroup, 0, len(env.Groups))
	for _, g := range env.Groups {
		tags := make([]catalog.Tag, 0, len(g.Tags))
		for _, t := range g.Tags {
			tags = append(tags, catalog.Tag{
				ID:          strings.TrimSpace(t.ID),
				Name:        t.Name,
				VideosCount: t.VideosCount,
			})
		}
		groups = append(groups, catalog.TagGroup{
			CategoryID: strings.TrimSpace(g.CategoryID),
			Category:   g.Category,
			Tags:       tags,
		})
	}
	return catalog.TagVocabulary{Groups: groups}, nil
}
