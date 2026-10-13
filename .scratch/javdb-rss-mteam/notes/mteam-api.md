# M-Team 的 API：查证笔记

**两类证据，别混用：**

| 标记 | 含义 |
|---|---|
| ✅ **一手** | 2026-10-13 用真 key（`javdb-apikey`）打出来的。原始响应在 `mteamprobe-out/`（已 gitignore） |
| 📖 **二手** | 从先例项目/文档读来的，未亲自验证。用它之前先验 |

第一版笔记（2026-10-12）**全部是二手**，其中两条被实测推翻（tracker 形状、`createdDate` 格式）
—— 见 §5 与 §6 的订正标记。**这一节的存在本身就是那条教训的兑现：**
「二手证据不能当依据，要标出来」。

## 1. 接口形态

| 项 | 值 | 证据 |
|---|---|---|
| base | `https://api.m-team.cc` | ✅ |
| 方法 | **POST**（所有接口，包括读） | ✅ |
| 鉴权 | 请求头 `x-api-key: <存取令牌>`（36 字符 UUID 形） | ✅ |
| 令牌从哪来 | 站点【控制台 → 實驗室 → 存取令牌】 | 📖 |
| 成功判据 | `message == "SUCCESS"`（失败时 `code` 非 `"0"`） | ✅ |

### ⚠️ 两条会让人白折腾半天的坑（都实测过）

**① User-Agent：`curl/*` 与空 UA 会被 Cloudflare 302 到 `www.google.com`。**

```
UA=curl/8.5.0            -> HTTP 302 -> https://www.google.com/
UA=<空>                  -> HTTP 302 -> https://www.google.com/
UA=Go-http-client/1.1    -> HTTP 200
UA=python-requests/2.31  -> HTTP 200
UA=Wget/1.21             -> HTTP 200
UA=javdb-rss/1.0         -> HTTP 200
```

`server: cloudflare` + `cf-ray: …-SIN`。**这不是鉴权失败，是 WAF 按 UA 拦的**
—— 拿它当「key 无效」排查会一路错到底。所以实现里**必须显式设一个非空、
且不含 `curl` 的 UA**，并且**把这个 302 认成独立的一类错误**（票 01/07）。

**② 请求体编码不统一：`search` 要 JSON，`detail` / `genDlToken` 要 form/multipart。**

```
detail  + Content-Type: application/json   -> message="參數錯誤"
detail  + -F 'id=467487'                   -> message="SUCCESS"
detail  + -d 'id=467487'（form-urlencoded）-> message="SUCCESS"
detail  + ?id=467487（query string）        -> message="參數錯誤"
```

站点自己的 wiki 就写着「文件仍有瑕疵：請求資料格式 json/form 可能與實際不符」。
**结论：别照文档猜，按端点分别试。**

## 2. 端点

| 用途 | 端点 | body | 证据 |
|---|---|---|---|
| 搜索 | `POST /api/torrent/search` | **JSON** `{keyword, pageNumber, pageSize, mode, categories, …}` | ✅ |
| 详情 | `POST /api/torrent/detail` | **form/multipart** `id=<tid>` | ✅ |
| 换下载链接 | `POST /api/torrent/genDlToken` | **form/multipart** `id=<tid>` → `{data:"<链接>"}` | ✅ |
| 取种子 | `GET <那条链接>` | 带 `x-api-key` | ✅ |

## 3. 速率（**站点自己标的是「建议值」**）

| 行为 | 建议速率 |
|---|---|
| `/torrent/search` | 1000 次／近 24 小时 |
| `/torrent/detail` | 100 次／小时 |
| 下载种子行为 | 100 个／小时 |
| 下载种子配额 | 1000 个／天 |

⭐ **「100 个/小时」是本功能唯一的瓶颈**（见 map 的 Q2）：搜索配额对 234 部想看
绰绰有余，而每次命中都要下载一次种子才能拿到 infohash。

