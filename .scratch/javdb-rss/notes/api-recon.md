# 侦察记录：JavDB 官方 App 私有 API

针对 `jdb_official_v1.9.35.apk` 的静态侦察结论。每条都标了证据来源，方便后续 session 复核。

## 0. 目标二进制指纹

**APK 不入库**（`.gitignore` 里排除了 `*.apk`）。下面的 Session 若需要它，先按指纹确认拿到的是同一份：

| 项 | 值 |
|---|---|
| 文件名 | `jdb_official_v1.9.35.apk` |
| 大小 | 34140434 bytes |
| sha256 | `3616335e465afe5f57376080ddd211ae61aad77e44ecf8aec9cb819deeea4a5e` |
| 版本 | `versionName 1.9.35`（`assets/.../gradle/app-metadata.properties`）/ Dart 包名 `astarte` / Android 包名 `xxx.pornhub.fuck` |
| **官方下载源** | <https://github.com/bdvajstudio/javdb/releases/download/v1.9.35/jdb_official_v1.9.35.apk> |

**✅ 来源已验证（2026-09-28）**：从上述官方发行源重新下载，sha256 与本地文件**逐位一致**
（`3616335e...4a5e`，34140434 bytes）。这份就是官方 v1.9.35 发行包，未被篡改。

历史版本也在同一仓库（v1.9.17 / 18 / 19 / 27 / 28 / 29 / 34 / 35，含 `.ipa`）。
若将来需要看旧版以定位签名常量的变更点（javdb-cli 逆的是 **1.9.28**），从这里取。

> 这为何重要：`SecurityUtil.getSecret()` 的输入是 **APK 签名证书**。若手上是第三方重打包的包，
> 签名证书不同 → 算出的 secret 不同 → 签名验证会一直失败。现已排除此风险。
> 结论：APK **不入库是安全的**，按上面的 URL 可确定性重建。

## 1. App 形态

| 事实 | 证据 |
|---|---|
| Flutter 应用，Dart 包名 `astarte`，Android 包名 `xxx.pornhub.fuck` | `lib/arm64-v8a/libapp.so` 字符串中的 `package:astarte/...`；`classes.dex` 中的 `Lxxx/pornhub/fuck/MainActivity;` |
| **Dart 代码未混淆** —— 类名、方法名、文件名完整保留 | `strings libapp.so` 可读到 `package:astarte/net/intercept.dart`、`_HomePageState`、`ActorPageProvider` 等 |
| 原生库 | `lib/arm64-v8a/libsecurity.so`（19 KB）、`libflutter.so`、ijkplayer 系列 |
| 构建者路径泄漏 | `file:///Users/oum/workspace/projects/astarte_app/.dart_tool/...` |

## 2. API 主机

| 主机 | 状态 | 证据 |
|---|---|---|
| `https://jdforrepam.com` | **可达**（本机直连 HTTP 200） | `strings libapp.so` 含该字面量；`curl` 实测有响应 |
| `https://staging.letidi.com` | DNS 不可解析（staging） | 同上 |

主机是动态下发的（`package:astarte/home/models/domain_entity.dart` + `/api/v1/startup`），上线前需确认是否有换域机制。

## 3. `jdsignature` 是硬门槛

```
$ curl -s https://jdforrepam.com/api/v1/startup
{"success":0,"action":"ParameterInvalid","message":"參數不能爲空: jdsignature","data":null}
```

- `GET /api/v1/movies/latest` 同样返回该错误 → **所有端点都强制校验 `jdsignature`**。
- 服务端错误文案是繁体中文，错误信封为 `{success, action, message, data}`。
- `jdsignature` 是 **HTTP 请求头**，不是查询参数。

### ⚠️ 两种签名失败形态（实测，极易漏掉一种）

2026-09-28 实测确认，签名问题有**两种不同的服务端表现**：

| 情形 | HTTP 状态 | `success` | `action` | `message` |
|---|---|---|---|---|
| **签名值为空/缺失** | **200** | 0 | `ParameterInvalid` | `參數不能爲空: jdsignature` |
| **签名值无效** | **400** | 0 | `InvalidSignature` | `無效的簽名` |

