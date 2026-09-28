# javdb-rss

把 JavDB 官方 App 里的订阅渲染成 qBittorrent 可以订阅的 RSS。

数据来自官方 App 自己的私有 JSON API —— 不是抓网页，因此字段干净、结构稳定。
服务本身无状态：它不记录「已经发过什么」，去重完全交给 qBittorrent 按 guid 处理。

这是一个单人自用的内网服务。**它不做任何鉴权。**

## 现在能跑到哪一步

| 需求 | 状态 |
|---|---|
| 番号订阅，只发一条磁链 | 链路已通（数据源待接） |
| 中文字幕优先 | ✅ 已实现（服务端直接给 `cnsub` 字段） |
| 女优订阅 + 参数透传 + 只追新 | 链路已通（数据源待接） |
| 读取 App 里收藏的女优 | 未开始，需要你手工导出 token |

**数据源目前是固定样例数据**（`provider: stub`）。真实的 App API 客户端要等两件事定下来：
签名实装的取舍（ticket 02）与番号 → 作品的解析规则（ticket 06）。
配 `provider: appapi` 会**直接启动失败**，而不是静默退回假数据 ——
「配了真实源却拿到固定数据」是那种能瞒很久的故障。

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
那里有一张 wayfinder 地图、9 张决策票和 3 份逆向侦察笔记，
记录了每个取舍、被否掉的方案和仍然未知的部分。改这个项目之前值得先读。
