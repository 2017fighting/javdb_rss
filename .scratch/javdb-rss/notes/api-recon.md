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

- `/root/clone/JAVDB_AutoSpider` — **走 javdb.com 网页**。已有：磁链提取、按优先级分类（`字幕 / hacked(UC无码破解>UC>U无码破解>U) / no_subtitle`）、演员订阅 + 新作 diff（ADR-054，`javdb/pipeline/subscription_monitor.py`）、FastAPI、qBittorrent 上传。**没有 RSS 输出**，订阅列表存在它自己的库里而不是读 JavDB 账号。它的磁链/字幕分类逻辑可作语义参考，但代码不能直接复用（我们走 App API + Go）。
- `/root/clone/javdb_crawler` — scrapy 骨架，只有 movie_pages / movie_detail 两个 spider，无可复用资产。

## 9. 结论

RSS 服务本身、磁链解析、qBittorrent 对接都不难。**整条路只有 `jdsignature` 一个未知数。**排除它之后，剩下的都是常规工程。