**这一点真实坑过我们**：健康探针最初只处理了 `ParameterInvalid`（HTTP 200），
而「签名常量失效」这条真实路径走的是 HTTP **400 + `InvalidSignature`**，
且当时 `GetJSON` 按状态码提前返回了字符串错误，`action` 根本没到达分类器。
结果是：**告警在最该响的时候报了 `signature_broken: false`**。

两个教训：

1. 这个 API 在 4xx 时**仍然返回标准信封**，所以不要按状态码提前短路，
   先把信封解出来拿 `action`。
2. 分类要按 `action` 而不是按错误文本。

### 其他必带参数

缺任何一个公共参数也会得到 `ParameterInvalid`（HTTP 200），
message 里的字段名会指出缺的是哪个。因此 `ParameterInvalid` 不完全等于
「签名无效」，但它同样意味着「我们的请求构造与服务端不一致」——
处置方式一样（改代码），所以探针把两者归为一类。

## 4. `libsecurity.so`：签名原料的一半

导出符号（`readelf -sW`）：

```
T JNI_OnLoad
T Java_xxx_pornhub_fuck_SecurityUtil_getSecret
T _Z7encryptPcS_i / _Z7decryptPcS_i
T _Z10AesEncryptPhS_i / _Z19Contrary_AesEncryptPhS_i
T _Z11ScheduleKeyPhS_ii
T _Z7MD5InitP7MD5_CTX / _Z9MD5UpdatePhj / _Z8MD5FinalPhPh / _Z12MD5TransformPjPh / _Z9MD5EncodePhPjj / _Z9MD5DecodePjPhj
T _Z15byteToHexStringPhiPc
```

`Java_xxx_pornhub_fuck_SecurityUtil_getSecret` 反汇编（radare2）流程：

1. `ActivityThread.currentApplication()` → `Context`
2. `context.getPackageName()` → `pm.getPackageManager()` → `pm.getPackageInfo(pkg, 0x40 /* GET_SIGNATURES */)`
3. `pi.signatures[0].toCharsString()`
4. MD5 → `byteToHexString`（hex）
5. `strncpy(dst, hex, 5)` → **只取前 5 个字符**
6. 存进静态字段缓存；取不到时 fallback `NewStringUTF("astarte")`

→ `SecurityUtil.getSecret()` 返回值 = **5 个字符**（正常路径为 APK 签名证书 MD5 的前 5 位 hex；异常路径为字面量 `"astarte"`）。

另：`assets/flutter_assets/assets/data/data.txt` 是一段 368 字节（492 base64 字符）的密文，`assets/config.properties.default` 内容为 `keyAmplitude=lksjfkdsjfkdsfjkld`。两者用途未定，见 ticket 02。

## 5. 签名拼接在 Dart 层

`libapp.so` 字符串证据：

- `package:astarte/net/intercept.dart`、`AuthInterceptor`、`TokenInterceptor`、`RequestInterceptorHandler`
- `MD5Digest.`、`SHA1Digest`、`HMac.withDigest`、`getSignature`
- `_signPrefix@1419441731`、`_signSuffix@1419441731`
- 参数名：`jdsignature`、`deviceId`、`timestamp`、`authorization`、`user-agent`、`app_version`、`nonce`

→ 签名 = f(请求参数, deviceId, timestamp, getSecret() 的 5 字符)，具体拼接与摘要算法待恢复。

## 6. 端点清单（`strings libapp.so` 提取）

与需求直接相关的：

