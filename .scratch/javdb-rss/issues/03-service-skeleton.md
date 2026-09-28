# RSS 服务骨架：Go 项目、配置模型、feed 渲染

Type: task
Status: open

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
