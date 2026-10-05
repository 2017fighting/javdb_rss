# javdb-rss 部署

服务在集群外已经跑得起来（初次交付与 followups 两轮共 25 张票全部结票），
这一轮的终点只有一句话：**它在自家集群里长期跑着，而且「签名常量失效」这件事
真的会被告知。**

四个跨两个仓库的部分：**home-ops**（私有：`apps/javdb-rss/` 与两行注册）
与 **javdb_rss**（公开：`/metrics`、两条 ADR、README）。

> 读票之前先读本文的 Decisions so far —— 票面只说「还剩什么要做」，
> 地图说「为什么这么定、否掉了什么」。

## 票

| # | 票 | 阻塞于 |
|---|---|---|
| 01 | [home-ops 应用注册](issues/01-home-ops-registration.md) | ~~02~~ 已 resolved（2026-10-05） |
| 02 | [app 侧 `/metrics`](issues/02-app-metrics-endpoint.md) | 无 |
| 03 | [ADR 0002/0003 与文档](issues/03-docs-adr-0002-0003.md) | 01 |
| 04 | [真实验收（含人工步骤）](issues/04-real-acceptance.md) | 01 02 03 |

01 依赖 02：`VMPodScrape` 打不到不存在的端点，先有 `/metrics` 再谈采集。
04 排最后：它的判据就是前三个的产物。

## Decisions so far

- **入口：页面走 Edge SSO，feed 走集群内 Service 名。**
  `javdb-rss.raenzo.com` 挂 authelia forward-auth（`edge-sso.yaml` 一行 +
  路由的 `extensionRef` filter，registry 的 deps 加 `authelia`）；
  qBittorrent 用 `http://javdb-rss.javdb-rss.svc.cluster.local:8080/rss/…` 订 feed。
  理由是**服务不做鉴权**（它自己 `deploy/k8s.yaml` 就写着这句），而唯一的机器消费端
  就在同一个集群里 —— 于是锁在外面的只有「人看的那一面」，feed 根本不经过 edge。
  **否掉的**：① 不加 SSO 的路由（LAN 里任何设备都能读到 144 位收藏与订阅）；
  ② `sidecar-*` 那套 authelia `bypass`（能让一个域名同时服务人和集群内客户端，
  但要改 authelia 的**全局** access_control，且本服务的能见度从此挂在一条 bypass 上）；
  ③ 只 ClusterIP 不做路由（上一轮整个 UI effort 的成果就只能 port-forward 打开）。
  顺带：新增 HTTPRoute **只让名字在 LAN 内**解析（Cluster DNS 从集群状态派生），
  隧道的公网 Caddy 上没有这条路由 ⇒ 互联网访问 404（实测口径见
  `home-ops/apps/netspeed/httproute.yaml` 的注释）。

- **凭据：SOPS Secret + `envFrom` 的 `JAVDB_TOKEN`。**
  `apps/javdb-rss/javdb-rss-secret.sops.yaml`（Opaque）→ `envFrom.secretRef`，
  配 reloader 注解 ⇒ 轮换＝手机上重跑导出脚本 → `sops edit` → push → 自动滚动。
  **`app_api.token_file` 留空**：留空是「用默认路径」（配置文件旁边的 `token.json`），
  读不到即匿名 —— 而 env 优先于文件，且**空值不算「设置了」**（这条是刻意的，
  否则 compose 里一个没填的 env 会把文件里可用的 token 顶掉）。
  **否掉的**：把 token 挂成 `/token/token.json`（要为一个已有 env 通道的东西
  多写一段挂载，且 `deploy/k8s.yaml` 那个形状会与集群里这份变成两种说法）。

- **状态：pin → `truenas-nfs-retain` 64Mi PVC，而且把「它没有恢复点」写下来。**
  pin 是本服务**唯一**的持久状态（丢了就丢 guid，上游无法重建同一选择）。
  诚实的账：CSI 的 `datasetPath` 是 `nova/k8s`，而仓里声明的两条宿主快照任务是
  `nova/data` 与 `nova/important/app` —— `nova/k8s` **不在里面**；集群里没有
  VolSync/restic 之类通用 PVC 备份；cnpg 的 Recovery point 只覆盖数据库卷。
  因此：pin 无恢复点，丢了就按规则重选，代价是约 1/4 作品换磁链（有界、且看得见）。
  **否掉的**：在 NAS 上加一条 `nova/k8s` 的快照任务（仓外、看不见的第二处声明，
  又是一条「无 manifest 声明、无告警看着」的东西）；`emptyDir`（Pod 生命周期 ——
  节点漂移与滚动重启正是最需要 pin 活下来的时刻，repo 里已经写过这条）。

- **调度：`profile: proxy`。** 上游 `jdforrepam.com` 从路由器隧道出去。判错是**可见的**
  失败（502 + `/readyz` 503 + 告警），不会静默错数据 —— 与项目那条原则一致。

