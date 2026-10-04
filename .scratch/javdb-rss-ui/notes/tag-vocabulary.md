# 标签的分组与可用性（上游契约复勘）

**问题**：用户想要「和 App 一样的标签分类选择器」——基本 / 年份 / 月份 / 主题 / 角色 /
服装 / 体型 / 行为 / 玩法 / 类别 / 时长。这 11 个分组从哪来？分组里的条目能不能真的筛？

**结论（2026-10-04，真实上游 + 真 token，但下面的端点都**不需要** token）：**

1. 分组是**上游给的**，我们不需要维护任何常量表 ——
   `GET /api/v2/tags?type={zone}`。
2. 11 组里的 **7 组能筛**（走 `filter_by_tags`），**1 组走另一条通道**（`基本` → `filter_by`），
   **3 组筛不了**（年份 / 月份 / 时长）—— 而且其中一组会把**别的标签**筛出来。
3. 女优页该显示的标签 = **她自己的 `tags[]`**，用词表把每个 id 落到分组上。
   4 位女优样本，**80/80 落位成功，零未解**。

证据落在 [`../evidence/`](../evidence/)（contractprobe 的原始响应），
复跑命令见文末。

---

## 1. 分组词表：`GET /api/v2/tags?type={zone}`

**匿名可用**（`token_file` 指向不存在的路径也能打）。`type` 必填 ——
不给报 `ParameterInvalid: 參數不能爲空: type`。

响应形状：

```json
{ "tags": [
  { "category": "基本", "category_id": "main",
    "tags": [ {"id": "p", "name": "可播放"}, {"id": "m", "name": "可下載"} ] },
  { "category": "年份", "category_id": "year",
    "tags": [ {"id": "2026", "name": "2026"} ] },
  ...
] }
```

`type=0` 的 11 组（顺序即上游顺序，`category` / `category_id` **原样可用**）：

| 顺序 | `category_id` | `category` | 条数 | id 形态 | 能筛？ |
|---|---|---|---|---|---|
| 1 | `main` | 基本 | 6 | 字母 `p m c s i v` | ✅ 但走 `filter_by`，**不是** `filter_by_tags` |
| 2 | `year` | 年份 | 26 | 数字 `2026`… | ❌ |
| 3 | `month` | 月份 | 12 | 数字 `12`…`1` | ❌ **且会筛错** |
| 4 | `subject` | 主題 | 60 | 数字 | ✅ |
| 5 | `role` | 角色 | 53 | 数字 | ✅ |
| 6 | `cloth` | 服裝 | 39 | 数字 | ✅ |
| 7 | `body` | 體型 | 20 | 数字 | ✅ |
| 8 | `behavior` | 行爲 | 40 | 数字 | ✅ |
| 9 | `play_method` | 玩法 | 37 | 数字 | ✅ |
| 10 | `category` | 類別 | 58 | 数字 | ✅ |
| 11 | `duration` | 時長 | 4 | 字符串 `lt-45` `45-90` `90-120` `gt-120` | ❌ |

合计 355 条。7 个可筛组共 **307** 条。

**`type` 的取值范围未确定，而且非法值静默回落**：`type=9` 与 `type=0`
响应**逐字节相同**（11 组 355 条）。`type=0/1/2/3` 各返回**不同**的词表：

| type | 组数 | 标签数 | 特征 |
|---|---|---|---|
| 0 | 11 | 355 | 有 主題/角色/服裝/體型/行爲/玩法/類別/時長，最丰富 |
| 1 | 8 | 193 | 多一个 `other` 其他，少 body/behavior/play_method/category |
| 2 | 11 | 253 | 有 `place` 地點 |
| 3 | 5 | 93 | 只有 main/year/month/`tag` 标签/duration |

「`type` 就是 `filter_by` 里的 zone」（0=有码 1=无码 2=欧美 3=FC2）**是推测**：
type=3 那种「一个笼统的 `tag` 组」符合 FC2 的特征，type=0 的词表最丰富、符合有码。
**没有**对着具体片单验证过 —— 要真用，得先按 zone 对一次片单。

---

## 2. ⚠️ id 不是全局唯一的：`月份` 与真实标签撞号

