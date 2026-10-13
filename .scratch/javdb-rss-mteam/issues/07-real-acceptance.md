# 07: 真实验收（真 key + 真 token + 真 qBittorrent）

**What to build:** 拿真东西走一遍。本仓库的传统是「真能用才算数」——初次交付就是拿
真实 qBittorrent v5.2.4 走通的，「想看」那条 feed 也是拿真实账号验的。

**Blocked by:** 01–06.

**Status:** ready-for-agent

- [ ] 在 App 里把一部**老片**标成想看 → 一个轮询周期内出现在 `/rss/want.xml` →
      `<enclosure>` 指向本服务 → qBittorrent 成功加上，且**它的 infohash 与本服务发出的
      `guid` 逐位一致**（用 qBittorrent 的 API 或种子列表核对）
- [ ] `guid` 跨轮询**不变**；跨 Pod 重建**不变**
- [ ] **首屏仍在 30 秒内**（qBittorrent 不报错）—— 这是最容易被后续改动破坏的一条
- [ ] 回填时标题里的「待定来源」计数真的在**下降**，且没有撞上站点的建议速率
- [ ] 手工「重置」一部作品 ⇒ WARN + 换源 + 磁盘上多一份。
      ⚠️ **这是预期行为**，验收时要确认它按预期发生，不要当成 bug 修掉
- [ ] 用一个**错的 key** 起服务 ⇒ 可见（JSON + metrics + 日志）且**不重试**
- [ ] `notes/mteam-api.md` §10 剩下的待验证项逐条打勾或标注
      （`genDlToken` 算不算下载配额、建议速率的实际手感）
- [ ] `make check` 与 `make race` 全绿

## Comments

`mode: adult` 能否按番号搜到 AV（原本的否决性项）、`infoHash` 是否恒为 `null`、
`labelsNew` 里有没有「中字」、`.torrent` 的 announce 带不带凭据 —— 这四条**已经**
在 2026-10-13 用真 key 结掉了（见 `notes/mteam-api.md` §4/§5/§7/§8）。
凭据寿命也已由用户观测结掉（几个月前加的种子仍正常汇报）。
本票剩下的都是只能在真环境里验的那些。
