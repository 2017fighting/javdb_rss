# 清单（片单）与标签上限（上游契约复勘 · 第二批）

承接 [`tag-vocabulary.md`](tag-vocabulary.md)，回答用户那两个「继续找 / 测一下」的问题，
外加「清单」这一节要的后端契约。

**结论三句话：**

1. **`filter_by_tags` 只认前 5 个 id，第 6 个起被静默丢弃。** App 那个「最多 5 个」
   不是它的 UI 习惯，是上游的硬限制 —— 我们的 UI 上限因此是**必需**，不只是跟随。
2. **年份 / 月份 / 時長 在上游没有可用的通道**（试了 4 个端点、30 个参数名，
   全部被静默忽略）。但它们的**数据本地就有**：`release_date` 与 `duration`
   在列表响应里 100% 出现，所以正确做法是**本服务自己过滤**，而不是继续找通道。
3. **清单**：`/api/v1/lists/simple` 是能用（需要 token）的发现端点；
   列表作品是 `filter_by=0:l:{id}`（zone **必须是 0**）。名字最像的那个端点
   `/api/v1/users/collected_lists` **实测 HTTP 500**。

证据落在 [`../evidence/`](../evidence/)（含 `lists/` 子目录），复跑命令见文末。

---

## 1. `filter_by_tags` 的 5 个上限 —— 已确认

### 判据：第 6 个 id 能不能改变结果

用一个 1 项标签当「探针」，其余标签全部**包含**那一项：

| 请求 | 结果 | 说明 |
|---|---|---|
| `filter_by_tags=103` | 1 条 `OFJE-629` | 探针标签，它只有这一部 |
| `filter_by_tags=103,10,312,48` | 1 条 | 这 4 个都含 `ZNxRmA` |
| `filter_by_tags=103,10,312,48,65` | 1 条 | 5 个都含 |
| `filter_by_tags=103,10,312,48,65,8` | **1 条** | 第 6 个 `8` **不含**那一部 —— 若被采纳应当是 **0 条** |
| `filter_by_tags=8,103,10,312,48,65` | **0 条** | 把 `8` 挪到第 1 位，它立刻生效 |
| `filter_by_tags=103,10,8,312,48,65` | **0 条** | 放在第 3 位也生效 |
| `filter_by_tags=103,10,312,48,65,8,17` | 1 条 | 第 7 个也丢 |

**只认前 5 个，按位置截断，不报错、不警告。**

（对照组设计：`65` 与 `8` 都含/不含 `ZNxRmA` 是事前用两两组合实测出来的，
不是猜的 —— 见 `evidence/` 里那张双标签对照。）

### 顺带确认：重复 id 会被合并

`filter_by_tags=68,68,68,68,68,68` → 31 条，与 `=68` 单查**完全相同**。
所以「6 个 token」里的重复不计入 5 个上限 —— 截断数的是**不同的 id**。

### 对代码/UI 的意义

- UI 的「最多 5 个」必须保留，而且**理由要写成「上游硬限制」**而不是「跟 App 一致」——
  否则将来有人「放开到 8 个」时会以为只是放宽一个 UI 约定。
- 多条标签的语义是**交集**（上一批已验），与 `filter_by` 主属性一致。

---

## 2. 年份 / 月份 / 時長：上游没有通道，改为**本地过滤**

### 试过什么

在 `/api/v1/movies/tags`（女优页与清单页用的那条）与 `/api/v1/movies/latest`、
`/api/v1/movies/top` 上，逐个试这些参数名（值都用真实的年份/月份/时长档位）：

```
year  years  filter_by_year  movie_year  release_year  publish_year  date  date_from
release_date_from  month  months  filter_by_month  movie_month
duration  durations  filter_by_duration  length  time  period
movie_filter_by（libapp.so 里扒出来的名字，试了两种写法）
filter_by_tags=2026 / =lt-45 / =gt-120 / =45-90 / =year:2020
```

**全部返回与基线逐条相同的结果**（＝被静默忽略），或 0 条。

也查了二进制：`lt-45` / `45-90` / `90-120` / `gt-120` 这四个字面量
**在 `libapp.so` 里一个都找不到**。不过这一条只是旁证 ——
词表本身是从 `/api/v2/tags` 拿的，App 不把字面量编进包里也正常。

> **方法论限制（必须写下来）：** 这与 `sort_by` 的结论同类 ——
> 黑盒只能**证真**（某个参数改变了结果 ⇒ 它生效），**不能证伪**。
> 「上游没有通道」准确的说法是「**我们找不到通道**」。
> 好在这一次不影响交付：见下。

### 但数据本地就有，所以不需要上游通道

`/api/v1/movies/tags?filter_by=0:a:EvkJ&limit=20` 的响应里：

```
duration 出现率     20/20
release_date 出现率 20/20
```

`duration` 的单位是**分钟**（实测值 58/71/85/120/190/480/720…，720 对应一部 12 小时合集；
与 `movieSlim` 的其它字段同一批返回）。

因此：

| 组 | 上游能给什么 | 正确做法 |
|---|---|---|
| 年份 | 只有 id（`2026`…），没有过滤通道 | 按 `release_date` **本地过滤**（服务已有 `since=`，可加 `until=`） |
| 月份 | 同上，而且 id 1–12 **与真标签 id 撞号** | 同上 |
| 時長 | 只有档位 id（`lt-45`/`45-90`/`90-120`/`gt-120`） | 按 `duration`（分钟）**本地过滤**，档位边界用上游给的这四个 id |

**为什么这个转向更重要：** 走上游通道的话，即使找到了，也只是把 `filter_by_tags`
那套语义再搬一遍；而本地过滤能表达上游根本表达不了的东西 ——
比如 `since` + `until` 的区间、以及「只看 120 分钟以上**且**带中文字幕」。

