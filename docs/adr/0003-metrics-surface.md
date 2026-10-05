# `/metrics`：把上游健康再讲一遍，并为此破一次依赖的例

服务增加第四个端点 `/metrics`，用 `github.com/prometheus/client_golang` 把
`health.Tracker` 的最近一次结论投影成 `javdb_rss_upstream_*` 与 `javdb_rss_build_info`。
**它不新增任何事实** —— 只是同一份快照的第三个消费者：`/readyz` 给编排器、
`/healthz/upstream` 给人、`/metrics` 给告警规则。引依赖是**有意的破例**。

## 背景

「签名常量失效」是本服务唯一已知会失效的输入（它派生自 App 内的 access key，
App 升级或服务端轮换都会让它作废），而处置是**改代码**、重试无用。在那之前，
这件事的可见性只有两条腿：`/readyz` 把实例从 Service 端点摘掉（喂编排器），
`/healthz/upstream` 的 JSON 给人和 CronJob 看。到了集群里，第三条腿变成必需：
**告警规则要一个能查询、有历史的机读形状**。

## Considered Options

- **手写 text exposition（零新依赖）。** 这是**推荐过的**方案，理由是本仓库的取舍口味：
  `go.mod` 当时只有一个直接依赖，而它已经为「少一个依赖」在别处做过相反的选择
  （`THIRD_PARTY_NOTICES.md`：不引 javdb-cli 的 SDK，「那会引入 37 个模块」）。
  **用户选了 client_golang**，理由是**不想自己维护 exposition 格式**
  （HELP/TYPE 行、名字转义、多 goroutine 下的输出一致）。✅ 如实记下：这是本仓库
  唯一一次「引依赖」的取舍，方向与上一次相反 —— 而上面那句话讲的是**那个具体 SDK**，
  不是一条「禁止依赖」的普遍规则。下一个读代码的人会拿它对比，所以写在这里。
- **不引 exposition 库，但也不手写：用 sidecar 把 JSON 转成指标。** 否掉：那会让
  「上游健康」有两个说法（Tracker 的状态 vs 转换层的映射），而 `/healthz/upstream`
  的 JSON 是**给人看**的形状、不是契约 —— 哪天它改了，失配是**静默**的。
- **一次把指标面铺开**（请求量、feed 生成耗时、上游耗时 Histogram）。否掉：本轮要的是
  「让一条告警能响」，先铺开只会长出一堆没人查的序列；将来真要加，client_golang
  的 Histogram 是白拿的。
- **注册到全局 `DefaultRegisterer`。** 否掉：它是**进程级**状态 —— 同一个测试二进制里
  构造第二个 `Server` 时重复注册会失败，而忽略那个错误就等于让第二个 Server 的
  `/metrics` **静默地**出第一个 Server 的序列。改成**每个 Server 自己的 registry**，
  并把「默认就有的那批」显式注册进去（Go 运行时 + 进程收集器），因此序列集与
  `promhttp.Handler()` 一致 —— 端出来的东西没变，变的是它不再会说谎。
- **`build_info` 不做。** 否掉：它与 ticket 15 那句「不做版本可见性」不冲突 ——
  那条讲的是「**最新**版本是多少」（本服务是拉取方，没有自更新需求，那是**别人的**状态），
  而这是**自己的运行态**，且只是 `/version` 的机读副本。
- **为「指标消失」加一条 `absent()` 规则。** 否掉：Pod 自己的死活归集群那层
  （`PodStuckContainerCreating` / `KubeContainerWaiting` / Flux 那层），
  而本服务是 `Recreate` —— 每次改配置都会让指标短暂消失，加 absent 就是给自己造假报。

## Consequences

- **依赖账**（照实记）：`go.mod` 从 3 个模块（`yaml.v3` + 两个 `x/` 间接）变成 11 个
  （3 直接 + 8 间接），`go list -m all` 的构建列表 37 个。镜像是编译后的静态二进制，
  不带依赖源码，因此 `THIRD_PARTY_NOTICES.md` **不动**（那份声明的口径是「唯一
  **vendored** 的第三方代码」）。
- **指标集的判断**（序列语义即契约，改之前先读这段）：
  - `checked` **单独成一条**，因为 Tracker 刻意区分「从未检查过」与「检查过且失败」；
    把没探过报告成 `ok=1` 是错的，而只看 `ok=0` 又分不开这两种含义。
  - `last_check_timestamp_seconds` 与 `check_latency_seconds` 在**从未检查过时不出现** ——
    报 0 等于说「1970 年检查过」，而 staleness 类的规则正是拿它算的。
  - **告警匹配 `signature_broken == 1`，不要用 `ok == 0`**：服务刚启动时 `ok` 就是 0。
  - 除自己的序列外还端出 `go_*` / `process_*`（有意）：本服务是「挂上就不管」的那一类，
    goroutine、内存、GC、fd 是排查时白拿的材料。
- **`/metrics` 与其余端点同端口同 mux**，因此它也落在 edge 的 SSO 后面 ——
  而 `VMPodScrape` 直接打 Pod IP，不经过路由，所以两者互不干扰。
- **版本现在有两个说法**：`/version`（给人）与 `javdb_rss_build_info`（给机器），
  同一个事实。它们都是 tag 原文（`v1.1.0`），而部署侧钉的是去 v 的镜像 tag（`1.1.0`）——
  三种形状是有意的，理由见 ADR-0001。
