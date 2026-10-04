# 发布镜像走 GHCR，只推全量精度 tag

推 `vX.Y.Z` tag 即由 GitHub Actions 构建 `linux/amd64` + `linux/arm64` 镜像并推送到
`ghcr.io/2017fighting/javdb-rss`，镜像 tag 为 `X.Y.Z`（正式版另加 `latest`）。
选 GHCR 是因为仓库本来就在 GitHub、`GITHUB_TOKEN` 自带推送权限，不必再引入一个
registry 的账号与凭据。

## 背景

三套部署形态里有两套要镜像（Docker Compose、K8s），而在此之前镜像只能在部署机上
`make docker-build` 出来 —— `deploy/k8s.yaml` 里那个 `image: javdb-rss:latest`
其实无处可拉。把「构建镜像」从部署机移到 tag 上，是为了让部署不再需要源码与 Go 工具链。

## Considered Options

- **浮动的小版本 tag（`1.2`、`1`）**：不做。单用户场景里它唯一的作用是让
  `docker compose pull` 静默换版本；要升级请改 tag，那是一个有意识的动作。
- **`latest`**：推，但只对正式版。预发布（`v1.2.3-rc1`）只出 `1.2.3-rc1`，
  不动 `latest` —— 否则一次候选发布就会悄悄改掉所有人的 latest。
- **多平台走全量 QEMU**：不做。`FROM --platform=$BUILDPLATFORM` + `ARG TARGETARCH`
  让 Go 原生交叉编译（实测 arm64 主机上构建 amd64 镜像，`go build` 那步 6 秒、
  与本地架构同速），只有最终 alpine 阶段的几条 `RUN` 需要模拟器。
- **provenance / SBOM attestation**：不做。它会额外产出一个 attestation manifest，
  老一些的 docker/containerd 拉到会看到 multi-manifest 的怪象，而这里没有任何下游
  会去验签名。可拉取性 > 供应链仪式。
- **私有包**：不做。仓库本身就是 public，包跟着 public 就省掉了部署机上的
  `docker login ghcr.io`（这一步要 PAT，也就意味着多一份要保管的凭据）。

## Consequences

- tag 一经推送就会产生一个镜像版本，而 GitHub 不允许删除 tag 上的 run 记录；
  恢复路径是**覆盖**（`git tag -f` + 重推），因此发布链路不做任何「标签不可重写」
  的假设（`concurrency` 用排队而非取消，也是同一个理由）。
- 发布链路必须自带 `make fmt-check / vet / race`：tag 推送**不**触发 `ci.yml`，
  「这个提交绿过」这件事只能在发布这一步重新确立，不能靠假设。
- 部署侧要钉版本时钉的是全量精度 tag（如 `1.0.0`），升级＝改一行。
  两份容器部署示例（`deploy/docker-compose.yml`、`deploy/k8s.yaml`）现在都这么写，
  且两处必须是同一个 tag —— 由 `internal/config/deploy_image_test.go` 守着，
  因为「一份改了另一份没改」的失败方式是静默的。k8s 那份显式写
  `imagePullPolicy: IfNotPresent`（对全量精度 tag 本就是默认值）：tag 内容不会变，
  没必要每次启动都去 registry 问一遍；反过来说，**哪天允许浮动 tag，这一行就得重新想**
  —— 这是「不推浮动 tag」的附带好处。（systemd 那份跑的是二进制，不涉及镜像。）
- 镜像里的 `/version` 报告 `v1.0.0`（tag 原文），镜像 tag 是 `1.0.0`（去 v）。
  两种形状是有意的：前者要与源码和本地 `make build` 的说法逐字对得上，
  后者是 docker 的惯例。
