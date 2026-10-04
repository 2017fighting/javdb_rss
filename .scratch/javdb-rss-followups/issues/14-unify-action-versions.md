# 14 — 统一 action 版本：ci.yml 的 checkout@v4 / setup-go@v5 → v7

**What to build:** `ci.yml` 与 `release.yml` 用**同一批** action 大版本。
现在 `release.yml`（票 13 新增）用的是当前大版本（`checkout@v7`、`setup-go@v7`、
docker 系 `v7/v4/v4/v6`），而 `ci.yml` 停在 `checkout@v4` / `setup-go@v5` ——
两套并存会让下一个读的人问「为什么这里不一样」。

背景见 [`javdb-rss/notes/ci.md`](../../javdb-rss/notes/ci.md) 第 2 条：这两个旧版本
在每次运行里打一条 Node 20 弃用警告；当时判为不开票的维护性观察，票 13 之后它
变成了**同一仓库里两套版本**，所以现在开票。

**Type:** task（形态是机械替换，但先得读跨大版本的 release notes）

**Blocked by:** None（票 13 只是把它从「观察」升级成「不一致」）

**Status:** resolved（2026-10-05；真 CI run `37226687575` 8 步全绿，弃用警告归零）

- [x] 读 `actions/checkout` v5–v7 与 `actions/setup-go` v6–v7 的 release notes
      （跨大版本，不是机械替换）：七个变更逐条对照本仓库，只有一条真影响到我们
      （setup-go 默认缓存键），对照表在 [`../../javdb-rss/notes/ci.md`](../../javdb-rss/notes/ci.md) 第 2 条
- [x] `ci.yml` 的两处 ref 升到 `v7`，`release.yml` 不动（它已经是 v7）
- [x] 一次真实 CI 运行里确认：Node 20 弃用警告**消失**（整份日志 `grep -c` 得 0），其余步骤全绿
- [x] `notes/ci.md` 第 2 条从「当前最新是 v7／本票不改」改成已解决，并记下这次的运行号
- [x] 顺带复核第 3 条：**尚未发生** —— 那一跑仍解析为 `ubuntu-24.04`（`24.04.5 LTS`）

## 有意不做的

- **不动 docker 系 action**：`release.yml` 里它们已经是当前大版本。
- **不动 `ci.yml` 的检查清单本身**：它逐条点名 `make` 目标是刻意的
  （见票 13 的「检查复用方式」），本票只换 action 版本。

## Answer（2026-10-05）

**五个框全过。** 提交 `1cac8f5`，run
[`37226687575`](https://github.com/2017fighting/javdb_rss/actions/runs/37226687575)，
job `111507675176`，**8 个步骤全绿**（与 ci.md §1 同一口径：`Set up job` + 7 个实质步骤
—— checkout@v7 · setup-go@v7 · gofmt · go vet · test (race) · docker build · build；
两个 `Post` 清理步也 success）。两个 workflow 现在共用同一批大版本。

验收用的是**差分判据**，不是「任务绿了」（升级前也绿）：

```bash
gh run view 37226687575 --log | grep -c 'Node.js 20 is deprecated'   # → 0（本票之后）
gh run view 37226396921 --log | grep -c 'Node.js 20 is deprecated'   # → 1（升级前，sha 8eecacb）
```

### 跨大版本读了什么（唯一真花代价的一条）

七个变更里六条对本仓库不适用 —— 凭据持久化到独立文件（CI 里没有 `git` 网络操作）、
v7 拦 `pull_request_target`/`workflow_run` 上的 fork PR（我们只挂 `push`/`pull_request`）、
`GOTOOLCHAIN=local`（`go.mod` 要 `1.27.1`，装到的就是它）、优先 `toolchain` 指令
（本仓库没有该行）、runner 下限 `v2.327.1`（托管 runner `2.337.0`）、Node 24 本身。
逐条理由在 ci.md 的对照表里。

**真代价是 setup-go `v6.3.0` 把默认缓存键从 `go.sum` 换成 `go.mod`**：升级后第一跑
`Cache is not found`，`go vet`/`test (race)`/`build` 由 `1s / 7s / 0s` 变
`17s / 28s / 12s`，整跑 46s → 1m40s。**已复核它只有一次**：同一运行的 attempt 2
命中 `Cache restored from key: setup-go-Linux-x64-ubuntu24-go-1.27.1-83d87e14…`，
三步回到 `0s / 5s / 0s`，整跑 45s。

### 第 3 条的复核结论

**没有发生。** 那一跑 `Image: ubuntu-24.04`、OS `24.04.5 LTS`、Included Software
`…/ubuntu24/20260927.320/…`、runner `2.337.0`。公告的准确形状是
[`actions/runner-images#14748`](https://github.com/actions/runner-images/issues/14748)：
**10-19 起分批滚动，11-19 前完成**（「2026-11」是完成日，不是起始日），
已写进 ci.md 第 3 条，连「迁移那天的两个可见信号」一起。

### 票面一处笔误（原文不改）

票面背景那句把 docker 系记成 `v7/v4/v4/v6`（四个），而 `release.yml` 里是**五个**：
`setup-qemu-action@v4` · `setup-buildx-action@v4` · `login-action@v4` ·
`metadata-action@v6` · `build-push-action@v7` —— 少记了 `login-action` 那个 `v4`。
结论不受影响（它们确实都已是当前大版本，本票也确实没动它们）。
按票 12 的先例（错在记录里就地订正、不回头改旧文），原文留着，订正记在这里。
