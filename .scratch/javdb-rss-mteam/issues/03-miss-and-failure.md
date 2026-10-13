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