## 4. ✅ 已钉死：**M-Team 任何端点都不给 infohash**

```
search  → 每一行的 infoHash 都是 null   （实测 5 个番号、20+ 行）
detail  → data.infoHash 也是 null      （实测 tid=467487，data 有 50 个键，infoHash 在，值是 None）
```

`detail` 的 50 个键里**没有** `torrent` 子对象、没有 `magnet`、没有 `announce`。
所以：**要 infohash，只有下载 `.torrent` 自己算这一条路。** 这与四个先例项目的做法一致
（它们干脆不用 infohash，见 §9）。ADR-0004 的前提因此是实测过的，不是推测。

## 5. ⚠️ 订正：tracker 里**没有 passkey**，而下载链接**会过期**

**第一版笔记写的是** `https://tp.m-team.cc/announce.php?passkey=…`（来自 2020 年的 gfwlist issue）。
**实测（2026-10-13）真实的 announce 长这样：**

```
https://tra1.m-team.cc/announce?credential=<base64>
  base64 解开 = sign=<32 hex>&t=<unix 秒>&tid=<种子 id>&uid=<用户 id>
```

- **没有 `passkey=` 参数。** 凭据是**按种子、按次签发**的签名串。
- **每次下载同一个种子，凭据都不同**（实测两次下载相隔 92 秒，`t` 与 `sign` 都变了，
  而 `info` 字典逐字节相同 → infohash 相同）。
- `.torrent` 的 `info.private = 1`（私种，符合预期），`announce-list` 为空
  （没有备用 tracker）。

**下载链接会过期（实测）：**

```
生成后立刻取      -> HTTP 200, 130736 B, application/x-bittorrent
约 5 分钟后再取   -> {"code":"1","message":"連結不可用！ 超出有效期","data":null}
```

⇒ **「把馒头自己的下载链接当 enclosure 交给 qBittorrent」这条路实测是死的**
（qbt 是稍后才去取的）。ADR-0004 里否掉它的理由现在有证据了。

### ✅ 已结（用户观测，2026-10-13）：那份**凭据**长期有效

它和下载链接用的是同一套 `sign&t` 形状，但**寿命完全不同**：

> 用户观测：qBittorrent 里**几个月前**加的馒头种子，现在仍正常汇报。

⇒ 凭据虽然按次签发，但 tracker **不按时效验它**（否则全站种子都会红）。
站点 wiki 的红种文档里也**没有**「凭据过期」这一条，只有「Invalid passkey → 重新下载种子」
与「torrent not register with this tracker → 种子已被删除」。

**证据等级：用户观测，不是测量。** 它足够支撑「存字节」（票 03），
但**不能**推出「凭据永不失效」—— 见下面的残余风险。

⚠️ **残余风险**：凭据绑账号。**用户重置 M-Team 的密钥后，已存下的每一份种子都会死**，
而本服务既看不见也修不了（`guid` 不能变）。唯一出口是逐部「重置」。

## 6. 搜索的参数面

`mode` 取值（📖 MCP server 的 tool schema）：`normal` / `adult` / `movie` / `music` /
`tvshow` / `waterfall` / `rss` / `rankings` / `all`。

✅ **`mode: adult` 实测可用**，按番号能精确搜到 AV（这是**否决性**的那一条，通过了）：

```
keyword=SSIS-001  mode=adult  -> 3 条，category=410（AV(有碼)/HD Censored）
keyword=三上悠亜   mode=adult  -> total=702
```

✅ **`createdDate` 的格式是 `"2021-02-22 15:19:51"`**（空格分隔、秒精度）。
**第一版笔记写成「毫秒时间戳」是错的**（那是先例 SDK 里 `TimeMillis()` 转换后的形状，
不是线格式）。它对「槽位规则跨来源比较」那条判断有影响 —— 但结论不变：
两边格式不同源，合并池子仍然是个坏主意。

