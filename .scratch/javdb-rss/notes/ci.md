# CI 首跑记录（GitHub Actions）

`.github/workflows/ci.yml` 的**首次真跑**与它暴露出的、本地测不出的东西。
本笔记只记**事实与日期**；这里出现的每条都能用文末的命令复核。

## 结论

**首次运行即绿，此后 12 次推送全绿，0 次失败。** 容器构建那一步在 CI 里也过了 ——
本地那次是「手工验证」，CI 这次是**干净检出**上重跑，两者不同（见下）。

| 项 | 值 |
|---|---|
| 仓库 | `2017fighting/javdb_rss`（public） |
| workflow | `.github/workflows/ci.yml`（`CI`，id `370746223`） |
| **首次运行** | run [`36661326933`](https://github.com/2017fighting/javdb_rss/actions/runs/36661326933)，sha `46fec9c`，2026-09-30T02:45:49Z，**success**，96s |
| 最近一次 | run `36691953808`，sha `99790c7`，2026-09-30T08:47:50Z，success，64s |
| 运行总数（截至 2026-09-30） | **12，全部 `push` 到 `main`，全部 success** |
| 失败 / 取消 / 超时 | **0** |

### 首次运行逐步结果（job `109716537179`）

8 个步骤全绿，含最后那个容器构建：

```
Set up job · checkout@v4 · setup-go@v5 · gofmt · go vet · test (race) · build · docker build
```

`docker build` 那一步 02:46:54 → 02:47:18（24s），从 `golang:1.27-alpine` 拉取、
`apk add ca-certificates tzdata`、编译、写出镜像 `javdb-rss:46fec9c`
（`writing image sha256:bf5cb9ad…`）**全部成功**。这是本仓库第一次在 CI 里构建镜像。

### 为什么「首次运行」这一步值得单独记

写票时（`4b99cd2`）仓库**从未 push 过**，CI 一次都没跑。本地验证镜像构建时
docker daemon 甚至没起来（见 [`../issues/04-config-and-deploy.md`](../issues/04-config-and-deploy.md)
的「⚠️ 未验证」）。所以「CI 里构建得出镜像」在这之前是纯假设。
现在它是实测事实，**且是在 `make build` 之后、对同一棵工作树**跑的（这点正是下一条的来源）。

## CI 暴露的、本地那份手工验证看不到的问题

### 1. 已构建的二进制被塞进了 docker 构建上下文（8 MB）

**现象**：CI 的 docker 步骤里 `transferring context: 8.16MB`（首次）
/ `8.52MB`（最近）。一次干净检出的 docker 上下文只有 **0.23 MB**
（本地复现：检出 `46fec9c` 后 `COPY . .` 得到 244,270 字节；先 `make build` 再构建
则变成 8,211,150 字节 —— 与 CI 的 8.16 MB 对得上）。

**成因**：`ci.yml` 的步骤顺序是 `… → build → docker build`。
`make build` 先在工作树根目录产出 `javdb-rss`（CI 里 7.6 MB），
然后 `docker build .` 把**整个工作树**当上下文；而 `.dockerignore` 只排了
`bin/` 与 `dist/`，**没有排根目录的 `javdb-rss`**（它同时被 `.gitignore` 的
`/javdb-rss` 忽略，所以 `git status` 看不见，人工审阅也容易漏）。
于是 `COPY . .` 把这份二进制拷进了 build 阶段。

**影响**：**只影响构建时间与层体积，不影响最终镜像的正确性** ——
最终阶段只 `COPY --from=build /out/javdb-rss`，实测镜像里只有
`/usr/local/bin/javdb-rss` 一个文件，没有残留的根二进制。
所以它不是 bug，是一个**没被注意到的浪费**，以及一个潜在脚注：
若哪天用不同的 `-ldflags` 先构建一次，那份「旧」二进制就会静静躺在 build 层里。

**为什么本地看不出来**：本地跑 `make docker-build` 时工作树里**本来就有**
（或本来就没有）一个 `javdb-rss`，取决于你上次 `make build` 没有；
而 CI 是**确定会先 build 再 docker build**，所以它每次都带上那份二进制。
这是「本地跑得通 ≠ CI 跑得通」的一个具体例子 —— 只不过这次差异是无害的那侧。

**没有就地改**（按票面要求）：改法是在 `.dockerignore` 加一行 `/javdb-rss`
（带上开头的 `/`，避免误伤同名目录）。留给后续，因为票面明确说「记进笔记而不是就地糊过去」。

**2026-10-05 已修**（票 13 的「顺带」项，用户明确要求一起做）：`.dockerignore` 加了
`/javdb-rss`，并把 `ci.yml` 里的容器构建**挪到 `make build` 之前** —— 两头都堵上。
只加忽略行也够，但那样这个浪费就取决于「有人记得那行」；顺序再排一道，它在结构上
不再可能发生 —— 真 CI 实测：构建上下文 `8.16MB` → `1.03MB`。复核见
[`../../javdb-rss-followups/notes/release-first-run.md`](../../javdb-rss-followups/notes/release-first-run.md)。

（注：`1.03MB` 不等于本笔记上文那个 `244,270` 字节 —— 那是 webui 落地**之前**的树，
现在多出来的大头是 `internal/webui/assets`，不是那份二进制。）

### 2. 两个 action 的 Node 20 弃用警告

每次运行都有一条，来自 `actions/checkout@v4` 与 `actions/setup-go@v5`
（它们声明跑 Node 20，被强制在 Node 24 上跑）：

```
##[warning]Node.js 20 is deprecated. The following actions target Node.js 20
but are being forced to run on Node.js 24: actions/checkout@v4, actions/setup-go@v5.
```

**现状**：只是 warning，任务照样 success。**当前最新**：checkout `v7.0.1`、
setup-go `v7.0.0`（2026-09-30 查）。升级是**跨大版本**（setup-go 从 v5 → v7 跨了 v6），
不是机械替换，需要读两版 release notes 再动。**本票不改**。

**2026-10-05 升级为不一致**：票 13 新增的 `release.yml` 直接用当前大版本
（checkout@v7 / setup-go@v7），而 `ci.yml` 仍停在 v4/v5 —— 这条从「已知遗留」变成了
**同一仓库里两套 action 版本并存**，因此不再留在此处，收进
[`issues/14-unify-action-versions.md`](../../javdb-rss-followups/issues/14-unify-action-versions.md)。
（首次发布 run `37226112866` 里 checkout@v7 与 setup-go@v7 都已正常跑过一遍。）

### 3. `ubuntu-latest` 将在 2026-11 迁到 Ubuntu 26.04

运行底座的注解：

```
"The ubuntu-latest label will migrate to Ubuntu 26 beginning October 19, 2026."
```

**当前** `ubuntu-latest` 解析为 `ubuntu-24.04`（runner `2.337.0`，镜像
`ubuntu-24.04 / 20260920.314.1`），Go 由 `go-version-file: go.mod` 定为 `1.27.1`
（与本地一致），setup-go 缓存命中。**等到 11 月迁移时再看一次即可**——
本项目的 CI 只依赖 Go 与 Docker，两者在 26.04 上都有，预判无需改动。
登记在此，是为了迁移当天有据可查，而不是重新考古。

## 复核方法

```bash
# 全部运行（含结论与耗时）
gh api "repos/2017fighting/javdb_rss/actions/runs?per_page=100" \
  --jq '.workflow_runs[] | "\(.created_at) \(.conclusion) \(.head_sha[0:7])"'

# 首次运行的逐步结果
gh api repos/2017fighting/javdb_rss/actions/runs/36661326933/jobs \
  --jq '.jobs[0].steps[] | "\(.number) \(.conclusion) \(.name)"'

# 构建上下文大小（找 "transferring context:"）
gh run view 36661326933 --log | grep 'transferring context'

# 干净检出里 docker 上下文的大小（对照 8 MB，把 Command 拷进 Dockerfile 跑）
# 无先 build：244,270 字节；先 make build：8,211,150 字节
```

## 与其它文档的关系

- 票面：[07 — CI 首次跑通](../../javdb-rss-followups/issues/07-ci-first-run.md)
- 部署形态（`Makefile` / Dockerfile / `ci.yml` 的出处）：
  [`../issues/04-config-and-deploy.md`](../issues/04-config-and-deploy.md)
- `ci.yml` 在 `789f0b1`（«落地部署形态»）随部署形态一起加入。
