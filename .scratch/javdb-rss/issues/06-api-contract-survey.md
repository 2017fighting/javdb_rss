# 数据接口契约测绘

Type: task
Status: resolved

> **阻塞边已移除（2026-09-28）**：原 `Blocked by: 02` 是在等「签名能跑」。
> 签名已实测跑通（见 ticket 01），探接口不再依赖 02。
>
> **唯一的软依赖**：下面「仍未确认」的第 1 项（`/users/collected_actors` 响应结构）
> 需要真实 token → 实际依赖 ticket 05。其余四项不依赖任何东西，可以现在就做。

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

## Answer

**完成，且真实数据源已接入、端到端实测可用。**

完整的实测契约记录在 [`notes/api-recon.md`](notes/api-recon.md) §10（新增的「实测契约附录」），
下面是结论与对地图的影响。

### ⭐ 最大的发现：`/api/v2/search` 是模糊搜索，不是精确查询

```
q=KV-328  → 8 条，number = KV-328 KV-323 KV-322 KV-326 KV-318 KV-324 KV-329 KV-327
```

按位置取 `movies[0]` 在多数时候**看起来是对的**（目标常在第一位），
但在番号尾部字符不同时会**静默命中错误作品** —— 不报错，只是发错片。

因此 `resolveExact` 显式按 `number` 精确比对，找不到**报错**，绝不退回近似结果。
这是本 service 最不该犯的一类错误，已用 6 组测试钉死（含「真目标排在中间」的陷阱用例）。

### ⭐ 第二发现：`filter_by` 是复合掩码，格式猜错会静默返回全站作品

早期误以为是 `a` / `apmc` 这种简单字母组合。实测那样请求拿到的是 `CD-26008`
—— **全站最新作品**，不是该女优的，**且不报错**。

真实格式：`{zone}:{letter}:{id}[:{main}:]:`，如 `0:a:EvkJ`。

### 关键数字（ticket 09 的输入）

- **`limit` 上限是 50**（传 100/200/500 都只给 50）
- `page` 分页可用、无重叠，按 `release_date` 倒序
- **端到端耗时**：番号 feed ~1.2s；女优 feed（第一页 50 部）**~6.1s**
- 一个女优 feed = 1 + N 次请求（N 为有磁链的作品数）

→ 结论：qBittorrent 每轮询一次就是 6 秒的上游压力，并发多 feed 会叠加。
**缓存或后台刷新很可能有必要** —— ticket 09 现在有真实数字可算了。

### feed 必须走 `/movies/{id}/magnets`

对比实测：

| 端点 | `cnsub` | `hd` | `created_at` 格式 |
|---|---|---|---|
| `/movies/{id}/magnets` | ✅ | ✅ | `09/27/2026` |
| `/search_magnet` | ❌ | ❌ | `2026-09-27T23:00:19.000Z`（ISO） |

**只有前者给磁链级 `cnsub`**，因此 feed 只能用它。
这也解释了 `feed.parseCreatedAt` 为何要接受两种格式。

### 交付

- `internal/appapi/source.go`：实现 `catalog.Source`（`Code` / `Actress`），
  并把线格式映射到领域模型（线格式改名只需改一处）
- `provider: appapi` **不再是错误**，现在是默认值；`stub` 保留为离线调试通道
- 数据源在**每次请求时重建客户端**，因此 host / token / lang / device_uuid 都能热重载
- 19 条新测试，全部离线

### 端到端实测（真实 API）

| 需求 | 结果 |
|---|---|
| 1 番号订阅 | ✅ `/rss/code/KV-328.xml` → 1 条，带正确 guid/enclosure |
| 2 中文字幕优先 | ✅ `SNOS-320`、`SNOS-275` 正确排为「中文字幕 ·」版本 |
| 3 女优 + `since` | ✅ `/rss/actress/EvkJ.xml` → 17 条；`?since=2026-06-01` → 过滤到 5 条 |
| 4 读 App 收藏 | ❌ 仍需 token（ticket 05）+ 呈现形态待定（ticket 10） |

`gofmt` / `vet` / `test` / `build` 全净。

### 一处工程失误（已防）

