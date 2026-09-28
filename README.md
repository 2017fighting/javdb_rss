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
| 读取 App 里收藏的女优 | ❌ 需要你手工导出 token + 呈现形态待定 |

数据源是**真实的 JavDB App 私有 API**。`provider: stub` 是离线调试通道。

## 跑起来

```bash
go build -o javdb-rss ./cmd/javdb-rss
cp config.example.yaml config.yaml
./javdb-rss -config config.yaml
```

改完配置后发 SIGHUP 即可生效，不用重启：

```bash
kill -HUP $(pidof javdb-rss)
```

配置文件写坏了会**保留旧配置继续服务**，并把错误记进日志 ——
一个手滑的 YAML 不该让正在服务的实例失去配置。

## 订阅地址

qBittorrent → 添加 RSS 订阅，填入完整 URL：

```
http://127.0.0.1:8080/rss/code/KV-328.xml                 番号订阅
http://127.0.0.1:8080/rss/actress/EvkJ.xml                女优订阅
http://127.0.0.1:8080/rss/actress/EvkJ.xml?since=2026-01-01   只要这个日期之后的
```

女优订阅的 query 参数**原样透传**给 App 自己的演员页 —— 本服务不解释也不改写它们。
可用的键见 `config.example.yaml` 里的注释。

`?since=` 是本服务自有的参数，不会被透传上去。

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
go test ./...      # 全部是离线测试，不访问网络
go vet ./...
```

## 这一版是怎么定下来的

设计决策的依据不在这个 README 里，而在 `.scratch/javdb-rss/` ——
那里有一张 wayfinder 地图、10 张决策票和 3 份逆向侦察笔记，
记录了每个取舍、被否掉的方案和仍然未知的部分。改这个项目之前值得先读。

签名算法来自 [javdb-cli](https://github.com/FlanChanXwO/javdb-cli)（MIT），
归属与改动见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
**如果哪天签名失效了，第一件事是去看那个项目是否已跟进。**