| 用途 | 端点 |
|---|---|
| 登录 | `/api/v1/sessions` |
| 当前用户 | `/api/v1/users`、`/api/v1/users/additional` |
| **App 里收藏的女优** | `/api/v1/users/collected_actors` |
| 收藏的番号 / 系列 / 片商 / 导演 / 列表 | `/api/v1/users/collected_codes`、`collected_series`、`collected_makers`、`collected_directors`、`collected_lists` |
| 女优详情 / 列表 / 推荐 | `/api/v1/actors/%s`、`/api/v1/actors`、`/api/v1/actors/recommend` |
| 女优收藏动作 | `/api/v1/actors/%s/collect_actions` |
| 最新作品 | `/api/v1/movies/latest` |
| 作品磁链 | `/api/v1/movies/%s/magnets` |
| 搜索 / 搜磁链 | `/api/v2/search`、`/api/v1/search_magnet` |
| 标签关注（app 的「订阅标签」） | `/api/v1/following_tags`、`/following_tags/%s/sort`、`/batch_push`、`/batch_destroy` |
| 看过 / 最近浏览 | `/api/v1/logs/movie_played`、`/api/v1/users/recent_viewed` |
| 排行榜 / Top | `/api/v1/rankings`、`/api/v1/rankings/actors`、`/api/v1/movies/top` |

完整列表（约 90 条）可由 `strings libapp.so | grep -oE "/api/v[0-9]/[^ \"]*" | sort -u` 复现。

注意：App 里的「订阅」语义有两处 —— **收藏女优**（`/users/collected_actors`）和 **关注标签**（`/following_tags`）。用户第 4 条需求指的是前者，需在 ticket 06 用真实响应确认。

## 7. 环境能力

| 工具 | 状态 |
|---|---|
| radare2 6.2.0 / rabin2 / objdump / gdb | ✅ 可用 |
| python3.14 + capstone 5.0.7 + mitmproxy 12.2.3 | ✅ 可用 |
| jadx | ✅ 可用（`/usr/bin/jadx`） |
| frida / objection / apktool / ghidra / blutter | ❌ 未安装 |
| `javdb.com` 直连 | ❌ TLS 被 RST（需要代理） |
| `jdforrepam.com` 直连 | ✅ 可达 |

## 8. 参考项目边界

- `/root/clone/JAVDB_AutoSpider` — **走 javdb.com 网页**。已有：磁链提取、按优先级分类（`字幕 / hacked(UC无码破解>UC>U无码破解>U) / no_subtitle`）、女优订阅 + 新作 diff（ADR-054，`javdb/pipeline/subscription_monitor.py`）、FastAPI、qBittorrent 上传。**没有 RSS 输出**，订阅列表存在它自己的库里而不是读 JavDB 账号。它的磁链/字幕分类逻辑可作语义参考，但代码不能直接复用（我们走 App API + Go）。
- `/root/clone/javdb_crawler` — scrapy 骨架，只有 movie_pages / movie_detail 两个 spider，无可复用资产。

## 9. 结论

RSS 服务本身、磁链解析、qBittorrent 对接都不难。**整条路只有 `jdsignature` 一个未知数。**排除它之后，剩下的都是常规工程。

---

# 10. 实测契约附录（2026-09-28，ticket 06）

用本服务自己的签名实现打的真实请求，全部记录在此。**每个 session 接手前读这一节。**

## 10.1 番号解析：`/api/v2/search` 是**模糊搜索**

```
GET /api/v2/search?q=KV-328
→ data.movies 有 8 条，number 分别是：
  KV-328  KV-323  KV-322  KV-326  KV-318  KV-324  KV-329  KV-327
```

**这是一个前缀/模糊搜索，不是精确查询。** 后果：

- 按位置取 `movies[0]` 在多数时候**看起来是对的**（目标常在第一位），
  但在番号尾部字符不同的情况下会静默命中错误作品 —— 不报错，只是发错片。
- 必须按 `number` 字段**精确比对**（忽略大小写与首尾空白），找不到就报错，
  **绝不退回近似结果**。

实测对照：

| 查询 | 命中数 | 首条 | 精确匹配位置 |
|---|---|---|---|
| KV-328 | 8 | KV-328 | 0 |
| REBDB-1047 | 10 | REBDB-1047 | 0 |
| SSIS-001 | 10 | SSIS-001 | 0 |
| KV-329 | 8 | KV-329 | 0 |
| ABC-123 | 0 | — | 无 |

`data` 只有两个键：`movies`、`current_page`。

