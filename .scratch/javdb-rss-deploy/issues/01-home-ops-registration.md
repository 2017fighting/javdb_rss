# 01 — home-ops 应用注册

**What to build:** javdb-rss 成为集群里的一等公民 —— `apps/javdb-rss/` 八个文件，
加 App Registry 一行、edge-sso 一行。

**Blocked by:** [02 — app 侧 `/metrics`](02-app-metrics-endpoint.md)（`VMPodScrape` 要有端点可打）

**Status:** resolved（2026-10-05：已落地并健康，见文末两节）

- [x] `namespace.yaml` —— `prune: disabled` 标签**必须**有（少了它，将来一次 prune 就能删掉整个 namespace）
- [x] `kustomization.yaml` —— resources 按创建顺序；`components: [../../infrastructure/components/edge-parentref]`；
      **绝不写 `namespace:`**（kustomize 的 namespace transformer 会在解密前改写 SOPS 的 metadata，2026-08-21 事故）
- [x] `configmap.yaml` —— `javdb-rss-config` 里的 `config.yaml`（**两个 ConfigMap 的坑**：
      add-app 模板里的 `<app>-env` 是环境变量，本服务要的是**配置文件**；
      而 `TZ` 不需要 —— 全仓库没有一处 `time.Local`/`LoadLocation`）：
      `listen: 0.0.0.0:8080`（容器里 127.0.0.1 连不上）、
      `base_url: http://javdb-rss.javdb-rss.svc.cluster.local:8080`（它只用于 channel 的 `<link>`，
      全代码两处）—— ⚠️ **票面最初写的是 `https://javdb-rss.raenzo.com`，落地当天改成了现在这个值**：
      把「人能不能点」当成了 `<link>` 的职责，而它其实是 **feed 的自指**，而这份 feed 的
      唯一机器消费端解析不了 raenzo.com 之外的名字，也解析不了 `.svc`（反过来同样成立）。
      理由与代价记在 ADR-0002。
      `token_file` **留空 + 注释**说明 token 走 `JAVDB_TOKEN`、
      `pin_file: /state/pin.json`、`device_uuid` 留空（留空不是随机：transport 的默认值是一个固定常量）、
      `lang: zh-CN`、`magnet_concurrency: 8`、`probe_interval: 15m`
- [x] `javdb-rss-secret.sops.yaml` —— Opaque + `JAVDB_TOKEN`；`sops --encrypt --in-place` 后
      `sops -d … >/dev/null` 往返校验。⚠️ `.sops.yaml` 的 `^.*\.sops\.ya?ml$` 已经覆盖，不用改它
- [x] `helmrelease.yaml` —— `chartRef` 指向舰队级的 `app-template`（版本钉在 `infrastructure/sources/app-template.yaml`，
      **不要**在应用里写版本）；`valuesFrom: javdb-rss-workload-profile`（少了它调度会静默消失）；
      `releaseName: javdb-rss`（app-template 的服务名跟着它，HTTPRoute 的 backendRef 也靠它）；
      `envFrom.secretRef: javdb-rss-secret`；`reloader.stakater.com/auto: "true"`；
      **探针照 `deploy/k8s.yaml` 那份**（`liveness=/healthz`、`readiness=/readyz`，
      周期 30s / 超时 5s / 失败阈值 3 与 2）—— **不套用 add-app 模板里的 `/` 探针**，
      本服务的三端点是有理由的：上游状态不许掺进 liveness（签名失效重启一千次也没用）。
      ⚠️ **不加启动探针**：`deploy/k8s.yaml` 里本来就没有，而二进制毫秒级就出第一个响应
      （票面最初写的「60×10s」是从 add-app 模板抄错了的，已改）。
      `strategy` 不用写（app-template 默认就是 `Recreate`，已在 live 集群核对 `bark`/`moviepilot`），
      而这一点是**承载性的**：pin 用 flock 做单写者；
      `persistence`：`config`（configMap / subPath / readOnly）+ `state`（
      `truenas-nfs-retain` 64Mi RWO）+ `tmp`（emptyDir）；
      `defaultPodOptions.securityContext` 与容器级加固照 `deploy/k8s.yaml`（10001 / readOnlyRootFilesystem /
      drop ALL）；resources `10m/16Mi` + limits `128Mi`；`service.main.annotations` 加 glance 卡片
