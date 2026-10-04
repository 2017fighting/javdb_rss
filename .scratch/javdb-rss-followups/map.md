# javdb-rss followups

`javdb-rss` 初次交付之后的收尾工作。

初次交付（地图在 `.scratch/javdb-rss/`）已经把需求 1/2/3/4 全部做完并验收：
番号订阅、女优订阅、字幕优先、`/collected` 发现端点 —— 需求 1/2/3 在**真实
qBittorrent** 上验收过，需求 4 用**真实 token** 跑通过。

这个目录收的是那一轮 code-review 里**明确标为「未修」**的，以及笔记里
**标注「未验证」**的条目。它们不是缺陷遗留，是当时的取舍与未知。
（后补的 10–12 是另一类：一张被记成已解决的欠账，以及记录层由此产生的失真。）

## 票

| # | 票 | 阻塞于 |
|---|---|---|
| 01 | 配置单一来源 | 无 |
| 02 | `/collected` 的截断不再静默 | 无 |
| 03 | 上游契约复勘 | ~~无~~ 已 resolved（2026-10-01） |
| 04 | 磁链选择：顺序无关还是接受风险 | 无 |
| 05 | `health` 包与上游细节解耦 | 无 |
| 06 | 收藏女优的批量挑选 | ~~无~~ 已 resolved（2026-10-05：由 javdb-rss-ui 交付） |
| 07 | CI 首次跑通 | ~~需先 push~~ 已 push（resolved） |
| 08 | 「钉住」磁链的存储与失效 | 无（04 决策新开） |
| 09 | pin 的切换语义 | 无（04 决策新开） |
| 10 | `since` 的语义定稿 | ~~无~~ 已 resolved（2026-10-05，grilling with user） |
| 11 | 按定稿语义落地 `since` | ~~10~~ 已 resolved（2026-10-05） |
| 12 | 订正决策记录：「ticket 09」歧义与 map 的假解决 | ~~无~~ 已 resolved（2026-10-05） |
| 13 | tag 推送即发布镜像到 GHCR | ~~无~~ 已 resolved（2026-10-05） |
| 14 | 统一 action 版本：ci.yml 的 v4/v5 → v7 | ~~无~~ 已 resolved（2026-10-05） |
| 15 | 三份部署配置改为拉已发布的镜像 | 13 |

（**01–14 全部 resolved，见下 Decisions so far**；
07 为机械验收项，已随 push 关闭。**13–15 是 2026-10-04 新开的**：
13 是发版链路本身（2026-10-05 结票），14/15 是它落地时按「记录层不骗人」
拆出来的两件 —— 14（同一仓库里两套 action 版本并存）同日结票，
15（镜像有了但部署还没用上）仍开着。）

10–12 的来源与前九张不同：不是 code-review 的「未修」，而是清账时发现的
**一笔孤儿欠账** —— `since` 的比较语义在初次交付的票 09 里被标成已解决，
但那张票从未回答它（详见 [10](issues/10-since-semantics.md) 的「一段错位史」）。

## 故意**不**开票的三项（理由值得留下）

| 项 | 为什么不开 |
|---|---|
| TLS 指纹是否需要（改用 utls） | 一旦 Cloudflare 开始拒朴素 `net/http`，请求会**失败** → 502 + `/readyz` 503 + 告警 CronJob。**它是可见的** |
| WAF / 长连接下的限流行为 | 同上：被限流会变成可见的失败，不是静默错数据 |
| `dedupe` 三个方法的重复模板 | 抽泛型 helper 的收益（约 15 行）不如现在的直白；评审也只列为判断性意见 |

判断依据是本项目自己反复确立的那条原则：

> **宁可看见错误，也不要静默降级。**

于是：**会被发现的未知可以接受，不会被发现的不行。** 04（磁链顺序）之所以开票，
正是因为它的失败方式恰恰是「不会被发现」：上游顺序一变，`guid` 就变，
qBittorrent 多下一份文件，而没有任何告警。

## 与初次交付的关系

初次交付的地图与票据在 `.scratch/javdb-rss/`（10 张票，全部 resolved）。
那里是**决策记录**；这里是可以直接开工的工作项。

两份都值得读：地图里有「为什么这么设计」，这里只有「还剩什么要做」。

## Decisions so far

- [磁链选择：顺序无关还是接受风险](issues/04-magnet-selection-stability.md)
  — **按 `created_at` 取最新（cnsub 优先）＋ 持久化「钉住」每个作品 id 已选中的磁链，只有新增 cnsub 才切换。**
  实测（211 部作品，真实 API）证明上游顺序根本不是时间序，而是一套不可重放的
  版本档位排序 —— 因此无法既「信任上游排序」又「顺序无关」。据此**推翻**了原取舍：
  不再信任 App 顺序，也不再有状态 ——后者改为有意为之的例外。
  代价：首次选定会让约 1/4 作品换磁链（纯函数规则与当前选择只一致 147/197）；
  新增持久状态（新票 08/09）。证据在 [`notes/api-recon.md` §10.4.1](../javdb-rss/notes/api-recon.md)。
