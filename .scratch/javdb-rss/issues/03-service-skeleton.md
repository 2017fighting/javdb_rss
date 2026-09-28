# RSS 服务骨架：Go 项目、配置模型、feed 渲染

Type: task
Status: resolved

> ## 已有关键输入（2026-09-28，来自 `01` `04` `08`）
>
> 骨架的接口形状已被上游决策钉死，开工前先读 map 的 **Notes**：
>
> - **路由**：`/rss/code/{番号}.xml`、`/rss/actress/{id}.xml`（无鉴权，无 secret 段）
> - **每部作品恒发 1 条 item**；`guid` = 纯 infohash（磁链 `hash`）—— 这是本 ticket 里
>   **最不能含糊的一点**，因为它决定 qBittorrent 去重是否可靠
> - 标题：字幕版 `[KV-328] 中文字幕 · <name>`，普通版 `[KV-328] <name>`
> - 监听默认 `127.0.0.1`；配置单 YAML + `SIGHUP`；无状态、不引数据库
> - 送进 App API 的公共参数固定七个 + `device_uuid`；`jdsignature` 是请求头
>
> **函数级“接缝”优先于功能完备**：App API 客户端必须能整块替换为一个假实现（无网络跑测试），
> 且签名模块要能单独替换（因为 Prefix 将来可能失效）。

## Question

这个 Go 单二进制的骨架长什么样？

**这条不阻塞于签名** —— 骨架 + 假数据就能写完，等 ticket 02 通了再把数据源接上。
现在开工的意义是把后面所有 ticket 要嵌进来的**接缝**先钉死。

要回答：

1. **项目布局**：`cmd/` + `internal/` 的切分。「App API 客户端」「订阅解析」「feed 渲染」
   「HTTP 路由」四块各自边界在哪，谁依赖谁。目标：App API 客户端可以被单独替换成假实现跑测试。
2. **配置模型**：订阅在配置里怎么声明？至少覆盖
   - 番号订阅（`code: ABC-123`）
   - 女优订阅（`actress: <id>` + 透传的 App 演员页参数 + 可选 `since=`）
   - 监听地址、token 存放位置
   配置是 YAML / TOML / 单行环境变量？**热重载**要不要（改了配置不重启）？
3. **HTTP 接口面**，对齐已定的「每个订阅一个 feed」：
   - `GET /rss/code/{番号}.xml`
   - `GET /rss/actress/{id}.xml?<透传参数>&since=<日期>`
   - feed 是**请求时现算**还是**读后台缓存**？—— 这条与 ticket 09（缓存与限流）耦合，
     骨架里要先把接口留成可换的形状，但**不要现在定死策略**。
   - 健康检查 / 版本端点
   - **鉴权**：feed URL 要不要带 secret token？（qBittorrent 的 RSS 支持带认证的 URL）
4. **RSS 渲染**：用现成库还是手写 XML？必须产的字段：
   `title` / `link` / `guid` / `pubDate` / `enclosure url=<magnet>` / `category`。
   `guid` 的构造规则是哪几个字段拼的（infohash？）—— 这条决定 qBittorrent 去重是否可靠，
   **是本 ticket 里最不能含糊的一点**。
5. **构建与运行**：`go build` 单二进制、Docker 镜像要不要（见 ticket 04）。

## 约束

- 无状态：不引入数据库。若为缓存引入存储，必须是可丢弃的（进程重启不影响正确性）。
- Go 标准库优先；引入第三方依赖要能说清为什么。
- 骨架里**不要**出现「番号解析」「字幕判定」的具体业务规则 —— 那是 ticket 06/08 的产出。

## 产出

- 可 `go build` 通过、能起 HTTP 服务、用假数据渲染出合法 RSS 的骨架
- `CONTEXT.md`：把本 effort 的领域词汇定下来（番号 / 女优订阅 / 磁链条目 / 中文字幕补充条目 /
  feed / 订阅）—— 按 `/domain-modeling` 的规范写，只放术语不放实现

## Answer

**骨架已交付，且已端到端实跑验证。**

### 项目布局：一个端口，四个模块

```
cmd/javdb-rss/        组装与启动
internal/catalog/     领域模型 + 槽位规则 + **Source 端口**
internal/feed/        RSS 渲染（纯函数）
internal/appapi/      App API 传输层（两个接口接缝）
internal/config/      YAML 配置 + SIGHUP 重载
internal/httpapi/     路由
internal/stub/        固定数据的假数据源
```