- [x] 【诚实的一笔】`helmrelease.yaml` 里写明 **pin 没有恢复点**（`nova/k8s` 不在两条宿主快照任务里、
      集群里没有通用 PVC 备份、cnpg 的 Recovery point 只覆盖数据库卷）
- [x] `httproute.yaml` —— hostname + 一条 PathPrefix `/` 的 rule + `extensionRef` filter
      指向 `authelia-forward-auth`；**不写 `parentRefs`**（edge 身份归 `edge-parentref` 组件）
- [x] `vmpodscrape.yaml` —— 照 `apps/hath/vmpodscrape.yaml` 的形状：selector 用
      `app.kubernetes.io/name` + `instance`（app-template 的标签），`port: http`、`path: /metrics`、
      `interval: 30s`；vmagent/vmalert 是 selectAllByDefault，不用改监控栈
- [x] `vmrule.yaml` —— 照 `apps/hath/vmrule.yaml` 的形状：`JavdbRssSignatureBroken`，
      `expr: javdb_rss_upstream_signature_broken == 1`，`for: 1h`，`severity: warning`，
      **注解里带上处置**（查 javdb-cli 是否已跟进 → 否则走逆向备灾清单）——
      这条告警的处置是「改代码」，不是「重试」
      - 注解里**不要**写 `next_step` 之外的东西：`/healthz/upstream` 的 JSON 里
        本来就有 `next_step` 与 `action`，规则不必复述上游的说法
- [x] App Registry 一行（`clusters/home/app-registry.yaml`，字母序）：
      `name: javdb-rss` / `path: apps/javdb-rss` / `profile: proxy` / `deps: [authelia, traefik]`
      —— deps 只写这两条：没有 CNPG（**不要** `infra-database-cluster`/`reflector`）、
      没有 media（**不要** `storage`，per-app 的 `truenas-nfs-retain` 不需要它 ——
      `bark`/`karakeep`/`lldap` 都没有这条 dep）
- [x] edge-sso 一行（`clusters/home/edge-sso.yaml` 的 `inputs`，字母序）
- [x] 【已知竞态】首次部署：`<app>-workload-profile` ConfigMap 要落进还不存在的 namespace，
      operator 会中止整次施加（app-registry 报 `namespaces "javdb-rss" not found`）。
      处置：临时加 `clusters/home/javdb-rss-ns-bridge.yaml`（只含 namespace 文档，与 app 里的那份逐字一致）
      → `flux reconcile resourceset app-registry -n flux-system --force` → **健康后删掉桥文件**
      （root-sync 的 prune 会因为 `prune: disabled` 标签放过活着的 namespace）
- [x] 本地验证：`kustomize build apps/javdb-rss`、`oxfmt --check`、`scripts/validate.sh`、
      `python3 -c "…valuesFrom 里有 javdb-rss-workload-profile…"`
- [x] 【同时改】`home-ops/CONTEXT.md` 的 **Edge SSO** 词条 Consumers 列表加 `javdb-rss`
      —— 只在那一行真的落进 `edge-sso.yaml` 之后改（提前改就是宣称一件还没发生的事）

## 有意不做的

- **不进 hardlink-substrate**（不碰媒体目录；本服务只交磁链）
- **不碰 CNPG**（它没有数据库；唯一的持久状态是那个 pin 文件）
- **不给 javdb-rss 加 CNPG 的 deps**（见上）
- **不改 `renovate.json5`**：内置 `helm-values` 管理器本来就扫 `helmrelease.yaml`
- **不改 authelia 的 `access_control`**：`default_policy: deny` + 兜底
  `domain_regex: ^.*\.raenzo\.com$` → `one_factor`，挂上 forward-auth 就等于要登录
- **不给它 3000 端口之类的第二处入口**：它只有一个 http 端口

## Answer（2026-10-05：本地清单已落地，等推送）

**落地的文件**（`apps/javdb-rss/`，8 个）：`namespace.yaml` / `kustomization.yaml` /
`configmap.yaml` / `javdb-rss-secret.sops.yaml` / `helmrelease.yaml` / `httproute.yaml` /
`vmpodscrape.yaml` / `vmrule.yaml`，加上 **App Registry 一行**与 **edge-sso 一行**
（均按字母序插入，未改动任何已存在的行）。

