# 配置与部署模型

Type: grilling
Status: resolved

## Question

这个服务最终怎么被跑起来、怎么被改配置？

给人类的问题（需要用户回答，不要替他决定）：

1. **跑在哪**：NAS / 家用服务器 / 容器 / 裸机？有没有现成的反向代理（Caddy / Nginx / Traefik）？
   是否已有 Docker 或 systemd 的部署习惯？
2. **谁能访问**：只在内网，还是 qBittorrent 跑在另一台机器/另一个网络？
   这决定 feed URL 要不要带 secret、要不要 TLS。
3. **配置怎么改**：改文件后重启？改文件热重载？还是一个 `/reload`？
   订阅会频繁增删吗？
4. **日志与可观测**：需要看到什么？（轮询失败、签名失效、接口字段变了）
   要不要把失败也变成一个「feed item」或者通知到别处？
5. **单二进制 vs 容器**：用户选了 Go 单二进制，但要不要**顺带**提供一个 Dockerfile？

## 为什么这是 grilling 而不是 task

这些是用户的价值取舍，不是事实。给推荐答案，但等用户拍板。

## 参考推荐

- 配置：单个 YAML，启动时读 + `SIGHUP` 重载（比 watch 文件简单，比重启体验好）
- 部署：裸二进制 + 可选 Dockerfile；不强制
- feed 鉴权：URL 里带一个 secret path 段（`/rss/<secret>/code/ABC-123.xml`），
  在内网场景下够用且比 Basic Auth 对 qBittorrent 友好
- 日志：结构化 stdout，签名失效/接口契约变化做成显式告警级日志

## 产出

写进 map 的 Decisions-so-far，并把需要落到骨架里的部分回写到 ticket 03。

## Answer

**已定（2026-09-28 与用户面对面 grill 得出）**

### 1. 部署形态：裸二进制为主 + 附 Dockerfile

- 主路径：`go build` 出单二进制，配一份 systemd unit 示例
- 另附多阶段构建的 Dockerfile（非 root 用户、挂载配置与 token），不强制使用

### 2. 鉴权：**不做**

用户选了纯内网。因此：

- URL 就是 `/rss/code/{番号}.xml`、`/rss/actress/{id}.xml`，无 secret 段、无 Basic Auth。
- **安全默认必须补上**：监听地址默认 `127.0.0.1`，**要暴露到局域网必须显式改配置**。
  没有鉴权的服务不应默认绑 `0.0.0.0`。这条要在 README 里写明。
- 验证：未授权的路径不构成风险，但**无鉴权 + 0.0.0.0 = 订阅列表公开**，需在文档里提醒。

### 3. 配置：单个 YAML + `SIGHUP` 重载

比 watch 文件简单，比重启体验好。

### 4. 日志：结构化 stdout

签名失效与接口契约变化做成显式 `WARN`，不得静默返回空 feed
（空 feed 会被误认为「没新片」，是最难排查的失败模式）。

### 5. 不阻塞骨架的部分

这些属于可迭代的实现细节，不构成 ticket：systemd unit 的细节、
Dockerfile 的 base image 选择、日志字段的具体命名。



## 实施补记（2026-09-30）

**形态已全部落地。** 当初这张票定的是「决策 + 形态」，实现留给了「真需要部署时顺手做」——
用户现在要求把它做出来，已完成。

### 交付

| 文件 | 说明 |
|---|---|
| `Dockerfile` | 多阶段；`CGO_ENABLED=0` 静态二进制；alpine + 非 root(10001) |
| `.dockerignore` | 排掉 APK / 凭据 / `.scratch/` / 逆向产物 |
| `deploy/javdb-rss.service` | systemd，含完整加固 |
| `deploy/docker-compose.yml` + `deploy/config.docker.yaml` | Compose |
| `deploy/k8s.yaml` | ConfigMap + Deployment + Service + **签名失效告警 CronJob** |
| `Makefile` | build / test / race / cover / check / docker-* |
| `.github/workflows/ci.yml` | gofmt + vet + race + build + docker build |
| `README.md` | 新增「部署」一节 |

### 三个值得说明的决定

**1. 容器里必须监听 `0.0.0.0`，但「可见性」由端口映射决定。**

裸机默认 `127.0.0.1` 是安全默认。但在容器里那是**容器自己的 loopback**，
宿主机连不上 —— 所以 `deploy/config.docker.yaml` 改成 `0.0.0.0`。
我在文件里写清楚了这层关系的含义：隔离由容器提供，**暴露出去**才是需要动手的决定
（`-p 127.0.0.1:8080:8080` vs `-p 8080:8080`）。K8s 用 `ClusterIP`，
并明确警告不要改成 LoadBalancer 而不考虑无鉴权这件事。

**2. `ca-certificates` 不是可选项。**

`alpine` 默认没有根证书，本服务要 HTTPS 访问上游 —— 缺了会得到
`x509: certificate signed by unknown authority`。已在 Dockerfile 里加了注释说明，
因为这个错误信息不会指向真正的原因。

**3. K8s 的 probe 分工是 ticket 02 那套设计的兑现点。**

`livenessProbe` → `/healthz`（只判进程存活，**不掺上游状态**，避免重启循环）；
`readinessProbe` → `/readyz`（上游坏了摘 endpoint 但不重启）。
两个端点都写了「为什么必须是这个」的注释。

### 额外做的一件事：签名失效告警 CronJob

`deploy/k8s.yaml` 里有一个 CronJob 每小时抓 `/healthz/upstream`，
只在 `signature_broken: true` 时失败并打印**处置步骤**（指向 javdb-cli 与逆向备灾清单）。

为什么用 CronJob 而不是 Prometheus 规则：签名失效是**月级**罕见事件，
且处置方式是「去改代码」，不需要秒级检测。一个人读的告警比一个指标更贴合这件事。
`signature_broken` 刻意排除普通网络故障 —— 避免半夜被叫起来改一个只需重试的东西。

### 验证

- `make help` / `fmt-check` / `vet` / `test` 均通过
- 4 份 YAML（k8s 的 4 个文档、compose、docker 配置、示例配置）全部解析合法
- `deploy/config.docker.yaml` 用真实服务加载成功：
  `开始监听 addr=0.0.0.0:8080 provider=appapi`，且探针一次成功（726ms）
- CI workflow YAML 合法

### ⚠️ 未验证（诚实标注）

**Docker 镜像没有真正构建过** —— 本机 docker daemon 未运行
（`dial unix /var/run/docker.sock: no such file or directory`）。
Dockerfile 的**语法与逻辑**经过审阅，但 `docker build` 未执行。
第一次 `make docker-build` 时请留意构建输出。
