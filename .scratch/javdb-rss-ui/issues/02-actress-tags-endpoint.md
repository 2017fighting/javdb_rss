# 02: 女优标签端点 `/actress_tags/{id}`

**Spec:** [`../spec.md`](../spec.md)

**What to build:** `GET /actress_tags/{id}`：一次上游读取同时交出显示名、**她支持的主属性**（上游顶层 `filter_tags`，也就是 feed URL 里 `main=` 的取值集合）与她**自己的 `tags[]`**。上游不给分组，分组由页面按词表反查。

现有的「取女优名字」（feed 标题用）打的正是同一个上游端点：合并成一次读取、一个实现三个字段，免得两份 envelope 各漂各的。匿名可读，不要 token。

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] 三个字段齐：`name`、`main`（`[{id,name}]`，如 EvkJ 的 `p`/`s`/`m`/`c`）、`tags`（`[{id,name,videos_count}]`）
- [ ] 字段**不叫**上游那个 `filter_tags` —— 它装的是主属性，不是标签，叫错名字下一个人会照着它去筛标签
- [ ] 没配 token 时仍然 200（匿名可读）
- [ ] 上游失败 → 502；女优白名单没放行 → 404「未知的订阅」
- [ ] feed 标题那条路径行为不变：取不到名字仍退回 id，绝不因为取名失败把一条本来能用的 feed 弄挂（现有测试继续过）
- [ ] handler 层测试（先例：`/collected`、`/collected_lists` 的测试）
