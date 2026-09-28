# Map: JavDB App 订阅 → qBittorrent RSS（wayfinder:map）

> **状态更新 2026-09-28**：本图在 charting 当天经历了一次**关键路径塌缩**。
> 原以为「必须先逆向 APK」是整条路的瓶颈，结果发现已有 MIT 许可的 Go 先例
> （[`FlanChanXwO/javdb-cli`](https://github.com/FlanChanXwO/javdb-cli)）把 `jdsignature`
> 完整实现，且已实测对我们目标版本的服务端有效。
> 9 张票中 4 张被降级或部分解掉。见 **Decisions so far** 的第一条。

## Destination

一个 **Go 单二进制** 的 RSS 服务。部署后 qBittorrent 订阅 `/rss/code/{番号}.xml` 与
`/rss/actress/{id}.xml?<App演员页原样参数>`；每个 feed 只吐 **App 返回顺序第 0 条的磁链**，
外加（若存在）**第 0 条 `cnsub=true` 的磁链**；女优订阅支持 `since=<日期>` 只追新；
登录态由用户**手工从 App 导出的 token** 提供；服务**无状态**，去重交给 qBittorrent。
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
- 「第一条磁链」= 信任 App 返回顺序的第 0 条，不自建排序规则
- 登录态 = 用户手工从 App 导出 token，**不逆向登录接口、不做自动登录**
- 女优参数 = 同构透传 App 演员页的查询参数
- 本 effort **允许把执行纳入地图**（用户明确要求做到能跑）——但 ticket 仍以决策为主

**已确认的服务端事实（不要重新试探，直接用）**
- `jdsignature` 是 **HTTP 请求头**，值为 `"{ts}.{suffix}.{md5(ts + prefix)}"`，
  Prefix/Suffix 是硬编码常量。**已实测对 1.9.35 服务端有效。**
- 必带 8 个公共 query 参数：`app_channel app_version app_version_number platform
  system_version device_model device_name device_uuid`，缺一即 `ParameterInvalid`。
- 中文字幕：电影级 `has_cnsub`，**磁链级 `cnsub`** —— 不需要解析文件名。
- 磁链自带 `hash`（infohash），可直接做 guid。
- 女优作品列表 = `GET /api/v1/movies/tags?filter_by&filter_by_tags&sort_by&order_by&page&limit`。

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

## Not yet specified

<!-- 看得出方向、但还捏不成 ticket 的东西 -->

- **Prefix 失效的检测与应对**：Prefix 由 App 内 access key 派生，App 升级或服务端轮换
  都可能使其作废。需要一个「多久探一次、失效时怎么告警」的判断，才知道要不要为它建票。
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
