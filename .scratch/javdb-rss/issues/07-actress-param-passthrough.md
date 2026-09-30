# 女优订阅：演员页参数透传

Type: task
Status: resolved

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


## Answer（2026-09-30）

**完成。** 票面列的「剩余工作」是文档 + 确认，但核实过程中发现了两处**真的代码问题**，
都已修掉。

### 派生产出

- `notes/actress-params.md` —— 完整参数表，**每条都对着真实上游实测**，
  并诚实标注了未验证的部分
- README 新增「女优订阅的参数：两类，别搞混」一节，含三个**会静默出错**的坑
- `config.example.yaml` 里我写错的示例已修

### 票面问题的实测答案

| 问题 | 答案 |
|---|---|
| 1 参数全表 | 见 `notes/actress-params.md` |
| 2 URL 形态 | 自有：`since` `pages` `page` `limit`；透传：`filter_by` `filter_by_tags` `sort_by` `order_by`。**不做前缀**（`jd_`）、不做整体编码 —— 自有参数只有四个且都是普通词，用一个 denylist 摘除即可 |
| 3 `since` 语义 | `release_date`，字典序 `>=`，解析不了则保留（临时语义，归 ticket 09） |
| 4 默认值 | **实测确认**服务端默认就是 `sort_by=release` + `order_by=desc`（不传与显式传返回完全相同的顺序） |
| 5 不认识的参数 | **转发**（实测上游静默忽略未知参数）。但见下：这个「静默忽略」很危险 |
| 6 女优 id | 短码（`EvkJ`），不是数字。经 `GET /collected`（现成 feed 路径）或 App 分享链接获得 |

### ⭐ 核实中发现的两个真问题

**① `filter_by` 的主属性必须逗号分隔，拼接会被静默忽略。**

```
0:a:EvkJ:c,m::    ✅ 实测只返回带中文字幕的作品（50/50）
0:a:EvkJ:cm::     ❌ 静默忽略，返回全部作品
```

而**我自己在上一轮代码审查的修复里把它写错了** —— `config.example.yaml` 里写的是
`"0:a:EvkJ:apmc::"`（拼接形式）。概念改对了（复合掩码），语法写错了，
而且失败方式依然是「静默给错结果」，与上一轮抓到的是同一类。

**② 我 spawn 的取名 goroutine 逃出了 net/http 的 panic 恢复。**

`net/http` 只为 handler 所在的 goroutine 恢复 panic。取名跑在新起的 goroutine 里，
它一旦 panic 杀掉的是**整个进程**而不只是那一个请求。这个是我写测试时真触发的
（测试替身嵌入 nil 指针），不是理论风险。已加 recover 并写测试钉住。

### 新加的校验

因为上游对非法掩码**静默忽略**，用户拼错会拿到「看起来正常但没应用筛选」的 feed。
因此本服务拦下**可证明是笔误**的那一种：主属性长度 > 1 且不含逗号。

- 这个条件不可能误伤合法配置（单个主属性就是一个字母，多个必须逗号）
- 刻意**不**校验字母本身是否在已知集合里 —— 那会在私有契约新增字母时
  判死一个本来能用的配置
- 返回 **400**（不是 502）：重试无用，是用户要改

实测：`filter_by=0:a:EvkJ:apmc::` → 400 并给出可直接照抄的正确写法。

### 未验证

- `filter_by_tags` 是否真的改变结果集 —— 用常见标签试时与全集相同，未确认
- `i` / `v` 两个主属性字母的含义
- `sort_by` 的完整合法取值集（只确认 `release` 与 `score` 有区别；
  无法区分「非法值」与「合法但恰好同序」）


### HITL 确认（2026-09-30）

票面要求的「用户确认」已完成，两项：

1. **URL 形态确认可以。** 展示给用户的是：

   ```
   /rss/actress/{id}.xml                          最常见
   /rss/actress/{id}.xml?since=2026-01-01         追新
   /rss/actress/{id}.xml?pages=3                  建库
   /rss/actress/{id}.xml?filter_by=0:a:{id}:c::   高级：只看中文字幕
   /rss/actress/{id}.xml?sort_by=score            高级：按评分
   ```

   用户明确**不需要**给常用筛选加友好短参数（如 `?subs=1`）——
   接受了「筛选属高级用法、需手写复合掩码」这个取舍。

2. **`filter_by` 拼接笔误保持硬报错（400）。** 用户认可理由：
   上游静默忽略拼错的掩码，不拦就会拿到「看起来正常但没应用筛选」的 feed。

至此本票全部产出齐备：参数表、README、透传接缝、用户确认。
