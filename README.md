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
  "size_mb": 3110, "cnsub": false, "created_at": "09/27/2026",
  "pinned_at": "2026-09-30T12:00:00Z"}}}
```

手工删掉某一条（「这个作品想重选」）后 `kill -HUP` 重读即可。
文件损坏或版本不认识时会**拒绝启动**，而不是用空表静默覆盖。

## 现在能跑到哪一步

| 需求 | 状态 |
|---|---|
| 番号订阅，只发一条磁链 | ✅ 可用 |
| 中文字幕优先 | ✅ 可用（服务端直接给 `cnsub` 字段） |
| 女优订阅 + 参数透传 + 只追新 | ✅ 可用（分页与缓存未做，见 ticket 09） |
| 读取 App 里收藏的女优 | ⚠️ 已实现为 `GET /collected`，但**尚未对着真实 API 验证过**（需要 token） |

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

## 部署

`deploy/` 下有三套现成的部署方式，**按你用哪套挑一份**：

| 文件 | 场景 |
|---|---|
| `deploy/javdb-rss.service` | systemd。已加固（普通系统用户、`ProtectSystem=strict` 只读文件系统、`StateDirectory` 提供可写状态、零 capability） |
| `deploy/docker-compose.yml` + `deploy/config.docker.yaml` | Docker Compose |
| `deploy/k8s.yaml` | Kubernetes（含签名失效告警的 CronJob） |

三份部署配置共用同一套配置结构，**键集合由
`internal/config/examples_sync_test.go` 强制一致**（取值可以不同：容器 / k8s
监听 `0.0.0.0`，token 与 pin 路径也不一样）。改配置项要三处同步，否则 `make test`
会失败 —— 这一条曾经靠人记，结果漏过一次（k8s 少了 `device_uuid`）。

### ⭐ pin 的落点：升级时必须让状态目录可写

pin（见上）是**唯一需要可写磁盘**的东西。三套部署分别把它放在了：

| 部署 | pin 位置 | 怎么提供 |
|---|---|---|
| 裸二进制 | `app_api.pin_file` 留空 → 配置文件旁边的 `pin.json` | 配置文件所在目录可写即可 |
| systemd | `/var/lib/javdb-rss/pin.json` | unit 里的 `StateDirectory=javdb-rss`（**需在配置里显式写上这个路径**） |
| Docker Compose | `/state/pin.json` | `./state:/state` 卷（先 `mkdir -p state`） |
| K8s | `/state/pin.json` | `javdb-rss-state` PVC（`ReadWriteOnce`） |

**升级注意**：旧版本没有 pin，没有可写状态目录也能跑。升级后如果目录仍不可写，
服务会**拒绝启动**并报清楚原因 —— 这是刻意的：默认关闭 pin 会让实例静默退回
有抖动的行为。裸二进制放在 `/etc` 等只读目录时，请显式把 `pin_file` 指到可写路径
（systemd 示例里已有说明）。

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

## 订阅地址

qBittorrent → 添加 RSS 订阅，填入完整 URL：

```
http://127.0.0.1:8080/rss/code/KV-328.xml                 番号订阅
http://127.0.0.1:8080/rss/actress/EvkJ.xml                女优订阅
http://127.0.0.1:8080/rss/actress/EvkJ.xml?since=2026-01-01   只要这个日期之后的
```

### 女优订阅的参数：两类，别搞混

| 类别 | 参数 | 谁在用 |
|---|---|---|
| **本服务自有** | `since` `pages` `page` `limit` | 我们消费，**不会**发给上游 |
| **原样透传** | `filter_by` `filter_by_tags` `sort_by` `order_by` | 原封不动转发给上游女优页 |

完整参数表（**每条都对着真实上游实测过**）见
[`.scratch/javdb-rss/notes/actress-params.md`](.scratch/javdb-rss/notes/actress-params.md)。

日常只用这几个：

```bash
/rss/actress/EvkJ.xml                     全部作品，最新的 50 部
/rss/actress/EvkJ.xml?since=2026-01-01    只要这个日期之后的（追新）
/rss/actress/EvkJ.xml?pages=3             翻三页（≤150 部，默认 1、上限 20）
/rss/actress/EvkJ.xml?filter_by=0%3Aa%3AEvkJ%3Ac%3A%3A   只看带中文字幕的
```

### ⚠️ 三个会**静默出错**的坑

上游对写错的参数**不报错、只忽略**。因此下面三件事必须记住：

1. **`filter_by` 是复合掩码**，不是字母组合。写 `apmc` 会静默返回
   **全站最新作品**而不是该女优的作品。正确形式：
   `0:a:<女优id>`，加筛选时主属性用**逗号**分隔：`0:a:EvkJ:c,m::`。
   （拼接写法 `0:a:EvkJ:cm::` 会被服务端静默忽略 —— 本服务会拦下这一种并返回 400。）
2. **`sort_by` 只有 `release` 和 `score` 有区别**，其余拼错的值都静默按发布日期排序。
3. **`page` / `limit` 会被本服务覆盖**，你传了不生效（分页由 `pages` 控制）。

**推荐 `filter_by` 留空** —— 本服务会自动构造正确的 `0:a:<女优 id>`。

## token：登录一次，或从环境变量给

需求「番号订阅」与「女优订阅」**不需要 token**。只有 `/collected`
（读你在 App 里收藏的女优）需要。

### ⚠️ 先读这条：这是单会话账号

**同一账号只能在一个地方登录，新登录会挤掉之前那个。**
也就是说本服务一登录，**你手机 App 上的会话就下线了**，反过来也一样。

这不是可以绕过的小事，它决定了下面每个选择。

### 本地：登录一次

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
    {"id": "EvkJ", "name": "河北彩花", "videos_count": 229,
     "feed": "/rss/actress/EvkJ.xml"}
  ]
}
```

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

`/collected` 返回 **503 并说明原因**，而不是 200 + 空列表。这是刻意的：
空列表会被理解成「你没收藏任何人」，而真相是「服务读不到」——
两者的下一步动作完全不同。

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

服务暴露三个端点，分别对应不同的故障处置：

| 端点 | 用途 | 上游坏了时 |
|---|---|---|
| `/healthz` | **存活**。只回答「进程还在吗」 | **仍然 200** |
| `/readyz` | **就绪**。上游不可用则 503 | 503 |
| `/healthz/upstream` | 机读详情（供 CronJob / 告警） | 503 + JSON |

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
 "error":"javdb api (HTTP 400): InvalidSignature: 無效的簽名","latency_ms":457}
```

告警规则建议匹配 `signature_broken: true` —— 它表示**要改代码，不是重试**。
普通网络故障不算在内（避免半夜被叫起来改一个其实只需要重试的东西）。

探针间隔由 `app_api.probe_interval` 控制，设 `0` 关闭。

## feed 的形状

每条 item 对应一部作品，**永远只有一条**，且**字幕优先**：

```
有中文字幕的磁链 → 用它
否则             → 用第一条
```

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

设计决策的依据不在这个 README 里，而在 `.scratch/javdb-rss/` ——
那里有一张 wayfinder 地图、10 张决策票和 3 份逆向侦察笔记，
记录了每个取舍、被否掉的方案和仍然未知的部分。改这个项目之前值得先读。

签名算法来自 [javdb-cli](https://github.com/FlanChanXwO/javdb-cli)（MIT），
归属与改动见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
**如果哪天签名失效了，第一件事是去看那个项目是否已跟进。**
