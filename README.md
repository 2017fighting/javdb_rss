# javdb-rss

把 JavDB 官方 App 里的订阅渲染成 qBittorrent 可以订阅的 RSS。

数据来自官方 App 自己的私有 JSON API —— 不是抓网页，因此字段干净、结构稳定。
去重完全交给 qBittorrent 按 guid 处理。

服务几乎是**无状态**的：它不记录「已经发过什么」。唯一的例外是 **pin** ——
它记住「每个作品上次下发的是哪条磁链」（见下）。除此之外，订阅内容完全由 URL 决定。

这是一个单人自用的内网服务。**它不做任何鉴权。**

## pin：本服务唯一的持久状态

feed 条目的身份（`guid`）就是磁链的 infohash，而**选哪条磁链**决定 infohash。
上游的候选顺序不保证稳定（实测：它既不是时间序，也不可用任何字段重放）。
不记住选择的话，上游一变、`guid` 就变，qBittorrent 会把它当成新内容再下一份 ——
而且是**静默的**，磁盘上多一份文件也没人告诉你。

所以服务把「每个作品上次选中的是哪条磁链」持久化成一张表（`pin.json`），
之后即使上游候选变化也继续发它。

**运维需要知道的三件事**：

1. **pin 不能丢。** 丢了它会按规则重新选 → 约 1/4 的作品 `guid` 变化 →
   qBittorrent 重复下载。上游无法重建出同一选择，因此 **丢 pin 就是丢 guid**。
   各部署方式都把它放在持久位置（见下）。
2. **pin 不可关闭。** 配置里的 `app_api.pin_file` 留空是「用默认路径」
   （配置文件旁边的 `pin.json`），不是「禁用钉住」。默认关闭会让升级后的实例
   静默退回有抖动的行为。
3. **只支持单副本。** pin 用文件锁保证单写者，第二个指向同一文件的实例会
   **拒绝启动**（可见的失败，好过两个 guid）。

pin 就是一个可读的 JSON 文件，可以直接看、直接改、直接备份：

```json
{"version": 1, "pins": {"aBc123": {"infohash": "0e8f...", "name": "KV-328",
  "size_mib": 3110, "cnsub": false, "created_at": "09/27/2026",
  "pinned_at": "2026-09-30T12:00:00Z", "source": "upstream"}}}
```

`source` 是这条磁链来自哪个数据来源（`upstream` 或 `mteam`）—— **它不是装饰**：
「首发冻结」靠它成立（候选池的来源与 pin 的来源不同时永不切换），
而 M-Team 那一条的 `<enclosure>` 也是靠它才发种子文件而不是磁链。
旧文件没有这一位，读成 `upstream`；写错名字会**拒绝启动**（不静默当上游）。

手工删掉某一条（「这个作品想重选」）后 `kill -HUP` 重读即可。
文件损坏或版本不认识时会**拒绝启动**，而不是用空表静默覆盖。

## 现在能跑到哪一步

