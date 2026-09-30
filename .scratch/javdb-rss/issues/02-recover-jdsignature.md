# 恢复 `jdsignature` 算法并在 Go 里复现

Type: task
Status: resolved
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

## Answer

**完成。算法已拷贝进本项目并实测有效，失效探针已按 k8s 用法做成 API。**

### 1. 依赖方式：拷贝，不依赖（用户拍板）

实测过依赖 javdb-cli SDK 的代价：**37 个模块**、二进制从 ~11MB 涨到 **17.25MB**，
且它的返回类型是 `map[string]any` —— 恰恰在最需要类型安全的那层边界上退化。

因此：把 3 行算法与两个常量拷进 `internal/appapi/signature.go`（**零新依赖**），
把 javdb-cli 当**参考实现**读，自己用类型安全的代码写番号解析（归 ticket 06）。

归属与改动记在 `THIRD_PARTY_NOTICES.md`（MIT 要求保留声明）。

### 2. 实现

```go
// internal/appapi/signature.go
signPrefix = "71cf27bb…e3d8aa"
signSuffix = "lpw6vgqzsp"
// Sign(ts) → "{ts}.{suffix}.{md5(ts + prefix)}"
```

测试里的黄金向量由 **Python 标准库独立算出**，不是从本实现反向导出的 ——
否则测试只能证明「代码没变」，不能证明「协议对」。
还钉住了三条容易搞错的性质：签名是时间戳的纯函数（不绑定 token/请求内容/设备）、
摘要必须是 32 位小写 MD5 hex、8 个公共参数一个都不能少。

### 3. 失效探针：做成 API 给 k8s 打（用户拍板）

用户的原话是「做成 api 吧，我用 k8s 来打」，因此没有做成内部定时器日志，
而是暴露了三个端点，**对应三种不同的处置**：

| 端点 | 用途 | 上游坏时 |
|---|---|---|
| `/healthz` | 存活。只答「进程还在吗」 | **仍 200** |
| `/readyz` | 就绪 | 503 |
| `/healthz/upstream` | 机读详情 | 503 + JSON |

**关键设计**：`/healthz` 刻意**不**掺上游状态。签名失效重启一千次也没用，
把上游状态掺进 liveness 只会制造重启循环。

后台探针的实现也考虑了运维细节：

- 启动时**立即**检查一次，不等一个间隔 —— 否则滚动发布的头一个 interval 里
  `/readyz` 一直处于「尚未检查过」
- 「尚未检查过」与「检查过且失败」是两种状态：前者返回 200
  （没证据说它坏，且返回 503 会让启动莫名失败）
- 日志只在状态**翻转**时打，不每 15 分钟刷屏
- 探针关闭（`probe_interval: 0`）是真正的空操作，不会先偷偷打一发
- 检查时**重读配置**，因此 host / lang / device_uuid 能热重载
- 刻意**不带 token**：`/startup` 是匿名端点，带上只会让「token 过期」污染
  「签名是否有效」这个信号

### 4. ⭐ 探针抓到了一个真 bug —— 而且正是它存在的意义

写完先做了一次**故意把 prefix 改坏**的实测。结果：

```
signature_broken: false        ← 告警在最该响的时候是哑的
```

原因有两层叠加：

**第一，签名有状态码完全不同的两种失败形态**（实测确认）：

| 情形 | HTTP | `action` |
|---|---|---|
| 签名值为空/缺失 | **200** | `ParameterInvalid` |
| 签名值**无效** | **400** | `InvalidSignature` |

而真正的「Prefix 变了」走的是**第二种**，我只处理了第一种。

**第二，`GetJSON` 在 `status >= 400` 时提前返回了字符串错误**，
`action` 根本没到达分类器 —— 错误文本里埋着 JSON，但已经退化成不可靠的字符串匹配。

修法：**不再按状态码短路，先把信封解出来拿 `action` 再做判断**。
这个 API 在 4xx 时仍然返回标准信封，所以这是安全的。
顺带把 `APIError` 加上 `Status` 字段供诊断，并把分类改成按 `action` 而不按文本。

已写进 `notes/api-recon.md` §3，并加了针对两种形态的测试——
包括「非信封 4xx 不能被误报成签名问题」（误报的代价是有人半夜被叫起来改代码，
而其实只需要重试）。

### 5. 另一处被测试抓到的自伤

修失败路径时把成功路径弄坏了：我用 `env.Action != ""` 判断「是不是信封」，
但**成功响应里 `action` 就是 `null`**，于是每条成功响应都被判成解析失败。
判据应当是「`success` 字段存在」。测试直接报了出来。

还有一处**注释在撒谎**：`health.Run` 的注释写「间隔每轮重读，所以热重载能生效」，
但实现是每轮**结束后**才重读 —— 从 1 小时改成 10 秒要等满 1 小时。
没有把它工程化掉（为了「立即生效」而把唤醒粒度封顶，
意味着配 1 小时也要每几十秒醒一次，不值得），而是**如实写清楚生效时机**，
并把测试改成验证真实行为。

