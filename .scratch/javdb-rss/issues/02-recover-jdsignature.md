# 恢复 `jdsignature` 算法并在 Go 里复现

Type: task
Status: open
Blocked by: 01

> ## ⚠️ 已被 ticket 01 大幅降级（2026-09-28）
>
> **本 ticket 的核心工作已经完成，且验收标准已提前命中。** 保留此票仅为把
> 「采用还是拷贝」这个决策走完。重新评估时必须先读 ticket 01 的 Answer。
>
> 已知（ticket 01 实测）：算法是 3 行 Go，Prefix/Suffix 是硬编码常量，
> 已用 Python 复刻并**证明对 1.9.35 服务端有效**；`jdsignature` 是请求头。
>
> 因此本 ticket 剩下的问题从「复现算法」变成了：
> 1. **依赖方式**：直接 `import github.com/FlanChanXwO/javdb-cli/sdk`（MIT，省事，
>    但把上游当生产依赖），还是把 3 行算法**拷贝进本仓库**（零依赖，但要自己维护常量、
>    并接受将来失效时无人替我们发现）？推荐**拷贝** —— 算法太小、太关键，不值得引入依赖。
> 2. **失效检测**：如何在上游 App 升级导致 Prefix 失效时**尽早发现**？
>    需要一条定时探针（打 `/api/v1/startup`，断言 `success:1`），失败即告警。
> 3. **TLS 指纹**：javdb-cli 用了 utls 伪装；我们朴素 urllib 也通了。
>    长跑批量请求时是否需要，标为待复验。
> 4. **最新版 APK 复核**：若将来失效，可先看 javdb-cli 是否已跟进（它活跃，3 天前还在推）。

## Question

`jdsignature` 到底怎么算出来的，能不能在 Go 里稳定复现？

这是整张地图唯一的关键未知。到这一步已经知道：

- `libsecurity.so` 的 `SecurityUtil.getSecret()` 返回 **5 个字符**
  （正常路径 = APK 签名证书 `toCharsString()` 的 MD5 hex 前 5 位；异常路径 = 字面量 `"astarte"`）。
- 签名逻辑在 Dart 层：`package:astarte/net/intercept.dart` 里有
  `AuthInterceptor` / `TokenInterceptor` / `_signPrefix` / `_signSuffix` / `MD5Digest.` /
  `HMac.withDigest` / `getSignature`；相关参数名有
  `jdsignature`、`deviceId`、`timestamp`、`nonce`、`authorization`、`user-agent`、`app_version`。

要回答的：

1. `jdsignature` 的**输入集合**是什么？逐项确认：
   - 参与的是 URL path、query、body、还是它们的某种排序/拼接？
   - `timestamp` 的单位与格式？服务端允许的时钟漂移窗口有多大？
   - `deviceId` 从哪来、是什么形态、服务端是否校验它与 token 绑定？
   - `nonce` 是否参与、是否必须唯一？
   - `getSecret()` 的 5 字符是直接当 key，还是再派生？
2. 摘要算法是 MD5、SHA1、还是 HMAC？盐/前缀/后缀的确切拼法？
   （`_signPrefix` / `_signSuffix` 两个静态字段的初值应该能直接给出答案）
3. 大小写、编码、空参数、数组参数、中文参数的归一化规则。
4. **是否有 GET 与 POST 两套签法**，还是统一一套。
5. 是否有时间戳新鲜度导致的**不可重放**？即我们能否离线生成签名，还是必须每次实时算。

## 验收标准

在 Go 里实现后，这个请求要返回**正常业务 JSON**（而不是 `ParameterInvalid`）：

```
GET https://jdforrepam.com/api/v1/startup?<签名参数>
```

并且至少再验证两个端点，确认算法不是只对 `/startup` 生效。

## 路线

起点是 ticket 01 的结论。若静态逆向可行 → 走 libapp.so 常量池 + 函数体；
若不可行 → 动态路线（模拟器 + 打补丁 APK + mitmproxy 抓真实请求，反推拼接规则）。
两条路都把**证据落在 `.scratch/javdb-rss/notes/jdsignature.md`**，连同 Go 实现一起。

**注意**：签名算法是硬编码在 App 里的，App 更新可能改变它。实现里要把算法版本和
「服务端开始拒绝」的检测点留出来，不要写死成不可诊断的黑盒。

## 产出

- `notes/jdsignature.md`：算法说明 + 证据（反汇编片段 / 抓包样本 / 常量池 dump）
- Go 实现（放哪由 ticket 03 的骨架决定；在骨架就位前先做成独立可跑的验证程序）
- 至少 3 个端点的验证记录
