# 15 — 三份部署配置改为拉已发布的镜像

**What to build:** 镜像可以从 GHCR 拉了（票 13），于是部署不再需要源码与 Go 工具链。
把两处容器化部署改成引用已发布的镜像，systemd 那份不动（它跑的是二进制）。

**Blocked by:** [13](13-release-image-ghcr.md)

**Status:** ready-for-agent

- [ ] `deploy/docker-compose.yml`：`build:` 段换成
      `image: ghcr.io/2017fighting/javdb-rss:1.0.0`（**钉全量精度版本**，
      与 ADR [0001](../../../docs/adr/0001-release-images-to-ghcr.md) 的「不推浮动 tag」一致）；
      把 `build:` 的做法缩成注释留给想自己构建的人
- [ ] `deploy/k8s.yaml` 第 86 行的 `image: javdb-rss:latest` 换成同一个 pinned tag
      —— 它现在其实**无处可拉**，这正是本票的一半理由
- [ ] `deploy/javdb-rss.service` **不动**，但 README 的部署表要说明这份跑的是二进制
- [ ] README「部署」段补：镜像已发布、坐标、怎么升级（改那一行 tag）、
      以及「升级前先确认 pin 状态目录仍可写」（那一条现有文字保留）
- [ ] `make test` 仍绿（三份配置的键集合由 `internal/config/examples_sync_test.go` 强制一致）

## 要一起定的（建票时未定）

- **k8s 的镜像拉取策略**：默认 `IfNotPresent` 对 pinned tag 是对的；
  若哪天允许浮动 tag，就得重新想这件事 —— 这也是「不推浮动 tag」的一个附带好处。
- **要不要顺手加一个「最新版本是多少」的可见性**：现在想知道最新版得去看 GHCR 页面
  或 `git tag`。可以为空（本服务是拉取方，没有自更新需求）。

## 有意不做的

- **不把 tag 写进更多地方**（如 k8s ConfigMap、README 的命令示例之外）：
  一处 pin 就够了，多写几处只会多几个会漂的点。