代价要说清楚：本地过滤是**在服务里**筛，因此它只能筛到已经拉回来的作品。
现有实现已经是「按 `pages` 拉若干页 → 渲染」，`since` 就是在这之后筛的 ——
所以这条路与原设计一致，**不是**新引入的缺点。

---

## 3. 清单（片单）

### 发现端点：用 `/api/v1/lists/simple`

| 端点 | 结果 |
|---|---|
| `/api/v1/users/collected_lists` | ❌ **HTTP 500**（GET/POST、带/不带 `page`、带/不带 token 全试过） |
| `/api/v1/lists/simple` | ✅ 需要 token；返回 `{"lists":[...]}` |
| `/api/v1/lists/{id}` | ✅ 详情（含 `list.name`）；公开清单匿名可读 |
| `/api/v1/lists/related` | 与清单无关（要 `movie_id`） |

`lists/simple` 的每项：

```json
{"id":"k4EVE4","name":"遥控跳弹","privacy":"open",
 "is_default":false,"movies_count":1,"has_movie":false}
```

实测该账号 **5 份**清单；`page=2` 返回空 → 同一套「空页 = 到底」。
其中一份 `is_default: true`、`privacy: "own"`、名字在 simple 里是 `default`
而在详情里是 `預設清單`（详情端点是本地化的）。

⚠️ **「我建的」≠「我关注的」**：`/lists/simple` 返回的是账号主人**自己建的**清单
（`privacy: "own"` 那份只有主人能看到，这是判据）。用户原话是「我关注的清单列表」，
而那个概念对应坏掉的 `collected_lists`。本服务因此实现的是「我建的」，
并把这件事写进了接口注释与 README —— 将来上游修好了，这里要回来看。

### 列表作品：`filter_by=0:l:{id}`，zone **必须是 0**

| 清单 | 上游声明 `movies_count` | `filter_by=0:l:{id}` 实得 | `filter_by=2:l:{id}` |
|---|---|---|---|
| `k4EVE4` | 1 | **1** | 50 |
| `p36Eww` | 9 | **9** | 50 |
| `ZX11zV` | 1 | **1** | 50 |
| `p31z8B` | 2 | **2** | 50 |
| `R9r77` | 6 | **6** | — |

4/4 与上游声明**逐位相同** —— 所以 `movies_count` 不只是参考值，可以拿去对账。
zone=2 那列是 50（＝一整页）说明**写错 zone 不报错，只会静默返回别的作品**。
清单形态里没有 zone 字段，因此 zone 写死 0，并且**不把它做成配置项**。

### feed 验收（服务自己跑出来的）

`/collected_lists` 给出 5 份真实清单；5 条 feed 全部 200，标题取到真名字：

```
k4EVE4  标题='JavDB · 遥控跳蛋'  条目=1   (movies_count 1)
p36Eww  标题='JavDB · 影绘 剪影' 条目=8   (movies_count 9) ← 有一部没有磁链，按规矩跳过
ZX11zV  标题='JavDB · 跳蛋蛋蛋'  条目=1   (movies_count 1)
p31z8B  标题='JavDB · vrvr'     条目=2   (movies_count 2)
R9r77   标题='JavDB · 預設清單'  条目=6   (movies_count 6)
```

`p36Eww` 的 8/9 正是「有磁链才发条目」那条规矩在起作用，不是漏了。

---

## 复跑方式

```bash
cd <repo>
OUT=/tmp/lists-probe

# ── 1. 标签上限：第 6 个被丢弃 ──
for ids in "103" "103,10,312,48" "103,10,312,48,65" "103,10,312,48,65,8" "8,103,10,312,48,65" "68,68,68,68,68,68"; do
  go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags \
    filter_by=0:a:EvkJ limit=50 filter_by_tags=$ids
done
# 读法：前两条 1 条（OFJE-629）；第三条 1 条；第四条仍 1 条 ⇒ 第 6 个没生效；
#       第五条 0 条 ⇒ 同一个 id 挪进前 5 就生效。

# ── 2. 日期/时长没有上游通道（应与基线逐条相同）──
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ limit=20 year=2020
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ limit=20 month=3
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ limit=20 duration=lt-45
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ limit=20 movie_filter_by=year:2020
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/latest limit=20 year=2020
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/top    limit=20 year=2020

# ── 3. duration / release_date 在列表响应里 100% 出现（本地过滤的前提）──
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ limit=50

# ── 4. 清单 ──
go run ./cmd/contractprobe -out $OUT raw /api/v1/lists/simple          # 需要 token
go run ./cmd/contractprobe -out $OUT raw /api/v1/lists/k4EVE4          # 详情（匿名可读）
go run ./cmd/contractprobe -out $OUT raw /api/v1/users/collected_lists # 500
for id in k4EVE4 p36Eww ZX11zV p31z8B R9r77; do
  go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:l:$id limit=50
  go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=2:l:$id limit=50
done
```

## 仍然未知（已登记，不是遗留）

- **年份/月份/時長有没有上游通道** —— 准确说法是「我们找不到」。不再追；
  本地过滤已经能表达得更多（区间、与其它条件组合）。
- **`type` 与 zone 的对应**（上一批就登记了，仍只有旁证）。
- **词表里不在该女优 `tags[]` 里的标签能不能筛** —— ticket 03 只测过她自己的 80 个。
- **`/api/v1/users/collected_lists` 为什么 500** —— 上游服务端错误，
  与本服务的参数无关（试过 4 种调用方式）。
- **`has_movie` 的确切语义** —— 本服务刻意不解析它（与订阅无关），因此也没查。
