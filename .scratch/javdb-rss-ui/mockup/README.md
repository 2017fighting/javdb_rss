# javdb-rss 订阅链接生成器 — 设计稿

给这个单人自用服务的**前端**：把「我要订什么」变成一串能直接粘进 qBittorrent 的 RSS URL。

> **数据全是真的，链接也全是真的。** 演员、标签词表、每位女优的标签、清单都来自
> `evidence/` 里 contractprobe 抓到的真实上游响应（144 位收藏演员、11 组 355 个标签、
> 21 位女优自己的标签、5 份真实清单）。清单与标签那两节依赖的后端**已经实现并验收过**。

## 怎么跑

```bash
cd .scratch/javdb-rss-ui
npm i                     # tailwindcss v4 + @tailwindcss/cli
npm run build             # mockup/src/input.css -> mockup/assets/app.css
node tools/gen-data.mjs   # evidence/ -> mockup/src/data.js（要重抓上游时才需要）
```

然后用 `serve_mockup` 把 `mockup/` 服务出来，浏览器打开 local + LAN 地址。
评审脚本需要一个能解析到的 playwright（本仓库把外面那份软链进 `node_modules/`）。

```bash
MOCKUP_ORIGIN=http://localhost:62100 ./tools/verify.sh
```

## 四个需求落在哪

| 需求 | 在哪儿 | 生成的 URL |
|---|---|---|
| 1. 收藏演员，挑几位，追新 / 全量，一人一条 | 「收藏女优」 | `/rss/actress/{id}.xml?since=<今天>` · `?pages=20` |
| 2. 想看，单独一个复制按钮 | 「想看」 | `/rss/want.xml` |
| 3. 分类选择器，**按 App 的 11 个分组**，最多 5 个标签 | 「标签筛选」· 女优模式 | `/rss/actress/{id}.xml?since=…&filter_by=0:a:{id}:c::&filter_by_tags=46,68` |
| 3b. 同上，**全站**（不挂实体） | 「标签筛选」· 全站模式 | `/rss/tags/{片库}.xml?main=c,m&tags=46,68&year=2020&month=3&duration=gt-120` |
| 4. 我关注的清单，一清单一链接 | 「清单」 | `/rss/list/{id}.xml` ✅ 后端已实现 |

### 标签分组：来自上游，不是我们编的

`GET /api/v2/tags?type={zone}` 直接给出分组（**匿名可读**）：分组名、顺序、组内标签全部原样用。
女优页显示的标签 = **她自己的 `tags[]`**，用 id 反查落到哪个组（撞号时按**名字**消歧）。

11 组里三组走**另一条通道**，这是这一版最重要的设计决定：

| 组 | 怎么筛 |
|---|---|
| 基本 `main` | `filter_by` 的字母位（`p m c s i v`）—— **不是** `filter_by_tags` |
| 主題/角色/服裝/體型/行爲/玩法/類別（7 组） | `filter_by_tags`，**上限 5 个**，多个之间是**交集** |
| 年份 / 月份 / 時長 | **全站模式**：真上游筛选（掩码第 4/5/6 槽，抓包反推 + 逐槽位验过）。**女优模式**：暂时禁用 —— 女优页掩码有没有这几个槽位还没验出来，塞进去一律 0 条，区分不了「槽位不存在」与「值不对」 |

**为什么不是 `filter_by_tags`：**

- **上限 5 是上游的硬限制**，不是 UI 约定。实测：第 6 个 id 被**静默丢弃**，
  而把同一个 id 挪到前 5 位就立刻生效（同一批请求的结果从 1 条变成 0 条）。
  所以 UI 的上限只能是 5，理由要写成「上游限制」。
- **年份/月份/時長 的 id 不是标签 id**：月份 1–12 与真标签 id **完全撞号**
  （`3` = 服裝:眼鏡），時長 的 id 形如 `lt-45`。实测 `filter_by_tags=45-90` 与
  `=45` 返回**逐条相同**的 6 部，而那 6 部的时长**全部 > 90 分钟**。
- 独立参数 `year=` / `month=` / `duration=` / `movie_filter_by=` 在 4 个端点上
  试了 30 个名字，**全部被静默忽略**。
- 但数据本地就有：`release_date` 与 `duration` 在列表响应里 **100% 出现**。
  所以正确做法是**本服务自己过滤**，而不是继续找通道 —— 而且本地过滤能表达
  上游根本表达不了的（区间、与其它条件组合）。

完整结论与复跑命令：[`notes/tag-vocabulary.md`](notes/tag-vocabulary.md)（分组与上限）、
[`notes/lists.md`](notes/lists.md)（上限、日期通道、清单契约）。

### 核心结构：底部「待复制」

每一条路径最后都汇到底部固定的**待复制**工具条。它存的是「怎么生成」
（实体 id / 模式 / 标签），URL 每次现算 —— 改服务地址、跨过零点，所有链接当场跟上。
标签与主属性都**按词表顺序**拼进 URL，不按点击顺序：同样的选择永远得到同样的链接。

