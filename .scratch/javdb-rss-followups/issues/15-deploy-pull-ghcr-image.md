# 15 — 三份部署配置改为拉已发布的镜像

**What to build:** 镜像可以从 GHCR 拉了（票 13），于是部署不再需要源码与 Go 工具链。
把两处容器化部署改成引用已发布的镜像，systemd 那份不动（它跑的是二进制）。

**Blocked by:** [13](13-release-image-ghcr.md)

**Status:** resolved（2026-10-05；compose 实拉起容器 `/version` 报 `v1.0.0`，`make test` 绿）

- [x] `deploy/docker-compose.yml`：`build:` 段换成
      `image: ghcr.io/2017fighting/javdb-rss:1.0.0`（**钉全量精度版本**，
      与 ADR [0001](../../../docs/adr/0001-release-images-to-ghcr.md) 的「不推浮动 tag」一致）；
      把 `build:` 的做法缩成注释留给想自己构建的人
- [x] `deploy/k8s.yaml` 第 86 行的 `image: javdb-rss:latest` 换成同一个 pinned tag
      —— 它现在其实**无处可拉**，这正是本票的一半理由
      （改完在 `deploy/k8s.yaml:92`，行号下移是因为上面多了一段注释）
- [x] `deploy/javdb-rss.service` **不动**，但 README 的部署表要说明这份跑的是二进制
      （部署表多了「跑的是什么」一列）
- [x] README「部署」段补：镜像已发布、坐标、怎么升级（改那一行 tag）、
      以及「升级前先确认 pin 状态目录仍可写」（那一条现有文字保留，这里只加一个指过去的路标）
- [x] `make test` 仍绿（三份配置的键集合由 `internal/config/examples_sync_test.go` 强制一致）
      —— `make fmt-check && make vet && make test` 全绿，14 个包 ok

**额外加的一道防线**（不在票面清单里）：`internal/config/deploy_image_test.go`
守着三件事 —— 两处坐标必须是**同一个** tag、必须是全量精度形状、systemd 那份仍是二进制。
为什么加：这两份文件漂移的失败方式是**静默的**（配置照样解析、容器照样起来，
只是跑的不是你以为的那份代码），而 k8s 那个无处可拉的 `latest` 正是这么活到今天的。
测试里**不写版本号作为独立的事实**：主检查只比较两份文件彼此是否一致 + 形状是否全量精度
（元测试的表里拿 `1.0.0` 当夹具，那只是输入数据，不是「当前版本」的第三个说法），
所以它不构成新的 pin 点，不违反票面「不把 tag 写进更多地方」。

## 要一起定的（建票时未定）

- **k8s 的镜像拉取策略**：取**显式写 `imagePullPolicy: IfNotPresent`**（对全量精度 tag
  本来就是默认值）。理由是让它成为一句可读的话而不是一个隐式默认：tag 内容不会变，
  没必要每次启动都去 registry 问一遍；反过来说 —— 哪天允许浮动 tag 了，这一行就得重新想，
  这正是「不推浮动 tag」的附带好处。同一句解释写在 `deploy/k8s.yaml` 里和
  ADR 0001 的 Consequences 里。
- **「最新版本是多少」的可见性**：**不做**。本服务是拉取方 —— 它的输入是 App 的 API，
  不是自己的版本，没有任何自更新或上报需求。想知道最新版的人本来就该看 `git tag`
  或 GHCR 页面；在服务里多加一处版本可见性 = 多一处要维护、会漂的说法，
  与本票「一处 pin 就够了」的方向相反。

## 有意不做的

- **不把 tag 写进更多地方**（如 k8s ConfigMap、README 的命令示例之外）：
  一处 pin 就够了，多写几处只会多几个会漂的点。实施后数了一下，「钉」这件事落在两处
  部署文件的 `image:` 行上（升级＝改这两行），测试不把版本号当事实。
  **README 那段升级示例里出现的 `1.0.0` 是例外，而且票面本来就允许**（「README 的命令
  示例之外」）—— 它在文档里标着「当前的坐标是…」，作用是让人看到改哪一行、改成什么形状；
  升级时它跟前两处一起改，不改的后果只是文档说旧了一个版本（不会静默换版本）。

## Answer（2026-10-05）

**五个框全过。** 两处容器部署钉的都是
`ghcr.io/2017fighting/javdb-rss:1.0.0`（index digest
`sha256:d8fcc9217b552eb34394e44c87dc609b742d34582e9542d19ae80f1f7e32a739`），
`deploy/javdb-rss.service` 一行未动。

### 验收证据（不是「看着对」）

**1. 坐标真能匿名拉。** 无凭据取 token 后拿 manifest：

