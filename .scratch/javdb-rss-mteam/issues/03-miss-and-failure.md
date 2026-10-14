# 03: 判定的另外两种结果 —— 负结果与查询失败

**What to build:** 馒头没有 ⇒ **永久**负结果（照旧发上游那份，且不再重问）；
没查成 ⇒ 带可重试标记但**不自动重试**；凭据失效与「上游坏了」在日志、JSON、指标上分得开。

**Blocked by:** 02.

**Status:** resolved（2026-10-13）

- [x] **负结果永久**：同一作品第二次渲染**不再打 M-Team**（有测试数上游被打了多少次）
- [x] **查询失败不构成判决**：它不被写成负结果；带可重试标记与失败原因；**不自动重试**
- [x] 三种结果（命中 / 负结果 / 查询失败）在判定表里是**可区分**的条目
- [x] 「负结果」在日志与文案里**不写成「失败」** —— 那是另一回事，处置完全相反
- [x] 凭据失效（401 / 无效 key）在 `/healthz/upstream` 的 JSON 里有一个**可判定字段**
      + `next_step`（自然语言，去控制台换 key），且**不**进 `/readyz`
      （照 `signature_broken` 的先例：重试无用的东西不该让人去重启）
- [x] 「上游坏了」与「M-Team 凭据失效」是两条不同的日志消息
- [x] 端到端：假 M-Team 分别返回空结果 / 401 / 网络错误 ⇒ 三种结果各自落地，feed 行为各自正确
      ✅ **2026-10-13 补上**（票 06 之后）：`TestWantFeedRendersAllThreeOutcomesEndToEnd`
      一次穿过五层，三种结果各走一遍。
- [x] `make check` 与 `make race` 全绿

## Answer（2026-10-13）

**落点**：`internal/mteam/store.go`（`Verdict` 加 `Failure` / `FailureKind`，
以及**推导式**的 `CredentialFailure()`）、`internal/mteam/source.go`（`enrich` 按三种
结果分派 + `recordFailure` + 两类日志措辞）、`internal/mteam/client.go`
（`CredentialGuidance`）、`internal/httpapi`（`/healthz/upstream` 多一个 `mteam` 段）。

⭐ **两处与票面不同的判断（记在这里）**：

1. **「可重试标记」就是 `Outcome` 本身，没有单独的 bool。**
   票面写的是「带一个可重试标记」，但**没有任何代码会读它** —— 三种结果的处置是：
   命中就用它、负结果永不重问、失败也不自动重试（只有「重置」会再试）。
   加一个谁也不读的字段正是「臆测的通用性」那种味道。真正需要机读区分的是
   **凭据失效 vs 其它失败**，那由 `FailureKind` 承担。

2. **「凭据坏了」是**推导**出来的，不是存的一份状态。**
   `Store.CredentialFailure()` 扫判定表，找 `Outcome=failed && FailureKind=credential`
   的记录。理由很具体：一份只活在内存里的信号**会在重启后消失，而且永远不会重新出现** ——
   因为那条失败已经被缓存了、不会重查。推导出来的版本天然持久、零额外状态。

**测试**：`internal/mteam` 新增 6 条（负结果永久 / 失败不是 miss / 三种结果可区分 /
凭据失败可从判定表读出 / 负结果的措辞里不能出现「失败」/ 既有那批）+ `internal/httpapi`
1 条（`mteam.credential_broken` + `next_step`，且 `/readyz` 与状态码**不受影响**）。

**已知边界**：判定仍然同步发生在请求内（票 04）；`/metrics` 的形状是票 06。

## 自查抓到的一个 P0（2026-10-13，在写装配时）

⚠️ **没配 M-Team 时，每一次 `/torrent/` 请求都会 SIGSEGV。**

`WithTorrents(mteamStore)` 里传的是 `var st *mteam.Store`（nil）—— 而把它交给一个
**接口**参数，得到的接口**不是 nil**。于是 `s.torrents != nil` 为真，handler 去调一个
nil 接收者的方法，当场 panic。

当时的测试全绿，因为 `newTestServer` 传的是**字面 nil**，不是类型化 nil。

两层都修：装配层合成一个入口 `WithSecondSource(st *mteam.Store)`（nil 时什么都不挂），
`mteam.Store` 的读方法对 nil 接收者返回「没有」而不是崩。测试
`TestTypedNilStoreBehavesLikeNoStore` 钉住第二层。

> 这是本轮第二个「测试全绿但生产会死」的 bug（第一个是票 02 的装配倒置）。
> 两个都是**装配层**的，而两个都因为测试用了比真实装配更弱的替身而漏掉。
> 票 04 开工前应当先看一眼 `cmd/javdb-rss/main_test.go` 能不能加一条
> 「真装配 + 假上游」的冒烟 —— 那类 bug 只在那里才看得见。

## 评审（两轴，2026-10-13）与处置

