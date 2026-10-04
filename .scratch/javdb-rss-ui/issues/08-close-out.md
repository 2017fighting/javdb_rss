# 08: 收口：契约回写 + 文档 + 真实验收

**Spec:** [`../spec.md`](../spec.md)

**What to build:** 把这轮 promote 里**改了行为**的几条作为新的硬规则写回 `ui-contract.md`（按女优 `filter_tags` 列基本组、女优 id 可手输、无 token 时禁用输出、截断信号可见），而不是只活在代码里 —— 这是设计循环的 LEARN 一步。`CONTEXT.md` 的 Language 节增补「标签词表」与「订阅链接生成器」；`README` 增「打开页面」一节（写清无 token 时哪些区可用）。

然后跑一遍完整的设计验证协议与全部守卫，最后用真上游 + 真 token 生成一条链接、粘进真实 qBittorrent 验收（照需求 1/2/3 的先例）。

**Blocked by:** 04, 05, 06, 07

**Status:** resolved

- [x] `ui-contract.md` 补上这四条硬规则及各自的理由（不是只加标签，要说清为什么）
- [x] `CONTEXT.md` 增两条术语；`README` 的「打开页面」写清无 token 与白名单两种情况下的行为
- [x] 全量守卫绿：Go 的 `make check`（fmt / vet / test）+ `make race` + `tools/verify.sh`（token-lint / 对比度 / axe / 触摸目标 / 焦点 / 行为断言 / rubric）
- [x] 一次真上游 + 真 token 的验收记录：页面上生成一条链接（含标签与年份），粘进 qBittorrent 后条目能出来
- [x] `.scratch/javdb-rss-ui/` 留下的设计记录与实现不再互相矛盾（旧结论一律改掉，不留已被证伪的说明）

## Comments

### 1. 契约回写（`ui-contract.md` 第七批，规则 12–15）

四条新硬规则，每条都带「为什么」与它在 `flows.mjs` 里的断言名：

12. **女优模式的基本组只列她自己的 `filter_tags`。** 词表 `main` 组有 6 个字母，按词表列全部
    会让人点到一个她一部作品都没有的字母 —— 链接能生成、不报错、返回空 feed。
13. **女优 id 可以手输；标签区不与收藏连坐。** 词表与 `/actress_tags/{id}` 都匿名可读，
    而 `/collected` 要 token；把「挑女优」绑死在收藏上会让标签区被一个无关的失败波及。
14. **拿不到就不发：禁用输出并说清缺什么。** 没 token 时「想看」「清单」一定 503，
    所以按钮 `disabled` + 写明缺 `token_file` + **页面上不出现那条链接**。
15. **截断必须可见，措辞是「已知不完整」而不是「出错了」。** 判定按契约「看 `truncated`
    键在不在」（服务 `omitempty`），未截断时这条提示完全不存在。

**同时改掉了两处已被证伪的旧规则**（规则 7 要求「页面上不能留旧解释」，这两条是写在契约里的旧解释）：

- 规则 4 的后半段：原本写「年/月/时长改由**本服务本地**按 `release_date`/`duration` 过滤，
  URL 里出现的是 `since=` 与未来的 `duration=`（未实现）」。真机抓包找到了**掩码槽位**这条
  通道后，这个方案既没实现也不该实现 —— 现在实的是「页面发语义参数、**掩码由服务拼**」。
- 规则 5：「年份/月份只给下界、要 `until=`」—— 年份落到掩码第 5 段后是**整年**、月份是整月。
  仍然成立的是 `since=` 那半句（本服务本地过滤，所以不能与 `year` 同时发）。

### 2. 术语与 README

- `CONTEXT.md` Language 节增补：**标签词表**（按片库分组、名字与顺序原样使用）与
  **订阅链接生成器**（页面；只发语义参数、本身不存状态），各带 `_Avoid_`。
- `README.md` 新增「打开页面」一节：怎么打开、它是什么（embed 的端点，不是前端工程）、
  只发语义参数、选择只在内存里；然后用两张表写清**没有 token 时各区行为**（收藏读不到 /
  标签区照旧可用且可手输 id / 清单与想看禁用并说明）与**白名单生效时**的行为
  （片库直接 disable、女优与清单仍渲染 + 顶部说明、状态取自 `/version`）。
- 「现在能跑到哪一步」表加一行页面；并把那一行过时的
  「`/collected` 尚未对着真实 API 验证过」改成实测结果。

### 3. 真实验收（真上游 + 真 token + 真 qBittorrent v5.2.4）

