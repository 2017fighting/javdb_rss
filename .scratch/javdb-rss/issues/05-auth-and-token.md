# 鉴权打通：token 怎么放、怎么导出、失效怎么办

Type: task
Status: resolved

> **阻塞边已移除（2026-09-28）**：原 `Blocked by: 02` 是在等「签名能跑」。
> 签名已实测跑通（见 ticket 01），探接口不再依赖 02。
> 02 剩下的「依赖还是拷贝」是**纯工程决策**，不阻塞本票。
>
> ## 部分已解（2026-09-28，来自 ticket 01）
>
> 原 Q1/Q2/Q3 已有一手证据，**不需要重新试探**：
>
> - **存放位置**：`Authorization: Bearer <token>` 请求头（`client/transport.go:165`）。
> - **不参与签名**：`jdsignature` 只由 `ts` + 硬编码 Prefix/Suffix 算出，**与 token 无关**。
> - **发布形式**：token 是 JWT（服务端错误名为 `JWTVerificationError`）。
> - **失效特征**：错误 `action` 属于
>   `JWTVerificationError` / `Unauthorized` / `LoginRequired` / `TokenInvalid` / `TokenExpired`
>   之一 → 可直接据此在日志里给出「去重新导出 token」的提示。（实测无 token 时返回
>   `JWTVerificationError: Invalid Signature`。）
> - 公共参数里的 `device_uuid` 取值任意（实测用一个饼字串 UUID 即可）；它是否需与 token
>   同源**仍未确认**（Q3 的剩余部分）。
>
> 因此本 ticket 真正剩下的是：**怎么从 App 里把 token 导出**（Q4，HITL）+ 验证它真能用。

## Question

用户选了「手工从 App 导出 token」。那么具体是导出什么、放到哪、过期怎么发现？

要回答：

1. **token 到底是什么**：调 `/api/v1/sessions` 之后服务端给的什么字段？
   在这之前先从 `libapp.so` 的字符串里定位 `TokenInterceptor` / `AuthInterceptor` 用的
   存储键名和 header 名（已知有 `authorization`、`setToken`/`getToken`）。
2. **放哪**：header（`Authorization: Bearer ...`？自定义 header？）还是 query 参数？
   `jdsignature` 的计算是否**包含** token（若包含，顺序与拼法要一并确定）。
3. **配套字段**：`deviceId` 是不是必须与 token 同源？`user-agent` / `app_version` 服务端是否校验？
   换一个 `deviceId` 用同一个 token 会不会被拒？
4. **怎么导出**（HITL，需要用户动手）：给用户一份**准确到可照抄**的步骤。
   优先选不需要 root 的路径 —— 例如 App 自己的本地存储（`sqflite` / `hive` / `shared_preferences`），
   在需要时再用 Android 备份或 adb 取。步骤要包含「导出后怎么验证它是活的」。
5. **失效表现**：token 过期 / 被顶下线时，接口返回什么？（HTTP 码 + 错误信封）
   服务端要怎么识别并在日志里给出**可操作的**提示，而不是静默返回空 feed。
6. **单账号多设备**：登录会不会踢掉 App 上的会话？

## 产出

- `notes/auth.md`：header 名、payload 形态、deviceId 处理、失效特征
- 给用户的导出步骤（可直接执行的清单）
- 至少一个用真实 token 打通的端点记录


## Answer（2026-09-30）—— **实现完成，但票不能关**

### 一个改变设计的发现：单会话账号

用户实测报告：**同一账号只能在一个地方登录，新登录会挤掉之前那个。**

这条决定了两件事：

1. `javdb-rss login` 必须在**动手之前**就把「你手机上的 App 会被踢下线」说出来 ——
   不可事后补救，也不该藏在文档里。
2. **「密码写进 .env 便于自动续期」与「手机还能用 App」是冲突的** ——
   每次自动续期都会踢掉手机。

因此自动续期被做成**由凭据是否存在来控制**的开关，而不是默认行为，
且每次触发都打 WARN 明说后果。

### 另一个发现：整条「从 App 挖 token」的路都可以不做

用户最初选「手工从 App 导出」。核实后发现那条路很贵：

- App 同时用 `shared_preferences` / `hive` / `sqflite` 三种存储
  （**没有** `flutter_secure_storage`），token 在哪个**未确定**
- Dart 层有 `getDecryptString` → 存储值**可能加密**
- 精确定位需要 blutter 做快照分析（另一张票的工作量）
- 先例项目从没做过

而 `POST /api/v1/sessions` 是一个**已实测确认**的普通表单登录：
字段 `username`+`password`，**无验证码、无设备绑定**，返回 `data.token`。

代价从「一轮逆向」降到「输一次账号密码」。用户据此改选了 CLI 登录。

### 交付

| 项 | 位置 |
|---|---|
| 契约与决策记录 | `notes/auth.md` |
| 登录实现 | `internal/appapi/login.go`（含 POST 表单支持） |
| token 来源与优先级 | `internal/config`：`JAVDB_TOKEN` > 文件 |
| 安全写入 | `config.SaveToken`：0600 + 临时文件 + rename |
| 登录命令 | `cmd/javdb-rss/login.go`（提示 / 写入 / **验证**） |
| 自动续期 | `appapiSource.withRelogin`（默认关闭） |