固定点 `0abf574`。两轴都判 **OK with notes**，但**都独立指出同一个 P1**：

⭐ **搜索命中之后的失败全都没记 —— 于是那部作品每轮都会重新搜索、重新下载。**

`Download` / `Infohash` / `SaveTorrent` 三个分支当时只打了一条日志就 `return`，
判定表里什么都没留。后果是每轮渲染都重来一遍，而**下载配额是 100 个/小时** ——
这正是票面禁止的「自动重试」。

**已修**：三个分支都调 `recordFailure`。已补
`TestDownloadFailureIsNotRetriedEveryRender`，并做变异检验（把 `recordFailure`
从下载分支拿掉 → 测试报「搜索了 3 次 —— 取种子失败也不该每轮重来」）。

⭐ **第二个 P1（两轴之一指出，我认了）：凭据信号不会自愈。**

换掉 key 之后，那条历史失败记录还在，于是 `/healthz/upstream` **永远**报
`credential_broken: true`，即使新 key 完全正常 —— 那是**假警报**，而假警报会让人
学会忽略这个字段。

**已修**：`Store.DropFailures()` + SIGHUP 时调用。理由是**失败本来就不是判决**
（负结果才是），而 SIGHUP 是运维显式说「我改了东西」的信号 —— 换 key 正是它最常见的用法。
它**不碰负结果**。已补 `TestDropFailuresClearsOnlyFailures`。

其余处置：

| 发现 | 处置 |
|---|---|
| P1/P2 **同义反复断言**：`next_step != mteam.CredentialGuidance`（拿被测实现的常量当期望值） | 已改成断言文案里的关键词（「控制台」/「存取令牌」/「重试无用」）。⚠️ 本文件开头就写着这条标准，而它被我违反了 |
| P2 空的 `case OutcomeMiss:` / `case OutcomeFailed:` + switch 外统一 `return`：删掉这两个 case 测试照样绿 | 已改成显式分组 + `default` 打 ERROR（认不出的结果要**说一声**再跳过，静默跳过会让作品永远拿不到 M-Team） |
| P2 测试注释声称钉住「重启后仍成立」，却没真的重启 | 已补 Close + Open |
| P2 `WithTorrents` 与 `WithSecondSource` 重复，且前者的注释与后者的设计意图矛盾 | 已删（生产装配只剩一个入口） |
| P2 M-Team 侧 3 处把 M-Team 叫「上游」（违反词表） | 已改（指真上游的那些留着 —— 它们是对的） |
| P2 `store.go` 注释里写了「已被缓存」（词表禁用词） | 已改 |
| P2 注释引用了不存在的方法名 `WithMTeamHealth` | 已改 |
| P2 端到端：票面第 7 条要求「空结果 / 401 / 网络错误三种各自落地」在 HTTP 层验 | ⚠️ **仍未做**，见下 |

**仍未做的一项（照实记）**：票面第 7 条要求的那三条**在 HTTP mux 层**的端到端测试
（空结果 / 401 / 网络错误 ⇒ 三种结果各自落地且 feed 行为正确）还没有 ——
现在它们只在 `catalog.Source` 接缝上验过。已把票面那一条从 `[x]` 改回 `[ ]`，
不当它做完了。

> 这是本轮第三个「测试比真实链路弱」的例子（前两个：票 02 的装配倒置、
> 票 03 的类型化 nil）。票 04 开工前应当先看 `cmd/javdb-rss/main_test.go`
> 能不能加一条「真装配 + 假上游」的冒烟。

## 真装配冒烟（2026-10-13，票 04 开工前补的）

本轮已经出过**三个**「测试全绿但生产会死/会烧配额」的 bug，而其中两个在装配层，
两个都是因为**测试用了比真实装配更弱的替身**：

| bug | 为什么测试漏了 |
|---|---|
| 装饰器装配倒置（票 02） | 测试用**裸假 source** 当 inner，绕过了「mteam 与 pin 谁包谁」 |
| 类型化 nil（票 03） | 测试传**字面 nil**，不是 main 里那个 `var st *mteam.Store` |

已补两条钉子：

- `cmd/javdb-rss/main_test.go` 的 `TestBuildSourceWithMTeamUsesTheSecondSource`
  —— 走**真正的** `buildSource` 且**传一个真的判定表**（命中与种子字节预置在表里，
  因此这条测试不发任何 HTTP）。装配顺序错一位它就响；已做**变异检验**确认
  （变异回去 → 报「第二个来源根本没生效（装配顺序错了？）」）。
- `internal/httpapi` 的 `TestWithSecondSourceIgnoresNil`
  —— 钉住类型化 nil 的**第一层**（`WithSecondSource` 的 nil 判断），
  此前只有第二层（Store 读方法 nil-safe）有钉子。

> 这两条补完之后，装配层那两类 bug 才算真的有网兜着。