`token.json` 里的 token 已被上游拒绝（`JWTVerificationError: 請登錄帳號`），
用 `~/.config/javdb-rss/recon.env` 里的凭据重跑 `javdb-rss login` 换了新的（会挤掉手机登录，
已确认这是可接受的）。随后按 b1eccf4（需求 1/2/3 的先例）的做法：**镜像构建 + 服务容器 +
`qbittorrentofficial/qbittorrent-nox` 容器同网**，经 Web API 验收。

1. 页面（真服务、真 token）：收藏区读出真实 **138 位女优**（144 − 6 男），
   词表按 `/tags?type=0` 从上游取，上游 chip =「上游正常」。切「全站标签」、
   选**眼鏡**（tag 3）+ **2023 年**，页面生成：

   ```
   http://javdb-rss:8080/rss/tags/0.xml?main=m&tags=3&year=2023
   ```

   逐字符检查不含 `filter_by` / `filter_by_tags`；这条 URL 同时进了底部「待复制」。
2. qBittorrent：`POST /api/v2/rss/addFeed` + `refreshItem` 后拿到 **50 条条目**、
   `hasError: false`、`lastBuildDate` 正确解析、标题
   「JavDB · 全站（有码 · 主属性 m · 1 个标签 · 2023 年）」；每条都有 `guid` 与 `torrentURL`。
3. 磁链真能被引擎接受：第一条以 `stopped=true` 加入，`success_count: 1`、
   infohash 与 `guid` 逐字相同，随后连文件删除（零流量）。
4. 验完即清：两个容器与网络都已删除。

**顺带对上的两件事**：`/collected` 也对着真 token 验了（144 位、6 位男优、`truncated` 键不出现）；
qBittorrent v5.2.4 的 WebUI API（2.15.1）里加 feed 的端点是 **`/api/v2/rss/addFeed`**
（不是文档里常见的 `/api/v2/rss/add`，后者在该版本返回 404）—— 这条写进了 README 的验收记录。

**一个观察（不是 bug）**：`year=2023` 的 feed 里部分条目 `pubDate` 落在 2024/2025 ——
`year` 筛的是上游的**发行年份**，`pubDate` 是**磁链创建时间**。筛选本身是对的：
`year=2025` 与不带 `year` 返回的是两批不同的 50 条（2025 那批全是 2025/2026 的磁链）。

### 4. 设计记录与实现对齐（本次清掉的旧结论）

- `notes/lists.md` §2 与开头结论：整节「改为**本地过滤**」的方案被推翻（从未实现），
  加了更正横幅、把对照表改成实际实现，并把「仍然未知」里的年/月/时长划掉。
- `notes/tag-vocabulary.md` §6 加更正横幅（§7 已推翻它）；§5 的 `gender` 缺口标注已修复；
  「这些结论对代码意味着什么」重写成 promote 后状态（后端四条都已落地，含全站订阅的**实际形状**
  与当初提议不同）；「仍然未知」收敛到女优掩码尾部 / `type`↔zone / 词表外公差三个。
- `mockup/README.md`：加 promote 完成横幅；把表里女优模式示例 URL 从
  `filter_by=…&filter_by_tags=…` 改成语义参数；`为什么不是 filter_by_tags` 的
  「本服务自己过滤」改成掩码槽位；「验证结果」的 40 项 → **171 项（真服务）」；
  「与真实实现的距离（promote 时要补的）」六项全部标为已完成；目录里的
  「五条硬规则」→ 15 条、「flows.mjs 40 项」→ 171 项；验证跑法改成起 stub 服务。
- `spec.md`：硬规则计数 11 → 15、断言数 40 → 171（并注明打的是真服务）。

### 验证

- `make check`（gofmt / vet / 全量测试）全绿；`make race`（竞态检测）全绿。
- `tools/verify.sh` 打 `provider=stub` 真服务：token-lint ✓、axe/触摸目标/焦点/溢出 ✓、
  **行为断言 171 项 ✓**、L4 rubric 7/7。
- 真实验收见上（真上游 + 真 token + qBittorrent v5.2.4）。
- 本次**只改文档**，没有改任何 Go 或前端代码 —— `git diff` 只有 `.md`。

### 保留的判断项

- **`type` ↔ `filter_by` 的 zone 对应**仍只是推测（三条旁证，没对着作品列表验证过）——
  端点只做透传 + 「0–3 之外判 400」，页面拿到什么显示什么；没有把这个推测写进代码或文案。
- **女优掩码的尾部槽位**（女优模式下的月份/时长）仍未抓包验出，页面照旧禁用并说明 ——
  这是 spec 里明确的 Out of Scope。
- **清单白名单开不开**仍是用户待定项，本票没有替他决定；页面只在白名单生效时说出来。
- 验收用的 `token.json` 是本地未跟踪文件（`.gitignore`），换 token 不影响仓库。