### 6. 实测验证（最终代码）

| | 正常签名 | 签名失效（故意改坏 prefix） |
|---|---|---|
| `/healthz` | 200 | **200** ← 已验证 liveness 不受污染 |
| `/readyz` | 200 | 503 |
| `/healthz/upstream` | `ok:true` 503ms | `signature_broken:true`, `action:InvalidSignature` |
| feed 描述 | 正常 | `⚠️ 上游不可用，本 feed 已停更。原因：…無效的簽名` |
| 日志 | 静默 | ERROR，只在翻转时 |

签名打到的是**真实**的 `https://jdforrepam.com/api/v1/startup`，不是 mock。

### 7. 残留风险（未解决，但已可观测）

- **Prefix 失效仍需要人改代码**。本票做的是「尽早发现」，不是「自动修复」。
  已有一份 Dart 逆向备灾清单（`notes/dart-toolchain-probe.md`），
  但第一反应应当先看 javdb-cli 是否已跟进。
- **TLS 指纹未复验**：javdb-cli 用 utls 伪装，我们用朴素 `net/http` 也通了。
  长跑批量请求时是否需要，仍未验证。

### 补跑的 code-review（2026-09-30）

Spec 轴找出两条**真缺陷**，另有一条基础设施失败（Standards 车道超时，未完成）。

1. ⭐ **CronJob 告警被自己的 readiness 探针挡住。** 签名一失效 `/readyz` 返 503 →
   Pod 被摘出 Service Endpoint → CronJob 通过 Service 域名连不上 →
   `set -e` 当场退出 → **根本走不到 `signature_broken` 判断**。
   也就是说最能说明「该去修代码」的那条告警，在它唯一该响的时候是哑的。
   已修：拆出第二个 Service（`javdb-rss-health`，`publishNotReadyAddresses: true`）
   专给健康检查用，CronJob 改走它。（ticket 04 的 Spec 轴独立发现了同一件事。）
2. **feed 描述的降级告警是不可达死代码。** 上游真坏时 `src.Code` 先返回错误、
   直接 502，`renderFeed` 根本不被调用。它当初「测试通过」只因为
   httpapi 测试用的是**永远成功的 stub 数据源**。已删除该逻辑，
   并把那条假通过的测试换成钉住真实行为的（502 而非降级 feed）。
   上游坏时的可见性由 502 + `/readyz` 503 + CronJob 告警三者共同保证。
3. **冷启动时会打「上游检查恢复正常」** —— 首次检查并未「恢复」任何东西，
   且违反了「正常签名保持静默」。已改为首次成功静默、首次失败才报。
4. **`provider=stub` 时仍启动外网探针** —— 而 stub 模式的启动日志明说
   「不会访问任何网络」。已改为 stub 不启动探针。
5. **`upstreamChecker` 带了无关的 `MagnetConcurrency`** —— 探针只打 `/startup`。
   已删（它会让读的人以为探针会拉磁链）。
   ⚠️ **这条第一次报「已删」是假的**：那次 `edit` 是原子的，其中一个 hunk 失配
   导致整批没应用，而我只补做了另一处。工具报过失败，我没注意到。
   补跑 Standards 车道时发现代码里还在，现已真删。
   （教训：`edit`/`str.replace` 之后必须回读确认，我在同一轮里已经因此栽过多次。）


### 补跑 Standards 车道（2026-09-30，上次超时失败）

5 条发现，全部已修：

1. **`deploy/k8s.yaml` 里 `magnet_concurrency: 8` 写了两遍**（YAML 重复键，
   后者覆盖前者 —— 不报错但显然是错的）。已删一条。
2. **`upstreamChecker` 的 `MagnetConcurrency` 其实没删掉**（见上）。
   已在装配处回读确认。
3. ⭐ **首次检查失败时日志缺了处置动作，而之后永远不会再报。**
   这是我为了让冷启动「成功保持静默」而引入的回归：首次那一支被简化成
   只记 action/err/latency，而后续轮次因 `prev.OK == st.OK == false` 不再触发。
   于是「服务一启动时签名就已失效」（Prefix 在你重启前刚失效）会得到
   **唯一一条不带任何指引的日志**。已抽出 `logUpstreamFailure` 让两种失败
   说同样的话，并加测试钉住（已验证「改回简化版该测试会失败」）。
4. **`health` 包硬编码了 App API 特定的 action 名与备灾 issue 路径**，
   而它自称「不依赖任何内部包」。**未修** —— 让 `Checker` 返回带诊断动作的
   富上下文是更大的重构，收益不如现在直白。记为已知取舍。
5. **`signature.go` 的注释说失效症状是 `ParameterInvalid`** —— 实测
   Prefix 失效表现为 **HTTP 400 + `InvalidSignature`**。这会把查日志的人
   引向错误的排查方向。已更正。