这是本次复勘最重要的发现，也是「照抄分组就会静默出错」的原因。

`type=0` 的 355 条里，**12 个 id 被多个组共用，全部是 `月份` 的 1–12**：

```
id=3   ->  月份:3   |  服裝:眼鏡
id=7   ->  月份:7   |  主題:處女
id=12  ->  月份:12  |  類別:成人電影
…12 个全撞
```

`filter_by_tags` 只认**标签 id**，所以把 `月份=3` 发出去，拿回来的是**服裝:眼鏡**那批。
没有报错，没有警告。

### 证据：非数字 id 会被截到数字前缀

| 请求 | 结果 |
|---|---|
| `filter_by_tags=45`（類別:第一人稱攝影） | 6 部 |
| `filter_by_tags=45-90`（時長档位） | **逐条相同的 6 部** |
| `filter_by_tags=lt-45` / `gt-120` | 0 部（无数字前缀） |
| `filter_by_tags=2026`（年份） | 0 部 |

`45-90` 与 `45` 返回**完全同一批**作品 —— 这说明非数字 id 被截到了数字前缀。
而那 6 部的时长**全部 > 90 分钟**，正是「用 `45-90` 想筛 45–90 分钟」会得到的最坏结果。

### 独立参数也被静默忽略

在 `/api/v1/movies/tags`（女优页用的那条）与 `/api/v1/movies/latest` 上：

```
year=2020  month=3  duration=lt-45  movie_filter_by=year:2020  movie_filter_by=2020
```

**全部返回与基线逐条相同的结果** —— 静默忽略。

`movie_filter_by` 是 `strings libapp.so` 里扒出来的一个参数名
（与 `filter_by`、`filter_by_tags`、`year`、`month`、`duration`、`movie_type` 并列），
试过的两种写法都不生效，含义仍未知。

> **仍未找到** 年份 / 月份 / 时长在 App API 上的正确通道。
> 年份可以拿 `since=` 近似代替（服务已有）；月份与时长没有替代。
> 按本项目的原则，**宁可不给，也不给一条会静默筛错的链接**。

---

## 3. `filter_by_tags` 的语义：交集（AND）

先前只验证过「每一个标签单独都改变结果集」（ticket 03），**没验证过多个**。

用两个小集合的标签跑全集对照（EvkJ）：

| 请求 | 结果 |
|---|---|
| `filter_by_tags=161` | 12 部 |
| `filter_by_tags=212` | 27 部 |
| `filter_by_tags=161,212` | **5 部** = 两者交集（并集是 34） |

**多个 id = 交集，与 `filter_by` 的主属性组合语义一致。**

---

## 4. 女优自己的标签 → 落到分组上

`GET /api/v1/actors/{id}` 的标签在**顶层 `tags`**，不在 `actor` 里
（`actor` 只有 id/type/avatar_url/name/…/videos_count）。每项是
`{id, name, videos_count}` —— **没有分组字段**：

```json
{"tags": [{"id":"10","name":"4小時以上作品","videos_count":117}, ...]}
```

所以分组只能靠**词表反查**。反查规则（因为 id 会撞号）：

1. 拿 id 去词表里找所有候选；
2. 候选里**名字相同**的那个胜出；
3. 只有一个候选就用它；否则**放弃显示**（宁缺勿错）。

4 位女优样本的结果：

| 女优 | 标签数 | 撞号 id | 名字消歧后 | 未解 |
|---|---|---|---|---|
| EvkJ（河北彩花） | 80 | 5 | 類別18 主題14 行爲14 角色12 玩法8 服裝8 體型6 | 0 |
| 83V（桃園憐奈） | 91 | 5 | 角色18 類別17 行爲16 主題14 服裝10 體型8 玩法8 | 0 |
| kzx6（田中檸檬） | 86 | 6 | 行爲18 類別15 角色14 主題14 服裝11 體型8 玩法6 | 0 |
| D2EdJ（花守夏歩） | 103 | — | 行爲22 類別18 主題17 角色14 服裝12 玩法11 體型9 | 0 |