**唯一的外部边界是 `catalog.Source`**（两个方法：`Code` / `Actress`）。
HTTP 层只认它，不知道背后是真实 API、缓存、还是假实现。
把端口放在 `catalog` 而不是消费方 `httpapi`，是为了让实现与消费方都只依赖领域，
不必互相认识 —— 并且端口能拿到编译期断言（`var _ catalog.Source = (*stub.Source)(nil)`）。

ticket 09 若要加缓存/后台刷新，在 `Source` 外包一层装饰器即可，**上层一行不改**。

### 两处未知被隔离成接口，而不是散在代码里

| 未知 | 接缝 | 归属 |
|---|---|---|
| 签名算法（Prefix 将来会失效） | `appapi.Signer` 接口 | ticket 02 |
| 番号 → 作品 的解析规则 | `Source.Code` 的实现内部 | ticket 06 |

`appapi.Client` 只放**实测确认过的传输事实**：`jdsignature` 是请求头、
8 个公共参数、错误信封。凭据类错误单独成 `AuthError` 型
（`JWTVerificationError` / `LoginRequired` / `TokenExpired` …），
因为它需要完全不同的处置 —— 日志里必须与普通 APIError 分开，
否则用户会看到一条永远为空的 feed 却不知道原因。

### 关键设计取舍

1. **监听默认 `127.0.0.1:8080`**（不是 `0.0.0.0`）。服务不做鉴权，
   所以「暴露出去」必须是一个需要动手改配置的决定。已写进 README。
2. **`provider: appapi` 直接启动失败**，不静默退回假数据。
   「配了真实源却拿到固定数据」是能瞞很久的故障，宁可启动不了。
3. **配置重载失败保留旧配置**，只记错误。一个手滑的 YAML 不该让实例失去配置。
4. **白名单是「写了才生效」而非「默认为空」**：用指针区分「没写 feeds: 段」
   与「写了但为空」，两种意图不同。
5. **feed 请求时现算**。不提前决定缓存 —— 成本量级还没量出来（ticket 09），
   而 `Source` 作端口已经把这个决定留成了可替换的。
6. **`guid` = 纯 infohash**，不含时间戳/番号/槽位。
   文档里写明了为什么：任何掺入都会破坏跨 feed 去重，
   并可能在磁链重合时产生两条 guid 指向同一个 infohash。

### 对已关闭票的两处修订（实测发现）

- **ticket 08 的标题格式**：原写「基名 = 磁链显示名」，但实测 App API 返回的
  磁链 `name` 通常就是番号本身，会渲染成 `[KV-328] 中文字幕 · KV-328`。
  改为**优先作品标题，退化到磁链 name，再退化到 infohash**。已回写票 08。
- **ticket 09 的 `since`**：语义未定稿，但**静默忽略用户传的参数比实现得不够完美更糟**。
  按 `release_date` 临时实现（字典序比较，不引入时间解析），
  且**解析不了的作品一律保留** —— 「按错误规则丢弃数据」比「多给几条」危险得多。
  每次启用都会打一条带 `见=ticket 09` 的 WARN。

### 验证

- `gofmt` / `go vet` / `go build` 全净；`go test -count=1 ./...` 全过
- 测试全是**离线**的，不访问网络
- 实跑验证：`/healthz`、`/version`、`/rss/code/{番号}.xml`、
  `/rss/actress/{id}.xml?filter_by=apmc&since=` 均正常；
  响应经**独立的 XML 解析器**（Python ElementTree）验证结构合法
- SIGHUP 实测：改配置生效；写坏配置后旧配置仍在服务
- 测试里钉住的两条关键不变量：
  **guid 在不同请求间逐字节稳定**、**guid 只由 infohash 决定**（不随番号/标题变）

### 一个真找到并修掉的 bug

`dn` 参数的空格被 `url.QueryEscape` 编成了 `+`。这在 query string 里合法，
但 `dn` 的值会变成客户端界面上的种子名 —— 用户会看到 `おしゃぶり予備校110+深月めい`。
已改为 `%20`，并加了一条测试钉住（包括「不得出现 +」）。
这类回归**除了用户自己看到奇怪的文件名，没有任何告警会提醒你**。

### 未做（且刻意不做）

- 真实数据源（ticket 02 / 06）
- 番号解析规则、字幕判定规则（都不是本票的产出；且字幕判定已被服务端 `cnsub` 解决）
- Dockerfile 与 systemd unit（ticket 04 决定了形态，但未列入本票产出；
  二进制已能直接跑，镜像化是随时可做的小事）

### 新暴露出的缺口（已开新票）

**需求 4「读我在 App 里订阅的女优」在地图的 Destination 里没有落脚点** ——
两个路由都是「给一个 id/番号」。读到的收藏列表要变成什么？已开 ticket 10。