- [「钉住」磁链的存储与失效](issues/08-pin-store.md)
  — **上游 movie id → 磁链记录的全局表，单个 JSON 文件 + 原子替换；`flock` 单写者；
  读写失败一律 fail fast；每请求合并写一次；不设 TTL/上限，只把条数记进日志。**
  丢 pin 就是丢 guid（上游无法重建同一选择），因此 pin 故意不可关闭。位置由新键
  `app_api.pin_file` 决定，留空默认配置文件同目录；四处部署都补了可写状态目录。
  08 交存储 + 装饰器骨架（`Policy` 接缝 + 临时实现），09 只换策略。
- [上游契约复勘](issues/03-upstream-contract-resurvey.md)
  — **两条「未验证」被推翻、一条被精确化，并新增一个入库的探针。**
  ① `i`/`v` 的含义：先前记「来自上游 `filter_tags`」**是错的**（上游只给
  `p`/`s`/`m`/`c` 起名，扫了 400 位女优；真出处是先例项目的 `MainFlags`）——
  实测 `i` = 有预览图、`v` = 有预览视频，判据是字母筛出的集合与
  `has_preview_images`/`has_preview_video` **逐位重合**。
  ② `filter_by_tags` **确认生效**（80 个标签逐个对照，先前「未确认」是样本太小）。
  ③ `sort_by` 的生效集是 `score` `hit` `update` `watched_count` `want_watch_count`
  （+默认 `release`，两阶段全集验证）；**完整集合不可枚举** —— 黑盒只能证真不能证伪，
  因此只能给「下界清单」，而服务端因此仍不校验它。
  ④ 主属性逗号列表 = **交集且顺序无关**（63 个组合全集验证）。
  ⑤ `size` 单位 = **MiB**（与 javdb.com 网页的 `data-size` 逐位对照），
  代码随之 `SizeMB` → `SizeMiB`，`pin.json` 旧键带迁移。
  ⑥ `device_uuid` **不绑定 token**（同 token 配三个 uuid 全过）；
  单会话「挤掉」的失效时刻夹在 (0ms, 750ms]，而登录在 479ms 返回 ——
  即「至多 271ms 内失效」，实践上没有宽限期（严格说未证明为 0）。
  工具：`cmd/contractprobe`（**入库**，因为它测的是长期要能复核的契约断言，
  与 ticket 04 那个用完即删的 `magnetprobe` 不同）。
- [pin 的切换语义](issues/09-pin-switch-semantics.md)
  — **只有出现 cnsub 才换 pin：非 cnsub pin 遇到任一 cnsub 就切；cnsub pin 只在出现
  `created_at` 严格更新的 cnsub 时切（同日/更旧不切）。pin 指向上游消失时继续返回快照
  （可能死链）并记 WARN；同日定序用 infohash 字典序；首次选定是纯函数
  `catalog.Select`；不缓存候选列表；每次切换一条 INFO + 落盘的「切换 N」计数。**
  ticket 08 的 `TemporaryPolicy`（钉住即不切）被 `DefaultPolicy` 取代。
- [`since` 的语义定稿](issues/10-since-semantics.md)
  — **比作品的 `release_date`，闭区间 `>=`；非法输入一律 400；日志只报异常 + 正常路径一条 Debug。**
  实测推翻了票面自己写的两处：合集再版不是「旧 `release_date`、新 `created_at`」，
  而是**新上架复用六年前的磁链**（`OFJE-662` 差 -2198 天）；`since` 在**三条**路由生效，
  而真正缺 `year`+`since` 那条 400 的是**全站**路由（清单路由早由 `buildEntityMask` 拒绝 `year`）。
  否掉磁链 `created_at` 与并集的理由：实测最近 30 天窗口内它保留 **0 部**（磁链 `created_at` 普遍早于发售日），
  而它想救的「旧作品被补上新磁链」结构上不在第 1 页 —— 取数是按 `release_date` 倒序分页的。
  顺带用**真实现存函数**测出三个静默失败：`since=2026-1-1` 静默丢 1–9 月、`since=hello` 静默只剩 1/6、
  ISO 时间戳静默丢弃当天发行的作品 —— 因此 `since` 非严格 `YYYY-MM-DD` 一律判 400。
  `pubDate` 不动（如实转述上游，可能早于 `since`）；`since` 只在取到的页里生效，
  窗口没走完只 WARN（RSS 没有机读位，也不自动翻页）。证据见 [`notes/api-recon.md` §10.4.2](../javdb-rss/notes/api-recon.md)。