```bash
TOKEN=$(curl -s "https://ghcr.io/token?scope=repository:2017fighting/javdb-rss:pull&service=ghcr.io" \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -sI -H "Authorization: Bearer $TOKEN" -H "Accept: application/vnd.oci.image.index.v1+json" \
  https://ghcr.io/v2/2017fighting/javdb-rss/manifests/1.0.0 | grep -i docker-content-digest
# → sha256:d8fcc92…（与上面一致；index 里两个 manifest：linux/amd64 + linux/arm64）
```

**2. 用真 compose 文件、在**没有源码也没有 Go 工具链**的临时目录里拉起来。**
把 `deploy/docker-compose.yml` 拷到 `/tmp`，配 `data/config.yaml`（取自
`deploy/config.docker.yaml`）与空的 `state/`、`token/`，然后 `docker compose up -d`：

```
NAME        IMAGE                                  STATUS
javdb-rss   ghcr.io/2017fighting/javdb-rss:1.0.0   Up (healthy)   127.0.0.1:8080->8080/tcp

$ curl -s 127.0.0.1:8080/version
{"version":"v1.0.0","provider":"appapi","whitelist":{"actresses":false,"lists":false,"zones":false}}
$ curl -s 127.0.0.1:8080/healthz        # → ok
$ curl -s -o /dev/null -w '%{http_code}' 127.0.0.1:8080/readyz   # → 200
log: level=INFO msg="pin 表已加载" path=/state/pin.json 条数=0
```

这一步同时验了两件票面关心的事：**拉得到**（此前 k8s 那份无处可拉），
以及 **`/version` 报的是 tag 原文 `v1.0.0`**（与 README/ADR 里那一段
「tag 去 v、`/version` 留 v」的说法对得上）。容器已 `down`，是临时目录，未落进仓库。

**3. k8s 清单整体仍然有效**：六份文档（ConfigMap / Deployment / Service / Service /
CronJob / PVC）逐个 `yaml.safe_load_all` 通过，读回的 `image` 与 `imagePullPolicy`
正是 `ghcr.io/2017fighting/javdb-rss:1.0.0` 与 `IfNotPresent`。

**4. 检查全绿**：`make fmt-check && make vet && make test` —— 14 个包全 ok，
含新增的 `TestDeployManifestsPinSamePublishedImage` /
`TestDeployImageCheckCatchesDrift` / `TestSystemdUnitRunsBinaryNotImage`。
元测试证明了这道防线不是空转：把 k8s 改回 `:latest`、把 compose 退回 `build:` 
或改成浮动 `:1.0`，它都会红（其中「compose 仍有生效 build:」还用真文件改一次验过）。

### 一处顺带订正

`deploy/docker-compose.yml` 里原先那句内嵌提示写的是
「也可以用现成镜像：把 `build` 换成 `image: ghcr.io/2017fighting/javdb-rss:latest`」
—— 它推荐的恰好是 ADR 0001 明确**不推**的浮动 `latest`（票 13 写这段注释时
镜像链还没定下 tag 形状）。现在这句被换成了注释掉的 `build:` 段，方向反过来：
默认拉已发布的全量精度 tag，想自建的人才去用 `build:`。

## 评审（/code-review，2026-10-05，`92bd5c6...df2060b`）

两条轴各一个 fresh-context 评审子代理（Standards 轴带 Fowler 嗅探基线，Spec 轴对着票面
逐框核）。两份报告要点：

### Standards 轴 —— 未发现硬违规，`Merge verdict: OK`

逐文件（三份 `.scratch` 记录 / README + `deploy/` / ADR / 新测试）均未发现文档标准违规。
基线嗅探只有一条、且被判为判断题：`deploy_image_test.go:k8sDeploymentImage` 的
「多文档 YAML 解码循环」与同包 `examples_sync_test.go:k8sConfigMapConfig` 形状相似
（可能的 Duplicated Code）—— 评审自己给了不改的理由：两者解析的目标结构体差异极大，
强行抽共享会引入 Speculative Generality。**同意，不改。**

### Spec 轴 —— 五个框全过，`Merge verdict: OK with notes`

三条记录：

1. **scope creep 一条**：新增的 `deploy_image_test.go`（含元测试）确实不在票面清单里。
   这是**有意的额外防线**，理由已写在票面「额外加的一道防线」一段；评审把它归类为
   超范围、没有判为错。保留。
2. **「测试里没有写版本号」口径不准**（已订正）：元测试的表里拿 `1.0.0` 当夹具输入。
   那句话的本意是「测试不把版本号当作独立事实」，票面措辞已按此改写。
3. **「README 只说形状、不写版本号」不成立**（已订正）：README 那段升级示例里就有
   `1.0.0`。票面「有意不做的」已改成如实口径：版本号出现在两处 pin 点 +
   README 的示例一处（票面原本就允许 README 示例），且那处明确标着「当前的坐标是…」。