验证时用 `curl -o` 把测试文件写进了**仓库根目录**。已清理，并把 `*.xml` / `*.log`
加进 `.gitignore` 防止再犯 —— 本项目自己不产出 xml，这个规则是纯保护性的。

### 对下游票的影响

- **ticket 07（女优参数透传）**：参数表已完全确认（§10.2），本票已按透传实现。
  唯一的行为约定：`page`/`limit` 被**覆盖**（本服务自己控分页），已写进文档与测试。
  东票可以直接关闭或降为「拿用户确认 URL 形态」。
- **ticket 09（成本模型）**：拿到了第一批真实数字（§10.6）。
- **ticket 10（需求 4）**：`/api/v1/users/collected_actors` 仍差 token，
  但**延迟到实现时再测**，不阻塞决策（ticket 10 是价值取舍）。

### 本次未做

- 未翻页（只取第一页 ≤50 部），已标注 TODO 指回 ticket 09
- `since` 仍用 `release_date`（临时语义，归 ticket 09）
- `size` 单位仍未从文档层面核实，但 end-to-end 实测与 App 显示一致（按 MB 假定）

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

### 补跑的 code-review（2026-09-30）

Spec 轴与 Standards 轴各找出真问题，全部已修：

1. ⭐⭐ **`validateMask` 漏掉了票面明确记录的那个陷阱。**
   票里写的是「写成 `a` 或 `apmc` 会静默返回全站作品」，而我的校验只看主属性段（第 4 段）——
   `splitMask("apmc")` 把它切成一段、`mainSeg` 为空，于是**立即 return nil 放行**。
   也就是说**我上一轮声称修好的那个陷阱，恰恰没被修**（实测确认：
   `filter_by=apmc` 当时返回 `err=<nil>`）。
   根因是我只校验了「形状细节」而没校验「骨架是否存在」—— 正交的两维只想了一维。
   已改为五项校验：骨架存在 / zone 是数字 / 实体字母是单个且为 `a` /
   **id 与 URL 里的女优一致** / 主属性逗号分隔。
   最后一项是新增的：`0:a:OtherActor` 会静默展示**别人的作品**而标题写着这个女优。
2. ⭐ **`/api/v2/search` 的 `limit` 没传。** 它是模糊搜索，**默认只返回 10 条**；
   近似结果多于 10 时真目标会被挤出第一页，表现成莫名其妙的「没有精确匹配」。
   已显式传满上限 50（实测 limit 生效且上限 50）。
   —— 顺带记一次我自己的错误：我先看到「page2 与 page1 有重叠」就断言
   「分页无效、不必修」，实际 `limit=50` 完全有效。又是从模糊观察下结论。
3. ⭐ **`resolveExact` 返回逗号拼接的 id，调用方 split 后再打 `/api/v4/movies/{id}`。**
   多余的往返（实测番号 feed 因此从 ~1.2s 降到 **0.72s**）、
   丢掉 `magnets_count`（因而无法对零做种短路）、
   并把线格式映射散到两处。已改为直接返回 `[]movieSlim`，`workByID` 整个删除。
4. **`filter_by` 被透传循环用原始值覆盖。** `buildEntityFilter` 校验并 trim 过它，
   但循环里 `base[k] = vs` 又把原始值写了回去 —— 校验白做（例如空白会重新出现）。
   已排除 `filter_by`。
5. **`hydrate` 不响应 ctx、也不快速失败。** `sem <- struct{}{}` 会让排队中的
   goroutine 死等；一个 401 已注定整次失败，剩下几十个请求仍会打完。
   已加 ctx 感知的信号量 + 首个失败即 cancel，并在取消时**明确报错**
   （不报就会返回一批缺磁链的作品，而上层只看到成功 —— 静默少给数据）。
   诚实标注：那段「取消即报错」的兜底**没有测试覆盖**（真实 HTTP 下无法稳定
   复现那个窄窗口），测试注释里写明了。
6. **`catalog.OwnParams` 是导出的可变 map** —— 任何包都能改，
   而它是跨层共享的事实。已改为不可导出的表 + `IsOwnParam()` 访问器。
7. **术语**：`actorID` → `actressID`；「发种/没种」→「磁链候选」；
   「筛选」→「条件」。
