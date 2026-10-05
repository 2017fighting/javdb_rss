# 在自家集群里跑：人走 SSO 的门，机器走集群内的名

把本服务部署进 home-ops（Talos + Flux）时，**入口、凭据、状态**三件事一起定了：
人的那一面（`javdb-rss.raenzo.com`）挂 Edge SSO；机器的那一面（qBittorrent 取 feed）
走集群内的 Service 名，**根本不经过 edge**；token 以 `JAVDB_TOKEN` 环境变量从 SOPS Secret 进；
pin 落一个 `truenas-nfs-retain` 的 PVC，且**它没有恢复点**。坐标上沿用 ADR-0001 的结论：
`deploy/` 下的三份是**示例**，作者自己的实例在另一个（私有）仓库里跑。

## 背景

初次交付与 followups 两轮（共 25 张票）之后，服务在集群外已经「真的能用」——
真 qBittorrent 订阅、guid 稳定、`/collected` 用真 token 通过。这一轮要它
**在自家集群里长期跑着，并且「签名常量失效」这件事真的会被告知**。

三件事必须一起定，因为它们是同一件事的三面：服务**不做鉴权**（它自己的
`deploy/k8s.yaml` 就写着这句），所以「谁能访问它」完全由部署形态决定；
而它的唯一机器消费端（qbittorrent-private）**就在同一个集群里**。

## Considered Options

- **页面与 feed 共用一个域名，人走 SSO、集群内走 authelia `bypass`。** 集群里已有先例
  （`sidecar-qbittorrent*.raenzo.com` 那套：领域相同，集群/路由器网段判 `bypass`）。
  否掉的理由：它要改 authelia 的**全局** `access_control`，而本服务的能见度从此挂在
  一条 bypass 规则上 —— 那条规则一旦被谁的改动碰到，服务要么对外裸奔、要么把
  qBittorrent 挡在门外，两个方向都不会有人立刻发现。
- **路由不加 SSO。** 最省事，也仍然**不出公网**（实测口径：新增 HTTPRoute 只让名字在
  LAN 内解析 —— Cluster DNS 从集群状态派生答案，而隧道的公网 Caddy 上没有这条路由，
  互联网访问 404）。否掉：局域网里任何设备（访客手机、别人的服务）都能读到 144 位收藏
  与整份订阅，而这一切只是一层 forward-auth 的距离。
- **只做 ClusterIP、不做路由。** 否掉：上一轮整个 UI effort（订阅链接生成器）的产物
  就只能靠 `kubectl port-forward` 看，那个 effort 的目的恰恰是「在浏览器里打开它」。
- **token 挂成文件 `/token/token.json`（`deploy/k8s.yaml` 的形状）。** 否掉：env 通道
  本来就存在且**优先**，为一个已经存在的东西多写一段挂载，还让集群里这份与示例那份
  变成两种说法。`token_file` 留空不是省略，是「用默认路径」——读不到即匿名，
  而空 env **不算**「设置了」（这条是刻意的，否则 compose 里一个没填的 env
  会把文件里可用的 token 顶掉）。
- **给 pin 也做一份备份。** 两条路都被否：在 NAS 上加 `nova/k8s` 的宿主快照任务 ——
  那是仓库外、看不见的第二处声明（CONTEXT.md 自己把这类东西列为缺点：无 manifest
  声明、无告警看着）；集群内再存一份 —— 为几百 KB 的文件搭一条备份路径，而拷出来那份
  自己也会腐。**结论是「没有恢复点」被写下来**，而不是被忘掉。
- **`emptyDir` 放 pin。** 明确否掉：它是 **Pod** 生命周期的，而节点漂移与滚动重启
  恰恰是 pin 最需要活下来的时刻（repo 的 `deploy/k8s.yaml` 早写过这条）。

## Consequences

- **集群外订不了 feed。** 这是选定的取舍，不是缺陷：要改就是一次有意识的动作
  （加一条 authelia `bypass`，或为 feed 单开一条不加 filter 的路由）。
- **`base_url` 是「feed 被消费的地址」，不是人用的页面。** channel 的 `<link>` 自称
  `http://javdb-rss.javdb-rss.svc.cluster.local:8080` —— 与 qbittorrent-private 订阅时用的
  **同一个字符串**，因此这份 feed 不会自称一个它唯一的机器消费端解析不了的名字。
  代价已接受：在浏览器里点那个链接会死（LAN 里的浏览器解析不了 `.svc.cluster.local`），
  而人用的入口是那个走 SSO 的页面 —— 它是一个书签，不是这个链接。
  ⚠️ 第一版写的是 `https://javdb-rss.raenzo.com`（「人用的域名」），当天改成现在这个值：
  那一步把「人能不能点」当成了 `<link>` 的职责，而它其实是 **feed 的自指**。
  范围很小：item 的 link 恒为 magnet、`guid` 恒为 infohash，而**取 feed 用的是客户端
  自己填的地址**（`feedURL()` 只是把 base_url 前缀到请求路径上）。
- **pin 的账是空的。** CSI 的 `datasetPath` 是 `nova/k8s`，而仓里声明的两条宿主快照任务
  （`nova/data`、`nova/important/app`）**不覆盖它**；集群里也没有 VolSync/restic 之类
  通用 PVC 备份；cnpg 的 Recovery point 只覆盖数据库卷。丢了就按规则重选磁链，
  代价是约 1/4 作品换 `guid`（有界、且看得见）。实测的另一面：PVC 虽然请求 64Mi，
  绑到的 PV 是 **1Gi**（`truenas-nfs-retain` 那边有最小/默认配额）—— 两种尺寸都远远够用。
- **三处镜像坐标，各报身份**（ADR-0001 的延伸）：`deploy/docker-compose.yml` 与
  `deploy/k8s.yaml` 两处示例之间**由测试守着不许漂**，第三处（私有仓库里的实跑）
  交给 Renovate 自动提 PR。README 里只写一句「示例 ≠ 实跑」，不给链接
  —— 那个仓库是私有的，给了也点不开。
- **单写者是硬约束，靠 app-template 的默认 `Recreate` 白拿**（已在 live 集群核对
  `bark`/`moviepilot`）：pin 用 flock 做单写者，两个 Pod 同时持有它会各钉一套磁链，
  同一部作品于是有两个 `guid`。第一版实现里这一条是显式写的 `strategy`，
  后来发现它是默认值，就不写了 —— 但**它现在是承载性的**，改 controller 类型前先想这条。
