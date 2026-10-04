# 14 — 统一 action 版本：ci.yml 的 checkout@v4 / setup-go@v5 → v7

**What to build:** `ci.yml` 与 `release.yml` 用**同一批** action 大版本。
现在 `release.yml`（票 13 新增）用的是当前大版本（`checkout@v7`、`setup-go@v7`、
docker 系 `v7/v4/v4/v6`），而 `ci.yml` 停在 `checkout@v4` / `setup-go@v5` ——
两套并存会让下一个读的人问「为什么这里不一样」。

背景见 [`javdb-rss/notes/ci.md`](../../javdb-rss/notes/ci.md) 第 2 条：这两个旧版本
在每次运行里打一条 Node 20 弃用警告；当时判为不开票的维护性观察，票 13 之后它
变成了**同一仓库里两套版本**，所以现在开票。

**Blocked by:** None（票 13 只是把它从「观察」升级成「不一致」）

**Status:** ready-for-agent

- [ ] 读 `actions/checkout` v5–v7 与 `actions/setup-go` v6–v7 的 release notes
      （跨大版本，不是机械替换；这是 ci.md 当时把它留成「已知遗留」的原因）
- [ ] `ci.yml` 的两处 ref 升到 `v7`，`release.yml` 不动（它已经是 v7）
- [ ] 一次真实 CI 运行里确认：Node 20 弃用警告**消失**，其余步骤仍全绿
- [ ] `notes/ci.md` 第 2 条从「当前最新是 v7／本票不改」改成已解决，并记下这次的运行号
- [ ] 顺带复核第 3 条（`ubuntu-latest` 2026-11 迁 Ubuntu 26.04）是否已经发生；
      若已发生，记下解析到的新镜像标签

## 有意不做的

- **不动 docker 系 action**：`release.yml` 里它们已经是当前大版本。
- **不动 `ci.yml` 的检查清单本身**：它逐条点名 `make` 目标是刻意的
  （见票 13 的「检查复用方式」），本票只换 action 版本。
