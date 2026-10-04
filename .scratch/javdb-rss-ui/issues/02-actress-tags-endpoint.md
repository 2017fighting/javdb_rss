# 02: 女优标签端点 `/actress_tags/{id}`

**Spec:** [`../spec.md`](../spec.md)

**What to build:** `GET /actress_tags/{id}`：一次上游读取同时交出显示名、**她支持的主属性**（上游顶层 `filter_tags`，也就是 feed URL 里 `main=` 的取值集合）与她**自己的 `tags[]`**。上游不给分组，分组由页面按词表反查。

现有的「取女优名字」（feed 标题用）打的正是同一个上游端点：合并成一次读取、一个实现三个字段，免得两份 envelope 各漂各的。匿名可读，不要 token。

**Blocked by:** None (can start immediately)

**Status:** resolved

- [x] 三个字段齐：`name`、`main`（`[{id,name}]`，如 EvkJ 的 `p`/`s`/`m`/`c`）、`tags`（`[{id,name,videos_count}]`）
- [x] 字段**不叫**上游那个 `filter_tags` —— 它装的是主属性，不是标签，叫错名字下一个人会照着它去筛标签
- [x] 没配 token 时仍然 200（匿名可读）
- [x] 上游失败 → 502；女优白名单没放行 → 404「未知的订阅」
- [x] feed 标题那条路径行为不变：取不到名字仍退回 id，绝不因为取名失败把一条本来能用的 feed 弄挂（现有测试继续过）
- [x] handler 层测试（先例：`/collected`、`/collected_lists` 的测试）

## Comments

已实现（`GET /actress_tags/{id}`，见 `internal/httpapi/server.go`、`internal/appapi/actress_tags.go`、
`internal/stub/actress_tags.go`；`catalog.Source` 增 `ActressTags`，pin/dedupe 两个装饰器与
`appapiSource` 各加一次透传/合并）。

几处值得记下的取舍：

- **一次读取，一个实现三个字段**：`ActressName`（feed 标题）与 `ActressTags` 共用
  `appapi.fetchActress` 的**唯一**上游读取点（`/api/v1/actors/{id}`）。名字与标签因此不会
  各自跟着上游漂。
- **名字纠正落在对外形状上**：上游顶层 `filter_tags`（装的是一组主属性）在本服务一律叫 `main`；
  响应用 `{id,name,videos_count,main,tags}`，并且测试里有一条断言「响应里没有 `filter_tags` 键」。
- **main 与 tags 是两种形状**：`main` 的项只有 `{id,name}`（上游没给作品数），`tags` 的项逐项带
  `videos_count`。分开成两个结构而不是合一个，免得 main 里凭空多出一个上游没给过的 `videos_count: 0`。
- **`id` 用 URL 里那个**：白名单与页面都认它，不依赖上游回显 `actor.id`。
- **stub fixture 故意保留三处 id 撞号**（12/7/3，名字分别是成人電影/處女/眼鏡，对应 zone 0 词表里的
  真标签）—— 那是票 04「按名字消歧」唯一能离线验到的入口。
- `videos_count` 是**女优自己**的作品数（上游 `actor.videos_count`），保留它；而 `main` 的项不带它，
  因为上游的 `filter_tags` 没有这个字段。
- 代码评审（standards+spec 两轴）提出并已修：`fetchActor` → `fetchActress`（CONTEXT.md 词汇表
  禁用 `actor`）；空 id 的错误包上 `catalog.ErrBadRequest`；stub 里两处重复的名字解析抽成
  `actressName`。
