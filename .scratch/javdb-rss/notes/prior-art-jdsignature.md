# 先例调研：JavDB App API、jdsignature 与公开实现确认

> 本文记录 JavDB 官方移动端私有 API 逆向先例与 `jdsignature` 算法调研结果。核心结论：**已有成熟、活跃、经验证的开源 Go 实现，可以完全省掉对 libapp.so 的静态逆向。**

---

## 1. 核心结论：能不能省掉静态逆向？

**能。理由如下：**

1. **公开实现已存在且完整可用**：开源项目 `FlanChanXwO/javdb-cli` 已经完整实现了 JavDB App JSON API 的客户端和 Go SDK，包含请求签名（`jdsignature`）、公共请求参数封装、实体与影片详情、女优收藏、磁力链接解析等全部链路。
2. **算法与常量已获双重印证且实测通过**：
   - 掘金公开文章《从 APK 到完整 API：逆向工程 JavDB 移动端全过程》（基于 1.9.35）详细记录了 Dart 汇编还原算法与常量解密过程；
   - `javdb-cli` 提供了预计算好的纯常量摘要算法，上级已用其实测打通 `https://jdforrepam.com` 的真实端点（`/api/v1/startup`、`/api/v1/movies/latest`、`/api/v1/actors/EvkJ` 均 200 返回真实数据）；
3. **架构建议**：Wayfinder 地图的 **ticket 02** 应直接从「静态分析 libapp.so 逆向算法」重写为「**验证并集成 / 裁剪 javdb-cli 的签名与请求实现**」。

---

## 2. 关键先例：javdb-cli 项目事实

