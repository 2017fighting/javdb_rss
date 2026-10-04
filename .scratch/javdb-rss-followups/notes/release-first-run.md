# 发布链路首跑记录（tag → GHCR）

[`.github/workflows/release.yml`](../../../.github/workflows/release.yml) 的**首次真跑**，
以及它验掉与暴露出来的东西。本笔记只记**事实与日期**；这里每一条都能用文末的命令复核。

## 结论

**首次运行即绿。** 推 `v1.0.0`（提交 `051ec18`）后 **2m35s** 内产出
`ghcr.io/2017fighting/javdb-rss`，两个 tag（`1.0.0` 与 `latest`）指向**同一个 index**
`sha256:d8fcc921…`，两个平台都在，包**创建即 public**，匿名可拉。

| 项 | 值 |
|---|---|
| 仓库 | `2017fighting/javdb_rss`（public） |
| workflow | `.github/workflows/release.yml`（`Release`，id `374804013`） |
| **首次运行** | run [`37226112866`](https://github.com/2017fighting/javdb_rss/actions/runs/37226112866)，sha `051ec18`，2026-10-04T18:52:40Z → 18:55:15Z，**success**，2m35s |
| job 1 | `提交先过 CI 同款检查` 18:52:43 → 18:53:42（**59s**）：checkout@v7 / setup-go@v7 / `make fmt-check` / `make vet` / `make race` 五步全绿 |
| job 2 | `构建并推送镜像` 18:53:45 → 18:55:14（**89s**）：qemu@v4 / buildx@v4 / login@v4 / metadata@v6 / build-push@v7 全部 success，含最后的摘要步骤 |
| 镜像 | `ghcr.io/2017fighting/javdb-rss:1.0.0` 与 `:latest` |
| index digest | `sha256:d8fcc9217b552eb34394e44c87dc609b742d34582e9542d19ae80f1f7e32a739` |
| 平台 | `linux/amd64` = `sha256:fc2a7ed7…`、`linux/arm64` = `sha256:9c430c69…` |
| 可见性 | **public**（包创建时即如此，见下） |
| provenance | index 里**恰好两个** manifest（`provenance: false` 生效：没有 attestation） |

同一个 sha 上 `ci.yml` 也跑了一次（run `37226108573`，success）—— 即这次的
「先 `docker build` 再 `make build`」顺序与 `.dockerignore` 的修正都在真 CI 里过了一遍：
**构建上下文从 `8.16MB` 降到 `1.03MB`**（`grep 'transferring context'` 复核）。

## 推 tag 之前本地先验掉的两件

1. **Dockerfile 的交叉编译路径**（本机 arm64，Docker 29.4.0 / buildx v0.33）：
   `FROM --platform=$BUILDPLATFORM` + `ARG TARGETOS/TARGETARCH` 两次构建都成功，
   `RUN … go build` 那步 **arm64 5.7s / amd64 6.0s** —— amd64 也是原生速度，
   没有全量 QEMU（模拟器只用在最终 alpine 阶段）。
   解出的二进制分别是 `ELF 64-bit … ARM aarch64` 与 `ELF 64-bit … x86-64`，
   不是只看 manifest 声称的架构。
2. **`VERSION` 注入**：起 arm64 容器（挂 `deploy/config.docker.yaml`、`/state` 指到临时目录），
   `/version` → `{"version":"v1.0.0-dryrun","provider":"appapi",…}`，`/healthz` 200，
   日志里 `pin 表已加载 path=/state/pin.json 条数=0`。

发布后的镜像又验了一遍同样的两件（`ghcr.io/2017fighting/javdb-rss:1.0.0`，
本地 pull 后起容器）：`/version` → **`v1.0.0`**（tag 原文，与 `make build` 的
`git describe` 同形），`/healthz` 200。

## 首跑推翻的一条写票判断：「包可见性要手动翻」是错的

建票时（Q16）的判断是「`GITHUB_TOKEN` 做不了，需要包所有者权限另做一步」，
并为此准备了两条退路（`gh api --method PATCH … visibility=public`、或在 UI 里点）。
**实测：包创建时就是 public，两条退路都没用上**，我也没有对包做任何设置。
机制是「用仓库自己的 `GITHUB_TOKEN` 推的包会链接到仓库并继承它的可见性」，
而本仓库是 public。

留一条边界：若将来包与仓库的链接断了，可见性就需要显式设置了 ——
那时 `gh api` 那条命令仍然可用（当前 token 有 `write:packages`）。

## 首跑顺带看见的两件小事（都不是本票引入的）

1. **`org.opencontainers.image.licenses` 是空串。** metadata-action 会去读仓库的
   LICENSE 文件，而本仓库**没有 LICENSE**（只有 `THIRD_PARTY_NOTICES.md`）——
   于是它写了一个空值。镜像元数据把一个仓库层面的事实暴露了出来：
   这个仓库目前没有许可证。**没动它**：加不加许可证不是发版链路该决定的事。
2. **`docs/adr/` 与「发布」这套词汇第一次进了这个仓库。** `CONTEXT.md` 的
   Language 段没动 —— 发布属于实现层，不是「订阅收敛」那个领域里的词。
   唯一的用词风险是「**钉**版本」（pin 镜像 tag）与「**钉住** (Pin)」（记住上次选中的
   infohash）撞车：README 的「发版与拉取镜像」刻意只说「钉住的形状」，把
   「钉全量精度 tag」放在 ADR 里，不让两个 pin 并排出现。

## 复核方法

```bash
# 这次运行的两个 job 与耗时
gh api repos/2017fighting/javdb_rss/actions/runs/37226112866/jobs \
  --jq '.jobs[] | "\(.name) \(.started_at) → \(.completed_at)"'

# 镜像的 tag 与 digest（含每个平台那份 untagged manifest）
gh api /user/packages/container/javdb-rss/versions \
  --jq '.[] | "\(.metadata.container.tags) \(.name)"'

# 匿名（不带任何凭据）能不能拉到：token 端点 + manifest
TOKEN=$(curl -s "https://ghcr.io/token?scope=repository:2017fighting/javdb-rss:pull&service=ghcr.io" | jq -r .token)
curl -s -H "Authorization: Bearer $TOKEN" \
  -H 'Accept: application/vnd.oci.image.index.v1+json' \
  https://ghcr.io/v2/2017fighting/javdb-rss/manifests/1.0.0 | jq -r '.manifests[].platform'

# 构建上下文大小（对照 8.16MB）
gh run view 37226108573 --log | grep 'transferring context'

# 镜像里的版本串与平台（本地是 arm64，pull 到的是 arm64 那份）
docker run --rm -p 127.0.0.1:18081:8080 \
  -v "$PWD/deploy/config.docker.yaml:/config/config.yaml:ro" \
  -v /tmp/dryrun-state:/state ghcr.io/2017fighting/javdb-rss:1.0.0 &
curl -s 127.0.0.1:18081/version
```

## 票 15 落地后的复核（2026-10-05）

两份容器部署改拉这里发布的镜像后，重新验了一遍「拉得到」。用的是**真**
`deploy/docker-compose.yml`，在一个**没有源码、没有 Go 工具链**的临时目录里：

```bash
cp deploy/docker-compose.yml /tmp/check/ && cd /tmp/check
mkdir -p data token state && cp <repo>/deploy/config.docker.yaml data/config.yaml
docker compose up -d          # 拉 ghcr.io/2017fighting/javdb-rss:1.0.0
curl -s 127.0.0.1:8080/version   # → {"version":"v1.0.0",...}
curl -s 127.0.0.1:8080/healthz   # → ok
```

容器报 healthy，日志里 `pin 表已加载 path=/state/pin.json 条数=0`。
k8s 那份仍是同一个坐标（`imagePullPolicy: IfNotPresent`），两处不许漂由
`internal/config/deploy_image_test.go` 守着。细节见
[票 15 的 Answer](../issues/15-deploy-pull-ghcr-image.md)。

## 与其它文档的关系

- 票面与决策表：[`../issues/13-release-image-ghcr.md`](../issues/13-release-image-ghcr.md)
- 为什么这么发（GHCR、不推浮动 tag、关 provenance）：[`docs/adr/0001`](../../../docs/adr/0001-release-images-to-ghcr.md)
- 这个文件在 ci.md 里被两处引用（构建上下文那条的「2026-10-05 已修」与
  Node 20 那条的「升级为不一致」）：[`../../javdb-rss/notes/ci.md`](../../javdb-rss/notes/ci.md)
- 拆出来的两件（均已 resolved，2026-10-05）：[14](../issues/14-unify-action-versions.md)（两套 action 版本）、
  [15](../issues/15-deploy-pull-ghcr-image.md)（部署已改拉已发布镜像：
  `deploy/docker-compose.yml` 与 `deploy/k8s.yaml` 都钉 `ghcr.io/2017fighting/javdb-rss:1.0.0`）