- **告警：app 出 `/metrics`，home-ops 出一条规则；`absent` 故意不做。**
  app 侧：`client_golang` + **默认 registry**，指标只是把 `health.Tracker` 再讲一遍
  （`checked` / `ok` / `signature_broken` / `last_check_timestamp_seconds` /
  `check_latency_seconds` + `build_info{version}`）。**`checked` 单独成一条**，
  因为 Tracker 刻意区分「从未检查过」与「检查过且失败」——把没探过说成健康是错的。
  home-ops 侧：`VMPodScrape` + `VMRule/JavdbRssSignatureBroken`（`== 1`, `for: 1h`,
  warning）→ Alertmanager 发邮件。
  **`absent` 不做**：Pod 自己的死活归集群那层（`PodStuckContainerCreating` /
  `KubeContainerWaiting` / Flux 那层），而本服务是 `Recreate` ——
  每次改配置都会让指标短暂消失，加 absent 是给自己造假报（hath 那份 vmrule 同取舍）。
  **「版本漂移」也故意不开票**：`build_info` 与仓库钉的 tag 不一致是**正常**状态
  （发版在前、Renovate 的 PR 在后），拿它报警等于每月假报一次。

- **坐标：`deploy/k8s.yaml` 留作示例，README 写明哪份是实跑。**
  现在镜像 tag 有两处、由 `internal/config/deploy_image_test.go` 守着；集群上之后是
  **三处**，而那份测试跨不到仓库外。选择是「三处坐标，但每处自报身份」：
  示例之间仍由测试守着不许漂，home-ops 那份交给 Renovate（内置 `helm-values`
  管理器本来就扫 `helmrelease.yaml`，moviepilot 就是这么被升的）。
  home-ops 是**私有**仓库，所以 README 里只写一句，不给链接（给了也点不开）。

- **验收：真 qBittorrent 订上 + guid 跨 Pod 重建不变 + 告警真的响一次。**
  本仓库的传统是「真能用才算数」（初次交付是拿真实 qBittorrent v5.2.4 走通的）。
  外加一条来自 home-ops 自己事故的教训（`d01a2bf`：刚上线的规则永远不会触发）：
  **一条从没响过的告警不算告警**。

- **记账：一个 effort、四张票；ADR 拆两条**（`0002` 部署形态、`0003` 指标面与依赖破例）。
  拆两条的理由：`0003` 记的是一个**有意的破例**（这个仓库在最接近的一次取舍里
  —— 不依赖 javdb-cli 的 SDK —— 选的是相反方向，见 `THIRD_PARTY_NOTICES.md`），
  埋进部署决定的子节里，下一个读的人很可能翻不到。
  依赖详情：投影落在 `internal/httpapi/metrics.go`（`/metrics` 是一个端点，
  而它与 `/healthz/upstream` 共享同一个判断与同一个 `Version`；放进
  `internal/health` 反而要把版本号与分类函数注入那个刻意零依赖的包）；
  registry 用**每个 Server 自己的**，不是全局 `DefaultRegisterer`（后者是进程级
  状态，测试里第二个 Server 会静默复用第一个的序列）—— 两处都是写代码时改的，
  详细理由在 [02 的 Answer](issues/02-app-metrics-endpoint.md)。

- **词表：`CONTEXT.md` 补了三条**（`上游` / `签名常量` / `上游健康`，新增 `### 访问上游`）。
  `上游` 是补欠账：全仓库都在用它，从未定义过。

## 与其它 effort 的关系

- `.scratch/javdb-rss/`：初次交付（10 张票，全 resolved）。签名失效检测与
  `/healthz/upstream` 出自那张图的票 02，本轮的告警是它的兑现点。
- `.scratch/javdb-rss-followups/`：收尾（15 张票，全 resolved）。票 13/15 定的
  「发布走 GHCR / 部署拉已发布镜像」是本轮能钉 `1.0.0` 的前提。
- `.scratch/javdb-rss-ui/`：订阅链接生成器（8 张票，全 resolved）。本轮的 SSO
  就是为它（`GET /`）才需要的 —— 它是这个服务唯一给人看的门面。

## Not yet specified

<!-- 看得出方向、但还捏不成票的东西 -->

- **更宽的仪表化**：请求量、feed 生成耗时、上游请求耗时的 Histogram。本轮只把
  Tracker 的状态讲了一遍；要铺开得先想清指标设计（哪些标签、什么分桶），
  否则先长出来的是一堆没人查的序列。
- **`build_info` 要不要接成「漂移告警」**：见上，现在这样会每月假报一次。
  真要接，得先回答「发版到 Renovate 合并之间那段窗口算不算异常」。
- **单会话账号是否该进词表**：它是一条**约束**（登录会踢掉手机、token 无 exp），
  不是一个领域词，本轮只加了 `上游` / `签名常量` / `上游健康` 三条。
- **NAS 侧为 `nova/k8s` 加宿主快照任务**：那是 NAS 运维，不是集群状态；
  进了就是第三条「仓里看不见的声明」。要加的话应当先想清楚它怎么被看见。
- **`THIRD_PARTY_NOTICES.md` 是否要记 client_golang**：它的措辞是「唯一 vendored
  的第三方代码」，而 go module 依赖不是 vendored（镜像里没有源码）—— 本轮不动它，
  理由记在 ADR-0003 里。
