# 05 — `health` 包与上游细节解耦

**What to build:** `health` 包自称「不依赖任何其他内部包」，只负责记录与打印「上游还能不能通」，但它硬编码了 App API 特有的错误名与一份备灾文档的路径。换一个语义完全不同的检查方时，这段文案就会是错的 —— 而它偏偏是排障时读的那条。

交付：诊断信息由**检查方**提供，`health` 只负责呈现与去重。

**Blocked by:** None — can start immediately.

**Status:** resolved（2026-09-30）

- [x] `health` 包里不再出现 App API 特有的错误名，或指向本项目的文档路径
- [x] 上游失败时日志**仍然**带上可操作的处置动作（现有行为不得回退 —— 这一条曾经回归过：首次失败的日志丢过处置动作）
- [x] 换一个语义不同的检查方时，日志文案无需修改 `health` 包
- [x] 现有的「失败日志带处置动作」测试仍然通过，且仍能在改坏实现时失败

## 实现（2026-09-30）

**接缝：`health.Result.Guidance`。**「下一步」是一句**上游特有的领域知识**，
因此它跟着 `Action` 一起由 `Checker` 提供，`health` 只负责原样记录（进 `Status.Guidance`）
与呈现（写进失败日志的 `下一步` 属性）。只有非空时才写入，避免空属性。

**知识的归属搬到了 `cmd/javdb-rss`**：新增 `upstreamGuidance(action)`，
判据复用 `appapi.IsSignatureAction`（不重写一份名字清单）：

- 签名类失败 → 「签名常量已与服务端不兼容，重试无用，需要改代码：先看 javdb-cli…
  否则见 `.scratch/javdb-rss/notes/dart-toolchain-probe.md`」
- 其余（网络 / 上游 5xx）→ 「这是普通上游/网络故障，稍后会自动重试…」

这也顺带修掉了旧文案的一个真缺陷：它把**任何**失败都带上「去改代码」的处置，
即使真相只是网络抖动。

**测试（两处，都是行为化的）**：

- `internal/health`：`TestFailureLogIsDrivenByCheckerGuidance` 换一个虚构的 SMTP 检查方，
  断言日志出现该检查方自己的 action + guidance，且**不得**出现
  `InvalidSignature` / `ParameterInvalid` / `javdb-cli` / `.scratch/` 任一泄漏。
  实测变异验证：把 guidance 从日志里去掉 → 红；把旧硬编码文案写回日志 → 红。
  `TestFirstCheckFailureCarriesActionableHint`（回归钉）改为断言检查方给的那句话。
- `cmd/javdb-rss`：`TestUpstreamCheckerCarriesGuidance` 让 `upstreamChecker` 打一个
  假 400 `InvalidSignature` 与一个假 502 服务端，分别断言两句话术（含「不该出现另一类话术」）。

## 补跑的 code-review（2026-09-30）

Standards 轴：**0 处违规**，两个 smell 均为判断题且仓规覆盖，判定不修。
Spec 轴：三条，已逐条处理：

1. **P1 —— 变异测试不完整（已修）**。原测试只断言 guidance 的**文本**出现在日志里，
   因此把日志属性名从「下一步」改掉、或把空 guidance 也写成属性，都不会红。
   补两条：`TestFailureLogPinsGuidanceAttribute`（钉属性名与 action/err 不丢）与
   `TestFailureLogOmitsGuidanceWhenCheckerGaveNone`（空 guidance 不留空属性）。
   变异验证：改属性名 → 红；无条件写属性 → 红。
2. **P2 —— scope creep（保留，已记录理由）**。「顺带修掉网络故障也被端出『去改代码』」
   不是新功能，而是**解耦本身暴露出来的**：旧文案把处置写在 health 里，
   根本无从区分两类失败；知识搬回检查方后，不分类反而是错的。
   文档路径从 `02-recover-jdsignature.md` 改为 `dart-toolchain-probe.md`：前者是
   **当初重建签名的过程记录**，后者开篇即自述为「未来 JavDB App 升级导致签名失效时
   重新逆向所需的环境准备清单与避坑指南」，才是真正该指的地方。
3. **P2 —— `Status.Guidance` 是悬空字段（已修）**。原本只存不用。已让
   `/healthz/upstream` 透出 `next_step`（有测试钉住），使该字段真正被消费；
   这也让机读消费方能拿到处置动作，而不只是一个错误名。README 的 JSON 样例同步更新。
