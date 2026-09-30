# 鉴权：token 从哪来、放哪、失效怎么办

本服务访问 App 私有 API 时的身份部分。**每条断言都在 2026-09-30 实测或由契约推导，
未确认的都单独标注。**

## ⚠️ 最要紧的一条：单会话账号

**用户实测报告：同一账号只能在一个地方登录，新登录会挤掉之前那个。**

这决定了两件事：

1. **本服务登录 = 用户手机上的 App 被踢下线。** 不可事后补救，因此
   `javdb-rss login` 在**动手之前**就把这句打出来。
2. **自动续期不是免费的。** 每次续期同样会踢掉手机。因此它被做成
   「由凭据是否存在来控制」的开关，而不是默认行为（见下）。

## token 从哪来

| 来源 | 优先级 | 场景 |
|---|---|---|
| 环境变量 `JAVDB_TOKEN` | **高** | 正式部署（k8s Secret / docker `env_file`）。容器里不方便挂可写文件 |
| `app_api.token_file`（默认 `token.json`） | 低 | 本地开发；由 `javdb-rss login` 写入 |

环境变量优先是刻意的：它是更明确的那一个。**空白的值不算「设置了」** ——
否则一个空的 env（compose 里写了但没填）会把文件里本来可用的 token 顶掉。

### 拿 token 的命令

```bash
javdb-rss login -config config.yaml
```

它做四件事：拿凭据 → 登录 → 写 token 文件（0600）→ **验证 token 真的能用**。

凭据取自 `JAVDB_USERNAME` / `JAVDB_PASSWORD`（可脚本化），没设就交互式询问
（密码不回显）。

验证这一步不是装饰：只用「登录成功」判定是不够的 —— 那只证明上游给了我们一串字符，
不证明它在后续请求里被承认。而这里失败的话，用户会在几天后才发现
「怎么 `/collected` 一直 503」。

## 登录契约

```
POST /api/v1/sessions
content-type: application/x-www-form-urlencoded
body: username=<用户名>&password=<密码>
→ {"success":1,"action":null,"data":{"token":"eyJ..."}}
```

实测确认的三点：

1. **字段是 `username`，不是 `email`。** 传 `email` 会得到
   `ParameterInvalid: 參數不能爲空: username`。
   （先例项目的文档在两处写了不同的名字，照抄会踩。）
2. **响应字段名历史上变过**，因此 `token` 与 `access_token` 都读。
3. **没有验证码、没有设备绑定。** 实测：任意 username/password 都会被走完流程，
   错误密码得到的是业务错误 `IncorrentUsernameOrPassword`（上游自己的拼写），
   而不是任何形式的挑战。

## token 放在哪

`Authorization: Bearer <token>` 请求头。

实测确认的三点：

- **它不参与签名。** `jdsignature` 只由 `ts` + 硬编码 Prefix/Suffix 算出，
  与 token 无关。同一个签名配不同 token 会被正常受理。
- **匿名可用。** 缺 token 时需求 1/2/3 完全正常（`/startup`、`/movies/latest`、
  `/actors/{id}`、`/movies/{id}/magnets` 都不需要它）。
  只有 `/users/collected_*` 这类账号维度端点需要。
- **`device_uuid` 不必与 token 同源。** 实测用任意 UUID 都能通过。
  它是不是被用来做设备指纹，**未确认**。

## 失效表现

错误信封里的 `action` 属于下面任一，即为凭据问题：

```
JWTVerificationError   Unauthorized   LoginRequired   TokenInvalid   TokenExpired
```

实测无 token 时得到 `JWTVerificationError: Invalid Signature`。

服务端对它的处置（`internal/httpapi`）：

- `GET /collected` → **503** + 文案说明要去重新导出 token，而不是 200 + 空列表
  （空列表会被理解成「你没收藏任何人」）
- 日志里单独成一类，与「上游故障」分开 —— 前者重试无用

