# 02: 想看里的一部作品命中 M-Team 时，feed 交出馒头那份种子

**What to build:** 配了 `MTEAM_API_KEY` 之后，`/rss/want.xml` 里一部**馒头上也有**的作品，
其条目交出**馒头那一份**：`<enclosure>` 指向本服务的 `GET /torrent/{infohash}.torrent`，
`<guid>` 仍是 infohash（从馒头那份种子里算出来的）；馒头没有的作品照旧发上游磁链。
判定**同步**发生在请求内 —— 规模问题由票 04 解决，不是本票的缺陷。

**Blocked by:** 01.

**Status:** resolved（2026-10-13）

**配置与开关**

- [x] 配置 `mteam.api_key`；环境变量 `MTEAM_API_KEY` 优先，且**空值不算「设置了」**
      （照 `JAVDB_TOKEN` 那条既有判断，否则 compose 里一个没填的 env 会把文件里的 key 顶掉）
- [x] 三份部署配置的键集合一致（`internal/config/examples_sync_test.go` 会拦）
- [x] **没有 key 时 M-Team 完全不生效**，feed 与改动前逐字节相同

**客户端契约**（每条都有实测出处，见 `notes/mteam-api.md`）

- [x] base `https://api.m-team.cc`，一律 POST，鉴权头 `x-api-key`，成功判据 `message == "SUCCESS"`
- [x] **显式设置一个非空、且不含 `curl` 的 User-Agent** —— 否则 Cloudflare 会 302 到
      `www.google.com`，而那**不是**鉴权失败
- [x] **请求体编码按端点分**：搜索用 JSON；`detail` 与 `genDlToken` 用 form/multipart
- [x] 三类「重试无用」的错误**可被上层判定**且都不重试：凭据无效、UA 被拦（302 到 google）、
      下载链接过期（`code=1` / `連結不可用！ 超出有效期`）
- [x] 搜索 `mode` 固定 `adult`，不做 normal fallback
- [x] 中文字幕**只读搜索返回的 `labelsNew` 里含 `中字`**；不要读 `detail` 的
      `hasChineseSubtitle`（实测它在有「中字」标签时仍是 `false`）

**infohash 与命中判定**

- [x] bencode 解析：对 `info` 字典的**原始字节切片**取 SHA-1，**不重新编码**
- [x] 只有 v2（无 v1）时**拒绝并报错**，不静默给一个错的
- [x] `announce` 不带凭据时**报错**（形状是 `…/announce?credential=<base64>`，
      解开为 `sign=&t=&tid=&uid=`；**不是 `passkey=`**）
- [x] 番号匹配判据是「番号两侧不是字母数字」（正则 `(?<![A-Za-z0-9])SONE[\s\-_]*168(?![A-Za-z0-9])`）；
      **不得**实现成「按非字母数字切词再比词元」（日文汉字会把 `SONE-168` 切成 `SONE`+`168`，
      于是一条都匹配不上，表现为「馒头没有」）

**判定表**

- [x] 独立于 pin 的文件、权限 `0600`、flock 单写者
- [x] 文件损坏或版本不认识 ⇒ **拒绝启动**；落盘失败 ⇒ **致命**（四条都照 `pin.Store` 的先例）
- [x] 包注释写明与 pin 的区别：**丢判定表不会改 `guid`**，丢 pin 会让客户端静默重复下载
- [x] 种子字节存在同目录的 `torrents/`，权限 `0600`

**装饰器与交付**

- [x] 作品**已有钉住** ⇒ 连问都不问（这是「首发冻结」；`pin.Store.Get` 已经存在，不需要新接缝）
- [x] 无钉住且有命中 ⇒ 候选池**整体换成** M-Team 那一组（来源优先，不是合并池）
- [x] 槽位规则在 M-Team 那一组**内部**照常生效（中文字幕优先 → `created_at` 最新 → infohash）
- [x] 装配顺序保持「`dedupe` 最外、`pin` 在里」，新装饰器同样在 `dedupe` 之内
- [x] `GET /torrent/{infohash}.torrent`：命中 ⇒ 200 + 字节逐位相同 + `application/x-bittorrent`；
      未知 infohash ⇒ **404**（不是 500、不是空文件）

**端到端**

- [x] 用 `httptest` 假 M-Team + 可编程 `catalog.Source`：want.xml 里该条目的
      `<guid>` == 从假种子算出的 infohash，`<enclosure url>` 指向本服务
- [x] 取那个 enclosure 得到的字节与假 M-Team 给的一致
- [x] `make check` 与 `make race` 全绿

## Answer（2026-10-13）

**落点**：新包 `internal/mteam/`（`client.go` / `torrent.go` / `store.go` / `source.go`）
+ `internal/config`（`mteam.api_key` / `mteam.state_dir` / `MTEAM_API_KEY`）
+ `internal/httpapi`（`GET /torrent/{infohash}.torrent`）+ `cmd/javdb-rss` 装配。

⭐ **四处与票面/规格不同的判断（都是实测逼出来的，记在这里）**：

1. **下载链接合法地 302 到 CDN，而 API 端点不跟随重定向 —— 客户端要两套策略。**
   票面没写这一条，而它踩过一次：一开始为了认 WAF 给**所有**请求禁了重定向，
   于是**每一次下载都被误判成「被 Cloudflare 拦掉」**。现在 `apiClient`（不跟）与
   `cdnClient`（跟）分开，两边各有注释说明为什么。
2. **成功判据取 `code == "0"`，不是票面写的 `message == "SUCCESS"`。**
   实测失败也是 HTTP 200（`{"code":"1","message":"key無效"}`），因此必须看 code；
   而 `message` 是自然语言，只用来分类失败。
3. **判定表只存「选中的那一条」，不存整份候选列表。** 规格写的是「候选快照」，
   但 M-Team 的候选**要下载才知道 infohash**，而我们只下载选中的那条 ——
   存一份「除了一条之外都没有 infohash」的候选表，只会让读它的人以为那些条目能用。
   于是「选哪条」发生在下载**之前**（用搜索元数据），池子最终是单元素。
4. **`catalog.Select` 的第三个键（infohash 字典序）在这里换成 torrent id。**
   同一条理由：选哪条必须发生在下载之前。这是「来源优先」的必然代价，不是疏漏。

**测试**：`internal/mteam` 五组（bencode 表驱动含键序非规范夹具、客户端契约、判定表
坏情况、命中判定与选一条、装饰器）、`internal/httpapi` 一条**端到端**
（want.xml → 命中 → enclosure → 取回字节，穿过五层）。

**真实验收（本次就做了一半）**：拿真 key 跑了一次完整链路 —— SSIS-001 搜索 3 条、
严格命中 3 条、下载 130736 字节、算出的 infohash `bde00a89b0e418e1643ae237ab01dbe0321befb4`
与早先用 python **独立**算的逐位相同；MIDA-821 正确返回 0 条。

⚠️ **本票的已知边界**：判定是**同步**发生在请求内的（规模问题由票 04 解决）；
「没命中」与「查询失败」都**不落盘**（票 03 解决），因此那两种情况下每轮都会重查一次。
