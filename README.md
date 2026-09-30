# javdb-rss

把 JavDB 官方 App 里的订阅渲染成 qBittorrent 可以订阅的 RSS。

数据来自官方 App 自己的私有 JSON API —— 不是抓网页，因此字段干净、结构稳定。
服务本身无状态：它不记录「已经发过什么」，去重完全交给 qBittorrent 按 guid 处理。

这是一个单人自用的内网服务。**它不做任何鉴权。**

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

配置文件写坏了会**保留旧配置继续服务**，并把错误记进日志 ——
一个手滑的 YAML 不该让正在服务的实例失去配置。

`make help` 列出全部命令。

## 部署

`deploy/` 下有三套现成的部署方式，**按你用哪套挑一份**：

| 文件 | 场景 |
|---|---|
| `deploy/javdb-rss.service` | systemd。已加固（DynamicUser、只读文件系统、零 capability） |
| `deploy/docker-compose.yml` + `deploy/config.docker.yaml` | Docker Compose |
| `deploy/k8s.yaml` | Kubernetes（含签名失效告警的 CronJob） |

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

## 读取 App 里收藏的女优

前提是你有一个从 App 导出的 token（下面单说）。有了之后：

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

**它是一个发现端点，不是 feed。** 本服务不会因为你收藏了谁就自动为它建订阅 ——
它只把清单交给你，由你决定把哪些 id 填进 `feeds.actresses` 或直接拿去填 URL。
这样就不必引入「一条 feed 混所有收藏女优」那种高成本形态
（20 个女优每轮轮询要打 360+ 次上游）。

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

**已知后果**：无字幕版先下、字幕版后到时，磁盘上会留两份 ——
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
