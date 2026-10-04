# 08: 收口：契约回写 + 文档 + 真实验收

**Spec:** [`../spec.md`](../spec.md)

**What to build:** 把这轮 promote 里**改了行为**的几条作为新的硬规则写回 `ui-contract.md`（按女优 `filter_tags` 列基本组、女优 id 可手输、无 token 时禁用输出、截断信号可见），而不是只活在代码里 —— 这是设计循环的 LEARN 一步。`CONTEXT.md` 的 Language 节增补「标签词表」与「订阅链接生成器」；`README` 增「打开页面」一节（写清无 token 时哪些区可用）。

然后跑一遍完整的设计验证协议与全部守卫，最后用真上游 + 真 token 生成一条链接、粘进真实 qBittorrent 验收（照需求 1/2/3 的先例）。

**Blocked by:** 04, 05, 06, 07

**Status:** ready-for-agent

- [ ] `ui-contract.md` 补上这四条硬规则及各自的理由（不是只加标签，要说清为什么）
- [ ] `CONTEXT.md` 增两条术语；`README` 的「打开页面」写清无 token 与白名单两种情况下的行为
- [ ] 全量守卫绿：Go 的 `make check`（fmt / vet / race）+ `tools/verify.sh`（token-lint / 对比度 / axe / 触摸目标 / 焦点 / 行为断言 / rubric）
- [ ] 一次真上游 + 真 token 的验收记录：页面上生成一条链接（含标签与年份），粘进 qBittorrent 后条目能出来
- [ ] `.scratch/javdb-rss-ui/` 留下的设计记录与实现不再互相矛盾（旧结论一律改掉，不留已被证伪的说明）