分类 id 表见 PT-depiler 的 `siteCategory`（成人区：`410/424/437/431/429/430/426/432/436/440/425/433/411/412/413`）。

## 7. ⚠️ 中文字幕：只信 `labelsNew`，**别信 `hasChineseSubtitle`**

```
tid=467487  labelsNew = ['中字']   labels = '4'   hasChineseSubtitle = False   ← 自相矛盾
```

`detail` 里那个名字最像样的字段（`hasChineseSubtitle`）**实测是 `False`**，
而 `labelsNew` 里明明有 `中字`（旧字段 `labels='4'` 也对应 `中字`，见 MoviePilot 的 `_LABEL_MAP`）。

⇒ **cnsub 的唯一可信来源是 `labelsNew` 里含 `中字`。**
这与本仓库既有的那条教训同构（`CONTEXT.md`：不要指望上游的 `name_zht`，
实测它恒为空）—— 一个看起来权威、实际不填的字段。

搜索侧也可按 `ChineseSubtitle()` 筛（📖 mt-sdk 的 SearchBuilder），本服务不需要它。

## 8. ✅ 真实语料实测（2026-10-13，用户当前的想看清单）

想看清单现在只有 **5 部**（2026-10-04 是 234 部 —— 是一份会被清空的**待办队列**，
不是只增不减的收藏）。逐部按番号打 M-Team：

| 番号 | 发行 | 搜索命中 | 严格命中 | 含中字 |
|---|---|---|---|---|
| SONE-168 | 2024 | 3 | 3 | 1 |
| WAAA-448 | 2024 | 3 | 3 | 1 |
| SONE-086 | 2024 | 5 | 5 | 1 |
| SNOS-198 | 2026 | 1 | 1 | 0 |
| MIDA-821 | 2026 | 0 | 0 | 0 |

- **命中率 4/5**（80%）。命中里**多数有一条带中字版** —— 所以「来源优先」不会
  必然丢掉中文字幕（这是 Q2 选 A 时的一个担忧，实测下来不成立）。
- **匹配规则实测 100% 精确**：用「番号两侧不是字母数字」作判据时，
  严格命中数 == 搜索命中数（没有误配）。
  ⚠️ 判据**不能**用「按非字母数字切词再比词元」—— 那样日文汉字会把
  `SONE-168` 切成 `SONE` + `168`，于是**一条都匹配不上**（我第一版就写错了，
  实测 0 命中，看着像「馒头没有」）。正确判据是正则
  `(?<![A-Za-z0-9])SONE[\s\-_]*168(?![A-Za-z0-9])`。
- M-Team 侧的种子名通常**以番号开头**（`SONE-086 2025 2160p DMM WEB-DL …-MTeam`），
  且不少是 M-Team 自己的重编码（`-MTeam`），画质可能**高于** JavDB 侧那条。

## 9. 先例项目怎么用它（都不是磁力）

| 项目 | 怎么拿下载链接 | guid 用什么 |
|---|---|---|
| PT-depiler | `genDlToken` → 那个 URL | torrent id（`/detail/{id}`） |
| Jackett `MTeamTp.cs` | `genDlToken?id=N` 作为 Link，`Download()` 时再解 | 详情页 URL |
| MoviePilot `mtorrent.py` | `genDlToken` 拼 enclosure | torrent id |
| MTfin `utils_mteam.py` | `genDlToken` → GET 成字节，落盘缓存 | — |

**四个先例没有一个用 infohash 做身份**，而且**四个都会踩上下载链接过期那个坑**
（Jackett 是唯一「下载时再解」的，反而躲过了）。本仓库要成为第五个，
而且是唯一一个为「拿 infohash」付一次下载代价的（理由见 `docs/adr/0004`）。

## 10. 仍未验证 / 待定

