# 10 — `since` 的语义定稿

**What to build:** `?since=<日期>` 的**比较语义** —— 一个能写进 `## Answer` 的决定，不是一段代码（落地见 [11](11-since-landing.md)）。

**Type:** grilling（交付是决定）

**Blocked by:** None — can start immediately.

**Status:** ready-for-human

## 这张票为什么存在（一段错位史）

初次交付地图的票 09（[`../../javdb-rss/issues/09-cost-cache-throttle.md`](../../javdb-rss/issues/09-cost-cache-throttle.md)）在票面里写下了自己的欠账：

> 本票仍需回答的是：**这个临时语义对不对**，以及上面 Q2/Q3/Q4……

临时语义由票 03 抢先落地：比 `release_date`、字典序 `>=`、**解析不了的一律保留**、每次启用打一条 WARN。但票 09 的 `## Answer` 只回答了缓存 / 并发 / 限流 / 翻页 —— **「这个临时语义对不对」从未被回答，票却标了 `resolved`**。于是今天：

- 三处 live code 仍写着「尚未定稿」（`internal/catalog/model.go` 与 `internal/config/config.go` 的 `TODO(ticket-09)`、`internal/httpapi` 的 WARN 与注释）；
- 记录层却说它已解决（`../../javdb-rss/map.md` 的 `## Not yet specified` 清理记录）—— 那处订正见 [12](12-record-integrity.md)。

本票是这条**孤儿欠账**的接收方。

## 要定什么

1. **比较字段**：作品的 `release_date`，还是被选中那条磁链的 `created_at`（也就是 `pubDate` 的来源）？
2. **区间**：`>=`（闭）还是 `>`（开）？边界那天的作品留不留？
3. **坏数据**：`release_date` 为空、或 `created_at` 两种格式都解析不了时，留还是丢？
4. **定稿后那条 WARN 何去何从**：删掉，还是换成只对「坏数据」报警？
5. **范围与不对称**：本决定对两条路由都生效（`/rss/actress/{id}.xml` 与 `/rss/list/{id}.xml` 都调 `filterSince`）。但两条路由的 `year`+`since` 处理**目前不对称** —— actress 判 400 并说明原因，list 只把 `year` 透传给上游。顺带定要不要对齐。

## 已经知道的事实（不要重新试探）

- **作品层只有 `release_date`**：三个列表端点共用的 `movieSlim` 就是
  `id/number/title/release_date/has_cnsub/magnets_count`。电影级**没有**「上架时间」这种字段。
- 所以「上架时间」的真实候选是**被选中那条磁链的 `created_at`** —— 也正是 feed 里 `pubDate`
  的来源。而 `catalog.ParseCreatedAt` / `CompareCreatedAt` 已经吃下线上两种格式
  （`01/02/2006`、`2006-01-02`，见 `internal/catalog/createdat.go`，followups 09 建的）。
  **因此「要解析时间」不构成否掉这个选项的理由 —— 落地成本已经很低。**
- 于是问题不是「哪个字段存在」，而是**「新」对用户意味着什么**：作品发行得晚，还是上游有人
  新发了磁链。**合集再版**是两者的分歧点 —— 旧 `release_date`、新 `created_at`；
  按前者它今天被静默丢掉。
- 已记录的副作用（`../../javdb-rss/notes/actress-params.md`）：`pubDate` 取 `created_at`，
  而过滤比的是 `release_date` —— **两个日期不同源**，所以过滤后仍会出现 `pubDate` 早于
  `since` 的条目。选 `created_at` 会让这两个日期自洽。
- 默认只取第一页（最新 50 部，**按 `release_date` 倒序**）。若改比 `created_at`，
  「新作品必然在顶部」这个前提要重新检查一次。

## 验收

- [ ] 上面 1–5 各有答案，写进 `## Answer`
- [ ] 每个答案写明**否定选项为什么被否**（本项目要的是留理由，不是留结论）
- [ ] 「缺日期一律保留」这条临时取舍说清是保留还是推翻，并归类它的失败方式（会不会静默丢数据）
- [ ] 列出「定稿后哪些文本、测试、行为必须跟着改」，作为 11 的输入
- [ ] 若结论是换字段：明说同一 `since` 的返回集合会变，以及是否需要用户动作
