# Map: JavDB App 订阅 → qBittorrent RSS（wayfinder:map）

> **状态更新 2026-09-28**：本图在 charting 当天经历了一次**关键路径塌缩**。
> 原以为「必须先逆向 APK」是整条路的瓶颈，结果发现已有 MIT 许可的 Go 先例
> （[`FlanChanXwO/javdb-cli`](https://github.com/FlanChanXwO/javdb-cli)）把 `jdsignature`
> 完整实现，且已实测对我们目标版本的服务端有效。
> **9 张已关闭**（`01` `02` `03` `04` `06` `08` `09`），并新开出 `10`。
> **需求 1/2/3 已经真实可用**（番号 feed 0.95s、女优 feed 1.8s、
> 字幕优先与 `since` 均已实测验证）。
> **当前前线：`05` `10`**（两条都只关系需求 4），外加 `07`（实现已完成，只差确认）。

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
- [请求成本模型：缓存、并发与限流](issues/09-cost-cache-throttle.md)
  — **实测推翻了原推荐。** 响应头显示上游只需 218ms（`X-Runtime` 仅 5ms），
  那 6.1s 是我们自己串行发 N+1 造成的。**并行化（默认并发 8）把女优 feed
  从 6.1s 降到 1.8s**，因此不需要引入缓存这个新概念（无陈旧数据风险）。
  另加 singleflight 合并并发相同请求（不是缓存，不存任何东西）。
  压测并发 32 / 34 请求零失败、无限流。新增 `?pages=N`（上限 20 页）供建库场景。
- [配置与部署模型](issues/04-config-and-deploy.md) —— *实施补记见票面*
  — 形态已全部落地：`Dockerfile`、`.dockerignore`、systemd unit（含加固）、
  docker-compose、k8s（含**签名失效告警 CronJob**）、`Makefile`、CI。
  三个要点：容器内必须监听 `0.0.0.0`（可见性由端口映射决定）；
  `ca-certificates` 是必需项；k8s 的 liveness/readiness 分工是 ticket 02 的兑现点。
  **未验证**：docker daemon 未运行，镜像未真正构建过。

## Not yet specified

<!-- 看得出方向、但还捏不成 ticket 的东西。
     2026-09-30 清理：删掉 6 条在 02/06/09 落地后已解决的（番号链路端点、
     since 比较字段、Prefix 失效检测、女优 feed 成本量级、番号消歧），
     以及 2 条其实已是 live ticket 的（token 成本 → 05，需求 4 形态 → 10）。 -->

- **磁链数组顺序的稳定性（已部分验证）**：用户选了「信任 App 顺序」。
  2026-09-30 实测：4 部作品 × 每部 4 次请求，顺序**完全一致** ——
  排除了「每次请求随机」这个最坏情况。
  仍未知的是**跨天/跨周是否稳定**：如果排序依据是做种数或时间，它会随时间漂移，
  那时同一部作品会选中不同磁链 → `guid` 抖 → qBittorrent 重复下载。
  要彻底确认需要隔几天再比一次。
- **TLS 指纹**：javdb-cli 用 utls 伪装 TLS 指纹；我们用朴素 `net/http` 也通了。
  短请求没问题，但长跑 / 高并发下是否被 Cloudflare 挑出来，未验证。
- **WAF 与长连接行为**：实测并发 32 未被限流，但那是短时压测。
  持续高频轮询下 Cloudflare 是否会开始拦，未知。
- **`size` 的单位**：假定为兆字节并据此填 `enclosure length`。
  qBittorrent 不拿它做判断，所以不影响功能 —— 价值低，但确实没核实。

## 验收状态（2026-09-30 用真实 qBittorrent 走通）

<!-- 这一节记录**真实验收**结果，不是代码写完就算。
     wayfinder 的终点是「qBittorrent 能订阅」，代理指标（RSS 是合法 XML）不算数。 -->

用 Docker 起了一个真实的 qBittorrent（v5.2.4）与我们自己的镜像，同网互通，
通过 qBittorrent 的 Web API 完成端到端验收：

| 验收项 | 结果 |
|---|---|
| **Docker 镜像能构建** | ✅ `docker build` 成功（此前从未构建过） |
| **qBittorrent 能订阅番号 feed** | ✅ 正确解出 `guid` / `title` / `torrentURL` / `date` / `category`，`hasError: false` |
| **qBittorrent 能订阅女优 feed** | ✅ 17 条，`title: JavDB · EvkJ` |
| **磁链能被 BitTorrent 引擎接受** | ✅ `success_count: 1`，正确解出 infohash，`dn` 渲染为正常空格（`%20` 修复生效） |
| **`guid` 跨轮询稳定** | ✅ 连续刷新 3 次 article id 不变 → 不会重复下载 |
| **需求 3 `since`** | ✅ `17 条 → 5 条`（`?since=2026-06-01`） |
| **需求 2 字幕优先** | ✅ feed 里出现「中文字幕 ·」条目 |
| **健康三端点** | ✅ `/healthz` `ok`、`/readyz` `ok`、`/healthz/upstream` `signature_broken: false` |

**至此需求 1/2/3 不再是「代码写完了」，而是「真的能用」。**

仍未验收的：

- **实际下载行为** —— 刻意没做。验收用的是停止态加入（`stopped=true`），
  零流量、验完即删。磁链能被接受这一点已证明；「下不下」是 qBittorrent 的策略。
- **CI 从未运行过** —— workflow 已写，仓库没 push 过。
- **token 路径从未走通** —— 管路写好了，没有真 token（依赖 ticket 05）。

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
