# 需求 4 的呈现形态：读到的 App 收藏女优怎么变成 feed

Type: grilling
Status: resolved

## Question

需求原文第 4 条：「可以设置自己的登录态，读取我在 app 里订阅的女优」。

**这条在地图的 Destination 里没有落脚点。** 两个路由都是「给一个 id 或番号」的形态：

```
/rss/code/{番号}.xml
/rss/actress/{id}.xml?<透传参数>
```

而「读取我在 App 里收藏的女优」产出的是**一份列表**，不是一条 feed。这份列表要变成什么？

（本票是 ticket 03 实现骨架时暴露出来的 —— 当时发现 `Source` 端口里没有位置放它，
才意识到地图的终点描述漏了这一环。）

## 候选方案

需要用户拍板。可选的不止一个，也可能要组合：

1. **聚合 feed**：新增 `/rss/collected.xml`，把收藏列表里所有女优的新作混在一条 feed 里。
   优点：一条 URL 搞定，qBittorrent 里只加一次；
   缺点：分不清哪条片子是哪个女优的（靠 `category` 弥补），且这是唯一一个
   「一条 feed 对应 N 个订阅」的路由，与已定的「每个订阅一个 feed」形态不一致。
2. **自动填充白名单**：启动时/定时拉收藏列表，把它当成 `feeds.actresses` 的内容。
   仍然一个女优一条 feed，只是 id 列表自动同步而不是手写。
   优点：完全符合已定形态；缺点：用户想排除某个女优时要额外设计「黑名单」。
3. **只做发现**：提供一个 `/collected` 端点，把收藏列表（id + 名字）列出来，
   用户自己拷贝 id 填进配置或 URL。
   优点：最简单、最不神奇；缺点：收藏变了要手动同步。
4. **列表本身作为一条 feed**：`/rss/collected.xml` 里每条 item 是**一个女优**而不是一部作品
   （链接指向该女优的 feed）。这满足字面上的「读取订阅的女优」，但 qBittorrent 拿到它没有用。

## 需要一并想清楚的

- **收藏列表分页**：`/api/v1/users/collected_actors` 的形态尚未知（ticket 06 待测）。
  如果收藏了几十个女优，一次请求拿不拿得全？
- **什么时候拉**：每次请求现算？还是启动时/定时缓存？
  考虑到它需要 token，而 token 可能过期，「过期时怎么办」要有答案。
- **女优名字**：`/rss/actress/{id}.xml` 的标题现在是 `JavDB · EvkJ`（id）。
  有了收藏列表就能拿到名字，要不要拿名字做 feed 标题？这是个顺带的小改。
- **token 缺失时的行为**：功能降级（只有 demand 1/2/3 可用）还是明确报错？
  现在骨架里是可选的 —— 有没有 token 都不影响启动。

## 软依赖

决定可以现在就做（它是价值取舍，不是事实）。但**实现**需要
ticket 05（手工导出 token）先通，否则没有东西可测。

## 产出

- 写进 map 的 Decisions-so-far
- 路由形态定下来后回写 README 与 config.example.yaml


## Answer

**已定（2026-09-30 与用户 grill 得出）并已实现。**

### 决定

1. **呈现形态 = 只做发现**：新增 `GET /collected`，返回收藏女优的 JSON 清单。
   **不做**聚合 feed，**不做**自动填充白名单。
2. **没有 token 时返回 503 + 明确文案**，不是 200 + 空列表，也不是 404。
3. **现算、翻页到底、不缓存。**
4. **女优 feed 标题用真名字**，拿不到退回 id。

### 为什么否掉了聚合 feed（这是本票最有价值的一条推理）

按票里的方案 1，`/rss/collected.xml` 要为每个收藏女优各拉一次列表 + 每部作品的磁链。
用 ticket 06/09 量出的真实数字算：

```
每个女优 ≈ 1 次列表 + 17 次磁链 = 18 次请求
20 个收藏女优 ≈ 360 次请求 ≈ 19 秒（并发 8，每次 ~430ms）
qBittorrent 每 15 分钟轮询一次就重来一遍
```

而 ticket 09 已定「不做缓存、不做后台刷新」。**方案 1 与已定的成本策略直接冲突** ——
要么接受这个成本，要么推翻票 09。用户选择了不引入这个矛盾。

`/collected` 的成本是「每次访问 1 次请求/页」，而它是**给人看的、低频的**，
qBittorrent 不会碰它 —— 缓存毫无意义。

### 实现

- `catalog.Source` 端口新增两个方法：
  - `CollectedActresses(ctx) ([]Actress, error)` —— 需要 token
  - `ActressName(ctx, id) (string, error)` —— 匿名可用
- 新增 `catalog.Actress` 类型与 `catalog.ErrNoToken` 哨兵错误
- `internal/appapi/collected.go`：实现两者
- `internal/httpapi`：`GET /collected` 路由 + `handleCollected` + `writeCollectedError`
- 女优 feed 标题经 `actressTitle` 取名字，**失败只记 debug 级且不影响 feed**

### 两处实现中发现的事实（与票面假设不同）

**① `name_zht` 恒为空，不要读它。** 实测（2026-09-30）：

```
accept-language: en     -> name = "Kawakita Saika"
accept-language: zh-CN  -> name = "河北彩花"
```

随 lang 变化的是 `name` 字段本身。先例项目 javdb-cli 的测试夹具给 `name_zht`
填了值，**照抄会写出一个永远落到 fallback 的实现**。

**② `lang` 默认从 `en` 改为 `zh-CN`。** 既然它决定名字语言，而本服务的用户与内容
都是中文的，默认 `en` 会让标题显示罗马音。这是一个**行为变更**，已写进 README。

### 验收

**已验证**（离线 + 真实上游）：

| 项 | 结果 |
|---|---|
| `/collected` 无 token | ✅ 503 + 可操作文案 |
| 女优 feed 标题 | ✅ `JavDB · 河北彩花`（真实上游，此前是 `JavDB · EvkJ`） |
| 番号 feed 不受影响 | ✅ `JavDB · KV-328` |
| `/collected` 给出的 feed 路径真的可访问 | ✅ 一致性测试 |
| `name_zht` 不被读取 | ✅ 测试里给它填了值，断言实现仍用 `name` |
| 跨页去重 / 翻页上限 / token 过期与缺失分开 | ✅ 各有测试 |

**未验证（诚实标注）**：`/collected` 的**成功路径从未对着真实 API 跑过** ——
它需要 token，而 ticket 05 未做。契约来自先例项目的实现与夹具
（`{actors: [...]}` + page 翻页），形状可信但未在 1.9.35 上复验。
一旦 ticket 05 通了，第一件事就是跑一次 `curl /collected` 确认。