**本地校验（全绿）**：

| 检查 | 结果 |
|---|---|
| `kustomize build apps/javdb-rss` | 7 个对象；`parentRefs` 由 edge-parentref 组件补上（`traefik`/`networking`/`websecure`） |
| `bash scripts/validate.sh` | **279 resources / 53 files 全部 Valid**，`CONSISTENCY LINT: OK (31 apps)`。⚠️ 这个数字是**修复闸门之后**才拿到的，见文末「一次记录订正」—— 当时那句「34 resources / 6 files」是**假的**（脚本在 macOS 上只渲染出 6 个 root 就说全绿） |
| `oxfmt --check .` | 310 files 全部已格式化 |
| `sops -d …` 往返 + oxfmt 前后 diff | 解出 `Secret/javdb-rss-secret` + `JAVDB_TOKEN`（115 字符），**格式化前后解密内容 byte-identity** |
| 渲染细节 | `valuesFrom: javdb-rss-workload-profile`；容器端口 `http/8080`；探针 `/healthz` + `/readyz`；`state` → `truenas-nfs-retain` 64Mi；envFrom `javdb-rss-secret`；VMRule `== 1, for: 1h, warning` |

**写清单时查实的几件事**（都影响你以后改这类应用）：

1. **app-template 不会从 Service 推导容器端口** —— 实测 `karakeep` 的容器 `ports` 是空的，
   而 `smtp-relay` 显式声明了（它的注释写着「不给就 0 targets」）。所以 `ports: [{name: http,
   containerPort: 8080}]` 是 `VMPodScrape` 能工作的**前提**，不是风格问题。
2. **Pod 标签**在 live 集群上核对过（`bark`/`smtp-relay`/`hath`）：
   `app.kubernetes.io/name` + `instance` 正是 app-template 给的，选择器因此是对的。
3. **token 是活的**：拿 `token.json` 里那串（就是被加密进 Secret 的那串）打
   `GET /collected` → `200` + **144 位**女优。加密进去的不是一串过期凭据。
4. **sops 3.13.3 默认 4 空格缩进**，而仓库里其它 secret 都是 2 空格 ——
   `oxfmt --write` 会归一化成 2 空格，且**解密内容不变**（ADR 记的那条性质在这里复现了一次）。
5. `scripts/validate.sh` 在 macOS 上有两处环境小坑（`nproc` 不存在、bash 3.2 的
   `extra[@]: unbound variable` 警告），**都不影响结论**；跑法是 `JOBS=8 bash scripts/validate.sh`。
   没有顺手改它 —— 与本票无关。

**还没做的**：无 —— 两项已于同日落地，见下面「落地（live）」。推送是对 live 集群的变更，
因此当时停在那里等确认。

## 落地（live，2026-10-05）

**顺序与坐标**：`javdb_rss` 先推 main + `v1.1.0`（CI run `37266716472` 与 Release
run `37266722480` 全绿，镜像 `1.1.0` 两个架构、`latest` 指向同一批 manifest），
再用一个提交把 home-ops 钉到 `1.1.0`（`0551318`）—— 依赖那行字在**镜像真的存在**之后才写。

**首次部署**：

| 步 | 结果 |
|---|---|
| root-sync 应用新 revision | `0551318c`；bridge 文件先把 `javdb-rss` 命名空间建出来 |
| 竞态 | **没发生** —— app-registry 渲染出的 Kustomization 一次就 Ready，没有 `namespaces "javdb-rss" not found` |
| HelmRelease | `Ready=True`，`Helm install succeeded`（app-template 4.2.0） |
| 镜像 | 实际拉的是 `harbor.raenzo.com:8443/ghcr/2017fighting/javdb-rss:1.1.0` —— Kyverno 改写与 pull-through 都对 |
| profile 注入 | `javdb-rss-workload-profile` = `nodeSelector: {proxy: "true"}` |
| 探针 | 1/1 Ready，`restarts=0`（首发与删 Pod 重建各一次） |
| 拆 bridge | 命名空间**存活**，owner 标签从 `flux-system` 转成 `javdb-rss` 的 inventory |

**两条与文档不完全一致的事实**（都不影响功能，但值得记）：