| 需求 | 状态 |
|---|---|
| 番号订阅，只发一条磁链 | ✅ 可用 |
| 中文字幕优先 | ✅ 可用（服务端直接给 `cnsub` 字段） |
| 女优订阅 + 参数透传 + 只追新 | ✅ 可用（缓存已定为**不做**：并行化 + singleflight 就够；`since` 语义也已定稿） |
| 读取 App 里收藏的女优 | ✅ 可用（`GET /collected`，2026-10-05 用真 token 验收：144 位、6 位男优、未截断） |
| 「想看」feed | ✅ 可用（`GET /rss/want.xml`，2026-10-04 用真实 token 与真实 qBittorrent 验收） |
| 清单订阅（自己的片单） | ✅ 可用（`GET /rss/list/{id}.xml` 与发现端点 `GET /collected_lists`，2026-10-04 用真 token 对着 5 份真实清单验收） |
| 全站订阅：标签 + 年份 + 月份 + 时长 | ✅ 可用（`GET /rss/tags/{片库号}.xml`，2026-10-04 用真机抓包反推出掩码语法后逐槽位验收） |
| 订阅链接生成器页面 | ✅ 可用（浏览器打开 `GET /`，见下面「[打开页面](#打开页面)」） |
| M-Team 优先（可选，默认关） | ✅ 可用（2026-10-14 用真 key + 真 M-Team + **真 qBittorrent v5.2.4** 验收：qbt 算出的 hash 与 feed 的 `guid` 逐位一致）。见下面「[M-Team 优先](#m-team-优先可选)」。⚠️ 集群里那份部署与**你自己的 qbt** 没被这次验收覆盖 |
| 主动推送（推给 qBittorrent + 回写「看过」） | ⛔ 本次不做（2026-10-04 用户决定）；已定的形状与 qbt/App API 契约留在 [`notes/want-push-deferred.md`](.scratch/javdb-rss/notes/want-push-deferred.md) |

数据源是**真实的 JavDB App 私有 API**。`provider: stub` 是离线调试通道。

## 跑起来

```bash
make build                      # 或 go build -o javdb-rss ./cmd/javdb-rss
cp config.example.yaml config.yaml
./javdb-rss -config config.yaml
```

改完配置后发 SIGHUP 即可生效，不用重启：

```bash
kill -HUP $(pidof javdb-rss)
```

SIGHUP 会同时重载配置**与重读 pin 表**（手工删掉一条 pin 后用它生效）。
两件事失败时都**保留旧值**并记日志 —— 一个手滑改坏的文件不该让正在服务的实例失去配置或状态。

`make help` 列出全部命令。

## 打开页面

服务自己把页面端出来：浏览器打开 `http://127.0.0.1:8080/`（或你配置的 `listen:`
地址）就是**订阅链接生成器**。挑女优、挑标签、挑清单，底部常驻的「待复制」随时备着
一串能直接粘进 qBittorrent 的 URL。

- 它是**服务的一个端点**，不是另起的前端工程：资产（`internal/webui/`）用 `embed`
  内嵌进单二进制，构建与运行都**不需要 Node**，页面也不依赖任何 CDN、第三方脚本或字体。
- 页面上只有**语义参数**（`main` / `tags` / `year` / `month` / `duration` / `since`），
  `filter_by` 与 `filter_by_tags` 掩码一律由服务构造 —— 那个「多一段就静默失效」的坑
  只有一处实现。
- 选择与「待复制」**只活在页面内存里**：刷新即空，服务端不存任何状态（与「URL 即订阅」
  一致）。只有两个 UI 偏好进 `localStorage`：服务地址与主题（深色默认跟随系统、可手动覆盖）。
- 页面**不做鉴权**，与「本服务不做鉴权、默认只监听 loopback」一致。它也**不新增数据暴露**：
  只是把已有的发现端点组织起来。
- 只在精确的根路径 `GET /` 上注册。`/rss/want` 这类打错的 feed 路径仍是干净的 **404** ——
  不会被喂成一张 HTML 页面（qBittorrent 只会说「这不是一个 feed」，用户也就看不出
  自己是少写了个 `.xml`）。

### 没有 token 时

`/collected` 与 `/collected_lists` 返回 503，页面照实显示它们的原文。各区域行为：

| 区域 | 没配 token 时 |
|---|---|
| 收藏女优 | 读不到。页面说「读不到收藏：还没配置 token」，指向 `app_api.token_file` |
| 某位女优的标签 | **照旧可用** —— 词表与女优标签都是匿名可读的，**直接手输她的 id** 即可 |
| 全站标签 | **照旧可用**（`GET /tags?type=` 匿名可读） |
| 清单 | 输出**禁用**并说明缺 `token_file` —— 它读的是 App 里的标记，没 token 一定 503 |
| 想看 | 同上：禁用并说明，页面上**不给**那条链接 |

标签区不被「收藏读不到」连坐，是因为它的数据依赖里根本没有 token ——
这不是宽容，是不让一个无关的失败波及另一个功能。

### 白名单生效时

配了 `feeds` 白名单（女优 / 清单 / 片库任一类）时，页面顶部会说明
**「没列出的订阅会返回 404」**：链接在页面上看得见，但不在白名单里的那些粘进
qBittorrent 只会得到 404。

页面据此的两种处理：

- **片库**：没被放行的片库直接 `disabled`（`feeds.zones` 是整段闸门，一个只列了 `codes`
  的 `feeds` 段也会让它 404），换一个片库或改配置。
- **女优 / 清单**：仍会渲染（它们的 id 来自你真实的收藏，不是白名单），靠顶部那句话提醒。

这条状态取自服务自己的 `/version`（`whitelist: {actresses, lists, zones}`）；读不到时
**不出**这条提示（宁可不说，也不编），没配白名单时同样不出。

上游是否正常则由页面头部的状态 chip 显示，文案就是服务自己 `/readyz` 的原文。

#### 实测（2026-10-05，真实账号 + 真实 qBittorrent v5.2.4）

照需求 1/2/3 的先例跑了一遍端到端，用的是**真上游 + 真 token**，
服务与 qBittorrent 各跑一个容器、同一个 Docker 网络：

1. **页面**（容器里的真服务，`provider: appapi` + 真 token）：收藏区读出真实 138 位女优
   （144 位里 6 位男优被默认过滤），标签词表按 `type=0` 从上游取，上游 chip 显示
   「上游正常」。切到「全站标签」选 **眼鏡**（tag `3`）+ **2023 年**，页面生成：

   ```
   http://javdb-rss:8080/rss/tags/0.xml?main=m&tags=3&year=2023
   ```

   逐字符检查：**不含 `filter_by` / `filter_by_tags`** —— 掩码由服务拼。
   这条链接也进了底部「待复制」。
2. **qBittorrent**：经 Web API 订阅这条 URL（该版本的加 feed 端点是
   `POST /api/v2/rss/addFeed`，不是文档里常见的 `/api/v2/rss/add` —— 后者在这版返回
   404，踩过一次），得到 **50 条条目**，`hasError: false`，`lastBuildDate` 正确解析，标题为
   「JavDB · 全站（有码 · 主属性 m · 1 个标签 · 2023 年）」；每条都有 `guid`
   （infohash）与 `torrentURL`（磁链）。
3. **磁链真能用**：取第一条的磁链经 Web API 以 `stopped=true` 加入（零流量），
   `success_count: 1`，infohash 与 `guid` 逐字相同，随后连文件一起删除。
4. 验完即清：两个容器与网络都已删除。

**一个值得记下的观察**：`year=2023` 的 feed 里，一部分条目的 `pubDate` 落在 2024/2025 ——
`year` 筛的是上游的**发行年份**，而 feed 的 `pubDate` 是**磁链被创建的时间**，
两者本来就可以不同年。筛选本身是对的：`year=2025` 与不带 `year` 返回的是**两批不同的**
50 条（实测 2025 那批全是 2025/2026 的磁链）。

`/collected` 也顺手对着真 token 验了：**144 位收藏、6 位男优、未触顶**（`truncated` 键不出现）。

## 部署

`deploy/` 下有三套现成的部署方式，**按你用哪套挑一份**：

| 文件 | 场景 | 跑的是什么 |
|---|---|---|
| `deploy/javdb-rss.service` | systemd。已加固（普通系统用户、`ProtectSystem=strict` 只读文件系统、`StateDirectory` 提供可写状态、零 capability） | **二进制**（不需要 Docker，见下） |
| `deploy/docker-compose.yml` + `deploy/config.docker.yaml` | Docker Compose | 已发布的镜像 |
| `deploy/k8s.yaml` | Kubernetes（含签名失效告警的 CronJob） | 同一个已发布的镜像 |

⚠️ **这三套是示例，不是作者实际在跑的那一份。** 作者自己的实例跑在一个**私有**的
Flux 仓库里（app-template 的 HelmRelease + Edge SSO + 一条对 `/metrics` 的 VMRule），
因此这里不给链接 —— 给了也点不开。取样例与实例的分工是：**示例之间**不许漂
（`deploy/docker-compose.yml` 与 `deploy/k8s.yaml` 由
`internal/config/deploy_image_test.go` 守着钉同一个全量精度 tag），而**实例**由
Renovate 盯着 GHCR 自动提 PR。取舍与理由见 [ADR-0002](docs/adr/0002-deploy-in-own-cluster.md)。

两处容器部署拉的是 GHCR 上已发布的镜像（坐标与升级方式见下面
「发版与拉取镜像」那一节）；systemd 那份跑的是**二进制** —— `make build` 产出它
（部署机因此需要源码与 Go 工具链，但**不需要 Docker**），装到 `/usr/local/bin`。
三套里只有它在升级时要重新构建那个文件。

三份部署配置共用同一套配置结构，**键集合由
`internal/config/examples_sync_test.go` 强制一致**（取值可以不同：容器 / k8s
监听 `0.0.0.0`，token 与 pin 路径也不一样）。改配置项要三处同步，否则 `make test`
会失败 —— 这一条曾经靠人记，结果漏过一次（k8s 少了 `device_uuid`）。

### ⭐ 状态文件的落点：升级时必须让状态目录可写

本服务有两份持久状态：**pin**（每个作品已下发的磁链）与 **判定表**（M-Team 的判定结果，
只在启用 M-Team 时存在）。三套部署分别把它们放在了：

| 部署 | pin | 判定表（启用 M-Team 时才需要） | 怎么提供 |
|---|---|---|---|
| 裸二进制 | `app_api.pin_file` 留空 → 配置文件旁边的 `pin.json` | `mteam.state_dir` 留空 → 配置文件旁边的 `mteam/` | 配置文件所在目录可写即可 |
| systemd | `/var/lib/javdb-rss/pin.json` | `/var/lib/javdb-rss/mteam` | unit 里的 `StateDirectory=javdb-rss`（**两个路径都需在配置里显式写上**） |
| Docker Compose | `/state/pin.json` | `/state/mteam` | `./state:/state` 卷（先 `mkdir -p state`） |
| K8s | `/state/pin.json` | `/state/mteam` | `javdb-rss-state` PVC（`ReadWriteOnce`） |

**升级注意**：旧版本没有 pin，没有可写状态目录也能跑。升级后如果目录仍不可写，
服务会**拒绝启动**并报清楚原因 —— 这是刻意的：默认关闭 pin 会让实例静默退回
有抖动的行为。裸二进制放在 `/etc` 等只读目录时，请显式把这两个路径指到可写位置
（systemd 示例里已有说明）。

⚠️ **两份状态的可丢性不同**（这也是它们不放在同一个文件里的理由）：
丢 **pin** 会让约 1/4 的作品换磁链、客户端**重复下载**；丢**判定表**只是要重查一遍
（几小时，但 `guid` 不变）。

### ⚠️ 容器与 K8s 下的监听地址

裸机默认监听 `127.0.0.1`，但**容器里必须改成 `0.0.0.0`** ——
`127.0.0.1` 是容器自己的 loopback，宿主机连不上。`deploy/` 下的配置已经改好了。

改完请想清楚**谁能访问它**：

- Docker：`-p 127.0.0.1:8080:8080` 只绑宿主机 loopback（推荐）；
  `-p 8080:8080` 则局域网可见。
- K8s：Service 用 `ClusterIP`（只有集群内可达）。**不要**改成 LoadBalancer
  或 NodePort 而不先考虑它没有身份验证这件事。

隔离由容器/集群提供，「暴露出去」由端口映射或 Service 类型决定 ——
请把它当成一个需要动手的决定。

### 发版与拉取镜像

发布不需要在本地做任何事 —— 推一个 tag 就会自动构建并推送镜像：

```bash
git tag -a v1.0.0 -m "v1.0.0"
git push origin v1.0.0
```

坐标是 `ghcr.io/2017fighting/javdb-rss`，平台 `linux/amd64` 与 `linux/arm64`。
每次发布推**两个 tag**：`1.0.0`（全量精度，唯一该被人钉住的形状）与 `latest`
（**仅正式版** —— `v1.0.0-rc1` 这类预发布只出 `1.0.0-rc1`，不动 `latest`）。
浮动的小版本 tag（`1.0`/`1`）**不推**：在单用户场景里它唯一的作用是让
`docker compose pull` 静默换版本，而升级应该是一个有意识的动作。

发布链路会先跑一遍与 CI 相同的 `make fmt-check / vet / race` 才构建 —— tag 推送
**不会**触发 `ci.yml`，所以「这个提交绿过」是在发布这一步重新确立的，不是假设。
两个平台是否真的都推上去了，写在那次 Actions run 的摘要里。

```bash
docker pull ghcr.io/2017fighting/javdb-rss:1.2.0
```

包是 **public** 的（与仓库一致：用仓库自己的 `GITHUB_TOKEN` 推的包会继承仓库的
可见性），因此**匿名可拉** —— 部署机上不需要 `docker login ghcr.io`。

⚠️ 镜像里的 `/version` 报告的是 **tag 原文**（`v1.0.0`），而镜像 tag 是去 v 的
`1.0.0`。两种形状是有意的：前者要与本地 `make build`（走 `git describe`）的说法
逐字对得上，后者是 docker 的惯例。理由与取舍见
[`docs/adr/0001-release-images-to-ghcr.md`](docs/adr/0001-release-images-to-ghcr.md)。

`deploy/docker-compose.yml` 与 `deploy/k8s.yaml` 都钉着同一个**全量精度 tag**
（当前的坐标是 `ghcr.io/2017fighting/javdb-rss:1.2.0`）：

```yaml
image: ghcr.io/2017fighting/javdb-rss:1.2.0
```

**升级＝改这一行**，然后 `docker compose up -d`（或 `kubectl apply -f deploy/k8s.yaml`，
Deployment 会自己滚动）。没有别的开关，也没有地方会静默换版本 —— 正因为不推浮动 tag，
`latest`/`1.0` 这类会自己动的写法在这里是不可选项。k8s 那份显式写着
`imagePullPolicy: IfNotPresent`（对全量精度 tag 本来就是默认值）：tag 内容不会变，
没必要每次启动都去 registry 问一遍。两处坐标不许漂，
由 `internal/config/deploy_image_test.go` 守着。

⚠️ **升级前先确认 pin 状态目录仍可写**（见上面「⭐ pin 的落点」那一节）——
升级后如果它不可写，服务会**拒绝启动**并报清楚原因。

想自己构建镜像的人：compose 文件里留着被注释掉的 `build:` 段，删掉 `image:`、
启用它再 `docker compose build` 即可（那需要源码与 Go 工具链，也正是默认不再做的事）。

## 订阅地址

qBittorrent → 添加 RSS 订阅，填入完整 URL：

```
http://127.0.0.1:8080/rss/code/KV-328.xml                 番号订阅
http://127.0.0.1:8080/rss/actress/EvkJ.xml                女优订阅
http://127.0.0.1:8080/rss/actress/EvkJ.xml?since=2026-01-01   只要这个日期之后的（含当天）
http://127.0.0.1:8080/rss/want.xml                        你在 App 里标了「想看」的全部作品
http://127.0.0.1:8080/rss/list/k4EVE4.xml                 你在 App 里建的某份清单
http://127.0.0.1:8080/rss/tags/0.xml                     全站（有码片库）的最新作品
http://127.0.0.1:8080/rss/tags/0.xml?tags=68,46          全站，只看这两个标签
http://127.0.0.1:8080/rss/tags/0.xml?year=2020&month=3&duration=gt-120
                                                        全站，2020 年 3 月、120 分钟以上
```

⚠️ **启用 M-Team 之后，`/rss/want.xml` 里的 `<enclosure>` 不一定是磁链** ——
馒头命中的那些作品发的是**本服务端出的种子文件**（`GET /torrent/{infohash}.torrent`）。
`<guid>` 仍然是 infohash，所以去重行为没变。见下面「[M-Team 优先](#m-team-优先可选)」。

### 全站订阅（`/rss/tags/{片库号}.xml`）

这是唯一能把**标签 + 时间**组合起来的一条 —— 而它的存在本身是**抓包**换来的：

`filter_by_tags` 作为独立参数**只对女优实体生效**（在清单/搜索/latest/top 上全被静默忽略，
用不存在的 id 做对照可证伪）。App「浏览」页根本不发那个参数：它把标签放进
**`filter_by` 掩码的一个槽位**里，与主属性、年份、月份、时长并列：

```
{片库号}:t:{主属性}:{标签}:{年份}:{时长}:{月份}
   0:t:m:68:2020:gt-120:3   →  有码 · 含磁鏈 · 标签 68 · 2020 年 · >120 分钟 · 3 月
```

| URL 参数 | 含义 | 边界 |
|---|---|---|
| 路径里的片库号 | `0`=有码 `1`=无码 `2`=欧美 `3`=FC2（实测四个库返回**不同**集合） | 只接受 0–3，写错是 **404** |
| `main` | 主属性字母，逗号分隔（`c`=含字幕 `p`=可播放 `s`=单体 `i`/`v`=预览图/视频） | 拼在一起（`cm`）是 **400** |
| `tags` | 标签 id，逗号分隔 | **最多 5 个**（上游硬限制），超过是 **400** |
| `year` | 四位年份 | |
| `month` | 1–12 | |
| `duration` | `lt-45` / `45-90` / `90-120` / `gt-120` | **必须与 `year` 同时给**，否则 **400** |

两处**本服务替你处理掉的静默坑**（详见 [`notes/tag-vocabulary.md`](.scratch/javdb-rss-ui/notes/tag-vocabulary.md) 第 7 节）：

1. **`m`（含磁鏈）总是被并进主属性。** 抓包里 App 浏览页发的就是 `0:t:m::::`；
   而实测不发 m 时 `0:t:::::` 返回的 50 部里 `magnets_count` **全是 0** ——
   feed 发不出没有 enclosure 的条目，去掉 m 只会得到一条空 feed。
   你给的其它主属性会保留（`c` → `c,m`），这件事写在 channel 描述里。
2. **时长单独给会被上游静默忽略**（`0:t:m:::90-120:` 返回的时长是 76–300），
   所以「有时长没年份」判成 400，而不是发一条没筛过的 feed。

`feeds.zones` 可选白名单按**片库**放行（不写=四个库全放行）。

### 清单订阅（`/rss/list/{id}.xml`）

内容就是你 App 里那份片单。清单 id 从发现端点拿：

```bash
curl http://127.0.0.1:8080/collected_lists
```

```json
{"lists": [
  {"id": "k4EVE4", "name": "遥控跳弹", "movies_count": 1,
   "privacy": "open", "feed": "/rss/list/k4EVE4.xml"},
  {"id": "R9r77", "name": "預設清單", "movies_count": 6,
   "is_default": true, "privacy": "own", "feed": "/rss/list/R9r77.xml"}
]}
```

与 `/collected` 一样：**它是发现端点，不是 feed**；形状也同构，
包括触顶时的 `truncated` / `pages_fetched` / `max_pages` 三个字段
（同样是「看键在不在」，不是看值）。

⚠️ **它是「你建的清单」，不是「你关注的清单」。** 两者在上游是两个概念，
而名字最像的那个端点（`/api/v1/users/collected_lists`）**实测返回 HTTP 500**，
所以服务用的是 `/api/v1/lists/simple`（能用的那个）。详见
[`notes/lists.md`](.scratch/javdb-rss-ui/notes/lists.md)。

参数与女优订阅**同一套**：`since` / `pages` 自有，`sort_by` / `order_by` /
`filter_by` / `filter_by_tags` 原样透传。`filter_by` 留空时服务自动构造
`0:l:{清单 id}` —— **zone 固定为 0**，因为清单形态里没有 zone，而写错 zone
上游不报错、只会静默给别的作品（实测：4 份清单在 `0:l:{id}` 下返回
9/1/2/6 条，与上游声明的 `movies_count` 逐位相同）。

```bash
/rss/list/k4EVE4.xml                 整份清单
/rss/list/p36Eww.xml?pages=3         翻三页
/rss/list/p36Eww.xml?since=2026-01-01 只要这个日期之后的
```

### 「想看」feed（`/rss/want.xml`）

这条 feed 的内容来自 **App 里你自己标的「想看」**（我没有 URL 参数，也没有白名单）：
在 App 里给一部片点「想看」，下一轮轮询它就会出现在这里。

规则两条：

1. **有磁链才发条目。** 「标了想看但还没有种」是这份清单的常态（很多片要等一段
   时间才出种），因此这类作品**不会**在 feed 里发条目 —— qBittorrent 用不了
   没有 `enclosure` 的条目。但它们不会静默消失：两个计数写进 **channel 标题**
   （qBittorrent 里看得见的就是它）：

   ```xml
   <title>JavDB · 想看（234 部 · 12 部待磁链）</title>
   <description>App 里「想看」的作品，共 234 部；其中 12 部尚无磁链，未列入本 feed</description>
   ```

   **为什么要写两处：** 实测 qBittorrent 的 RSS API 响应里 feed 对象只有
   `articles` / `hasError` / `isLoading` / `lastBuildDate` / `title` / `uid` / `url`
   —— **根本没有 `description`**，所以描述在 qbt 里看不见，只有标题会被呈现。
   描述则留给会读它的 RSS 阅读器与肉眼看 XML 的人（完整句子更适合那里）。
   两处由同一个构造产出，不会一边说 12 部、一边说 13 部。

   清单读不完（上游还有数据）时，标题里还会多一段「列表可能不完整」。
   不这么写的话，「少了几条」与「服务没读到这张清单」在 qbt 里长得一模一样。
2. **磁链的选择与别的 feed 完全一样**（中文字幕优先 + 钉住），因此 `guid` =
   infohash，与番号/女优订阅**跨 feed 自动去重**。

需要 token（它读的是你在 App 里的标记）。没配 token 时返回 **503 并说明原因**，
而不是 200 + 空 feed。

这条 feed **不受 `feeds` 白名单约束** —— 白名单描述的是「你要订哪些番号/女优」，
而这张清单的边界由你在 App 里画（一个会变的列表放不进配置）。

#### 实测（2026-10-04，真实账号 + 真实 qBittorrent v5.2.4）

首次拿真实 token 跑出来的是：**想看共 234 部**（上游每页 10 条，第 25 页为空）、
其中 12 部尚无磁链、**222 条条目**。首次渲染耗时约 **20 秒**（24 页清单 +
222 次磁链请求，并发受 `app_api.magnet_concurrency` 控制，默认 8；上游当时约
0.8–0.9s/请求）。这是 N+1 换来的代价，与女优 feed 同一个形状；清单越大越慢，
而它每 15 分钟只会被拉一次。

qBittorrent 侧：`hasError: false`，正确解出 `guid`（infohash）与 `torrentURL`（磁链），
guid 跨轮询逐条不变；磁链能被它的引擎接受（`success_count: 1`）。

⚠️ **两条 qbt 侧的坑**（详见上面的 deferred note）：

1. qbt **每 feed 只保留 50 篇文章**（`RSS\MaxArticlesPerFeed` 默认 50）——
   222 条条目它只看得到前 50 条。要整张清单都进 qbt，去
   `设置 → RSS → 每 feed 最大文章数` 调大。
2. 排查 401 时先看 Host 头：qbt 5.x 要求 `Host` 的端口与它监听的端口一致
   （端口映射后很容易踩）。

翻页上限是 **40 页（400 部）**。描述里出现「触顶」就说明清单又长了，
上调 `maxWantPages`（`internal/appapi/want.go`）。

#### 启用 M-Team 之后，标题里会多一个计数

打开 M-Team 优先（见下）之后，判定**还没就绪**的作品会被**扣住不发** ——
但它们不会静默消失，标题里会多一段：

```
<title>JavDB · 想看（234 部 · 12 部待磁链 · 37 部待定来源）</title>
```

两个计数是**两种不同的等待**：一个在等上游出种，一个在等服务判定用哪个来源。
前者只能等，后者**会自己推进** —— 那个数字应当随时间下降，一直不降就是判定卡住了。

（首次把上限写成 20 页时，真实数据当场触顶 —— 234 部会被截掉 34 部，
而 feed 描述里的「触顶」把这件事实说了出来。这就是不静默的价值。）

### 女优订阅的参数：两类，别搞混

| 类别 | 参数 | 谁在用 |
|---|---|---|
| **本服务自有** | `since` `pages` `page` `limit` | 我们消费，**不会**发给上游 |
| **语义参数** | `main` `year` `tags` | 我们消费：拼进掩码 / 翻译成 `filter_by_tags` |
| **原样透传** | `filter_by` `filter_by_tags` `sort_by` `order_by` | 原封不动转发给上游女优页 |

#### 语义参数（女优订阅与清单订阅）

抓包验过之后加的：这几个维度不该让用户自己拼掩码。

```bash
/rss/actress/EvkJ.xml?year=2021              掩码 → 0:a:EvkJ::2021（实测整年：19 部全在 2021）
/rss/actress/EvkJ.xml?year=2021&main=c       掩码 → 0:a:EvkJ:c:2021（8 部，cnsub 8/8）
/rss/actress/EvkJ.xml?year=2021&tags=48      + filter_by_tags=48（年份与标签能组合）
/rss/list/k4EVE4.xml?tags=68&main=c          标签走 filter_by_tags，主属性走掩码
```

⚠️ **掩码的段数是有讲究的，所以别自己拼**：年份只能落在掩码的**第 5 段**，
后面不能再有东西。实测 `0:a:EvkJ:c:2021:`（第 6 段是空的）会让上游
**丢掉年份**而 main 仍然生效 —— feed 看起来筛了 2021、实际跨到 2026。
`0:a:EvkJ::2021:gt-120` 更糟：整条被丢弃，直接回到未筛选。

因此：

- 服务自己构造的掩码只写到第 5 段，**不留尾段**；
- 你手写 `filter_by` 时，第 5 段非空而后面还有段 → **400**；
- `0:a:EvkJ:c::`（尾部空段）实测无害，继续放行。

**清单订阅不支持 `year`**（实测槽位被忽略：`0:l:p36Eww::2025` 返回整份清单），
`month` / `duration` 在女优与清单上都不支持（只有全站形态有）——
这两类一律 **400**：静默忽略会造出一条看着筛过、其实没筛的 feed。

其余几条边界（都是 400，理由都来自实测）：

| 写法 | 为什么拦 |
|---|---|
| `year=2021&since=2026-01-01` | 两个「范围」说的是一件事。`year` / `month` 是上游筛选（整年 / 整月），`since` 是本服务的本地过滤（「这个日期起」）—— 同时发的结果**必然是空 feed**，而「选了年份反而是空的」会让人以为功能坏了。三条路由（女优 / 清单 / 全站）都判 400 |
| `since=2026-1-1`（或 ISO 时间戳、任何非严格 `YYYY-MM-DD`） | 比较是**字符串字序**，宽一点的写法不会报错，只会静默丢掉一整段：不补零丢 1–9 月，带时间戳丢当天发行的作品 |
| `main=x` | 上游只认它自己词表里的字母：`p`(可播放) `m`(含磁鏈) `c`(含字幕) `s`(單體影片) `i`(含預覽圖) `v`(含預覽視頻)。写别的不会报错，只会被静默忽略 |
| `main=cm` | 主属性必须逗号分隔（`c,m`），拼在一起会被静默忽略 |
| `tags=68&filter_by_tags=46` | 同一件事的两种写法，不猜以哪个为准 |
| `filter_by_tags` 超过 5 个 | 上游只认前 5 个，第 6 个被静默丢弃 |

> `main` 的字母集合是**校验**的，与 `sort_by` 刻意不同 —— 后者合法取值从外部
> 不可枚举（黑盒只能证真不能证伪），硬校验会把上游新增的合法值判死；
> 而主属性有一份上游直接给出的词表，未知字母只可能是笔误。

完整参数表（**每条都对着真实上游实测过**）见
[`.scratch/javdb-rss/notes/actress-params.md`](.scratch/javdb-rss/notes/actress-params.md)。

日常只用这几个：

```bash
/rss/actress/EvkJ.xml                     全部作品，最新的 50 部
/rss/actress/EvkJ.xml?since=2026-01-01    只要这个日期之后的（含当天，**闭区间**）
/rss/actress/EvkJ.xml?pages=3             翻三页（≤150 部，默认 1、上限 20）
/rss/actress/EvkJ.xml?filter_by=0%3Aa%3AEvkJ%3Ac%3A%3A   只看带中文字幕的
```

关于 `since` 的三件需要知道的事（语义已定稿，2026-10-05）：

- 比的是作品的**发行日期**，且必须是严格的 `YYYY-MM-DD`（否则 400）。
- 它**只在取到的页里生效**：`?since=2020-01-01&pages=1` 只会拿到最新 50 部里的那些。
  下界能走多深由 `pages` 决定。实测 `pages=1` 会真的少给：站点 `?since=<今天>&pages=1`
  给 50 部，而 `pages=2` 给 100 部且**全部满足 since** —— 这种情况下日志里会有
  「取数窗没走到 `since`」的 WARN（每个订阅只报一次，带订阅路径）。
- `pubDate` 取自磁链的上游创建时间，可能与 `since` 不同源 —— 刚上架的合集
  可能标着六年前的日期。它是如实转述上游，刻意不改（qBittorrent 靠 guid 去重，不看日期）。

### ⚠️ 三个会**静默出错**的坑

上游对写错的参数**不报错、只忽略**。因此下面三件事必须记住：

1. **`filter_by` 是复合掩码**，不是字母组合。写 `apmc` 会静默返回
   **全站最新作品**而不是该女优的作品。正确形式：
   `0:a:<女优id>`，加筛选时主属性用**逗号**分隔：`0:a:EvkJ:c,m::`。
   （拼接写法 `0:a:EvkJ:cm::` 会被服务端静默忽略 —— 本服务会拦下这一种并返回 400。）
2. **`sort_by` 只有 `release` 和 `score` 有区别**，其余拼错的值都静默按发布日期排序。
3. **`page` / `limit` 会被本服务覆盖**，你传了不生效（分页由 `pages` 控制）。

**推荐 `filter_by` 留空** —— 本服务会自动构造正确的 `0:a:<女优 id>`。

## M-Team 优先（可选）

**默认关着。** 打开之后，`/rss/want.xml` 里一部作品如果**馒头上也有**，feed 就交出
馒头那一份（种子文件）；馒头没有的，照旧发上游那份磁链。

它存在的理由只有一个：**几年前的老片，上游的磁链慢得下不动，而馒头上很快。**
它**不是**「更好的来源」—— 既不如上游新、也不如上游全。

### 打开

```yaml
mteam:
  api_key: "<站点【控制台 → 實驗室 → 存取令牌】>"
```

或者用环境变量 `MTEAM_API_KEY`（**优先于**配置文件，且**空值不算「设置了」** ——
否则 compose 里一个写了但没填的 env 会把文件里可用的 key 顶掉）。
**没有第二个开关**：配没配 key 就是开关。三份部署配置里都有这一段。

### 它怎么工作

判定**不在请求路径里做**，而在一个后台循环里（`mteam.scan_interval`，默认 15 分钟，
设 `0` 关闭 —— 关掉是真正的空操作，连清单都不去读）。

理由是硬的：`/rss/want.xml` 的一次渲染已经要 ~20 秒，而 qBittorrent 在
**30 秒无字节传输**时就掐断请求 —— 而判定要「搜索 + 下载种子」，234 部就是分钟级。
所以：**判定在后台做，feed 只读结果。**

- **限速**：种子下载 ≤ 100 个/小时（站点自标的建议值，`mteam.max_downloads_per_hour`）。
  搜索配额是 1000/24h，富余得多。因此**首次回填是渐进的** —— 标题里那个
  「N 部待定来源」会慢慢降下去。
- **可续跑**：判定表就是进度。重启从「没有判定的作品」继续，不从头来。
- **三种结果分得清**：命中就用它；**负结果**（查过、馒头没有）是**永久**判决，不再重问；
  **查询失败**（网络/限流/凭据）**不构成判决**，带原因落盘但**不自动重试**。
- 已下发过的作品**连问都不问**（见下「首发冻结」）。

### 交付物：一份**种子文件**，不是一个磁链

馒头是私种，而它 announce 里那份按次签发的凭据**只存在于种子文件里** ——
API key 换不到。所以命中时：

- `<enclosure>` 指向**本服务**的 `GET /torrent/{infohash}.torrent`，**不再是磁链**；
- **`<guid>` 仍然是 infohash**（从那份种子里算出来的），所以跨 feed 去重、洗版重下、
  重启不变这些性质一条没丢。

### ⚠️ 这个端点会交出你的凭据

本服务**不做鉴权**，而 `/torrent/` 端出的种子里带着你的 M-Team tracker 凭据。
**谁能访问到本服务，谁就能拿到它。** 隔离请靠容器/集群边界（见上面「⚠️ 容器与 K8s 下的
监听地址」与「⚠️ 关于监听地址」两节）。

这比「别人能看到你的订阅内容」严重一档 —— 站点明文写着凭据泄露要重置。

### ⚠️ 轮换 M-Team 密钥的后果

凭据绑账号。**你换了 M-Team 的密钥之后，本服务已经发出的每一份种子都会失效**
（客户端里表现为红种），而本服务**看不见也修不了** —— 它只能继续端出那份已经死掉的种子。

唯一的出口是逐部**重置**（见下），代价是客户端会重新下载一份。

### 首发冻结，与「重置」

来源一经下发就**冻结**：一部作品以哪个来源发过，之后就不再换 ——
因为换来源必然换 infohash（馒头改过 `info.source`），也就是磁盘上多一份。

所以想换源只有手工做一次**重置**：删掉**两处**，然后 `kill -HUP`。

```bash
# 1. 删掉这部作品的判定（mteam.json 的 verdicts 里，键是作品 id）
# 2. 删掉这部作品的 pin（pin.json 的 pins 里，同一个键）
kill -HUP $(pidof javdb-rss)
```

⚠️ **删两处而不是一处是必要的**：只删判定、pin 还在，来源仍然被冻结。
两份文件里都存着作品的 `name`（番号），可以直接按番号 grep 找到那一条。

下一次渲染会重新判定，并**允许换来源**。

⚠️ **这一步会让客户端重新下载一份**（新的 infohash 就是新的 guid，qBittorrent 眼里
那就是一份新内容）—— 而**服务端看不见你删了什么**：删掉的那条 pin 就是唯一的证据，
删完之后它眼里的这部作品与「第一次见到」一模一样。**所以这条提醒只能由文档给你，
日志给不了。**

（日志里那条 `pin 切换 —— 客户端会多下一份` 的 WARN 说的是**另一种**换源：
已经钉住的作品被规则换成了另一条，例如上游出现了中文字幕版。）

（`kill -HUP` 还会**清掉所有「查询失败」的记录**，让它们重查一遍 ——
那正是你换完 key 之后想要的事。它不碰负结果，那是判决。）

### 状态目录

判定表与种子落在 `mteam.state_dir`（留空 = 配置文件旁边的 `mteam/`）。
它**必须可写且持久**：不可写时服务拒绝启动（fail fast，照 pin 的先例）。
里面有两个东西，敏感度不同：

| 文件 | 内容 | 敏感 |
|---|---|---|
| `mteam.json` | 判定表本身 | 否 |
| `torrents/*.torrent` | 种子文件（权限 0600） | ⚠️ **带着你的账号凭据** |

**判定表可以丢**（丢了只是要重查一遍，几小时，但 `guid` 不变）；**pin 不能丢**
（丢了会让约 1/4 的作品换磁链、客户端重复下载）。这就是它们不放在同一个文件里的理由。

## token：登录一次，或从环境变量给

需求「番号订阅」与「女优订阅」**不需要 token**。需要 token 的是这三种读 App 里
**你自己标记的东西**的端点：`/collected`（收藏的女优）、`/collected_lists`
（你建的清单）与 `/rss/want.xml`（想看清单）。

⚠️ 一个例外值得记一笔：清单 **feed 本身**（`/rss/list/{id}.xml`）不需要 token，
因为作品走的是匿名端点。只有「列出你有哪些清单」与「清单标题用真名字」这两件
事需要它（`privacy: own` 的清单匿名读不到名字，那时标题退回 id，feed 照常可用）。

### ⚠️ 先读这条：这是单会话账号

**同一账号只能在一个地方登录，新登录会挤掉之前那个。**
也就是说本服务一登录，**你手机 App 上的会话就下线了**，反过来也一样。

这不是可以绕过的小事，它决定了下面每个选择。

---

### 从手机导出：不踢手机（推荐）

如果**手机还要继续用这个账号**，那就别跑 `login`，改把手机 App 正在用的那串 token
搬过来 —— 服务于是用的就是手机那**一个**会话，谁也不踢谁。

先说清楚为什么不能「把数据目录 pull 下来再解」：**那是密文，离线解不开。**
它落在 `shared_prefs/FlutterSharedPreferences.xml` 的 `flutter.accessToken`
（176 base64 字符 / 131 字节），密钥在 Android KeyStore 里 ——
`classes.dex` 里有 `AndroidKeyStore` / `KeyGenParameterSpec$Builder` /
`AES/GCM/NoPadding` / `KeyStore$SecretKeyEntry`，设备侧的 `native.xml` 里只有两个
**别名**（`KEY_ALIAS` / `TOKEN_ALIAS`）。KeyStore 的密钥永不出 TEE，所以那不是
「工作量问题」而是**做不到**，不要在上面花时间。

明文只在进程内存里出现过 —— App 得把它放进 `authorization: Bearer …` 请求头。
所以做法就是读那份内存。仓库里带了这个工具（纯 shell，不需要 Frida）：

```bash
# 扫一个已经在跑的 App
./scripts/export-phone-token.sh
# adb 看到多个设备时（同一台手机 USB + 无线调试）钉住一个
./scripts/export-phone-token.sh --serial 4b0350c4
# App 没在跑就先起它；装在私密空间要带 --user
./scripts/export-phone-token.sh --launch --user 10
```

它会打印 token、并把 payload 解出来给你核对；**不写盘、不碰网络**，也不替你决定 token
落到哪儿，用完删掉推上去的临时脚本。实测约 16 秒。

它怎么工作：App 必须把 token 放进 `authorization: Bearer …` 请求头，所以明文一定会在
进程内存里出现一次 —— 脚本按 `/proc/<pid>/maps` 逐区 `dd` 读 `/proc/<pid>/mem`，用
**一条有界的** HS256 模式把 JWT 抠出来（写成 `A|B|C` 交替会让 toybox 的 grep 退化约
10 倍：6m09s vs 16s）。前置条件只有两个：**root 的手机**（读别的进程的内存是 root-only）
和 adb。

怎样认哪串是对的：**三段式 + 签名段恰好 43 字符**，且把第二段解出来应当是
`{"id":…,"username":…}`（脚本已经替你做了这件事）：

```bash
printf '%s' '<token 的第二段>' | tr '_-' '/+' | base64 -d 2>/dev/null
# {"id":1234567,"username":"123456789"}
```

这条模式**不会**误伤本服务读到的另外那些 base64（话题列表、域名 blob）—— 它们不含 `.`，
三段式对不上。实测（Xiaomi 13 / Android 15 / Magisk）命中且仅命中一条。

拿到之后写进去（脚本刻意不替你写 —— token 是凭据，落盘与否由你决定）：

```bash
# 方式 A：token.json（与 config.yaml 同目录，权限 0600）
printf '{"token":"%s"}\n' '<上面那串>' > token.json && chmod 600 token.json

# 方式 B：环境变量（优先级高于文件）
export JAVDB_TOKEN='<上面那串>'
```

两个容易卡住的点：

- **adb 看见多个设备**（同一台手机同时插着 USB 又开了无线调试）时每条命令都要指定设备，
  否则报 `more than one device/emulator`：`adb -s <serial> …`，或导出 `ANDROID_SERIAL`。
- **App 装在私密空间**（小米「第二空间」之类）时数据在 `/data/user/10/` 而不是
  `/data/data/`，启动也要带上 user：`adb shell am start --user 10 -n $PKG/$PKG.MainActivity`。

导出之后验证（这一步**不会碰手机**：探针读到已配置 token 时不会登录）：

```bash
unset JAVDB_USERNAME JAVDB_PASSWORD          # ⚠️ 否则 token 读不到时它会自己登录、把你手机踢掉
go run ./cmd/contractprobe collected
# 开头应当打印「使用已配置的 token（不会碰你的手机会话）」
```

完整的证据链（为什么密文解不开、为什么这条路与 `login` 是二选一）在
`.scratch/javdb-rss/notes/auth.md` 的「从 App 里挖 token」。

---

### 本地：登录一次（会把你手机挤下线）

```bash
javdb-rss login -config config.yaml
```

会提示输账号密码（密码不回显），写入 `token.json`（权限 0600），
然后**验证这个 token 真的能用** —— 不是只确认登录接口返回了字符串。

### 正式部署：用环境变量

```bash
export JAVDB_TOKEN=eyJhbGciOi...
```

环境变量**优先于**文件。k8s 用 Secret、compose 用 `env_file: .env` 都行。
容器里不方便挂可写文件，这条通道就是为它准备的。

### token 不会过期 —— 所以**不建议**开自动续期

实测解出的 JWT：payload 只有 `{id, username}`，**没有 `exp`**。
也就是说 token 不按时间失效，它只在**别处登录**时被挤掉 ——
而那个「别处」通常就是你自己的手机。

开了自动续期（设 `JAVDB_USERNAME` + `JAVDB_PASSWORD`）会变成拉锯战：

```
你打开 App           → 服务的 token 失效
服务自动重登          → 把你手机踢下线
你再次打开 App       → 服务的 token 又失效
…无限循环
```

**推荐：不要设 `JAVDB_PASSWORD`。** token 不过期，一次登录就够用；
真被挤掉了重跑 `javdb-rss login` 即可。

只有当你打算**手机上不再登录这个账号**时，自动续期才有意义。

### 有了 token 之后

（下面这些端点都需要它）

```bash
curl http://127.0.0.1:8080/collected
```

```json
{
  "actresses": [
    {"id": "EvkJ", "name": "河北彩花", "videos_count": 229, "gender": 0,
     "feed": "/rss/actress/EvkJ.xml"}
  ]
}
```

`gender` 是**上游直接给的**：`0` = 女优，`1` = 男优。它不是可有可无的字段 ——
收藏里**确实有男优**（实测该账号 144 位里 6 位：森林原人、小沢とおる…），
而要看的是「只看女优」。它**不带 `omitempty`**：`0` 是绝大多数，用 `omitempty`
会让最常见的取值从 JSON 里消失，消费方只能靠「键不在就当成 0」来猜。

`GET /collected` 返回收藏女优清单：**它是一个发现端点，不是 feed。** 本服务不会因为你收藏了谁就自动为它建订阅 ——
它只把清单交给你，由你决定把哪些 id 填进 `feeds.actresses` 或直接拿去填 URL。
这样就不必引入「一条 feed 混所有收藏女优」那种高成本形态
（20 个女优每轮轮询要打 360+ 次上游）。

#### 清单不完整时（截断信号）

收藏列表是**按页拉取**的，翻页有一个上限（当前 20 页）。
实测上游每页固定 **10 条**，因此上限 = 约 200 位收藏（当前是 144 位的约 1.4 倍）。
收藏数一旦超过这个上限，`/collected` **不会**静默给你一份看起来完整的残缺列表，
而是在响应里多出三个字段：

```json
{
  "actresses": [ ... 已读到的部分 ... ],
  "truncated": true,
  "pages_fetched": 20,
  "max_pages": 20
}
```

**判定规则：看 `truncated` 键存不存在，而不是看它的值。**
未触顶时这三个字段**完全不存在**（不是一个永远为真的字段）——
没有 `truncated` 就说明这是一份完整清单。
收藏数恰好等于或略低于上限时，服务会多探一页确认「确实没有了」，
因此不会对你报假警。

```bash
# 脚本里判断清单是否完整：
curl -s http://127.0.0.1:8080/collected | jq -e 'has("truncated") | not'
```

截断时仍然返回已读到的部分（它们的 feed 路径都能用），只是明确告诉你
「这不是全部」。服务日志里也会有一条 WARN。如果真被截断，说明收藏已经多到
读不完 —— 需要调高 `maxCollectedPages`（`internal/appapi/collected.go`）。

### 没有 token 时

`/collected` 与 `/rss/want.xml` 返回 **503 并说明原因**，而不是 200 + 空列表。
这是刻意的：空列表会被理解成「我没收藏任何人」/「我没标过任何想看」，
而真相是「服务读不到」——两者的下一步动作完全不同。

「番号订阅」和「女优订阅」**不需要 token**，不受影响。

### 关于 `lang`

`app_api.lang` 决定上游返回的**女优名字用哪种语言**：

```
lang: en      →  "Kawakita Saika"
lang: zh-CN   →  "河北彩花"
```

默认已是 `zh-CN`。它同时影响女优 feed 的标题（能用名字就用名字，
拿不到退回 id）。不要指望上游的 `name_zht` 字段 —— 实测它在新版服务端恒为空。

## ⚠️ 关于监听地址

默认监听 `127.0.0.1:8080`，**刻意不是 `0.0.0.0`**。

这个服务没有鉴权，任何能访问到它的人都能看到你的订阅内容。要让别的机器上的
qBittorrent 订到，你得在配置里显式改 `listen:` —— 让「暴露出去」是一个需要动手的决定，
而不是一个默认值。

## 健康检查（k8s）

服务暴露四个端点，分别对应不同的读者与故障处置：

| 端点 | 读者 | 用途 | 上游坏了时 |
|---|---|---|---|
| `/healthz` | 编排器 | **存活**。只回答「进程还在吗」 | **仍然 200** |
| `/readyz` | 编排器 | **就绪**。上游不可用则 503 | 503 |
| `/healthz/upstream` | **人** | 机读详情（带 `next_step` 这类自然语言） | 503 + JSON |
| `/metrics` | **告警规则** | Prometheus 文本格式（可查询、有历史） | 一条 gauge 翻成 1 |

后两者读的是**同一个 `health.Tracker` 快照** —— 多一个消费者不等于多一份真相。

```yaml
livenessProbe:
  httpGet: { path: /healthz, port: 8080 }
readinessProbe:
  httpGet: { path: /readyz,  port: 8080 }
```

**`livenessProbe` 必须打 `/healthz` 而不是 `/readyz`。** 本服务唯一已知会失效的输入是
签名常量（它派生自 App 内的 access key，App 升级或服务端轮换都会让它作废）——
签名失效重启一千次也没用。把上游状态掺进 liveness 只会制造重启循环。

### 这条探针抓过真 bug

它检查 `/startup` 是否能通过签名。打开后：

- 上游坏掉 → `/readyz` 返回 503（实例从 Service 端点摘掉，**不重启**）
- feed 的 channel 描述里会出现可见告警（`⚠️ 上游不可用，本 feed 已停更…`），
  你在 qBittorrent 界面里就能看到，而不必盯着一条安静的空 feed 自己猜
- 日志里打一条 ERROR，只在状态**翻转**时打，不会每 15 分钟刷屏

```json
// GET /healthz/upstream（上游正常时）
{"checked":true,"ok":true,"checked_at":"2026-09-28T04:04:35Z","latency_ms":503,"signature_broken":false}

// 签名失效时
{"checked":true,"ok":false,"action":"InvalidSignature","signature_broken":true,
 "error":"javdb api (HTTP 400): InvalidSignature: 無效的簽名","latency_ms":457,
 "next_step":"签名常量已与服务端不兼容，重试无用，需要改代码：先看 javdb-cli 是否已跟进…"}
```

启用 M-Team 之后，同一个 JSON 里还会多一个**单独命名**的段：

```json
{"mteam": {"credential_broken": true, "error": "M-Team 凭据无效：key無效",
           "checked_at": "2026-10-13T12:00:00Z",
           "next_step": "M-Team 的存取令牌已失效（重试无用）：去站点【控制台 → 實驗室 → 存取令牌】重新取一个，改配置后重启或发 SIGHUP"}}
```

⚠️ 它**不改变状态码、也不进 `/readyz`** —— 换 key 不是重启能解决的，
把实例从 Service 端点里摘掉只会让 feed 一起消失（那时连「为什么」都看不到了）。
这与 `signature_broken` 是同一条判断。

`next_step` 是**检查方给出的处置动作**，原样透出。`/healthz/upstream` 不知道
`InvalidSignature` 是什么意思 —— 它只是把上游探针说的话转出去。因此换一个语义
不同的上游时，这里不需要改一行代码。

告警规则建议匹配 `signature_broken: true` —— 它表示**要改代码，不是重试**。
普通网络故障不算在内（避免半夜被叫起来改一个其实只需要重试的东西）。

### `/metrics` 上的那一组序列

```
javdb_rss_upstream_checked                     1      # 是否跑过至少一次检查
javdb_rss_upstream_ok                          0      # 最近一次是否成功
javdb_rss_upstream_signature_broken            1      # 是否「要改代码」那一类
javdb_rss_upstream_last_check_timestamp_seconds …
javdb_rss_upstream_check_latency_seconds       …
javdb_rss_build_info{version="v1.2.0"}          1
```

⚠️ **告警要匹配 `signature_broken == 1`，不要用 `ok == 0`。** `ok == 0` 有两种含义：
「刚启动、还没检查过」与「真的坏了」，而服务启动时它就是 0 —— 拿它报警会在每次
重启时制造一次假报。要分开这两种含义，得看 `checked`（它为此单独成一条）。
`last_check_timestamp_seconds` 与 `check_latency_seconds` 在**从未检查过时不出现**：
报 0 等于说「1970 年检查过」，而 staleness 类的规则正是拿这个字段算的。

启用 M-Team 之后，还会多一组（**没配它就没有这组序列** —— 与 `provider=stub` 时
没有 upstream 那组同一条判断）：

```
javdb_rss_mteam_judged                          0/1    # 有没有过至少一条判定
javdb_rss_mteam_verdicts{outcome="hit|miss|failed"} …  # 判定表里各结果的条数
javdb_rss_mteam_last_decision_timestamp_seconds …      # 最近一条判定的时刻
javdb_rss_mteam_pending_works                   …      # 最近一轮还剩几部没判定
javdb_rss_mteam_stalled_cycles                  …      # 连续多少轮没有推进
javdb_rss_mteam_credential_broken               0/1    # 存取令牌是否失效
```

⚠️ **`judged` 是必须的**：一条判定都没有时，三个 `verdicts` 全是 0 ——
那与「判定过、三个结果都是 0」在计数上长得一模一样。
而 `last_decision_timestamp_seconds` 与 `pending_works` 在**从未判定/从未跑过时不出现**：
报 0 等于说「1970 年判定过」，或者「跑过且没有欠账」。

⚠️ **要盯的是 `stalled_cycles`**：它是「看起来在工作、实际没在工作」的信号 ——
周期照跑、日志照打，而判定表一条都没多（上游一直失败、限速把自己锁死、清单读不到
都会长这样）。一个可用的规则是 `stalled_cycles > 6`（约一个半小时没推进）。

除了上面这些，`/metrics` 还带 **Go 运行时与进程收集器**（`go_*` / `process_*`）：
本服务是「挂上就不管」的那一类，goroutine、内存、GC、fd 是排查时白拿的材料。

这一组序列的取舍（为何引 exposition 库、为何每个 Server 用自己的 registry、
为何 `build_info` 不算推翻 ticket 15）记在 [ADR-0003](docs/adr/0003-metrics-surface.md)。

探针间隔由 `app_api.probe_interval` 控制，设 `0` 关闭。

## feed 的形状

每条 item 对应一部作品，**永远只有一条**，且**字幕优先**：

```
有中文字幕的磁链 → 取 created_at **最新**的那条中文字幕
否则             → 取所有候选里 created_at **最新**的那条
created_at 相同  → 按 infohash 字典序（升序）定序
```

⚠️ 「否则取第一条」是**旧说法**（那时还信任上游的磁链顺序）：实测证明上游顺序不是时间序，
而是一套不可重放的版本档位排序 —— 因此现在按字段定序，**不读上游切片顺序**。
完整定义见 [`CONTEXT.md`](CONTEXT.md) 的**槽位规则**；而「已经下发的那条不会因为
候选增删而漂」（只有出现中文字幕才可能切）由**钉住**负责 ——
那是本服务唯一的持久状态，也是 `guid` 稳定的那一半。

`guid` 是**纯 infohash**。这一点很要紧，它同时保证了三件事：

- 同一部作品即使同时出现在番号订阅和女优订阅里，qBittorrent 也只下一次；
- 洗版会产生新 infohash，于是会自动重新下载；
- guid 完全由内容决定，服务重启、配置调整都不会让它变化。

**已知后果**：无中文字幕版先下、中文字幕版后到时，磁盘上会留两份 ——
qBittorrent 不会自动删旧版。清理旧版是 qBittorrent 的职责，本服务只负责交出磁链。

## 项目结构

```
cmd/javdb-rss/        组装与启动
internal/catalog/     领域模型 + 槽位规则 + 数据源端口
internal/feed/        RSS 渲染（纯函数）
internal/appapi/      App 私有 API 传输层（签名与番号解析各留一个接口接缝）
internal/config/      YAML 配置 + SIGHUP 重载
internal/httpapi/     路由
internal/mteam/       第二个数据来源（M-Team）：客户端 / 种子解析 / 判定表 / 后台循环
internal/webui/       订阅链接生成器页面（资产 embed 进二进制）
internal/stub/        固定数据的假数据源
```

数据源是唯一的外部边界（`catalog.Source`）。要加缓存或后台刷新，
在这一层包一个装饰器即可，上层完全不用动。

## 开发

```bash
make check         # gofmt + vet + test
make race          # 带竞态检测
make test          # 全部是离线测试，不访问网络
```

CI（`.github/workflows/ci.yml`）跑的是同一组命令加容器构建 ——
本地几秒就能跑完，没有理由让问题只在 CI 里暴露。

## 这一版是怎么定下来的

设计决策的依据不在这个 README 里，而在 `.scratch/` ——
`javdb-rss/`（一张 wayfinder 地图、10 张决策票和 3 份逆向侦察笔记，
记录初次交付的每个取舍、被否掉的方案和仍然未知的部分）、
`javdb-rss-followups/`（那轮 code review 里明确未修的 15 张）、
`javdb-rss-ui/`（订阅链接生成器那个 effort）、
`javdb-rss-deploy/`（部署进自家集群：入口、凭据、状态与告警）。
改这个项目之前值得先读。真正定型的那些取舍另写成 [docs/adr/](docs/adr/)。

签名算法来自 [javdb-cli](https://github.com/FlanChanXwO/javdb-cli)（MIT），
归属与改动见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
**如果哪天签名失效了，第一件事是去看那个项目是否已跟进。**

## 许可证

本项目以 **MIT** 许可发布，全文见 [LICENSE](LICENSE)。

唯一 vendored 的第三方代码（`internal/appapi/signature.go` 里的签名算法）也是 MIT，
其归属与我们的改动见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
这两份声明**会随镜像一起分发**（镜像内 `/usr/share/licenses/javdb-rss/`）——
MIT 要求分发副本时带上它们，而镜像是本服务主要的二进制分发渠道。
