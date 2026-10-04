# 11 — 按定稿语义落地 `since`

**What to build:** 带 `?since=` 的订阅按 [10](10-since-semantics.md) 的结论过滤，行为从 URL 可观察（保留 N / 丢弃 M / 坏数据 K）；并且**「尚未定稿」这件事在仓库里彻底不存在**。

**Blocked by:** [10 — `since` 的语义定稿](10-since-semantics.md)

**Status:** ready-for-agent

- [ ] 表驱动单测钉住 10 的结论：边界日（等于 `since` 那天）、缺日期、坏格式，以及 10 认定的核心分歧场景（合集再版，或 10 结论里的等价场景）
- [ ] 两条路由（`/rss/actress/{id}.xml`、`/rss/list/{id}.xml`）行为一致 —— 若 10 决定对齐 `year`+`since`，那条 400 一并落地
- [ ] `grep -rn "尚未定稿"` 与 `grep -rn "TODO(ticket-09)"` 在代码与 live 文档里归零（`javdb-rss/issues/` 下的历史票面是**记录**，不改）
- [ ] `config.yaml` / `config.example.yaml` 的 `since` 注释、`notes/actress-params.md`、`README.md` 的参数表与实现一致
- [ ] 日志变成定稿形态：正常路径不再每请求 WARN；若 10 保留「坏数据一律保留」，那么**出现坏数据时仍必须 WARN 并计数**（那是可见性问题，不是语义问题）
- [ ] 留一句给后人的注释：为什么是这个比较字段 —— 理由进代码，不只进票面
- [ ] 若结论是换字段：README / notes 里给一句话说明「同一 `since` 的返回集合会因此变化」