1. **PVC 实际是 1Gi，不是请求的 64Mi。** `truenas-nfs-retain` 那边有最小/默认配额：
   `spec.resources.requests.storage=64Mi` 而 PV 与 bound 容量都是 `1Gi`。pin 只有几 KB，
   两种尺寸都远远够用 —— 但拿着这句去看 NAS 的人会发现它比清单里写的大。
2. **`pin_file` 可写这件事在首启就自证了**：服务在状态目录不可写时会**拒绝启动**，
   而它起来了 —— 所以 `fsGroup` 那一半（评审补的）至少在 NFS 这条路上是有效的。

**真实验收（票 04）的关键几项已在本次跑完**，明细在 [04 的 Answer](04-real-acceptance.md)。

## 评审（两轴，2026-10-05）

`/code-review`，固定点 `HEAD~1`（就这一个提交），子代理互不污染。

| 轴 | 结论 | 处置 |
|---|---|---|
| Spec | 缺 pod 级 `defaultPodOptions.securityContext`（票面第 36 行要求照 `deploy/k8s.yaml`） | **接受并修**：补 `runAsUser/runAsGroup/fsGroup 10001` + `fsGroupChangePolicy: OnRootMismatch` + `seccompProfile`（pod 级），容器级只留 `allowPrivilegeEscalation` / `readOnlyRootFilesystem` / `drop ALL`。`fsGroup` 不是仪式：`/state` 是 NFS 后备的 PVC，而这个组合是 `lldap` 给同一类卷用的那一对 |
| Spec | `home-ops/CONTEXT.md` 的 Edge SSO Consumers 未同步 | 有意延后 —— 票面写着「只在那一行真的落进 `edge-sso.yaml` 之后改」。它随**拆 bridge 的那个提交**一起落 |
| Spec | bridge 文件「提前」进 diff（与票面「还没做」矛盾） | 误报：文件必须在提交里才能先于 operator 那一轮落地。票面措辞已订正（上面） |
| Standards | 裸写 `ADR-0002` 与 home-ops 自己的 `0002-traefik-v3-gateway-api-mode.md` **撞号** | **接受并修**：四处引用全限定为 `javdb_rss ADR-0002` / `javdb_rss ADR-0003`（含 app-registry 那行注释） |
| Standards | 清单本身违反 add-app 约定？ | 无：`valuesFrom`、容器端口命名、探针、prune-disabled、无 `namespace:` 都被判为合规 |

**评审后重跑**：`kustomize build` + `bash scripts/validate.sh`（**279 resources / 53 files 全 Valid**、
CONSISTENCY LINT OK，见下节）+ `oxfmt --check .` 全绿。

## 一次记录订正（2026-10-05，同日）

本票原先写着「`JOBS=8 bash scripts/validate.sh` → 34 resources / 6 files 全部 Valid」。
那句话的**数字是错的，而且是错得有价值的那种**：当时 home-ops 的 `scripts/validate.sh`
在 macOS 自带的 bash 3.2 下有两个只在这类机器上发作的毛病 ——

1. `JOBS="${JOBS:-$(nproc)}"`：`nproc` 来自 coreutils，macOS 没有 → `set -e` 以 127 退出；
2. `kustomize build "${extra[@]}"` 在**空数组**上被 bash 3.2 + `set -u` 判成未绑定变量，
   而 `render_root` 跑在后台子 shell 里，那个错误**杀掉了 render，却没让脚本失败**。

后果：53 个 build root 里 47 个死在子 shell，kubeconform 只看了剩下 6 个，
然后打印 `VALIDATE: all kustomizations render + conform, consistency lint green` 并以 0 退出。
我引用的「34 resources / 6 files」正是那 6 个幸存的 root 的 Summary 行 ——
也就是说，**当时的「全绿」有 47/53 是空转**。

已修（home-ops `a8c26c2`：`detect_jobs` 走 nproc → `getconf _NPROCESSORS_ONLN` → 4；
空数组改用 `${arr[@]+"${arr[@]}"}` 并写明原因），修后全量跑是
**279 resources in 53 files, Invalid: 0** —— 结论没变，但证据终于是真的了。

本票自己的那条直接证据（`kustomize build apps/javdb-rss` + 渲染结果的逐项核对）
**不受影响**：那是直接跑的，不是靠这个脚本。