| 属性 | 详情 |
|---|---|
| **项目地址** | [FlanChanXwO/javdb-cli](https://github.com/FlanChanXwO/javdb-cli) |
| **开源协议** | **MIT License**（完全兼容闭源/二次开发，可直接引入依赖） |
| **语言与版本**| Go 1.27.1 |
| **活跃度** | 58 stars, 最新版本 `v0.8.1`（2026-09 仍在持续维护更新） |
| **公开 SDK 路径**| `github.com/FlanChanXwO/javdb-cli/sdk`（`package javdb`） |
| **核心协议路径**| `internal/javdb/protocol/signature`、`internal/javdb/appapi` |

### 覆盖端点确认

| 需求端点 | javdb-cli 对应文件与能力 | 备注 |
|---|---|---|
| `/api/v1/sessions` | `internal/javdb/appapi/endpoint/auth/auth.go` (`Login`) | 提交 `username` / `password` 表单，获取 JWT Bearer Token |
| `/api/v1/users/collected_actors` | `internal/javdb/appapi/endpoint/user/user.go` (`Collected("actors")`) | 支持翻页参数 `page`，解析响应 `data["actors"]` |
| `/api/v1/actors/{id}` | `internal/javdb/appapi/endpoint/entity/entity.go` (`EntityDetail`) | 不带额外参数直接 GET；其作品列表通过 `/api/v1/movies/tags` 结合 `filter_by` 查询 |
| `/api/v1/movies/{id}/magnets` | `internal/javdb/appapi/endpoint/movie/movie.go` (`MovieMagnets`) | 返回磁力列表，无需登录态 |
| `/api/v2/search` | `internal/javdb/appapi/endpoint/search/search.go` (`Search`) | 支持 `q`, `page`, `limit`, `movie_type` (zone), `type` 等参数 |
| 磁力信息与字幕 | `internal/javdb/appapi/endpoint/magnets/magnets.go` | 字段包括：`hash`, `cnsub`（**中文字幕布尔标记**）, `hd`（高清）, `size`, `files_count` |

---

## 3. `jdsignature` 签名算法与参数规范

### (1) 签名规则

- **位置**：HTTP **请求头** `jdsignature`（**不是** URL Query 参数）。
- **格式**：
  ```
  jdsignature: {timestamp}.{suffix}.{md5(timestamp + prefix)}
  ```
  其中：
  - `timestamp`：当前 Unix 秒级时间戳（10 位整数，如 `1784134914`）
  - `suffix`：固定字符串 `"lpw6vgqzsp"`
  - `prefix`：固定 128 位十六进制字符串：
    `"71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e7d5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa"`
  - 拼接后取小写 32 位 MD5 hex。

### (2) 5 字符 Secret 来源与推导链路（静态原理备忘）

1. Native 库 `libsecurity.so` 的 `SecurityUtil.getSecret()` 读取 APK 证书 DER 十六进制（以 `30820...` 开头），截取前 5 位得到 `secret = "30820"`。
2. 对 `"30820"` 做 MD5 得到 32 字节密钥：`"da97c8240e2ad99a2d331eed95c411f5"`。
3. Dart 层读取硬编码在常量池中的两个 Base64 密文，按 `min(i, 31)` 逐字节减去密钥对应字符的 ASCII 码，再做一次 Base64 解码，分别还原出上述 `prefix`（128 字符 hex）和 `suffix`（`"lpw6vgqzsp"`）。
4. **结论**：因为密钥与密文均为静态硬编码，在 App 协议升级前 `prefix` 与 `suffix` 是纯常量，**无需运行时调用 JNI 或解析证书，直接内嵌常量即可**。

### (3) 服务端强制校验的公共 Query 参数

所有发往 `https://jdforrepam.com` 的请求必须附带以下公共参数（缺少任一均报 `ParameterInvalid`）：

```text
platform=android
app_channel=official
app_version=1.9.28 (或 1.9.35)
app_version_number=10928 (或 1.9.35 对应整数)
system_version=13
device_model=Pixel 6
device_name=Pixel
device_uuid=<UUID 字符串>
```

### (4) 版本差异与残留风险

- `javdb-cli` 声明逆向自 `JavDB.apk 1.9.28`。
- 本地目标 APK 为 `v1.9.35`（sha256: `3616335e465afe5f57376080ddd211ae61aad77e44ecf8aec9cb819deeea4a5e`）。
- **现状**：1.9.28 与 1.9.35 的签名常量完全一致，上级实测 1.9.28 的算法在当前服务端全通。
- **残留风险**：若服务端未来执行强升级、弃用旧常量池或轮换密钥，此套预计算常量可能失效，届时需重新从新版 APK 常量池解密更新 `prefix`。

---

## 4. 传输层观察与 TLS 指纹

- `javdb-cli` 内部使用了 `github.com/bogdanfinn/fhttp` 与 uTLS 做移动端 TLS 模拟。
- **实测验证**：上级使用最朴素的 Python `urllib`（标准 TLS 握手）直连 `https://jdforrepam.com` 同样顺利获得 200 JSON 响应。
- **初步判断**：`jdforrepam.com` 属于私有 API 节点，目前**未开启**类似网页端 `javdb.com` 的强 Cloudflare TLS 指纹拦截规则，Go 原生 `net/http` 大概率可直接工作（标记为待确认，建议在 ticket 02/03 中做一次标准 Go `http.Client` 请求验证）。

---

## 5. 参考来源

1. **FlanChanXwO/javdb-cli** (Go CLI & SDK)  
   https://github.com/FlanChanXwO/javdb-cli  
   - 签名源码: https://github.com/FlanChanXwO/javdb-cli/blob/main/internal/javdb/protocol/signature/sign.go
   - 磁力过滤源码: https://github.com/FlanChanXwO/javdb-cli/blob/main/internal/javdb/appapi/endpoint/magnets/magnets.go
2. **从 APK 到完整 API：逆向工程 JavDB 移动端全过程**（掘金，2026-03-15）  
   https://juejin.cn/post/7617004481947172874
3. **某XXXDB绕过vip权限验证，直接播放求助**（吾爱破解，2025-11-28）  
   https://www.52pojie.cn/thread-2076386-1-1.html
4. **JavDB 官方 Android APK 发布页**  
   https://github.com/bdvajstudio/javdb/releases