**作品精简形态**（`/api/v2/search`、`/api/v1/movies/tags`、`/api/v1/movies/latest` 共用）：

```
id, number, title, origin_title, thumb_url, cover_url, duration,
magnets_count, can_play, play_subtitle, has_preview_video,
has_cnsub, has_preview_images, release_date, new_magnets, first_magnets, preview_images[]
```

## 10.2 女优作品列表：`filter_by` 是**复合掩码**

早期误以为 `filter_by` 是 `a` / `apmc` 这种简单字母组合。
**错** —— 那样请求不带实体 id，服务端返回的是**全站最新作品**（实测拿到 `CD-26008`）而不是该女优的作品，**而且不报错**。

真实格式（实测确认，javdb-cli 的 `masks.go` 也一致）：

```
{zone}:{letter}:{id}[:{main}:]:
```

- `zone`：censored=0 uncensored=1 western=2 fc2=3
- `letter`：actor=a series=s maker=m director=d code=c list=l
- `id`：实体 id（如女优 `EvkJ`）
- `main`（可选）：主属性逗号列表

可用样例：

```
filter_by=0:a:EvkJ              该女优全部作品
filter_by=0:a:EvkJ:apmc::       带主属性筛选
```

主属性字母表：`p`=Playable `m`=Downloadable `c`=Subtitles `s`=Individual `i` `v`。

端点 `GET /api/v1/movies/tags`，参数 `filter_by`、`filter_by_tags`、`sort_by`（默认 `release`）、
`order_by`（默认 `desc`）、`page`、`limit`。`data` 含 `movies`、`has_collected`、`current_page`。

## 10.3 ⭐ 分页上限：`limit` 最大 **50**

```
limit=20  -> 20 条
limit=50  -> 50 条
limit=100 -> 50 条   ← 被截断
limit=200 -> 50 条
limit=500 -> 50 条
```

分页实测（EvkJ，按 `release_date` 倒序）：

```
page=1 -> 20 条, SNOS-449 (2026-10-27) .. SNOS-233 (2026-05-26)
page=2 -> 20 条, OFJE-629 (2026-05-12) .. OAE-293 (2025-12-24)
page=3 -> 20 条, OFJE-590 (2025-12-23) .. OFJE-609 (2025-07-29)
（三页之间无重复）
```

> **注意**：EvkJ 的 `videos_count` 是 229，但作品列表里出现了 `OFJE-*` 合集、
> 且按 release 倒序时第一页顶部有无中文名的条目。说明这个列表**不纯是单体作品**，
> 且 `videos_count` 与列表条数的关系不直接。

## 10.4 磁链列表

两个端点给**不同的字段集**，用途不同：

**`/api/v1/movies/{id}/magnets`** ← feed 用它（有 `cnsub`）

```json
{"magnets":[{"name":"KV-328","hash":"0e8f4789...","size":3110,
  "cnsub":false,"hd":true,"files_count":2,
  "created_at":"09/27/2026","pikpak_url":"https://keepshare.org/..."}]}
```

**`/api/v1/search_magnet?q=`** ← **没有 `cnsub`/`hd`**，且 `created_at` 是 ISO

```json
{"magnets":[{"id":14460812510,"title":"KV-328","hash":"0e8f4789...",
  "size":3110,"files_count":2,"created_at":"2026-09-27T23:00:19.000Z"}]}
```

**结论：feed 必须走 `/movies/{id}/magnets`**，因为只有它给磁链级的 `cnsub`。
`search_magnet` 的 `created_at` 是 ISO 格式（与另一种的 `09/27/2026` 不同），
这解释了为什么 `feed.parseCreatedAt` 要接受两种格式。

**一次调用返回全部磁链**（样本 1 条），未见分页。

### 10.4.1 `magnets[]` 的顺序不是时间序（ticket 04 取证，2026-09-30）

**背景**：feed 条目取「第一条 `cnsub`，否则 `magnets[0]`」，而 `guid` = 该条的
infohash。因此上游一旦重排，guid 就变、qBittorrent 会再下一份 —— 且没有任何告警。
这次用**真实 API**把「顺序到底是什么」量了出来（211 部作品，探针 `cmd/magnetprobe/`）。