## 验证结果（脚本跑的，不是「看起来还行」）

```
L1 token-lint（硬闸门）      PASS   源码与编译产物里没有自己的色值字面量
L2 对比度（硬闸门）           PASS
axe × 375/768/1440 × 亮/暗   PASS   4 分区 × 空态/填满态，0 违规
触摸目标 ≥24px / 焦点可见 / 溢出 PASS
主操作 ≤1 / 区域              PASS
行为断言 40 项                PASS   含「分组名取自上游」「第 6 个标签被禁用」
                                    「清单链接可复制」「选年份 → since= + pages=20」
L4 rubric                    7/7
```

这套检查抓出过 8 个真 bug，其中三个是**点了完全没反应**（`state.tags.add` 写错、
`#tag-count` 从没被更新、分组折叠后测试点不到 chip），一个是差点把
`filter_by_tags` 判成坏了的解析 bug（控制组也返回 0）。

## 需要你定的事

1. ~~時長那一组要不要做~~ / ~~年份月份要不要卡上界~~ —— **两个前提都被推翻了**：
   上游本来就有年/月/时长三个槽位（掩码第 4/5/6），所以不需要 `duration=` 也不需要
   `until=`，而且年份是**整年**、月份是**整月**（不是「只有下界」）。
   現在这两个控件在**全站模式**下是真筛选，在女优模式下禁用（等抓包）。
3. **清单要订阅哪几份。** 5 份真实清单里有一份是你账号的默认清单（`R9r77`，私有）。
   要不要给 `feeds.lists` 白名单填上？（不填就是全放行，URL 即订阅。）
4. ~~要不要把「全站标签」接通~~ —— **已经接通了**，而且我上一轮的结论是错的。
   更正：`filter_by_tags` 只对女优实体生效**没错**，但我据此推断了「全站标签不可能」，
   漏掉了另一条通道 —— **标签可以是 `filter_by` 掩码里的一个槽位**。
   触发更正的是真机抓包（模拟器 + mitmproxy）：App「浏览」页发的是 `0:t:m::::`。
   完整语法与逐槽位证据见 [`notes/tag-vocabulary.md`](notes/tag-vocabulary.md) 第 7 节。
   后端已经实现 `GET /rss/tags/{片库号}.xml`。
5. **「加入待复制」的主次**（上一轮那条还开着）：现在主操作是「复制」，标签区的加入是次要按钮。

## 与真实实现的距离（promote 时要补的）

- **服务地址默认值**：mockup 写死 `http://127.0.0.1:8080`。真页面由服务自己提供时
  默认应当是 `location.origin`，并持久化到 localStorage。
- **数据来源**：`GET /collected`（+ `gender`，见下）、`GET /collected_lists`、
  上游 `GET /api/v2/tags`、以及一条「取某女优 `tags[]`」的读取。
  除女优标签外都是匿名可读的，所以标签筛选在没配 token 时也能用。
- **`/collected` 的 `gender`**：已经补上了（服务透出 `gender`：0=女优 138 / 1=男优 6）。
  设计稿里「只看女优 / 全部演员」开关接的就是它，默认只看女优（138 位）。
- **`/collected` 的截断信号**（`truncated` / `pages_fetched` / `max_pages`）还没进 UI ——
  它必须进：清单已知不完整时不能长得像完整的。
- **示例数据只带了 21 位女优的标签**（一次一位的上游请求，不能 144 位全抓）。
  真实实现里点了哪位取哪位，并给它一个加载态。
- **空态/错误态**目前靠 URL hash（`#state=notoken` 等）演示。

## 目录

```
mockup/index.html                页面骨架（可访问性标记、缺什么的说明都在这里）
mockup/app.js                    交互 + URL 构造 + 分组消歧 + 折叠
mockup/src/data.js               生成物：真实上游数据
mockup/src/input.css             Tailwind v4 入口：shadcn 令牌 + 组件类
mockup/assets/app.css            编译产物（提交进库，评审时不用装 node）
ui-contract.md                   跨屏一致性的事实来源（含两处令牌偏离 + 五条硬规则）
notes/tag-vocabulary.md          ⭐ 分组词表 / 撞号 / 交集语义
notes/lists.md                   ⭐ 5 个 id 上限 / 日期通道 / 清单契约
evidence/                        contractprobe 的原始响应，笔记里每条结论的证据
tools/verify.sh                  全部验证
tools/gen-data.mjs               evidence/ -> mockup/src/data.js
tools/a11y.mjs                   axe / 触摸目标 / 焦点 / 溢出 / 主操作
tools/flows.mjs                  四个需求的行为断言（40 项）
tools/shots.mjs                  断点截图
tools/rubric.json + rubric.mjs   L4 的 7 条布尔答案与算分
```