**关键**：撞号那 12 个 id 里，女优标签命中的都是**真标签**（例如 id=10 在她的
`tags[]` 里名字是「4小時以上作品」，于是落到 `類別` 而不是 `月份`）。
所以「先按名字消歧」不是防御性代码，是**正确性的必要条件**。

另外 `filter_tags`（顶层）给的是**这位女优支持哪些主属性**，EvkJ 是
`p s m c` 四个 —— 与词表 `main` 组的 6 个不同。App 的女优页因此只显示这 4 个。

---

## 5. 顺带发现：`/collected` 丢掉了 `gender`

`/api/v1/users/collected_actors` 的每一项带 `gender` 与 `type`：

```json
{"id":"D2EdJ","type":0,"name":"花守夏歩","gender":0,"videos_count":179}
```

实测 144 位收藏里 **`gender=0` 138 位（女优）、`gender=1` 6 位（男优）**。
`gender=1` 的样例：森林原人、小沢とおる、イセドン内村、ナルシス小林。

而服务的 `/collected` 只透出 `{id, name, videos_count, feed}` ——
**前端无法区分男女**，而用户要的是「女优」。设计稿里因此加了一个
「只看女优 / 全部演员」开关，并把这个缺口标出来。

`/api/v1/actors/{id}`（详情）**没有** gender 字段，所以这个信息只能在收藏列表里拿。

---

## 复跑方式

```bash
cd <repo>
OUT=/tmp/tag-probe

# 1. 分组词表（11 组 355 条）
go run ./cmd/contractprobe -out $OUT raw /api/v2/tags type=0
# 2. 其它 zone 的词表（1/2/3 各不同）
go run ./cmd/contractprobe -out $OUT raw /api/v2/tags type=3
# 3. 非法 type 静默回落
go run ./cmd/contractprobe -out $OUT raw /api/v2/tags type=9

# 4. 女优自己的标签（顶层 tags）
go run ./cmd/contractprobe -out $OUT raw /api/v1/actors/EvkJ

# 5. 撞号 / 截断的证据：45-90 与 45 必须逐条相同
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags \
  filter_by=0:a:EvkJ filter_by_tags=45-90 limit=50
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags \
  filter_by=0:a:EvkJ filter_by_tags=45 limit=50

# 6. 多标签 = 交集（161 与 212 的交集 5 部，并集 34 部）
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ filter_by_tags=161 limit=50
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ filter_by_tags=212 limit=50
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ filter_by_tags=161,212 limit=50

# 7. 独立参数被忽略（应与基线逐条相同）
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ year=2020 limit=20
go run ./cmd/contractprobe -out $OUT raw /api/v1/movies/tags filter_by=0:a:EvkJ movie_filter_by=year:2020 limit=20

# 8. 收藏列表带 gender（服务把它丢了）
go run ./cmd/contractprobe -out $OUT raw /api/v1/users/collected_actors page=1
```

`-out` 目录下的原始响应就是本文表格的证据。

---

## 这些结论对代码意味着什么

**设计稿（本 effort）已经按上面实现**：分组、名称、顺序全部取自上游；撞号按名字消歧；
年份/月份/时长**不显示为可点 chip**，而是单独一段说明 + 实测证据；标签 id 按词表顺序
排序后拼进 `filter_by_tags`（同样选择永远得到同样 URL）；上限 5 个是 UI 约定
（**上游没有实测过上限**，App 的 5 是它自己的规矩）。

**要真做，后端缺三条**：

1. `GET /tags?zone=N` —— 转发分组词表（匿名，无状态，纯转发）。
2. 女优的 `tags[]` 得能被前端拿到（现在 `/collected` 给的是收藏列表，
   不含标签）。要么加 `GET /tags/actress/{id}`，要么让 feed 路由带出来。
3. `gender` 要进 `/collected`（否则前端分不出男女）。
4. 可选：`/rss/tags/{zone}/{ids}.xml` —— 全站标签订阅。形状是提议，**未定**。

**仍然未知（已登记，不是遗留）**：`year`/`month`/`duration` 的正确通道；
`type` 与 zone 的对应；`filter_by_tags` 有没有上限；
词表里不在该女优 `tags[]` 里的标签能不能筛（ticket 03 只测过她自己的 80 个）。
