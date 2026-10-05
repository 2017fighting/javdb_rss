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

## 已知会挡路的两件（先记在这里）

1. **首次部署的 namespace 竞态**：见 [01](01-home-ops-registration.md) 的 bridge 文件那一条。
2. **token 用哪一串**：服务与手机共用同一个会话（单会话账号），所以用
   `scripts/export-phone-token.sh` 导出的那串 —— `javdb-rss login` 会把手机挤下线。
   如果验证期间手机上的 App 又登录了一次，服务的 token 会失效（`/collected` 系 503）：
   那不是 bug，重导一次即可。

## Answer

（落地后回填）
