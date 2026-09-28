# Map: JavDB App 订阅 → qBittorrent RSS（wayfinder:map）

> **状态更新 2026-09-28**：本图在 charting 当天经历了一次**关键路径塌缩**。
> 原以为「必须先逆向 APK」是整条路的瓶颈，结果发现已有 MIT 许可的 Go 先例
> （[`FlanChanXwO/javdb-cli`](https://github.com/FlanChanXwO/javdb-cli)）把 `jdsignature`
> 完整实现，且已实测对我们目标版本的服务端有效。
> **8 张已关闭**（`01` `02` `03` `04` `06` `08`），并新开出 `10`。
> **需求 1/2/3 已经真实可用**（实测：`/rss/code/KV-328.xml` 1.2s、
> `/rss/actress/EvkJ.xml` 6.1s、字幕优先与 `since` 均已验证）。
> **当前前线：`05` `10`**（两条都只关系需求 4）。

## Destination

一个 **Go 单二进制** 的 RSS 服务。部署后 qBittorrent 订阅 `/rss/code/{番号}.xml` 与
`/rss/actress/{id}.xml?<App演员页原样参数>`；每个 feed **每部作品恒发 1 条 item**，
**字幕优先**（有 `cnsub=true` 的磁链就发它，否则发 `magnets[0]`），`guid` = 纯 infohash；
女优订阅支持 `since=<日期>` 只追新；登录态由用户**手工从 App 导出的 token** 提供；
服务**无状态**、**不做鉴权**（纯内网）、默认监听 `127.0.0.1`；去重交给 qBittorrent。
数据来源于 **JavDB 官方 App 私有 API**（`https://jdforrepam.com/api/v1`）。

## Notes

**领域**：JavDB 官方 Android App 私有 API 接入 + RSS/Syndication 服务 + qBittorrent 集成。
**API 已不再需要逆向** —— 契约与实测证据集中在 [`notes/api-recon.md`](notes/api-recon.md)，
**每个 session 开工前先读它**。

**已定（不再重开）**
- 数据源 = 官方 App 私有 API（**不走** javdb.com 网页抓取）
- 语言/形态 = Go 单二进制
- Feed 粒度 = **每个订阅一个 feed URL**（不做聚合 feed）
- 状态模型 = **无状态**，服务端不持久化「已下发」；qBittorrent 按 guid 去重
- **槽位规则 = 字幕优先，每部作品恒发 1 条**：有 `cnsub=true` 就发第一条 `cnsub=true`，
  否则发 `magnets[0]`。（用户主动收窄了原需求里的「也返回」→ 不发两条）
- **`guid` = 纯 infohash**（磁链 `hash`），不带番号/槽位前缀 —— 跨 feed 自动去重、洗版自动重下、重启不变
- 登录态 = 用户手工从 App 导出 token，**不逆向登录接口、不做自动登录**
- 女优参数 = 同构透传 App 演员页的查询参数
- 部署 = 裸二进制为主 + 附 Dockerfile；配置 = 单 YAML + `SIGHUP` 重载
- **鉴权 = 不做**（纯内网）→ 因此**监听地址默认 `127.0.0.1`**，暴露到局域网必须显式改配置
- 日志 = 结构化 stdout；签名失效/契约变化必须显式 `WARN`，**不得静默返回空 feed**
- 本 effort **允许把执行纳入地图**（用户明确要求做到能跑）——但 ticket 仍以决策为主

**已确认的服务端事实（不要重新试探，直接用）**

> 完整实测契约（字段、样本、分页、耗时）在
> [`notes/api-recon.md`](notes/api-recon.md) §10 —— **接入前必读**。

- `jdsignature` 是 **HTTP 请求头**，值为 `"{ts}.{suffix}.{md5(ts + prefix)}"`，
  Prefix/Suffix 是硬编码常量。**已实测对 1.9.35 服务端有效。**
- 必带 8 个公共 query 参数，缺一即 `ParameterInvalid`。
- 中文字幕：电影级 `has_cnsub`，**磁链级 `cnsub`**。
  只有 `/api/v1/movies/{id}/magnets` 给磁链级 `cnsub`，`/search_magnet` 不给。
- ⚠️ **番号：`/api/v2/search` 是模糊搜索**。`q=KV-328` 返回 8 部不同番号的作品。
  必须按 `number` 精确比对，**绝不能取 `movies[0]`**。
- ⚠️ **女优：`filter_by` 是复合掩码** `{zone}:{letter}:{id}[:{main}:]:`，
  如 `0:a:EvkJ`。写成 `a` 或 `apmc` 会**静默返回全站最新作品**。
- **`limit` 上限 50**；`page` 分页无重叠，按 `release_date` 倒序。
- ⚠️ **签名有两种失败形态**：缺失 → HTTP 200 + `ParameterInvalid`；
  无效 → HTTP 400 + `InvalidSignature`。该 API **在 4xx 时仍返回标准信封**。

**代码骨架已存在（ticket 03），改代码前先看它**

```
cmd/javdb-rss/        组装与启动（SIGHUP 重载、优雅退出）
internal/catalog/     领域模型 + 槽位规则 + **Source 端口**
internal/feed/        RSS 渲染（纯函数，可字节级测试）
internal/appapi/      App API 传输层（`Signer` 与「番号解析」两个接口接缝）
internal/config/      YAML + SIGHUP 重载
internal/httpapi/     路由
internal/stub/        固定数据的假数据源
```

- **唯一外部边界是 `catalog.Source`**（`Code` / `Actress` 两个方法）。
  加缓存/后台刷新就在这层包装饰器，上层一行不改。
- 当前 **`provider: appapi` 是默认值且已可用**（实测打通）；`stub` 保留为离线调试通道。
  数据源在每次请求时重建客户端，因此 host / token / lang / device_uuid 都能热重载。
- **健康检查三端点**（ticket 02）：`/healthz`（存活，不掺上游）/
  `/readyz`（就绪）`/healthz/upstream`（机读详情，含 `signature_broken`）。
  改这块前先读 README 的「健康检查（k8s）」一节 —— 端点职责不能混。
- `CONTEXT.md` 是领域词汇表，改代码前先对齐用语。
- 测试全部离线；`go test ./...` / `go vet ./...` / `gofmt -l .` 应当全净。

**每个 session 应 consult 的 skill**
- 设计讨论：`/grilling` + `/domain-modeling`
- 交付前审计：`/code-review`
- **仅在签名失效时**才需要：`/root/clone/reverse-skill` + [`notes/dart-toolchain-probe.md`](notes/dart-toolchain-probe.md)

**可复用资产（注意边界）**
- [`FlanChanXwO/javdb-cli`](https://github.com/FlanChanXwO/javdb-cli) —— **MIT**，Go，活跃（3 天前还在推）。
  签名实现只有 3 行；它还完整复刻了动态域名解密（`endpoint/route/decrypt.go`）。
  它逆的是 **1.9.28**，我们目标是 **1.9.35**。
- `/root/clone/JAVDB_AutoSpider` —— 走**网页**，代码不可复用；但其字幕/无码磁链分类优先级
  （`UC无码破解 > UC > U无码破解 > U`）是「同一条磁链的优劣」的语义参考。

**环境约束**
- `javdb.com` 本机直连被 RST（需代理）；`jdforrepam.com` 直连可达。
- 官方 APK 发行源：<https://github.com/bdvajstudio/javdb/releases>（v1.9.35 已 sha256 核验一致）。

## Decisions so far

- [先例调研：JavDB App API 与 jdsignature 是否已有公开实现](issues/01-prior-art-jdsignature.md)
  — **能省掉静态逆向。** 找到 MIT 许可的 Go 先例 `FlanChanXwO/javdb-cli`：
  `jdsignature = "{ts}.{suffix}.{md5(ts+prefix)}"`（请求头）。已实测对 **1.9.35** 服务端有效，
  `/startup`、`/movies/latest`、`/actors/{id}`、`/movies/{id}/magnets` 均拿到真实数据；
  `/users/collected_actors` 只差 token。顺带解掉中文字幕判定（磁链级 `cnsub`）
  与演员页参数集。**唯一残留风险**：Prefix 源自 App 内 access key，App 升级可能使其失效。
- [配置与部署模型](issues/04-config-and-deploy.md)
  — 裸二进制 + 附 Dockerfile；**不做鉴权**（纯内网）因此监听默认 `127.0.0.1`；
  单 YAML + `SIGHUP` 重载；结构化 stdout，签名失效必须 `WARN` 而非静默空 feed。
- [中文字幕补充条的 feed 语义](issues/08-subtitle-supplement-semantics.md)
  — **字幕优先，每部作品恒发 1 条**；`guid` = 纯 infohash（跨 feed 去重、洗版自动重下）；
  标题字幕版加 `中文字幕 ·` 前缀；`pubDate` 取 `created_at`。
  已知后果：无字幕版先下、字幕版后到时磁盘留两份（清理属 qBittorrent 职责，已出界）。
  基名规则已被 ticket 03 修订为「作品标题 → 磁链 name → infohash」。
- [RSS 服务骨架](issues/03-service-skeleton.md)
  — 交付一个能跑、有离线测试的 Go 骨架。**唯一外部边界是 `catalog.Source`**；
  签名与番号解析各留一个接口接缝；监听默认 `127.0.0.1`；`provider: appapi` 直接启动失败。
  实测确认：guid 跨请求逐字节稳定、SIGHUP 重载失败保留旧配置。
  顺带修订了 08（标题基名）与 09（`since` 临时按 `release_date` 实现并打 WARN）。
  新暴露缺口：需求 4 在地图终点里没有落脚点 → 已开 ticket 10。
- [恢复 `jdsignature` 并在 Go 里复现](issues/02-recover-jdsignature.md)
  — **拷贝而非依赖**（依赖 javdb-cli SDK 实测要 37 个模块、二进制涨到 17.25MB）。
  已实测打通真实 `/api/v1/startup`。失效探针按用户要求做成 API 给 k8s 打：
  `/healthz` 不掺上游状态（避免重启循环）、`/readyz` 反映上游、
  `/healthz/upstream` 出机读详情含 `signature_broken`。
  **探针先做了一次故意签坏的实测，拓到了一个真 bug**：签名有 `InvalidSignature`(400)
  与 `ParameterInvalid`(200) 两种形态，而当时 `GetJSON` 按状态码提前短路导致
  `action` 丢失 —— 告警在最该响的时候是哑的。已修并写进 `notes/api-recon.md`。
- [数据接口契约测绘](issues/06-api-contract-survey.md)
  — **真实数据源已接入，需求 1/2/3 端到端可用**。两个高危陷阱已查出并用测试钉死：
  ① `/api/v2/search` 是**模糊搜索**（`q=KV-328` 返回 8 部不同番号），
  取 `movies[0]` 会静默发错片 → 改为按 `number` 精确比对，找不到报错；
  ② `filter_by` 是**复合掩码** `{zone}:{letter}:{id}[:{main}:]:`，
  写成 `a` 会静默返回**全站最新作品**而不是该女优的。
  另确定 `limit` 上限 **50**、feed 必须走 `/movies/{id}/magnets`（只有它给 `cnsub`），
  并量出成本：番号 feed ~1.2s、女优 feed（50 部）**~6.1s**（→ ticket 09 输入）。

## Not yet specified

<!-- 看得出方向、但还捏不成 ticket 的东西 -->

- **番号订阅 → item 的完整链路**：ticket 08 已定「字幕优先、恒 1 条、guid=infohash」，
  ticket 07 已定「透传 `/movies/tags` 参数」，但**「番号字符串 → 作品」这一步仍未定**：
  是走 `/api/v2/search?q=` 还是 `/api/v1/search_magnet`？一个番号多部作品时怎么办？
  这直接决定 `/rss/code/{番号}.xml` 能否实现。
- **`since=<日期>` 的比较字段**：`movies/latest` 有 `release_date`，但演员页列表里有没有、
  格式是什么，尚未确认。没有它需求 3 的「只追新」就悬空。
- **Dockerfile 与 systemd unit 尚未写**（ticket 04 定了这个形态，但它没被列进
  ticket 03 的产出）。二进制已能直接跑，镜像化是随时可做的小事，不构成决策，
  因此不开票 —— 等真需要部署时顺手做。
- **`size` 的单位未经核实**：ticket 03 假定 App API 的 `size` 是兆字节，
  并据此填 `enclosure length`。qBittorrent 不会用它做判断，所以不影响功能，
  但 ticket 06 测接口时顺手核一下。
- **需求 4 的呈现形态尚未定**：`Source` 端口里根本没有位置放「收藏列表」——
  ticket 03 实现时才意识到地图的终点描述漏了这一环。见 ticket 10。
- **Prefix 失效的检测与应对**：Prefix 由 App 内 access key 派生，App 升级或服务端轮换
  都可能使其作废。需要一个「多久探一次、失效时怎么告警」的判断。
  也要看一眼 javdb-cli 是否已跟进 —— 它活跃，很可能比我们先发现。
- **token 的获取成本**：用户要手工从 App 导出。导出路径是 App 本地存储
  （sqflite/hive/shared_preferences）还是需要 root/adb？多久过期一次？
  这决定第 4 条需求的**实际可用性** —— 如果一周一导，体验会很差。
- **女优 feed 的成本量级**：`/api/v1/actors/EvkJ` 显示 `videos_count: 229`，
  但作品列表走 `/movies/tags`（有 `page`/`limit`）。到底一页能拿多少、
  要不要为每部再拉一次 magnets —— 量化之后才知道 ticket 09 要不要真做缓存。
- **磁链数组顺序的稳定性**：用户选了「信任 App 顺序」，但还没确认这个顺序是稳定排序
  还是每次不同。若不稳定，`guid` 会抖，qBittorrent 会重复下载。
- **番号消歧**：`/api/v2/search?q=ABC-123` 是否会返回多部（合集、同名不同片商）。
- **TLS 指纹**：javdb-cli 用 utls 伪装；我们朴素 urllib 也通了。长跑批量请求时是否需要？
- **TLS/HTTP 长连接下的 WAF 行为**：researcher 标为未验证项。

## Out of scope

- **清理旧版磁链（洗版去重）** —— 字幕版出现后旧的无字幕版会留在磁盘上。
  删除旧文件是 qBittorrent 的职责，本服务只负责交出发什么磁链。
- **静态逆向 libapp.so** —— 先例已存在，本 effort 不做。
  备灾清单留在 [`notes/dart-toolchain-probe.md`](notes/dart-toolchain-probe.md)，
  等 Prefix 真失效时作为**新 effort** 启动，不在这里毕业。
- **逆向登录接口 / 自动登录 / 验证码处理** —— 用户选择手工导出 token。
- **写回 App**：收藏、取消收藏、`/collect_actions`、`/following_tags/batch_push` 等一切写操作。
- **下载与播放链路**：`/movies/%s/play`、`/resume_play`、`magnet_apps`、PikPak 桥接、
  qBittorrent 上传/分类/洗版。RSS 只负责把磁链交出去。
- **javdb.com 网页抓取路线** —— 数据源已定为 App API；整体出界。
- **非订阅类端点**：排行榜、Top、推荐、评论、文章、女优推荐、广告、钱包/推广/提现。
- **多用户 / 公网多租户**：单实例、单人使用。