- [按定稿语义落地 `since`](issues/11-since-landing.md)
  — **一个实现文件**（`internal/httpapi/since.go`：`parseSince` 校验 + `sinceBound.filter` 过滤与日志），
  三条路由（女优 / 清单 / 全站）共用，校验放在取数**之前**。
  `pages` 的两个跨层事实（每页上限 50、夹到 20 页）从 appapi 私有实现上提到 `catalog`，
  新增 `catalog.WindowFull`：`pages` 现在有**两个消费者**（appapi 决定翻几页、httpapi 判断窗口是否取满），
  两处各写一份迟早会漂，而漂的表现是窗口判断静默算错。
  真实验收：`?since=2026-01-01` → 11 条（Debug：取到 50 / 有磁链候选 17 / 保留 38 / **保留且能成条目 11**）；
  `?since=2025-01-01` 触发「取数窗没走到 `since`」；`?since=2030-01-01` 触发「一条都没剩」且**不**报窗口；
  坏形状与 `year`/`month`+`since` 一律 400。「保留 38 而 feed 只有 11 条」就是拆计数器的实证理由。
  **已知限制**：窗口判断是必要条件检测，上游分页重叠时会被去重压低而漏报（只漏报、不假报）；
  要精确需让 `catalog.Source` 的三个取作品方法像 `Collection` 那样带回
  `Truncated`/`PagesFetched` —— 跨 5 个实现 × 3 个方法的接口改动，未做。
  **落地后修订（同日）**：两条 since 告警改为**每个订阅只报一次**（它们是配置的性质，
  每轮都真），并补上 `订阅=<path>`；窗口条件从 `>= since` 收紧为 `> since`。
  依据是实测：`/rss/tags/0.xml?since=<今天>&pages=1` 给 50 部，`pages=2` 给 100 部
  **且全部满足 since** —— 这是真警报，所以是保留告警而不是降级。
- [订正决策记录：「ticket 09」歧义与 map 的假解决](issues/12-record-integrity.md)
  — **记录层不再骗人。** 初次交付地图的 `## Not yet specified` 曾把「since 比较字段」
  当作已解决删掉（无决策文本支撑，与三处 live code 矛盾）：现已在 map 里写明它是**假解决**、
  欠账为何没有归属、收口在哪张票，并明确「错误在 map 里纠正、不回头改票 09」。
  同时把**指向 since 或成本模型**的裸编号引用唯一化（map 两处、`notes/api-recon.md` §10.6），
  顺带把 §10.6 里当时那句「缓存很可能有必要」标成**后来被实测推翻**。
  两件当时有意没做、**同日又按要求补做的**：
  （a）同 effort 内的 08/09 边界引用仍不动 —— 它们就地能解析，不是 since 引用；
  （b）followups 的 map 从 `README.md` 改名为 `map.md`（合乎
  [`docs/agents/issue-tracker.md`](../../docs/agents/issue-tracker.md) 的 `.scratch/<effort>/map.md`），
  连带改了 `internal/appapi/pagination.go` 里指向它的注释；
  （c）初次交付地图里那两句旧「服务无状态」也订正为「唯一例外是钉住」，
  并指向 `CONTEXT.md` 的权威定义（原先那句「剩下可能还要收拾」已从本图的
  `## Not yet specified` 里拿掉）。
  另：原注释写「删掉 6 条」而名单只列 5 条，第 6 条无法复原，如实标注不猜。
- [tag 推送即发布镜像到 GHCR](issues/13-release-image-ghcr.md)
  — **推 `vX.Y.Z` 即构建并推 `ghcr.io/2017fighting/javdb-rss`（`linux/amd64` + `linux/arm64`），
  只推全量精度 `X.Y.Z` 与 `latest`（latest 仅正式版），发布前先跑与 CI 同一批 `make` 目标。**
  三处推翻/订正值得记：① 建票时把「抽成可复用 workflow」当推荐，查完事实后**推翻** ——
  `ci.yml` 的步骤本就是 `make` 目标的薄壳，抽取换不到单一真源；② 建票时判「包可见性要手动翻」，
  实测**包创建即 public**（用仓库自己的 `GITHUB_TOKEN` 推的包继承仓库可见性），退路没用上；
  ③ ci.md 记的那条 8 MB 构建上下文顺带修掉（`.dockerignore` + 把容器构建排到 `make build` 之前），
  真 CI 实测 `8.16MB → 1.03MB`。
  首跑（run `37226112866`，2m35s）两个 job 全绿：`/version` 报的是 tag 原文 `v1.0.0`（本地 pull
  下来起容器 curl 出来的），匿名可拉，index 里恰好两个 manifest（`provenance: false` 生效）。
  **有意没做**：不加 `workflow_dispatch` 当重发入口、不推浮动 `1.2`/`1`、不建 GitHub Release、
  不动 `Makefile`（多平台镜像 `--load` 不进本地 docker，目标天然半残）。
  拆出的两件是 [14](issues/14-unify-action-versions.md)（两套 action 版本并存）与
  [15](issues/15-deploy-pull-ghcr-image.md)（镜像有了但部署还没用上）。
  完整事实与复核命令在 [`notes/release-first-run.md`](notes/release-first-run.md)。