### token 寿命 —— 已实测：**不会自己过期**

用真实 token 解出的 JWT（2026-09-30）：

```
段数: 3     算法: HS256
payload 的键: ['id', 'username']      ← 没有 exp、没有 iat、没有 nbf
长度: 115 字符
```

**没有 `exp` 声明。** 也就是说这个 token 不按时间过期 ——
它只在**会话被挤掉**时失效（单会话账号，别处登录）。

这条推翻了「自动续期是为过期准备的」这个直觉。它的真实含义见下。

## 自动续期 —— ⚠️ 建议**不要开**

**默认关闭。** 只有同时设了 `JAVDB_USERNAME` 与 `JAVDB_PASSWORD` 才启用
（只设一个不算 —— 那会拿空密码去打上游）。

### 为什么不建议开：它会和你的手机打拉锯战

token **不会自己过期**（见上）。所以它失效的唯一原因就是
「你在别处登录了」—— 而你最常「在别处登录」的地方就是**你自己的手机**。

于是开了自动续期的实际效果是：

```
你打开 App           → 服务的 token 失效
服务检测到并自动重登  → 把你手机踢下线
你再次打开 App       → 服务的 token 又失效
服务再次自动重登      → 又把你踢下线
…无限循环
```

这不是「偶尔踢一次」的代价，而是**你和它抢同一个会话**。

### 那什么时候才该开

只有当这个账号**纯粹给本服务用**、你手机上不再登录它时，自动续期才有意义
（或者你愿意接受手机端不可用）。

**推荐做法：不设 `JAVDB_PASSWORD`。** token 不会过期，所以正常情况下
一次登录就够用；真的被挤掉了，重跑 `javdb-rss login` 即可 ——
那时你知道自己在做什么。

启用后：遇到**凭据类错误**时自动重登一次并重试。

三个刻意的约束：

1. **只对凭据类错误触发。** 网络抖动、上游 5xx、参数写错都不会触发登录 ——
   那会白白踢掉用户手机。
2. **每次触发都打 WARN**，明说「你手机上的 App 会话会被挤下线」。
   这件事用户没有别的途径能发现（App 只会突然要求重新登录）。
3. **并发下只登一次。** 多个请求同时发现 token 失效时，若各登各的会
   **互相把对方刚拿到的 token 挤掉**（单会话），结果是永远在登录。
   实测：去掉这层保护，12 个并发请求会登 12 次。

启用它的取舍很清楚：

```
不设 JAVDB_PASSWORD  -> 永不自动登录，手机安全；token 失效时只能人工重登
设了 JAVDB_PASSWORD  -> 自动续期，但每次续期都会踢掉手机
```

## 为什么没有做「从 App 里挖 token」

用户最初选的是「手工从 App 导出 token」。核实后发现那条路比想象中贵：

- App 同时用了 `shared_preferences` / `hive` / `sqflite` 三种存储
  （**没有** `flutter_secure_storage`），token 存在哪一个**未确定**
- Dart 层有 `getDecryptString`，说明存储值**可能是加密的**
- 精确定位需要上 blutter 做 Dart 快照分析 —— 那是另一张票的工作量
- 先例项目从没做过这件事（它只做 CLI 登录）

而 CLI 登录是一条已实测确认的表单 POST。因此改用登录，代价从
「一轮逆向」降到「输一次账号密码」。

> `javdb-rss login` **就是**这个流程的向导。没有再写一个 bash wizard：
> 唯一需要人做的动作是「输密码」，而二进制自己会提示、写入、验证 ——
> 外面再套一层脚本只是仪式。

## 未验证的部分

- **token 的寿命（JWT `exp`）** —— 需要真 token 才能读
- **`device_uuid` 是否被用作设备指纹** —— 实测换任意值都能用，但长期行为未知
- **单会话的具体语义** —— 「挤掉」是立即失效还是宽限期，未测
