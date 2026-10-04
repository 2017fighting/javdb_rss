# 01: 标签词表端点 `/tags?type=`

**Spec:** [`../spec.md`](../spec.md)

**What to build:** 服务多一个只读发现端点 `/tags?type={片库号}`：把上游按片库给出的**标签分组词表**交出来 —— 分组名、分组顺序、组内标签与顺序一律原样。它是页面「按上游分组挑标签」的唯一数据来源，也让「这个片库有哪些标签」不再依赖一份冻结快照。匿名可读，不要 token。

非法片库号必须判 400：上游对非法值是**静默回落**（`type=9` 与 `type=0` 响应逐字节相同），照原样透传等于把另一个片库的词表当成你要的答案给出去。

**Blocked by:** None (can start immediately)

**Status:** resolved

- [x] `GET /tags?type=0` 返回分组词表，分组顺序、分组名、组内标签顺序与上游一致（`category_id` 与 `category` 都保留）
- [x] `type` 只接受 0–3；缺省、非数字、越界一律 400，并在文案里写出有效取值
- [x] 它是发现端点家族的一员：在 `/rss/` 之外、不带 `.xml`、返回 JSON
- [x] 没配 token 时仍然 200（匿名可读）
- [x] 上游失败 → 502；片库白名单没放行 → 404「未知的订阅」（与 `/rss/tags/{片库号}.xml` 同一套语义）
- [x] `stub` provider 提供结构性 fixture（0/1/2/3 各一套、组数不同、标签列表可缩写），离线与浏览器套件都能跑
- [x] handler 层测试（先例：`/collected`、`/collected_lists`、`/rss/tags/{zone}.xml` 的测试）

## Comments

已实现（`GET /tags`，见 `internal/httpapi/server.go`、`internal/appapi/tags.go`、
`internal/stub/tags.go`）。一处与 spec 的**有意偏离**：spec 写的响应形状里带
`videos_count`，但实测 `/api/v2/tags` 的标签**只有 `id`/`name`**（见
[`../evidence/tags_type0.json`](../evidence/tags_type0.json)、`../notes/tag-vocabulary.md` §1），
所以那个字段带 `omitempty`，上游没给就不出现 —— 编一个 `videos_count: 0` 出来
等于宣布「这个标签下一部片都没有」。上游哪天开始给，它会自动透出。
