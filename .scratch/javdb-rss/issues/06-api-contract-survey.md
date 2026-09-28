# 数据接口契约测绘

Type: task
Status: open
Blocked by: 02

> ## ⚠️ 大部分已被 ticket 01 顺带解答（2026-09-28）
>
> 实测拿到的真实响应已经把本 ticket 的几个最贵的未知解掉了。重新评估前先读本档底部
> 的「已确认契约」一节。**本 ticket 剩余的范围已经很小。**

## Question

把 feed 要用的那几个端点的**真实响应**摸清楚，落成一份字段清单。

签名一通就立刻做，用真实 token 打真接口。

要测绘的端点：

| 端点 | 要确认的事 |
|---|---|
| `/api/v2/search?q=<番号>` | 番号怎么变成 movie id；是否返回多部；分页参数；消歧需要的字段 |
| `/api/v1/search_magnet` | 与 `/api/v2/search` 的分工；是否能直接搜到磁链 |
| `/api/v1/movies/{id}/magnets` | **磁链数组的字段全集**；是否有 `subtitle` / `has_subtitle` / 语言 之类的显式标记；**数组顺序**由什么决定（做种数？体积？时间？）；`dn`（显示名）里是否带「字幕」字样 |
| `/api/v1/actors/{id}` | **演员页接受的全部 query 参数**（排序、类型、分页、筛选）；返回的作品数组字段；`release_date` 字段的形态与精度 |
| `/api/v1/users/collected_actors` | 用户在 App 里「收藏的女优」长什么样；分页；是否包含女优 id 与名字；**总数有多少** |
| `/api/v1/movies/latest` | 是否能作为「追新」的另一个入口（按日期扫而不是按番号搜） |

**每个端点都要记录**：
- 完整 URL（含 query）
- 一次真实响应的 JSON 骨架（字段名 + 类型 + 示例值，敏感值脱敏）
- 分页模型（page/per_page/offset？总数在哪）
- 错误信封（正常错误 vs 鉴权失败的差别）
- 是否需要登录（用匿名请求对照一次）

## 特别关注

- **中文字幕到底怎么判**。两条路都要查清，因为 ticket 08 要在它们之间做取舍：
  1. App API 是否直接给字幕标记
  2. 若没有，磁链显示名 `dn` 的格式是什么，能不能稳定解析出「中文字幕」
     （参考 `/root/clone/JAVDB_AutoSpider` 的分类优先级：`UC无码破解 > UC > U无码破解 > U`，
     以及 `字幕 / hacked / no_subtitle` 三类）
- **`/api/v1/movies/{id}/magnets` 是不是一次就返回全部磁链**（还是要翻页）。
  这直接决定 ticket 09 的成本模型。

## 产出

`notes/api-contract.md`：上面每个端点一节，字段清单 + 脱敏样本 + 未知项标注。
这份文件同时是 ticket 07（女优参数）和 ticket 08（字幕语义）的输入。

## 已确认契约（ticket 01 实测，2026-09-28）

以下已有一手证据，**不需要重测**，只需补充尚未覆盖的部分：

### `GET /api/v1/movies/{id}/magnets` —— ⭐ 最关键的一张表

```json
{"magnets":[{"name":"KV-328", "hash":"0e8f4789bdcab713effc3a07d1309a776c867b3e",
  "size":3110, "cnsub":false, "hd":true, "files_count":2,
  "created_at":"09/27/2026", "pikpak_url":"https://keepshare.org/..."}]}
```

- **`cnsub`：逐条磁链的中文字幕布尔值** —— 需求 2 的判定依据，**不需要解析文件名**。
- `hash`：infohash → 直接可做 `guid`/磁链构造。
- `size`（MB）、`hd`、`files_count`、`created_at` 都在。
- **一次调用返回全部磁链**（本样本 1 条），未见分页。

### `GET /api/v1/movies/latest`

每条作品含：`id`、`number`、`title`、`origin_title`、`thumb_url`、`cover_url`、`duration`、
`magnets_count`、`can_play`、`play_subtitle`、`has_preview_video`、**`has_cnsub`**、
`has_preview_images`、`release_date`、`new_magnets`、`first_magnets`。

→ **`has_cnsub`（电影级）+ `release_date`** 让「追新」与「中文字幕」可以在一页里判完。

### `GET /api/v1/actors/{id}`

返回 `share_info`、`has_collected`、`actor{id,type,avatar_url,name,name_zht,height,bust,cup,waist,hips,
 twitter_id,videos_count,...}`、
**`filter_tags`**（如 `[{p:Playable},{s:Individual works},{m:Downloadable},{c:Subtitles}]`）、`tags[]`。
—— `filter_tags` 就是 App 演员页可选项的来源。已知样本 `videos_count: 229`。

### 通用

- 错误信封：`{success, action, message, data}`；`success` 可能是 `0/1` 或 `false/true`。
- 鉴权失败的错误 `action`：`JWTVerificationError`、`Unauthorized`、`LoginRequired`、
  `TokenInvalid`、`TokenExpired`（来自 javdb-cli 的分类）。
- 必带公共参数：`app_channel`、`app_version`、`app_version_number`、`platform`、`system_version`、
  `device_model`、`device_name`、`device_uuid`。

### 仍未确认（本 ticket 真正剩余的工作）

1. `/api/v1/users/collected_actors` 的真实响应结构（**需要 token**）——分页？字段？总数？
2. 磁链数组的**排序依据**（是稳定的吗？改动吗？）—— 用户选了「信任 App 顺序」，
   但仍需确认它是稳定排序而非每次随机。
3. `/api/v1/movies/tags` 的分页模型（总数在哪、`limit` 上限是多少）。
4. `/api/v2/search?q=<番号>` 的行为：是否返回多部、如何消歧。
5. `accept-language` 对返回内容（如 `name_zht`）的实际影响。

