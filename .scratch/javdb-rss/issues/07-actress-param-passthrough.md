# 女优订阅：演员页参数透传

Type: task
Status: open

> **阻塞边已移除（2026-09-30）**：`06` 已 resolved，且本票的**代码部分已随 06 落地**
> （`buildEntityFilter` 的复合掩码、三个自有参数 page/limit/pages 的覆盖、
> 透传参数的合并规则，均已实现并有测试）。
>
> 因此本票剩下的**不是**代码，而是它的另两项产出：
>   1. `notes/actress-params.md` —— 参数全表（目前散在 `api-recon.md` §10.2）
>   2. 用户确认过的 URL 形态示例，写进 README
> 见票面底部「剩余工作」。

> ## ✅ 核心未知已解（2026-09-28，来自 ticket 01）
>
> **「App 里的参数」就是这一组**，打在 `GET /api/v1/movies/tags` 上：
>
> | 参数 | 含义 | 默认 |
> |---|---|---|
> | `filter_by` | 掩码，形如 `a{p,m,c,s}` = 该女优 + 可播放/可下载/带字幕/单体作品 | — |
> | `filter_by_tags` | 标签 id 的 CSV | 空 |
> | `sort_by` | 排序字段 | `release` |
> | `order_by` | `desc` / `asc` | `desc` |
> | `page` | 页码 | `1` |
> | `limit` | 每页条数 | `20` |
>
> mask 字母表：实体前缀 `a`=actor `s`=series `m`=maker `d`=director `c`=code `l`=list；
> main 属性 `p`=Playable `m`=Downloadable `c`=Subtitles `s`=Individual `i` `v`。
> 女优区域掩码：`censored:0 uncensored:1 western:2 fc2:3`。
> （来源：`internal/javdb/appapi/endpoint/entity/entity.go` + `model/types.go` + `/api/v1/actors/{id}` 的 `filter_tags`）
>
> **因此本 ticket 从「枚举参数」降级为「定透传形态 + 收集用户确认」。**

## Question

用户要求「参数控制按照 App 里的参数来」—— 把 App 演员页的查询参数**原样透传**。
那么具体透传哪些、怎么暴露在 RSS URL 上？

要回答：

1. **参数全表**：App 演员页实际会发的 query 参数有哪些？（ticket 06 的产出）
   逐个确认：名字、取值域、默认值、是否可分页、是否 VIP 门槛。
2. **RSS URL 的形态**：用户在 qBittorrent 里手填的 URL 长什么样？
   ```
   /rss/actress/EvkJ.xml?sort=release_date&type=all&since=2025-01-01
   ```
   哪些参数**原样透传**，哪些是**本服务自己消费**的（`since`）？
   两者混在同一个 query 里，怎么避免撞名？（例如加 `jd_` 前缀，或把透传参数整体 URL-encode 进一个 `q=`）
3. **`since` 的语义**：用哪个字段比？`release_date` 还是入库时间？边界是 `>` 还是 `>=`？
   时区怎么办？——「我已经有这个人的所有作品了，只需要追新」这句话的正确实现依赖这个。
4. **默认值**：不传参数时，排序/类型取什么？必须与 App 打开演员页时的默认一致，
   否则用户会看到和 App 不一样的顺序。
5. **稳定性**：参数名是 App 私有契约，App 更新可能改。透传方案本身抗变（我们不改写），
   但要**校验**：收到不认识的参数时是转发还是报错？
6. **女优 id 怎么拿**：RSS URL 里的 `{id}` 是 App 里的数字 id、还是 javdb 的 `/actors/EvkJ` 短码？
   要给用户一个「怎么从 App 里找到这个 id」的办法，以及
   `/api/v1/users/collected_actors` 能不能直接产出可用的 id 列表。

## 产出

- `notes/actress-params.md`：参数全表 + 透传规则 + 默认值
- 用户确认过的 URL 形态示例（写进 README）
- 落到骨架（ticket 03）里的 query 处理接缝

## 注意

这条是 **HITL 收尾**：参数表要拿给用户过目确认「这就是我要的」。

## 剩余工作（2026-09-30 核实）

代码部分已随 ticket 06 完成，本票只剩两件小事加一次确认：

1. **参数全表独立成 `notes/actress-params.md`。**
   目前参数表散在 `notes/api-recon.md` §10.2，内容是对的但不完整 ——
   缺 `pages`（ticket 09 新增的自有参数）与三个自有参数被覆盖的说明。
2. **把 URL 形态写进 README 并请用户过目。**
   README 现在有示例，但没有明确列出「哪些参数是本服务自有的、
   哪些是透传给 App 的」—— 这是用户最容易搞混的一处。
3. **用户确认**：确认 URL 长这样就是他想要的（本票是 HITL，
   代码做完不等于需求确认）。