- [x] ~~**`.torrent` 里那份 tracker 凭据的寿命**~~ —— ✅ 2026-10-13 结：
      用户观测到几个月前加的种子仍正常汇报 ⇒ 长期有效 ⇒ 存字节（见 §5 与票 03）。
      **残余风险（密钥轮换）已记入 ADR-0004 与票 03。**
- [ ] `genDlToken` 本身算不算「下载种子」配额（实测连调几次没有明显限流）。
- [ ] 建议速率的实际手感：真按 100 个/小时下载会不会被限流。
- [ ] `mode: adult` 之外是否需要 normal fallback（本轮定为不做）。

## 11. 否掉的做法（留个记录，别下次再想一遍）

- **cookie 访问** —— 站点明文说要停用账号。
- **用 `x-api-key` 换 passkey** —— 现在知道了：根本不存在 passkey，只有按次签发的凭据。
- **合成不含 announce 的磁力** —— 私种，死链。
- **合成含真 announce 的磁力** —— 可行（把 `credential=` 写进 `tr=`），但 feed 里就带凭据，
  且没法验证客户端能否取到 metadata。**本轮不做磁力**，见 map 的 Q1。
- **把馒头自己的下载链接当 enclosure** —— ✅ **实测死了**（5 分钟就过期，见 §5）。
- **本服务做 tracker 中转**（`tr=` 指向我们）—— tracker 只给 peer 地址、不给 metadata，
  所以它**修不了**「取不到 metadata」；它唯一买到的是隐藏凭据，代价是把那条 `tr`
  永久写进客户端（本服务停机/换端口 = 所有已发出的种子停止 announce）。
  而「本服务自己发 `.torrent`」免费拿到同样的隐藏效果。

## 12. 怎么重跑（本节的命令就是上面那些结论的出处）

⚠️ **必须带一个非空、且不含 `curl` 的 UA**（否则 §1 的 302）。

```sh
KEY=$(tr -d '\r\n' < javdb-apikey)
UA='Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36'
H=(-H "x-api-key: $KEY" -H "User-Agent: $UA" -H 'Origin: https://kp.m-team.cc/' -H 'Accept: application/json, text/plain, */*')

# §4 搜索（JSON 体）—— 看 infoHash 是不是 null、labelsNew 里有没有「中字」
curl -sS "${H[@]}" -H 'Content-Type: application/json' \
  --data '{"mode":"adult","keyword":"SSIS-001","pageNumber":1,"pageSize":5,"visible":1}' \
  https://api.m-team.cc/api/torrent/search

# §4 详情（**form** 体）—— 看 infoHash 是不是 null、hasChineseSubtitle 是不是陷阱
curl -sS "${H[@]}" -F 'id=467487' https://api.m-team.cc/api/torrent/detail

# §5 下载链接 + 种子（form 体）
URL=$(curl -sS "${H[@]}" -F 'id=467487' https://api.m-team.cc/api/torrent/genDlToken | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"])')
curl -sS "${H[@]}" -L "$URL" -o /tmp/s.torrent   # 立刻取：130KB 的 bencode
sleep 300 && curl -sS "${H[@]}" -L "$URL"          # 5 分钟后再取："超出有效期"
```

想看清单（用真 token，只读、**不要跑服务** —— 跑服务会就地写 pin 表）：

```sh
go build -o /tmp/cp ./cmd/contractprobe
/tmp/cp -config config.yaml raw /api/v2/users/review_movies status=want_watch page=1
```

计算 infohash（对 `info` 的**原始切片**取 SHA-1）见 §8 与票 02；
本次实测值：`tid=467487` → `bde00a89b0e418e1643ae237ab01dbe0321befb4`。

> 还没做成 `cmd/mteamprobe`（像 `cmd/contractprobe` 那样可复跑）。本轮是一次性取证，
> 原始响应在 `mteamprobe-out/`。**若票 01/08 需要反复复核这些契约，再把它工具化**
> —— 先例的理由（「结论会被反复引用」）在这里同样成立。
