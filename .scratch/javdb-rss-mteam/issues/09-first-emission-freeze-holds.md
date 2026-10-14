# 09: 首发冻结真的成立

**What to build:** 一部已经按上游下发过的作品**不会**被悄悄换来源；而一部已经按馒头
下发的作品，**下一次渲染仍然是馒头那条**（`<enclosure>` 仍是本服务的种子地址，
`guid` 不变）。来源只在「重置」之后才会变。

**Blocked by:** None (can start immediately).

**Status:** resolved（2026-10-14）

## 背景：这是 Docker 验收当场抓到的 P0

```
verdict.at    = 01:31:09~14   ← 4 个命中（馒头那条 infohash）
pin.pinned_at = 01:32:33      ← 晚 80 秒
WARN: pin 切换 —— 旧磁链=e8556ddc…（馒头）新磁链=ec52393f…（上游）
```

第一次渲染**确实**把馒头那条钉住了（enclosure 是我们的种子地址，实测），
**第二次渲染又把它换回了上游**。机制：

1. 有 pin 的作品 → M-Team 层「原样放行」—— 放行的是**上游那个候选池**；
2. 而 `pin.Policy` 拿到那个池子后，按既有的 cnsub/时间规则判定上游那条更好 → **换 pin**；
3. 于是 guid 抖回上游 = 客户端多下一份，而且**馒头从此再也不生效**。

「原样放行 → pin 保持它的 pin」这个推理是错的：**策略会切换**，
所以「放行上游池子」根本不是中立动作。

## 交付

- [x] `pin.Record` 记住来源（零值 = 上游；旧文件照读，不炸）
- [x] 来源在 pin.json 里是**可读的**（手改那份文件是本服务的既有运维手段，
      一个数字位会让它变成猜谜）
- [x] `recordOf` / `magnetOf` 带上它 —— 投影不再把馒头的 pin 变成一条**磁力**
      （私种的裸磁力是**死链**，这是同一个根因的第二个症状）
- [x] 策略：池子的来源与 pin 的来源不同 ⇒ **永不切换**
      （冻结落在策略里，而不是靠上层「放行」得恰到好处）
- [x] 端到端：第一次渲染把馒头那条钉住 ⇒ **第二次渲染还是它**
      （enclosure 仍是 `/torrent/…`，guid 逐位不变）
- [x] 上游 pin 的既有行为**不变**：cnsub 升级仍会切（那是 ticket 09 定稿的规则）
- [x] 变异检验：把「来源不同就不切」那一条去掉 ⇒ 新测试当场响
- [x] `make check` 与 `make race` 全绿

## Comments

写这张票时我确认了一件事：这个修复**必须**动 `pin.json` 的 schema，
而 spec 当初明确否掉了它（「收益只是措辞更集中」）。验收证明那个收益不是措辞 ——
**真正决定最终发哪条的是策略，而策略只有在知道来源的前提下才可能尊重来源。**
决策反转记在票 10。

## Answer（2026-10-14）

**落点**：`catalog.MagnetSource` 加了 `String`/`MarshalText`/`UnmarshalText`（来源在
`pin.json` 里是**可读的名字**，认不出的名字**报错**而不是静默当上游）、
`pin.Record.Source`、`recordOf`/`magnetOf` 带上它、以及
`DefaultPolicy.KeepOrSwitch` 里那条「来源不同 ⇒ 永不切换」。

⭐ **最关键的一点：冻结落在策略里，不在装饰器里。**
`pin` 的策略才是决定最终发哪条的地方，所以「M-Team 层原样放行」只有在策略
自己认得来源时才是中立的。

**测试**（两处，都做了变异检验）：
- `internal/pin`：`TestKeepOrSwitchNeverCrossesSources`（四个情形：两个跨来源不切 +
  两个同来源内 cnsub 照旧切）、`TestRecordSourceIsReadableAndRoundTrips`
  （可读名字 / 旧文件照读 / 认不出的名字报错）。
- `internal/httpapi`：`TestMTorrentSourceSurvivesASecondRender` —— **渲染三次**。
  变异（去掉跨来源那条）复现的正是 Docker 抓到的原现象：
  `第二次渲染的 enclosure = "magnet:?xt=urn:btih:upstream-hash…"` +
  `guid 变了`。

**真 M-Team 复验（Docker，全新 state）**：三次连续渲染的 4 条 enclosure
**全部**是 `/torrent/…`、guid 集合三次 md5 逐位相同、**零条**「pin 切换」WARN
（修之前是 4 条）。跨容器重建之后 guid 集合仍相同、判定表仍 5 条（不重查）。

> 这条 P0 是**票 07 的 Docker 验收**抓到的，而当时 6 张票的测试全绿 ——
> 因为**所有测试都只渲染一次**，而那个 bug 只在第二次渲染才现形。
