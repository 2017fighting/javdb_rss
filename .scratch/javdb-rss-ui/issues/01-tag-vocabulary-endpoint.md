# 01: 标签词表端点 `/tags?type=`

**Spec:** [`../spec.md`](../spec.md)

**What to build:** 服务多一个只读发现端点 `/tags?type={片库号}`：把上游按片库给出的**标签分组词表**交出来 —— 分组名、分组顺序、组内标签与顺序一律原样。它是页面「按上游分组挑标签」的唯一数据来源，也让「这个片库有哪些标签」不再依赖一份冻结快照。匿名可读，不要 token。

非法片库号必须判 400：上游对非法值是**静默回落**（`type=9` 与 `type=0` 响应逐字节相同），照原样透传等于把另一个片库的词表当成你要的答案给出去。

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `GET /tags?type=0` 返回分组词表，分组顺序、分组名、组内标签顺序与上游一致（`category_id` 与 `category` 都保留）
- [ ] `type` 只接受 0–3；缺省、非数字、越界一律 400，并在文案里写出有效取值
- [ ] 它是发现端点家族的一员：在 `/rss/` 之外、不带 `.xml`、返回 JSON
- [ ] 没配 token 时仍然 200（匿名可读）
- [ ] 上游失败 → 502；片库白名单没放行 → 404「未知的订阅」（与 `/rss/tags/{片库号}.xml` 同一套语义）
- [ ] `stub` provider 提供结构性 fixture（0/1/2/3 各一套、组数不同、标签列表可缩写），离线与浏览器套件都能跑
- [ ] handler 层测试（先例：`/collected`、`/collected_lists`、`/rss/tags/{zone}.xml` 的测试）