- [统一 action 版本](issues/14-unify-action-versions.md)
  — **`ci.yml` 的 `checkout` `v4 → v7`、`setup-go` `v5 → v7`，与 `release.yml` 共用同一批大版本。**
  跨大版本逐条读过（不是机械替换）：七个变更里六条对本仓库不适用 —— 凭据持久化到独立
  文件（CI 无 `git` 网络操作）、v7 拦 `pull_request_target`/`workflow_run` 上的 fork PR
  （我们只挂 `push`/`pull_request`）、`GOTOOLCHAIN=local`（`go.mod` 要 `1.27.1`，
  装到的就是它）、优先 `toolchain` 指令（本仓库没有该行）、runner 下限 `v2.327.1`
  （托管 runner `2.337.0`）、Node 24。
  **唯一真代价**是 setup-go `v6.3.0` 把默认缓存键从 `go.sum` 换成 `go.mod`：升级后
  第一跑冷缓存（`Cache is not found`，`go vet`/`test (race)`/`build` 由 `1s/7s/0s`
  变 `17s/28s/12s`，整跑 46s → 1m40s），attempt 2 即命中新键、整跑回到 45s。
  验收用差分判据而不是「任务变绿」（升级前也绿）：真 CI run `37226687575`（sha `1cac8f5`）
  8 步全绿，且**同一份日志里弃用警告 0 次**（升级前的 run `37226396921` 是 1 次）。
  顺带复核 ci.md 第 3 条：`ubuntu-latest` **尚未**迁到 26.04，仍解析为 `24.04.5 LTS`。
- [收藏女优的批量挑选](issues/06-collected-bulk-picking.md)
  — **交付不在本 effort 里：由 [`.scratch/javdb-rss-ui/`](../javdb-rss-ui/spec.md) 做的**
  （UI 的 spec 首页就写着「取代 06 的验收清单」；那 8 张票全部 resolved，含 08 的真实验收：
  真上游 + 真 token + 真 qBittorrent v5.2.4）。**2026-10-05 结票**，
  四个框逐条对上，并补了一次**当前时刻**的冒烟（不依赖那几张票的自述）：
  `GET /` → 200（单页、资产内嵌）；`GET /collected` → 200，**144 位**（女 138 / 男 6，
  与 UI spec 的用户故事数字一致），元素带 `feed` / `gender` / `id` / `name` / `videos_count`；
  `truncated` 键**不存在**（未截断，契约「看键在不在」成立）；两个新只读端点
  `/tags?type=0` 与 `/actress_tags/EvkJ` 均 200。
  没重跑「往真实 qBittorrent 里粘链接」那一步 —— 08 已经做过，重跑不增加信息。

## Not yet specified

<!-- 看得出方向、但还捏不成 ticket 的东西 -->

- **CI 的一条维护性观察**（来自 ticket 07 首跑，见
  [`javdb-rss/notes/ci.md`](../javdb-rss/notes/ci.md)）：`ubuntu-latest` 迁
  Ubuntu 26.04（[`actions/runner-images#14748`](https://github.com/actions/runner-images/issues/14748)：
  10-19 起滚动，11-19 前完成；2026-10-05 复核时仍是 `ubuntu-24.04`）。
  原先并列的另两条都已收掉：`.dockerignore` 漏排根目录 `javdb-rss` 导致的 8 MB
  构建上下文由票 13 修掉（加忽略行 + 把容器构建排到 `make build` 之前，两头都堵上，
  实测 `8.16MB → 1.03MB`）；`checkout`/`setup-go` 的 Node 20 弃用警告由票 14 收掉
  （两处升到 `v7`、与 `release.yml` 同批，实测日志里归零）。
  Ubuntu 26.04 迁移仍只是时间问题：本项目的 CI 只依赖 Go 与 Docker，
  两者在 26.04 上都有（#14748 的对照表里 Docker Buildx 两版同为 `0.37.0`），
  到那天复核一次即可。
- **ticket 03 复勘暴露的两条口径未知**（都记在 notes 里，未开票）：
  `tags[].videos_count` 与 `filter_by_tags` 实得条数口径不同（80 个标签里 15 个
  对不上，两个方向都有）；`i`/`v` 的含义是从字段反推的，没有上游文案佐证。
  两者都不开票：一个只影响「拿它做条数预期」这种用法，认错也是可见的。
