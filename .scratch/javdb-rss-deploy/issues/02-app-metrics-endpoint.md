# 02 — app 侧 `/metrics`

**What to build:** `/metrics` 出「上游健康」的机读形状，让 home-ops 的 VMRule 能对签名失效报警。
指标**只是把 `health.Tracker` 现有的状态再讲一遍** —— 不新增任何事实，只新增一个机读形状。

**Status:** resolved（2026-10-05）

- [x] `internal/health`：`Collector`（实现 `prometheus.Collector`）投影 Tracker ——
      `checked` / `ok` / `signature_broken` / `last_check_timestamp_seconds` /
      `check_latency_seconds`
      - `signature_broken` 的判据复用 `appapi.IsSignatureAction`。
        ⚠️ **不要在 health 里认具体错误名** —— 那个包刻意不认识任何一个名字
        （`Status.Action` 的注释写着理由），投影者是最外层，才有资格认。
      - `checked` 必须**独立成一条**（0/1）。把「从未检查过」说成 `ok=1` 是错的，
        而 Tracker 本来就分得开这两个状态（`known`）。
- [x] `javdb_rss_build_info{version="vX.Y.Z"} 1` ← `httpapi.Version`（`/version` 的机读副本）
- [x] `internal/httpapi`：挂 `GET /metrics`，用**默认 registry**（`promhttp.Handler()`），
      于是 `go_*` / `process_*` 一并端出来 —— 本服务是「挂上就不管」的那一类，
      看 goroutine / 内存 / GC 正是白拿的那部分
- [x] 依赖：`github.com/prometheus/client_golang`。这是**有意的破例**（本仓库此前
      唯一一次依赖取舍是反方向的），理由与代价记在 `docs/adr/0003-metrics-surface.md`
- [x] 测试（用**独立 registry**，不用全局的，否则测试之间互相污染）：
      - 从未检查过 → `checked 0`、`ok 0`、**没有** `last_check_timestamp_seconds` 的样本
      - 检查失败且 `action` 是签名类 → `signature_broken 1`；非签名类失败 → `0`
      - 检查成功 → `ok 1`、latency 与 timestamp 与 Tracker 里的值一致
      - `build_info` 的 version 与 `httpapi.Version` 逐字相同
      - 元测试：断言抓下来的文本里**没有**未转义的非法字符？—— 不做（那是
        client_golang 的职责，而本项目只为自己的判断写测试）
- [x] `README.md`「健康检查（k8s）」一节补第 4 个端点，并写清它与其他三个的关系：
      `/readyz` 给编排器、`/healthz/upstream` 给人、**`/metrics` 给告警规则**，
      三者读的是同一份状态
- [x] `make check`（gofmt/vet/test）与 `make race` 全绿

## 为什么是 client_golang 而不是手写 text exposition

用户的选择，理由是**不想自己维护 exposition 格式**（HELP/TYPE 行、名字转义、
多 goroutine 下输出一致）。代价照实记：整棵依赖树进 `go.mod`，而本仓库此前
为「少一个依赖」做过相反的选择（`THIRD_PARTY_NOTICES.md` 里不引 javdb-cli 的 SDK：
「那会引入 37 个模块」）。**给下一个读代码的人留一句注释**，别让他以为这是没想过的。

## Answer（2026-10-05）

**实现落在 `internal/httpapi/metrics.go`**（加上 `server.go` 里一行注册），
依赖 `github.com/prometheus/client_golang v1.24.1`。

⭐ **一个与票面/地图不同的判断（事后改的，记在这里）**：原计划把 `Collector` 放进
`internal/health`。写过一行后发现那是错的分层：`health` 的卖点就是**零内部依赖**、
且它刻意**不认识任何一个具体错误名**（`Status.Action` 的注释写着理由），
而「哪些 action 算签名类」是 `appapi` 的知识。放进 health 就得把版本号与一个
分类函数都**注入**进去 —— 为了让最内层包多知道两件它不该知道的事。
而 httpapi 本来就同时持有这三样（`s.upstream`、`Version`、`appapi.IsSignatureAction`），
`/healthz/upstream` 的 `signature_broken` 本来也在这里算。
→ **`/metrics` 是一个端点，它的投影就该和别的端点住在一起。**

