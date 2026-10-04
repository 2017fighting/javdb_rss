# 13 — tag 推送即发布镜像到 GHCR

**What to build:** 推 `vX.Y.Z` tag 时自动构建并推送 `ghcr.io/2017fighting/javdb-rss`
（`linux/amd64` + `linux/arm64`），并且**不在公开仓库里留下「未验证就成立」的假设**：
发布前先过与 CI 同一批 `make` 目标，发布后把真正推上去的平台写进 run 摘要。

交付物：[`.github/workflows/release.yml`](../../../.github/workflows/release.yml)、
Dockerfile 的多平台交叉编译路径、`.dockerignore` 与 `ci.yml` 里那处构建上下文的修正、
ADR [`docs/adr/0001-release-images-to-ghcr.md`](../../../docs/adr/0001-release-images-to-ghcr.md)、
README 的「发版与拉取镜像」小节。

**Blocked by:** None

**Status:** resolved（2026-10-05；首次运行即绿，事实见 [`../notes/release-first-run.md`](../notes/release-first-run.md)）

- [x] 推 `vX.Y.Z` → 镜像出现在 GHCR，tag 为 `X.Y.Z`；正式版另有 `latest`，预发布没有
- [x] 镜像 manifest 里 `linux/amd64` 与 `linux/arm64` **都在**（不是声明了，是 inspect 里列出来）
- [x] 镜像里的 `/version` 报告版本串＝tag 原文（`v1.0.0`）
- [x] 发布前先跑 `make fmt-check / vet / race`；它失败时**不会**推送镜像
- [x] 包可见性为 public：**匿名**（不带凭据）就能拉到 manifest
- [x] 本地双平台干跑通过（不推送），且 `.dockerignore` 的修正让构建上下文不再含那份 8 MB 二进制

## 决策（2026-10-04，与用户逐条 grilling）

第一轮问的是触发与形态，第二轮补了两处 Q4 推翻、预发布策略与镜像 tag，
第三轮是文档落点、本地验证强度与发布权限。

| 面 | 决定 | 为什么 |
|---|---|---|
| 触发 | 严格 semver `v[0-9]+.[0-9]+.[0-9]+` 与预发布 `…-*` 两条 | `v*.*.*` 会放过 `vfoo.bar.baz`；预发布是真实场景，拒绝它只会让人临时手改 workflow |
| 镜像 tag | `X.Y.Z` + `latest`（仅正式版） | 浮动 `1.2`/`1` 在单用户场景里只会造成静默升级 |
| 覆盖 | 允许；恢复路径＝`git tag -f` + 重推 | 不可覆盖意味着一次失败的发版要先删包，代价大于收益 |
| 平台 | `linux/amd64,linux/arm64`，`BUILDPLATFORM` 原生交叉编译 | 部署形态（homelab compose / k8s）正好是 ARM 高发区；全量 QEMU 是纯浪费 |
| 发布前门槛 | release.yml 里内联 `make fmt-check/vet/race` | tag 推送不触发 `ci.yml`；真源是 Makefile，抽 workflow 换不到单一真源 |
| 检查复用方式 | **推翻** Q4 的「抽成可复用 workflow」 | ci.yml 的步骤本就是 `make` 目标的薄壳，抽取只多一个文件并把 docker build 拆成独立 job |
| 包可见性 | public（**实测：创建即 public，无需另做一步** —— 见 Answer 的订正） | 仓库是 public，包跟着 public 就省掉部署机上的 PAT |
| 元数据 | `provenance: false`，开 OCI labels（source/revision/version/created） | 可拉取性 > 供应链仪式；`revision` 是排查「线上镜像是不是那个 commit」的唯一凭据 |
| 版本串 | 注入 `VERSION=v1.2.3`（tag 原文），镜像 tag 是 `1.2.3` | 与本地 `make build` 的 `git describe` 逐字一致，/version 不骗人 |
| 首个版本 | `v1.0.0`，不建 GitHub Release | 自动 notes 只会生成一串「docs: …」噪音；要 Release 时手动建 |
| action 版本 | 新文件直接用当前大版本（checkout@v7 等） | 新文件没有历史包袱，出生就没有 Node 20 弃用警告；ci.yml 的统一升级另开 [14](14-unify-action-versions.md) |
| 非 main 上的 tag | 允许，不校验 | hotfix 分支打 tag 是正常操作；「提交绿过」由 verify job 保证，与分支无关 |
| 部署侧 | 三件套改成拉镜像**另开** [15](15-deploy-pull-ghcr-image.md) | 本票只到「镜像已存在且可匿名拉取」为止 |