### 三个刻意的设计点

1. **登录后验证，不只是「登录成功」。** 只用 Login 成功判定不够 ——
   那只证明上游给了一串字符，不证明它在后续请求里被承认。
   命令会拿新 token 去打 `/collected`，失败就明确报「token 已写入但验证失败」。
2. **自动续期只对凭据类错误触发。** 网络抖动、上游 5xx、参数写错都不登录 ——
   那会白白踢掉用户手机，且对真问题毫无帮助。
3. **并发下只登一次。** 单会话账号下，多个请求各登各的会**互相挤掉刚拿到的 token**。
   实测：去掉「拿到锁后再确认」这层保护，12 个并发请求会登 12 次。
   已加测试钉住，并验证过「去掉保护该测试会失败」。

### 验证

真实（对本地假上游）跑通四条路径：

| 路径 | 结果 |
|---|---|
| 正常登录 | ✅ 警告先行 → 登录 → 写 0600 文件 → 验证读到 2 位收藏女优 |
| 密码错误 | ✅ 明确报错（含对上游拼写 `IncorrentUsernameOrPassword` 的说明），**不创建文件** |
| 已设 `JAVDB_TOKEN` | ✅ 提示「不会写文件」，**不创建文件** |
| `-force` | ✅ 照常写入 |

新增 15 条测试，`gofmt` / `vet` / `test -race` 全绿。

### ✅ 真实验证（2026-09-30，用户实跑）

用户跑通 `javdb-rss login`，输出：

```
✓ 登录成功
✓ 已写入 /root/clone/javdb_rss/token.json（权限 0600）
✓ 验证通过：读到 144 位收藏女优
    D2EdJ  花守夏歩
    Mm5v4  渚あいり
    …
```

**这同时验证了三件此前只是「据契约推测」的事：**

1. **`/collected` 的响应形状确认。** `{actors:[{id,name,videos_count}]}` 与预期一致，
   连续翻页读到 144 条（跨页去重也正常）。此前它只来自先例项目的实现与夹具。
2. **`lang: zh-CN` + 读 `name` 的决策确认。** 名字是中文（`花守夏歩`），
   证明「不要读 name_zht、靠 accept-language 控制语言」是对的。
3. **`device_uuid` 不必与 token 同源** —— 我们用的是配置里那个常量 UUID，通过了。

### ⭐ 新发现：token 没有 `exp`，自动续期不该开

解出真实 token 的 JWT：

```
段数: 3    算法: HS256
payload 的键: ['id', 'username']     ← 没有 exp / iat / nbf
长度: 115 字符
```

**token 不会按时间过期**，只在会话被挤掉时失效。

这推翻了「自动续期是为过期准备的」这个直觉 —— 实际上它会**和用户的手机打拉锯战**：

```
打开 App → 服务的 token 失效 → 服务自动重登 → 踢掉手机 → 再打开 App → …
```

因此建议改为**不要开**（不设 `JAVDB_PASSWORD`）。已写进 README、
`config.example.yaml` 与 `notes/auth.md`。

### ⭐ 第二个发现：144 位收藏，远多于预估

`/collected` 返回 **144** 位。这有两个后果：

1. **反证了「聚合 feed」方案必须否掉。** 我在 ticket 10 里按 20 位估算
   「≈360 次请求 / 轮询」，实际是 **144 × ~18 ≈ 2600 次** —— 差 7 倍。
   否掉它的决定现在有了更硬的数字支撑。
2. **「只做发现」的可用性变差。** 144 个 id 手工挑是可行的（`/collected`
   每条都带现成 feed 路径，可 `jq -r '.actresses[].feed'` 批量取出），
   但「从 144 个里挑出你真正要追的十几个」这件事本身没有工具支持。
   **这是 ticket 10 决策的一个未预见后果**，记在这里供后续判断。

### 仍存在的已知限制

- `maxCollectedPages = 20`：收藏超过 ~400 位时会被静默截断。
  144 位离它还很远，但值得知道。
- `/collected` 的**分页页大小未测**（只知道翻页工作正常）。

### 原「仍未完成」节（已由上面的实跑满足）


票的产出第 3 项是「**至少一个用真实 token 打通的端点记录**」。

它需要你的真实账号密码，我无法代做。**请跑一次**：

```bash
cd /root/clone/javdb_rss
JAVDB_USERNAME=<你的用户名> JAVDB_PASSWORD=<你的密码> ./javdb-rss login -config config.yaml
```

（或直接 `./javdb-rss login -config config.yaml` 交互输入。）

跑完请把输出贴回来。我要确认两件事：

1. **`/collected` 的响应形状**是否真的如契约所料（`{actors:[{id,name,videos_count}]}`）——
   目前它来自先例项目的实现与夹具，**未在 1.9.35 上复验**
2. **JWT 里有没有 `exp`** —— 它决定 token 多久过期，进而决定自动续期有没有必要

跑通之后本票就能关，需求 4 也才真的算可用。