⭐ **第二个改掉的判断**：地图上写的是「用默认 registry」。真去挂的时候发现
全局 `DefaultRegisterer` 是**进程级**状态：同一个测试二进制里构造第二个 Server，
重复注册会失败，而忽略那个错误就等于让第二个 Server 的 `/metrics` **静默地**
出第一个 Server 的序列。改成**每个 Server 自己的 registry**，并把「默认就有的
那批」显式注册进去（`NewGoCollector` + `NewProcessCollector`），
因此序列集与 `promhttp.Handler()` 一致 —— 端出来的东西没变，变的是它不再说谎。
测试 `TestMetricsExposesRuntimeCollectors` 钉住 `go_*`/`process_*` 真的在。

**测试（8 条，全在 `internal/httpapi/metrics_test.go`）**：

| 测的 | 为什么值得测 |
|---|---|
| 未检查过：`checked 0` / `ok 0` / `signature_broken 0`，且时间戳与耗时**不在**正文里 | 「从未检查过」既不是健康也不健康；报 0 的时间戳等于说 1970 年检查过 |
| `InvalidSignature` / `ParameterInvalid` → `signature_broken 1` | 两种签名失败形态都要被认出来 |
| 普通网络故障 → `signature_broken 0`，但 `checked 1` + `ok 0` | 「真的坏了」与「刚启动」必须分得开 |
| 成功时带 `CheckedAt.Unix()` 与 0.444s 耗时 | staleness 规则与 Grafana 靠这两个字段 |
| `provider=stub`（无 Tracker）时没有 upstream 系列 | 没有可坏的依赖就不该凭空造一条「上游正常」的证据 |
| `build_info` 的 version 与 `/version` 逐字相同 | 两处报不一样的版本比不报更坏 |
| `go_goroutines` / `process_resident_memory_bytes` 在 | 钉住「默认 registry 那批收集器」真的注册了 |

**本地与真实验收**：`make check`（fmt-check + vet + 全量测试）与 `make race` 全绿；
再用 `config.example.yaml`（`provider: appapi`）真起一次服务 curl：
`/metrics` 出 6 条 `javdb_rss_*`（首次检查已跑，`checked 1` / `ok 1` /
`latency 0.97s` / `signature_broken 0`）+ `go_*`/`process_*`，`/version` 仍 200。

**文档**：README 的健康检查一节从「三个端点」改成四个（新增「读者」一列：
编排器 / 人 / 告警规则），并加了 `/metrics` 的序列清单与那条
**「告警匹配 `signature_broken==1`，不要用 `ok==0`」**的警告；
`deploy/k8s.yaml` 的头部注释从「三端点」改正，并指向 ADR-0003。

## 评审（两轴，2026-10-05）

固定点 `c466490`（本次工作开始前的 tip）。两条结论对本票有影响：

1. **词表自相矛盾（Standards P1，已修）**：本票批次给 `CONTEXT.md` 新加了 `上游` 词条，
   里面把 `服务端` 列为避开词 —— 然后在同一条词条的下一行（`签名常量`）与 `metrics.go`
   的注释里又用了它。两轴都抓到了。**处置不是改那两处用词，而是撤掉那条禁令**：
   `服务端` 在仓库里已有 **76 处**（`internal/` 与 notes 遍布），它就是既有用词，
   把 76 处无关代码一起改才是错的那一半。现改为在 `上游` 词条里注明「代码与笔记里
   也常直接叫它服务端，同一个东西」，`_Avoid_` 只留 `JavDB`。
   —— 这也正是「仓库既定标准优先于外来的口味」在那份词表上的用法。
2. **`deploy/k8s.yaml` 头部注释的改动归票 03（Spec，越界）** —— **半接受**：
   其中「三端点 → 四个端点」那句是**被本票改成了假话**的，必须同批改；
   指向 ADR-0003 那一句算票 03 的，已随本票提前落地，票面如实记下。

其余两条（规格层面的 `defaultPodOptions.securityContext` 缺项、裸写 `ADR-0002` 的撞号）
都在 home-ops 那一半，处置见 [01 的评审节](01-home-ops-registration.md)。