## 有意没做的

- **不加 `workflow_dispatch`** 当重发入口：那样构建的 ref 语义与 tag 不一致，比重新打 tag 更绕。
- **不推浮动小版本 tag、不打 GitHub Release**（理由见 ADR 与上表）。
- **不动 `Makefile`**：多平台镜像 `--load` 不进本地 docker，一个「多平台本地构建」目标
  天然是半残的；双平台干跑用一次性命令。
- **不动 `deploy/javdb-rss.service`**：systemd 那份跑的是二进制，与镜像无关。

## Answer（2026-10-05）

**六个验收框全过，首次运行即绿。** 完整事实与复核命令在
[`../notes/release-first-run.md`](../notes/release-first-run.md)，这里只列结论：

- 推 `v1.0.0`（提交 `051ec18`）→ run
  [`37226112866`](https://github.com/2017fighting/javdb_rss/actions/runs/37226112866)，
  **2m35s，两个 job 全绿**；镜像 `ghcr.io/2017fighting/javdb-rss` 出现 `1.0.0` 与
  `latest`，两者指向**同一个 index** `sha256:d8fcc921…`
- manifest 里 `linux/amd64`（`fc2a7ed7…`）与 `linux/arm64`（`9c430c69…`）都在，
  且 index 里**恰好两个** manifest —— `provenance: false` 生效，没有 attestation
- 镜像里 `/version` 报告 **`v1.0.0`**（tag 原文）；这不是只看 run 的结论，是本地
  `docker pull` 下来起容器 curl 出来的
- 发布前的三条 make 在 verify job 里跑了 **59s** 全绿；失败时 `publish` 因为 `needs:`
  根本不会开始（不是「推了再回滚」）
- 可见性 public：匿名（不带任何凭据）向 ghcr 的 token 端点与 manifest 端点都拿到 **200**
- 本地双平台干跑通过（arm64 `go build` 5.7s / amd64 6.0s，都是原生速度）；
  `.dockerignore` 与步骤顺序的修正真 CI 实测：构建上下文 `8.16MB` → **`1.03MB`**

### 一处订正：包可见性不需要手动翻

上表「包可见性」那一行原本写的是「`GITHUB_TOKEN` 做不了，需包所有者权限另做一步」
（Q16 据此准备了两条退路：`gh api --method PATCH` 与 UI）。**这条判断是错的** ——
包在创建时就是 public，两条退路都没用上，我也没有对包做任何设置。机制是
「用仓库自己的 `GITHUB_TOKEN` 推的包会链接到仓库并继承可见性」，而本仓库是 public。
行已就地订正，细节在笔记里。

### 顺带看见、**没有**就地改的两件

1. 镜像标签里 `org.opencontainers.image.licenses` 是**空串**：metadata-action 去读仓库的
   LICENSE 文件，而本仓库没有（只有 `THIRD_PARTY_NOTICES.md`）。加不加许可证不是发版
   链路该决定的事。
2. `CONTEXT.md` 的 Language 段**不动**：发布属于实现层，不属于「订阅收敛」那个领域。
   唯一的用词风险是「钉版本」（pin 镜像 tag）与「钉住 (Pin)」（记住上次选中的 infohash）
   撞车 —— README 只写「唯一该被人钉住的形状」，两个 pin 不并排出现。
