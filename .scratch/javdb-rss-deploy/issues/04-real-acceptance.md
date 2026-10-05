# 04 — 真实验收（含人工步骤）

**What to build:** 不是「代码写完了」，而是「真的能用」。代理指标（渲染通过、Pod Ready）不算数 ——
这是本仓库初次交付就定下的判据。

**Blocked by:** [01](01-home-ops-registration.md)、[02](02-app-metrics-endpoint.md)、[03](03-docs-adr-0002-0003.md)

**Status:** ready-for-human（有两步只有人能做：浏览器过 authelia、邮箱里确认那封信）

- [ ] **收敛**：`flux get kustomizations -A | grep javdb-rss` Ready；
      `kubectl -n javdb-rss get pods,httproute,vmrule,vmpodscrape`
- [ ] **页面（人工）**：浏览器打开 `https://javdb-rss.raenzo.com` → 落到 authelia → 登录 → 页面出来。
      反过来的那一半也要看：**未登录时不会被放行**（否则 SSO 是装饰）
- [ ] **feed（机器）**：用 `/api/v2/rss/addFeed`（qBittorrent v5.2.4 实测的端点，
      不是文档里常见的 `/api/v2/rss/add`）把
      `http://javdb-rss.javdb-rss.svc.cluster.local:8080/rss/…` 加进真实的
      qbittorrent-private，确认解出 `guid` / `title` / `torrentURL`、`hasError: false`
- [ ] **guid 稳定**：连续刷新 3 次 article id 不变；再 `kubectl -n javdb-rss delete pod`，
      重建后**同一作品的 guid 不变**（这一条才是「pin 真的落在 PVC 上」的证据）
- [ ] **指标**：VM 里查得到 `javdb_rss_upstream_signature_broken` 与 `javdb_rss_build_info`，
      且 `build_info` 的 version 与 `kubectl -n javdb-rss get deploy -o jsonpath=…image` 的 tag 对得上
      形状（`v1.0.0` vs `1.0.0` —— 两种形状是有意的，见 ADR-0001）
- [ ] **告警真的响一次**：人为让 `javdb_rss_upstream_signature_broken == 1`
      （临时把规则的 `for` 改成 `0s` 并用一条造出来的序列 / 或临时改表达式），
      → 等它进入 Alertmanager → **邮箱里确认收到**（人工）→ 把规则改回去并确认恢复。
      依据：home-ops 自己出过 `d01a2bf`「刚上线的规则永远不会触发」
- [ ] **结论回填**：把上面每一条的实际输出写回票面（数字、原文、失败时的原始输出），
      再往 `map.md` 的 Decisions so far 追加一句指向本票

## Answer（2026-10-05）

前六步里有五步已经跑完，只剩两处**只有人能做**的确认。

| 验收项 | 结果 |
|---|---|
| 收敛 | `kustomization/javdb-rss` Ready（revision `959c1b3d`）；`httproute` / `vmpodscrape` / `vmrule` 三件都 operational |
| Pod | 1/1 Ready，`restarts=0`（包括删 Pod 重建那次） |
| **页面（机器的那一半）** | 未登录 `https://javdb-rss.raenzo.com/` → **302 → `https://authelia.raenzo.com/?rd=…`**；对照 `whoami.raenzo.com` 未挂 SSO → 200 |
| **页面（人的那一半）** | ⏳ **待你**：浏览器登录一下，看页面能不能出来（这是唯一需要人的一步） |
| **feed（消费端）** | 用票 08 验过的 `/api/v2/rss/addFeed` 把 `http://javdb-rss.javdb-rss.svc.cluster.local:8080/rss/actress/EvkJ.xml` 加进真 qbittorrent-private → qBittorrent 里的 `/JavDB` 解析出 **17 条**，`magnet:?xt=urn:btih:…` 正确、标题带「中文字幕 ·」。你原有的三个订阅（Sunny Torrents / qingwapt / 青蛙）未被触碰 |
| **guid 稳定** | 同一 Pod 连取两次 17 条逐字相同；**删 Pod 重建后逐条仍不变**；此时 `pin.json` 在 PVC 上 17 条，新 Pod 启动日志 `pin 表已加载 path=/state/pin.json 条数=17` |
| **指标** | VM 里 6 条：`javdb_rss_upstream_{checked 1, ok 1, signature_broken 0, last_check_timestamp_seconds, check_latency_seconds 0.713}` + `javdb_rss_build_info{version="v1.1.0"} 1`；`up{namespace="javdb-rss"}=1` |
| **VMRule** | vmalert 的 `/api/v1/rules` 里找得到 `JavdbRssSignatureBroken`，`health: ok`，初始 `state: inactive` |
| **告警真响一次** | 见下 |
| **邮箱确认** | ⏳ **待你**：收件箱里应当有 **两封**（firing + resolved） |

### 告警那一次（机器证据链）

做法是**改规则、不造假数据**：临时把 live 的 VMRule 改成 `expr: vector(1)` + `for: 0s`，
看它走完整条链，然后用 git 里的真版本覆盖回去。

| 环节 | 证据 |
|---|---|
| vmalert 求值 → firing | Alertmanager `/api/v2/alerts`：`JavdbRssSignatureBroken`，`state=active`，`receivers=['email']`，`startsAt 05:29:00Z` |
| Alertmanager → 发信 | `alertmanager_notifications_total{integration="email"}` 在 05:30 与 05:40 各 +1 |
| **Mail Relay 真的投出去了** | maddy 日志（两个副本合看）：`3aaac37e`（firing）与 `f69f6ddc`（resolved），发件人 `alertmanager@raenzo.com`、收件人 `hello@raenzo.com`、`delivered attempt:1` |
| 规则被 git 恢复 | 覆盖回 `javdb_rss_upstream_signature_broken == 1` / `for: 1h`（live 已核） |
| 告警 resolved | Alertmanager 里已清出（0 条）；`send_resolved: true` 生效 |

⭐ 这一步的价值不在「测了一条规则」，而在它是 home-ops 自己那次事故的解法：
`d01a2bf`「刚上线的规则永远不会触发」—— 一条从没响过的告警不算告警。

### 两个把我也坑了一下的东西（值得记）

1. **`kubectl port-forward` 别套 `timeout`。** 我给 vmalert 与 Alertmanager 的转发都加了
   `timeout 90`，结果轮询「等了 4 分钟没出现」其实是我自己的转发已经死了 —— 误判过一次。
   后来用 `timeout 1200` 这样足够长的值，或者干脆不管它。
2. **Mail Relay 有 2 个副本。** 只看 `kubectl logs deploy/smtp-relay` 会只读到一个 Pod，
   于是「resolved 那封没发出去」是个假结论 —— 逐个 Pod 看才能看到全貌。
   查邮件链路时要枚举 Pod，不是拿 Deployment 的默认选择。

## 已知会挡路的两件（先记在这里）

1. **首次部署的 namespace 竞态**：见 [01](01-home-ops-registration.md) 的 bridge 文件那一条。
2. **token 用哪一串**：服务与手机共用同一个会话（单会话账号），所以用
   `scripts/export-phone-token.sh` 导出的那串 —— `javdb-rss login` 会把手机挤下线。
   如果验证期间手机上的 App 又登录了一次，服务的 token 会失效（`/collected` 系 503）：
   那不是 bug，重导一次即可。

## Answer

（落地后回填）
