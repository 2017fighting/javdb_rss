# 11 — 按定稿语义落地 `since`

**What to build:** 带 `?since=` 的订阅按 [10](10-since-semantics.md) 的结论过滤，行为从 URL 可观察（保留 N / 丢弃 M / 坏数据 K）；并且**「尚未定稿」这件事在仓库里彻底不存在**。

**Blocked by:** [10 — `since` 的语义定稿](10-since-semantics.md)

**Status:** ready-for-agent

- [ ] `since` 必须是严格 `YYYY-MM-DD`，否则 400。现在 `since=2026-1-1` 会静默丢掉 1–9 月、`since=hello` 会静默只剩 1/6、ISO 时间戳会静默丢弃当天发行的作品 —— 三条路由共用一份校验
- [ ] `year`（女优、全站）或 `month`（全站）与 `since` 同时给 → 400，三条路由规则一致（女优已有；清单靠自己的 `year` 拒绝间接覆盖）
- [ ] `release_date` 为空或形状不对 → **保留**，并**分开**计数（与「丢弃」不是一回事）
- [ ] 日志：删掉「语义尚未定稿」那条 WARN；正常路径一条 **Debug**（作品 N / 能成 item M / 保留 K / 丢弃 D / 坏数据 E）；「筛空」（含 `since` 在未来）与「取数窗没走到 `since`」各一条 **WARN**
- [ ] 表驱动单测：闭区间边界日、四种坏形状的 `since` 各判断为 400、坏 `release_date` 保留、两条 WARN 各被触发一次
- [ ] 文案与文档：`grep -rn "尚未定稿"` 与 `grep -rn "TODO(ticket-09)"` 在代码与 live 文档里归零（`javdb-rss/issues/` 下的历史票面是**记录**，不改）；`config.yaml` / `config.example.yaml` / `notes/actress-params.md` / `README.md` 与实现一致
- [ ] 三件事写进 notes 与 README：`since` 比 `release_date` 闭区间（`>=` 是因为订阅链接生成器发 `since=<今天>`，开区间会把当天发行的挡在外面）、`pubDate` 可能早于 `since`（如实转述上游，刻意不改）、`since` 只在取到的页里生效（要更深的历史用 `pages`）
- [ ] 留一句给后人的注释：为什么是这个比较字段 —— 理由进代码，不只进票面
