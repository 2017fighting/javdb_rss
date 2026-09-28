# 先例调研：JavDB App API 与 jdsignature 是否已有公开实现

Type: research
Status: resolved

## Question

在动手逆向 `libapp.so` 之前，先确认有没有人已经公开做过这件事。

具体要回答：

1. **有没有公开的 JavDB 移动端 API 客户端 / SDK / 逆向笔记？**
   （关键词方向：`jdsignature`、`jdforrepam`、`astarte_app`、`SecurityUtil.getSecret`、
   `xxx.pornhub.fuck`、javdb app api、javdb 私服接口）
2. **有没有现成的 Flutter AOT（Dart snapshot）逆向工具链可用**，能在**当前环境**跑起来？
   重点关注能否拿到 libapp.so 的常量池与函数体。需要明确每个工具的：
   前置依赖（Dart SDK 版本匹配、CMake、Capstone）、是否能装、装完能不能吃下 v1.9.35 这个 so。
   - 备选线索：matching Dart SDK version、`--obfuscate` 未启用这一点对工具选择的影响
   - 也看看有没有**不需要完整工具链**的取巧路径（例如 Dart 常量池里 `_signPrefix` / `_signSuffix`
     邻近字符串可以直接定位）
3. **`jdforrepam.com` 这个域名/IP 有没有公开的指纹记录**（证书、whois、同源域名），
   能佐证它是不是 JavDB 官方的 API 主机。
4. **JAVDB_AutoSpider 之外的同类项目**是否已经有人做过「App API → RSS」这件事。

## 为什么这个必须先做

如果第 1 项有答案，ticket 02 的工作量可能从「一个 session 的静态逆向」塌缩成「照抄算法并验证」。
如果第 2 项没有可用工具，ticket 02 的路线要从「静态分析 libapp.so」改成
「动态 hook / 打补丁 APK + mitmproxy 抓真实请求」，那是完全不同的 ticket。

## 产出

一份 Markdown，写在 `.scratch/javdb-rss/notes/` 下，每条结论带来源链接。
结论要**明确回答**：「能不能省掉静态逆向」。若不能，列出**在本环境实际可装可用**的工具，
以及每个工具的失败模式。

## Answer

**结论：能省掉静态逆向。整张地图的关键路径消失。**

### 找到的完整先例

[`github.com/FlanChanXwO/javdb-cli`](https://github.com/FlanChanXwO/javdb-cli)
—— *"Unofficial JavDB App API CLI, public Go SDK, and agent-ready automation skill."*

| 项 | 值 |
|---|---|
| 语言 | **Go**（`go 1.27.1`）—— 与本 effort 选定栈一致 |
| License | **MIT** |
| 活跃度 | 58 stars；创建 2026-07-18；最后推送 **2026-09-24**（距本 session 3 天） |
| 版本 | v0.8.1，tag 齐全（v0.1.0 → v0.7.0+） |
| SDK 导入路径 | `github.com/FlanChanXwO/javdb-cli/sdk`（`package javdb`） |
| 内部结构 | `internal/javdb/appapi/endpoint/{auth,user,entity,magnets,movie,search,browse,lists,rankings,route}`、`internal/javdb/protocol/{signature,httpx}` |

### 签名算法（原样，`internal/javdb/protocol/signature/sign.go`）

```go
// Reverse-engineered from JavDB.apk 1.9.28; golden vector verified 2026-07-16.
//	jdsignature = "{ts}.{suffix}.{md5(ts + prefix)}"
const (
    Prefix = "71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e7d5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa"
    Suffix = "lpw6vgqzsp"
)
func Sign(ts int64) string {
    if ts <= 0 { ts = time.Now().Unix() }
    sum := md5.Sum([]byte(fmt.Sprintf("%d%s", ts, Prefix)))
    return fmt.Sprintf("%d.%s.%s", ts, Suffix, hex.EncodeToString(sum[:]))
}
```

注释说明 Prefix 是「由 access key `30820` + App 内 `CONST_PREFIX`/`CONST_SUFFIX` 预算得出」。

**关键修正**：`jdsignature` 是 **HTTP 请求头**，不是查询参数。
（`client/transport.go:161` → `h.Set("jdsignature", signature.Sign(ts))`。）
本 session 早期用 query 试探，得到的 `ParameterInvalid` 是错因，不是签名不对。

### 实测验证（本 session 亲自做的，这是本 ticket 最硬的证据）

用 Python 复刻上面 3 行算法，直接打 **我们目标版本的服务端**：

```
GET https://jdforrepam.com/api/v1/startup
  header: jdsignature = "<ts>.lpw6vgqzsp.<md5(ts+Prefix)>"
→ {"success":0,"action":"ParameterInvalid","message":"Parameter cannot be empty: platform"}
```

签名**通过**（错误从「jdsignature 为空」变成「platform 为空」）。补上公共参数后：

| 端点 | 结果 |
|---|---|
| `/api/v1/startup` | ✅ `success:1`，返回 `splash_ad` / `user` / `backup_domains_data`（即 assets 里那个加密域名的同族密文） |
| `/api/v1/movies/latest` | ✅ `success:1`，真实作品数组 |
| `/api/v1/actors/EvkJ` | ✅ `success:1`，真实女优详情 + `filter_tags` + 229 部计数 |
| `/api/v1/users/collected_actors` | ⚠️ `JWTVerificationError: Invalid Signature` — **签名已过，只差 token** |

**必带的公共参数**（缺一即 `ParameterInvalid`）：
`app_channel=official`、`app_version`、`app_version_number`、`platform=android`、
`system_version`、`device_model`、`device_name`、`device_uuid`。
`accept-language` 会切换服务端错误文案（zh-TW ↔ en）。

### 版本差风险（本方案唯一残留风险）

javdb-cli 逆的是 **1.9.28**，我们手上是 **1.9.35**。本次实测通过，说明服务端至今未使该
Prefix 失效。但 Prefix 源自 App 内的 access key，**App 升级或服务端轮换会使其失效**。
若失效，症状是 `ParameterInvalid` 重新出现 —— 届时才需要走 libapp.so 静态逆向
（工具链前置条件见 `notes/dart-toolchain-probe.md` 的备灾清单）。

### 次要发现

- javdb-cli 使用 `bogdanfinn/tls-client` + `utls` 做 TLS 指纹伪装；
  但本 session 用最朴素的 Python `urllib` **同样成功** → 初步判断 TLS 指纹**非必需**（标注为待确认，
  长跑批量请求时值得复验）。
- `HostMirror = https://jdforrepam.com`、`HostMain = https://javdb.com` —— 与侦察一致。
- `/startup` 返回的 `backup_domains_data` 与 APK 里 `assets/.../data.txt` 同族
  （前缀 `JCxJQTR1DerICeuy4lmmW` 相同），说明那是**动态域名下发**的密文；
  javdb-cli 已在 `endpoint/route/decrypt.go` 完整复刻其解密链路
  （`key/iv = getDecryptString(input, 常量)` → MD5 还原 → 逐字节相减 → Base64 → AES-CBC → PKCS7）。

### 产出

- 本答案 + `notes/prior-art-jdsignature.md`（researcher 车道记录）
- `notes/dart-toolchain-probe.md`（delegate 车道的环境矩阵，转为**备灾记录**）

### 对地图的影响

- **ticket 02 的验收标准已提前命中** —— 见 ticket 02 的新答案区。
- ticket 06 最贵的未知（中文字幕如何判定）已被本 session 顺带解掉：
  电影级 `has_cnsub`，**磁链级 `cnsub`**。

