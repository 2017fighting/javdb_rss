# 06 — 收藏女优的批量挑选

**What to build:** 用户有 **144 位**收藏女优，而「只做发现」这个形态意味着：服务把清单交出来，由用户自己决定订哪些。现在这一步要手工从 144 条里挑、再把 id 抄进订阅 URL —— 清单里虽然带了现成的 feed 路径，但「挑选」本身没有工具支持。

交付：一条能产出「我选定的订阅 URL 列表」的途径，不需要手抄 id。

**Blocked by:** None — can start immediately.

**Status:** resolved（2026-10-05；交付方是 `.scratch/javdb-rss-ui/`，逐框核对见 ## Answer）

**Spec:** [`../../javdb-rss-ui/spec.md`](../../javdb-rss-ui/spec.md) —— 完整规格、实现决定与验收标准都在
那里，下面这四个框由它取代（设计稿、ui-contract、证据与 40 项行为断言也都在 `.scratch/javdb-rss-ui/`）。
做法是：服务自己把那张页面端出来（资产内嵌、单二进制），数据来自服务自己的发现端点；
为此补两个只读端点（标签词表 `/tags?type=`、单个女优的标签 `/actress_tags/{id}`）—— 仍然无状态。

- [x] 能按某种可指定的条件筛出子集（按名字、按作品数、或直接列举）—— 页面列出收藏（144 位，默认只看女优 138 位、可切全部演员），可按**名字或 id 搜索**、可按性别切；元素里带 `videos_count`（作品数）
- [x] 输出可直接粘贴进 qBittorrent 的 feed URL 列表 —— 常驻「待复制」输出台，逐行复制 + 一次复制全部；08 用真 qBittorrent v5.2.4 粘进去验收过
- [x] **不引入服务端状态** —— 页面只持久化「服务地址 + 主题」，选择与待复制只活在内存里（`app.js` 头注释与 ui-contract）
- [x] 不破坏 `/collected` 现有的响应形状 —— 形状变更只有 followups **02** 加的那组截断信号（`omitempty`，未截断时**键不存在**），而 UI 只是**读**它（契约规则 15）。`.scratch/javdb-rss-ui/` 那 8 张票没有一张改过响应形状

## Answer（2026-10-05 结票）

交付不在本 effort 里 —— UI 的 spec 首页就写着「**取代:** `…/06-collected-bulk-picking.md` 的验收清单」，
实际交付方是 [`.scratch/javdb-rss-ui/`](../../javdb-rss-ui/spec.md)（8 张票全部 resolved，
含 [08 的真实验收](../../javdb-rss-ui/issues/08-close-out.md)：真上游 + 真 token + 真 qBittorrent v5.2.4）。

四个框逐条对上，并做了一次**当前时刻**的冒烟 —— 不依赖那几张票的自述，而是直接问现在的服务：

| 检查 | 实测（2026-10-05，真上游 + 真 token） |
|---|---|
| 页面由服务自己端出、资产内嵌（单二进制） | `GET /` → 200，25 KB 单页 |
| 收藏清单 | `GET /collected` → 200，**144 位**（女 138 / 男 6）—— 与 UI spec 用户故事的 138/144 一致 |
| 元素里带的东西 | `feed`（现成路径）、`gender`、`id`、`name`、`videos_count` |
| 截断信号 | `truncated` **键不存在**（未截断）—— 契约「看键在不在」成立 |
| 两个新只读发现端点 | `/tags?type=0` → 200、`/actress_tags/EvkJ` → 200 |

**没重复做的核对（诚实标注）**：没有把「往真实 qBittorrent 里粘一条链接、条目出来」重跑一遍 ——
那是 08 已经做过的真实验收，重跑不增加信息。本票结的是「这条途径已存在、且**现在**可用」。