**结论：`magnets[]` 既不按发布时间升序，也不降序。**

| 指标（197 部 ≥2 条候选） | 结果 |
|---|---|
| 相邻两条「旧→新」 | 433 对 |
| 相邻两条「新→旧」 | 439 对 |
| 相邻两条同日 | 136 对 |
| 第 0 位是最**新**的 | 72/197 |
| 第 0 位是最**旧**的 | 68/197 |
| 全程单调「旧→新」 | 25 部 |
| 全程单调「新→旧」 | 20 部 |

**它看起来是「版本档位」排序，不是时间排序。** `name` 字段带了档位信息，
样本 `SNOS-056`（按返回顺序）：

```
[0]  2026-01-28  SNOS-056-C.torrent            cnsub=true   ← 中文字幕
[1]  2026-09-01  SNOS-056-U.无码破解.torrent    cnsub=false  ← 最新的一条却在第 2 位
[2]  2026-01-25  SNOS-056-UC.无码破解.torrent   cnsub=false
[3]  2026-01-23  SNOS-056                      cnsub=false
...
[11] 2026-02-08  SNOS-056.[4K]@R90s            cnsub=false
[14] 2026-01-24  SNOS-056-中文字幕             cnsub=false  ← 名字带「中文字幕」但 flag=false
[17] 2026-01-24  [线上博彩…] SNOS-056 …        cnsub=false
```

档位与地图里 AutoSpider 的优先级（`UC无码破解 > UC > U无码破解 > U`）对得上，
但本服务**不解析 name**（CONTEXT.md：字幕判定只信 `cnsub` 字段）。

**顺序不可用任何单一字段重放**：`hd 优先` 只解释 218/300，`体积降序` 98/300，
`created_at` 50/300 —— 档位内还有一套看不到的键（很可能是做种数/热度）。

**对 ticket 04 的意义**：顺序是策展档位而非实时热度，佐证了「同会话 0 重排」
（此前 4 部×4 次也一致）；跨天/跨周仍未知。参考：若改成「cnsub→HD→体积→infohash」
这类确定性规则，也只与当前选择一致 **147/197（≈75%）** → 约 1/4 的作品会换磁链。

**另一处更正**：本端点今天返回的 `created_at` 是 `YYYY-MM-DD`（如 `2026-01-28`），
不是上面样本里的 `09/27/2026`。旧样本的格式存疑，`feed.parseCreatedAt`
两种都接受，不影响功能。

**这条证据促成的决定**：见 ticket 04 的 `## Answer` —— 改成「按 `created_at` 取最新、
cnsub 优先」并把每个作品 id 选中的磁链持久化钉住（新票 08/09）。

## 10.5 `/api/v4/movies/{id}` 详情

比列表形态丰富得多：`number_letter`、`summary`、`score`、`reviews_count`、
`maker_id/name`、`director_id/name`、`series_id/name`、`tags[]`、`actors[]`、
`relative_movies[]`、`actor_movies[]`、`play_sources[]`。

外层还有 `share_info`（形如 `"KV-328\nhttps://javdb.com/v/82J0Md"`）、`show_vip_banner`。

**注意 `score` 是字符串**（`"4.27"`），不是数字。

## 10.6 成本模型（ticket 09 的输入）

一次女优 feed 的请求数：

```
1 次 /api/v1/movies/tags（一页最多 50 部）
+ 每部有磁链的作品 1 次 /api/v1/movies/{id}/magnets
```

实测 EvkJ 第一页 50 部 → 约 **1 + N 次请求**（N 为 magnets_count>0 的条数）。
端到端实测耗时：

| 请求 | 耗时 |
|---|---|
| `/rss/code/KV-328.xml` | ~1.2s |
| `/rss/actress/EvkJ.xml`（第一页 50 部） | **~6.1s** |

**这就是 ticket 09 要面对的数字**：qBittorrent 每轮询一次就是 6 秒的上游压力，
而且并发多 feed 会叠加。缓存或后台刷新很可能有必要。
