# javdb-rss 订阅链接生成器 — 设计稿

> **promote 已完成（2026-10-05）。** 这份 mockup 现在只是**设计记录**：真正的页面在
> `internal/webui/`（资产 embed 进单二进制），行为断言（171 项）打的是
> `provider=stub` 的**真服务**，不是这里的静态页。下面凡是与 promote 结果不同的描述
> 都已就地更正并标注。

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

⚠️ promote 之后 `input.css` 同时扫 `internal/webui/{index.html,assets/app.js}`
（`@source` 那两行）—— 因为**产物** `internal/webui/assets/app.css` 是给那个页面用的，
只扫 mockup 就会漏掉新加的 utility。改完页面后重跑 `npm run build`，
再把 `mockup/assets/app.css` 拷成 `internal/webui/assets/app.css`（两份当前逐字节相同）。

然后用 `serve_mockup` 把 `mockup/` 服务出来，浏览器打开 local + LAN 地址。
评审脚本需要一个能解析到的 playwright（本仓库把外面那份软链进 `node_modules/`）。

**不想跑浏览器也能看图**：`shots/` 里是 14 张断点截图（375/768/1440 × 明暗 ×
空态/填满态），**已经入库**。它们是 1x 的（不是 2x）—— 浏览器里 1:1 就这么大，
而 2x 会让这 14 张从 4MB 涨到 10MB。要更清楚就改 `tools/shots.mjs` 重跑。

### 验证现在打的是真服务（promote 后的变化）

`tools/verify.sh` 不再指向 mockup 的静态页，而是指向一份 `provider=stub` 的**真服务**
（于是那 171 项断言守的是上线的东西）。跑法：

```bash
# 另开一个终端先起服务（配置里 provider: stub，见 verify.sh 头部注释）
cd <repo> && go run ./cmd/javdb-rss -config /tmp/javdb-rss-verify-stub/config.yaml
# 再跑全套守卫
cd .scratch/javdb-rss-ui && ./tools/verify.sh http://127.0.0.1:8080
```

mockup 的 `index.html`/`app.js` 直接从浏览器打开已经不再是验证对象（设计记录而已）。

## 四个需求落在哪

| 需求 | 在哪儿 | 生成的 URL |
|---|---|---|
| 1. 收藏演员，挑几位，追新 / 全量，一人一条 | 「收藏女优」 | `/rss/actress/{id}.xml?since=<今天>` · `?pages=20` |
| 2. 想看，单独一个复制按钮 | 「想看」 | `/rss/want.xml` |
| 3. 分类选择器，**按 App 的 11 个分组**，最多 5 个标签 | 「标签筛选」· 女优模式 | `/rss/actress/{id}.xml?since=…&main=c&tags=46,68` |
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
| 年份 | **两种模式都是真上游筛选**，而且是**精确的某一年**（不是「起」）：女优模式走掩码第 5 段（`0:a:{id}:{main}:{year}`），全站模式走掩码第 5 槽 |
| 月份 / 時長 | **只有全站模式有**（掩码第 7 / 6 槽）。月份可以**单独给**（跨年，实测 `0:t:m::::3` → 各年 3 月），时长必须与年份同给。女优订阅里禁用 —— App 的女优筛选面板里也没有这两项，而且掩码里多写一段会让**年份被静默丢弃**，服务那侧直接 400 |

**为什么不是 `filter_by_tags`：**

- **上限 5 是上游的硬限制**，不是 UI 约定。实测：第 6 个 id 被**静默丢弃**，
  而把同一个 id 挪到前 5 位就立刻生效（同一批请求的结果从 1 条变成 0 条）。
  所以 UI 的上限只能是 5，理由要写成「上游限制」。
- **年份/月份/時長 的 id 不是标签 id**：月份 1–12 与真标签 id **完全撞号**
  （`3` = 服裝:眼鏡），時長 的 id 形如 `lt-45`。实测 `filter_by_tags=45-90` 与
  `=45` 返回**逐条相同**的 6 部，而那 6 部的时长**全部 > 90 分钟**。
- 独立参数 `year=` / `month=` / `duration=` / `movie_filter_by=` 在 4 个端点上
  试了 30 个名字，**全部被上游静默忽略**。
- ⚠️ **更正：** 由此推出的「所以正确做法是本服务自己按 `release_date` / `duration`
  本地过滤」**是错的、也从未实现**。真机抓包找到了掩码槽位这条通道，现在页面发语义参数、
  **掩码由服务拼**（契约硬规则 8）。完整更正见
  [`notes/tag-vocabulary.md`](notes/tag-vocabulary.md) 第 7 节与 [`notes/lists.md`](notes/lists.md) 第 2 节。

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
行为断言 171 项（真服务）   PASS   含「分组名取自上游」「第 6 个标签被禁用」
                                    「清单链接可复制」「选年份 → since 让位」
                                    「URL 里不含 filter_by / filter_by_tags」
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

## promote 已经完成（原「与真实实现的距离」）

下面的缺口现在都已补齐，留在这里当**已核对**的清单（每一条都指向实现处）：

- **服务地址默认值**：✅ 默认 `location.origin`，可改并持久化到 localStorage。
- **数据来源**：✅ `/collected`（含 `gender`）、`/collected_lists`、`/tags?type=`、`/actress_tags/{id}`；
  除 `/collected*` 外都匿名可读，所以标签筛选在没配 token 时也能用。
- **`/collected` 的 `gender`**：✅ 服务透出（0=女优 138 / 1=男优 6，实测 144 位）。
- **`/collected` 的截断信号**：✅ 已进 UI（票 07）：清单已知不完整时明说「已知不完整」，
  未截断时这条提示不出现。
- **女优标签按需取 + 加载态**：✅ 点了哪位取哪位，有加载态与失败态。
- **空态/错误态**：✅ 真状态（`/collected` 的 503、上游 502…），不再靠 URL hash 演示。

## 目录

```
mockup/index.html                页面骨架（可访问性标记、缺什么的说明都在这里）
mockup/app.js                    交互 + URL 构造 + 分组消歧 + 折叠
mockup/src/data.js               生成物：真实上游数据
mockup/src/input.css             Tailwind v4 入口：shadcn 令牌 + 组件类
mockup/assets/app.css            编译产物（提交进库，评审时不用装 node）
ui-contract.md                   跨屏一致性的事实来源（含两处令牌偏离 + 15 条硬规则）
notes/tag-vocabulary.md          ⭐ 分组词表 / 撞号 / 交集语义
notes/lists.md                   ⭐ 5 个 id 上限 / 日期通道 / 清单契约
evidence/                        contractprobe 的原始响应，笔记里每条结论的证据
tools/verify.sh                  全部验证
tools/gen-data.mjs               evidence/ -> mockup/src/data.js
tools/a11y.mjs                   axe / 触摸目标 / 焦点 / 溢出 / 主操作
tools/flows.mjs                  行为断言（171 项，打的是 provider=stub 的真服务）
tools/shots.mjs                  断点截图（生成物**入库**）
tools/rubric.json + rubric.mjs   L4 的 7 条布尔答案与算分
```
