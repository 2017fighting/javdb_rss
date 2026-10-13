# 02: 想看里的一部作品命中 M-Team 时，feed 交出馒头那份种子

**What to build:** 配了 `MTEAM_API_KEY` 之后，`/rss/want.xml` 里一部**馒头上也有**的作品，
其条目交出**馒头那一份**：`<enclosure>` 指向本服务的 `GET /torrent/{infohash}.torrent`，
`<guid>` 仍是 infohash（从馒头那份种子里算出来的）；馒头没有的作品照旧发上游磁链。
判定**同步**发生在请求内 —— 规模问题由票 04 解决，不是本票的缺陷。

**Blocked by:** 01.

**Status:** ready-for-agent

**配置与开关**

- [ ] 配置 `mteam.api_key`；环境变量 `MTEAM_API_KEY` 优先，且**空值不算「设置了」**
      （照 `JAVDB_TOKEN` 那条既有判断，否则 compose 里一个没填的 env 会把文件里的 key 顶掉）
- [ ] 三份部署配置的键集合一致（`internal/config/examples_sync_test.go` 会拦）
- [ ] **没有 key 时 M-Team 完全不生效**，feed 与改动前逐字节相同

**客户端契约**（每条都有实测出处，见 `notes/mteam-api.md`）

- [ ] base `https://api.m-team.cc`，一律 POST，鉴权头 `x-api-key`，成功判据 `message == "SUCCESS"`
- [ ] **显式设置一个非空、且不含 `curl` 的 User-Agent** —— 否则 Cloudflare 会 302 到
      `www.google.com`，而那**不是**鉴权失败
- [ ] **请求体编码按端点分**：搜索用 JSON；`detail` 与 `genDlToken` 用 form/multipart
- [ ] 三类「重试无用」的错误**可被上层判定**且都不重试：凭据无效、UA 被拦（302 到 google）、
      下载链接过期（`code=1` / `連結不可用！ 超出有效期`）
- [ ] 搜索 `mode` 固定 `adult`，不做 normal fallback
- [ ] 中文字幕**只读搜索返回的 `labelsNew` 里含 `中字`**；不要读 `detail` 的
      `hasChineseSubtitle`（实测它在有「中字」标签时仍是 `false`）

**infohash 与命中判定**

- [ ] bencode 解析：对 `info` 字典的**原始字节切片**取 SHA-1，**不重新编码**
- [ ] 只有 v2（无 v1）时**拒绝并报错**，不静默给一个错的
- [ ] `announce` 不带凭据时**报错**（形状是 `…/announce?credential=<base64>`，
      解开为 `sign=&t=&tid=&uid=`；**不是 `passkey=`**）
- [ ] 番号匹配判据是「番号两侧不是字母数字」（正则 `(?<![A-Za-z0-9])SONE[\s\-_]*168(?![A-Za-z0-9])`）；
      **不得**实现成「按非字母数字切词再比词元」（日文汉字会把 `SONE-168` 切成 `SONE`+`168`，
      于是一条都匹配不上，表现为「馒头没有」）

**判定表**

- [ ] 独立于 pin 的文件、权限 `0600`、flock 单写者
- [ ] 文件损坏或版本不认识 ⇒ **拒绝启动**；落盘失败 ⇒ **致命**（四条都照 `pin.Store` 的先例）
- [ ] 包注释写明与 pin 的区别：**丢判定表不会改 `guid`**，丢 pin 会让客户端静默重复下载
- [ ] 种子字节存在同目录的 `torrents/`，权限 `0600`

**装饰器与交付**

- [ ] 作品**已有钉住** ⇒ 连问都不问（这是「首发冻结」；`pin.Store.Get` 已经存在，不需要新接缝）
- [ ] 无钉住且有命中 ⇒ 候选池**整体换成** M-Team 那一组（来源优先，不是合并池）
- [ ] 槽位规则在 M-Team 那一组**内部**照常生效（中文字幕优先 → `created_at` 最新 → infohash）
- [ ] 装配顺序保持「`dedupe` 最外、`pin` 在里」，新装饰器同样在 `dedupe` 之内
- [ ] `GET /torrent/{infohash}.torrent`：命中 ⇒ 200 + 字节逐位相同 + `application/x-bittorrent`；
      未知 infohash ⇒ **404**（不是 500、不是空文件）

**端到端**

- [ ] 用 `httptest` 假 M-Team + 可编程 `catalog.Source`：want.xml 里该条目的
      `<guid>` == 从假种子算出的 infohash，`<enclosure url>` 指向本服务
- [ ] 取那个 enclosure 得到的字节与假 M-Team 给的一致
- [ ] `make check` 与 `make race` 全绿
