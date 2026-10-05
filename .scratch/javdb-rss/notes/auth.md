# 鉴权：token 从哪来、放哪、失效怎么办

本服务访问 App 私有 API 时的身份部分。**每条断言都在 2026-09-30 实测或由契约推导，
未确认的都单独标注。**

## ⚠️ 最要紧的一条：单会话账号

**用户实测报告：同一账号只能在一个地方登录，新登录会挤掉之前那个。**
**2026-10-01 复勘确认了它的时序：「挤掉」是立即的，没有可观测的宽限期**
（见文末「复勘记录」）。

这决定了两件事：

1. **本服务登录 = 用户手机上的 App 被踢下线。** 不可事后补救，因此
   `javdb-rss login` 在**动手之前**就把这句打出来。
2. **自动续期不是免费的。** 每次续期同样会踢掉手机。因此它被做成
   「由凭据是否存在来控制」的开关，而不是默认行为（见下）。

## token 从哪来

| 来源 | 优先级 | 场景 |
|---|---|---|
| 环境变量 `JAVDB_TOKEN` | **高** | 正式部署（k8s Secret / docker `env_file`）。容器里不方便挂可写文件 |
| `app_api.token_file`（默认 `token.json`） | 低 | 本地开发；由 `javdb-rss login` 写入**或由从手机导出后写进去**（见文末「从 App 里挖 token」） |

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
- **`device_uuid` 不必与 token 同源，也不与它绑定。** 实测（2026-10-01）：
  同一个 token 配**三个不同的** `device_uuid`（默认值、另一个固定值、随机值）
  各打一次需要凭据的端点，**全部受理**。因此换 `device_uuid` **不会**让已发的
  token 失效 —— 它不是「会话绑定到设备」那种指纹。
  它是否被服务端用于**统计或风控**（那是内部状态，从外部不可观测）仍然未知，
  代价见文末。

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

## 从 App 里挖 token：做了什么、当初为什么没做、现在怎么做（2026-10-05）

用户最初选的是「手工从 App 导出 token」。核实后那条路被**搁置**，理由当时写得是：

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

### 上面的判断对了一半 —— 结论对，原因错了

结论（「这条路贵」）在当时是对的，但 2026-10-05 把存储位置**查实**后发现，
最后那条理由指错了方向：**不是「要 blutter 逆 Dart 快照」，而是密文的密钥在
Android KeyStore 里 —— 逆什么都不能离线解出来。** 当时若真去上 blutter，会白花那张票。

实测确认的三件事：

| 事实 | 证据 |
|---|---|
| token 落地在 `shared_prefs/FlutterSharedPreferences.xml` 的 `flutter.accessToken` | 真机（Xiaomi 13 / Android 15 / 私密空间 **user 10**）数据目录；可用 `scripts/export-phone-token.sh` 同源的思路自查（`grep -a accessToken shared_prefs/*.xml`） |
| 它是密文，176 base64 字符 / **131 字节**，**不是 16 的整数倍** | 同上（所以不是 AES-CBC/ECB，与 GCM 或流模式一致） |
| 密钥在硬件密钥库，**不可导出** | `classes.dex` 里有 `AndroidKeyStore` / `KeyGenParameterSpec$Builder` / `AES/GCM/NoPadding` / `KeyStore$SecretKeyEntry`；设备侧 `shared_prefs/native.xml` 有 `KEY_ALIAS` / `TOKEN_ALIAS`（两个随机 16 字符串 = **别名**，不是密钥） |

旁证两条（都是观察，不是证明）：

1. token 明文**实测 115 字符**，密文 131 字节 —— `131 = 115 + 16`，正好是 GCM 的 tag 长度。
2. 我把两个别名、`accessKey`、`device_uuid` 及其 md5/sha256 派生 × CTR/CFB/OFB/CBC/ECB ×
   4 种 IV 全试了一遍，`accessToken` 与 `urlDomain` 都**零命中**。也就是说密钥**不是设备上
   任何一个可见字符串的简单派生** —— 与「密钥在 TEE 里」一致。

> ⚠️ 这个发现**不要**顺手推广到 `urlDomain` / `backup_domains_data` 那两个 368 字节的
> 域名 blob：`assets/.../data/data.txt` 是打进 APK 的，必须在任何设备上都能解，
> 所以它的密钥一定是设备无关的静态密钥，不可能是 per-device 的 KeyStore 密钥。
> 那两个 blob 的现状是「已知固定 IV（`242c494134750deac809ebb2e259a658`）、已知分组
> 模式（`16 字节 IV || 352 字节密文`）、明文必为域名 JSON、只剩密钥派生待定」，
> 下一步仍是逆 `libsecurity.so` 的 `_Z8decryptPcS_i` / `_Z11ScheduleKeyPhS_ii`
> —— 对域名 blob **仍然有效**。（取证环境与两个 blob 的对照在本地 harness 的
> `~/android-capture/FINDINGS.md` §8，不在本仓库里。该 harness 未开源，见下文“为什么不把
> harness 也开源”。）

### 那怎么拿 token：读**运行时内存**，不读文件

「读文件」这条路彻底出界（离线解密在密码学上就不可能），但明文**一定**会在进程内存里
出现一次 —— App 得把它放进 `authorization: Bearer <token>` 请求头。所以做法是
在它活着的地方取：

```bash
./scripts/export-phone-token.sh --launch --user 10     # ~16s
```

脚本**在仓库里**（`scripts/export-phone-token.sh`），不依赖 `~/android-capture` 那套
harness —— 任何人有 root 手机 + adb 就能跑。

它做的事：`/proc/<pid>/maps` 逐个可读区 → `dd bs=4096 if=/proc/<pid>/mem` →
一条**有界的** HS256 JWT 模式。四个刻意的选择：

0. **放仓库里，不放 harness 里。** 第一版是 `~/android-capture/45-scan-token.sh`
   （已删）。那个目录是私人机器上的，而且里面有 34 MB 的第三方 APK 与真实账号抓包；
   把它当作「复跑做法」是**不合格**的，读者拿不到。工具搬进 `scripts/`，只有一处副本。

1. **不需要 Frida。** 第一版是 Frida（内存扫描），能跑；但 `maps` + `dd` + `grep -a`
   在 root 设备上同样够用，而它去掉了三个活动部件：往设备推 frida-server、客户端与
   服务端版本对齐、用 uv 钉住客户端。纯 shell 也让它在「不想在手机上留一个 59 MB
   插桩服务」时仍然可用。
2. **模式必须有界**（`eyJ…{10,200}…{43}`）：无界会吃进后面恰好也像 base64 的字节，
   而有界既卡死边界（HS256 的签名段恒为 43 字符），又保证不会匹配本 App 里另外那些
   base64（话题列表、域名 blob）—— 它们**不含 `.`**，三段式根本对不上。
3. **不用交替分支。** 同一件事写成 `A|B|C` 会让 toybox 的 grep 退化约 10 倍
   （实测 6m09s vs 16s，因为在多 GB 的行上反复回溯）。

判据不是「搜到了一个 `eyJ`」，而是**三段式 + 43 字符签名 + payload 解开是
`{"id":…,"username":…}`**（脚本会把 payload 解出来并打印，让你能直接对）。
实测命中且仅命中一条：

```
header  eyJhbGciOiJIUzI1NiJ9
payload {"id":<数字>,"username":"<数字>"}   <- 与设备侧 flutter.userName 一致（具体值不必写进仓库）
```

**它不踢手机**：读的是 App 已经在用的那个会话，服务与手机从此共用同一串 token。
对比之下 `javdb-rss login` 会挤掉手机上的会话（本文开头那条）。代价因此从
「一轮逆向」变成了「一条命令」。

### 什么时候仍然该用 `javdb-rss login`

- **token 已经被顶掉、且你不想/不能碰手机**（导出这条路要求 App 处于登录状态、
  且刚发过带凭据的请求）；
- **这个账号纯给本服务用**、手机上不再登录它 —— 那自动续期（`JAVDB_PASSWORD`）
  才开始有意义；
- 手机没 root（这条路要求能读 `/proc/<pid>/mem`，那是 root-only）。

### 复跑

运行时侧（仓库内，任何人可跑，前置只有 root 手机 + adb）：

```bash
./scripts/export-phone-token.sh --serial 4b0350c4 --launch --user 10
```

文件侧（可选，只为确认「盘上确实只有密文」）：把数据目录弄到本机后 `grep -a accessToken`，
或者用本地 harness 的 `~/android-capture/40-grab-token.sh`（它会在 `/data/user/*/<pkg>`
里找 JWT，找不到时会把这层原因打出来）。

不写盘、不碰网络。

### 为什么不把 harness 也开源

上面有些取证是在 `~/android-capture` 那套 harness（模拟器 + mitmproxy + Frida）里做的。
它**不入库**，三个理由里前两个是仓库自己已经表过态的：

1. **里面有 34 MB 的第三方 APK。** `.gitignore` 第 1–12 行已经写明这类文件不入库
   （「第三方发行物，提交进 git 会永久留在历史里」），而且安装脚本是从官方 release
   下载 + sha256 校验的 —— 别人不需要我们转发。
2. **里面有 56 MB 的 `frida-server` 二进制**，同理：上游自己发布。
3. **`logs/` 里是真实账号的抓包**（45 处 `Bearer`、推荐位、观看记录）。即使那些 token
   早已失效，把一个人的浏览行为发布出去是另一类问题，不是本项目应该做的。

结论：**harness 不开源**，但「可能被复跑的东西」尽量搬进仓库 —— 目前是
`scripts/export-phone-token.sh`，而它的结论、证据与失败记录（这才是 harness 真正的价值）
已经全在本文与 `docs/adr` 里。

## ✅ 已实测（2026-09-30）：`/collected` 的每页条数与上限

用真实 token 逐页拉完 `/api/v1/users/collected_actors`：

```
page 1..14 -> 每页 10 条
page 15    -> 4 条（最后一页不满）
page 16    -> 0 条（空页 = 到底）

每页固定 10 条（与实际参数无关），总收藏 = 144 位
```

结论：`maxCollectedPages = 20` = **约 200 位**上限，是当前 144 位的约 **1.4 倍**
（多出 56 位、5 页的余量）—— 余量并不宽裕，收藏再涨 56 位就会触顶。

**触顶不再静默（ticket 02 followup 已关闭）。** 曾经的隐患是：收藏超过上限时，
`/collected` 会照常返回一份不完整的列表，用户无法分辨「我就收藏了这么多」
与「服务只读到了这么多」—— 那是本服务唯一会静默少给数据的地方。
现在：前 20 页都非空时会再探一页，那页非空才说明「确实还有更多」——
此时实现返回 `catalog.Collection{Truncated: true}`，`/collected` 在响应里带上
`truncated: true` + `pages_fetched` + `max_pages` 三个字段（仅在触顶时出现），
并记一条 WARN 日志。那次探针把信号精确到「还有更多」而不是「踩到了上限」，
因此一个恰好收藏了 200 位的用户不会看到假警告。详见 README。

（`token 的寿命` 已实测，见上文 —— 它**不在**未验证列表里。）

**复核（2026-10-01，ticket 03）：与本页记录完全一致。** 用
`contractprobe collected` 从两个方向各测一遍：

- **上游原始分页**：`page 1..14` 每页 10 条、`page 15` 4 条、`page 16` 0 条（到底），
  总数 144 —— 与上面那次逐字相同。
- **服务自己的路径**（`appapi.CollectedActresses`）：读到 **144** 位、
  翻页 **16** 次、上限 20、`truncated: false`。

两条都印证了「每页固定 10 条」「空页 = 到底」「当前余量约 1.4 倍」。
之所以给这条开一个可复跑的探针（而不是记一句「已实测」），是因为这个端点是
本服务**唯一会少给数据**的地方 —— 它的契约值得能原地重测。

## 复勘记录（2026-10-01，ticket 03）

本文先前有一个「未验证的部分」小节，列了两条断言。它们现在都有结论 ——
结论已在正文，方法与证据在这里。

工具：`cmd/contractprobe session`（本仓库内，可复跑）。它复用 `internal/appapi`
的传输层与签名，因此它发的请求与服务发的**逐字节同类**。

> ⚠️ 这个实验**会登录两次**，因此会把你手机 App 上的会话挤下线 ——
> 与 `javdb-rss login` 的副作用一样。探针刻意**不写**你的 token 文件：
> 探针不应改动被观测系统的状态。

### 1. `device_uuid` 是否被用作设备指纹 —— 结论：**不是**（就授权判定而言）

做法：登录一次拿到 token1，然后**同一个 token** 配三个不同的 `device_uuid`
各打一次 `/api/v1/users`：

```
✓ token1 + 原 device_uuid       受理
✓ token1 + 另一个固定 uuid       受理
✓ token1 + 随机 uuid             受理
```

三个都过。因此：

- **换 `device_uuid` 不会让已发的 token 失效** —— 这条是可操作的结论，
  也是这个问题在本服务里唯一会咬人的一面（若会失效，`device_uuid` 就必须
  被当作不可变标识来管）。
- 三个值全过（而不是「换一个也能用」）说明它不是白名单式的校验：
  随机 UUID 同样被接受。

**仍然未知的部分**：服务端是否在**内部**按 `device_uuid` 做统计或风控。
那是服务端的内部状态，从外部**不可观测** —— 不是「还没测」，是测不了。
**代价**：如果上游某天开始按设备做限流，我们会先看到失败（可见），
因此可以接受（依据仍是那条原则：宁可看见错误，不要静默降级）。
**缓解**：配置里本来就允许固定 `device_uuid`（`app_api.device_uuid`），
让一台实例长期保持同一身份。

### 2. 单会话「挤掉」的语义 —— 结论：**立即失效，没有可观测的宽限期**

做法（第一版做错过，见下）：让第二次登录**在后台进行**，同时在前台**持续**
用 token1 打 `/api/v1/users`，每次记录请求的**发出**与**返回**时刻。

一次实测（第二次登录用**不同**的 `device_uuid`）：

```
token1 的请求窗口              结果
0s→325ms                  ✓ 受理
426ms→750ms               ✗ 被拒（JWTVerificationError: 請登錄帳號）

第二次登录返回于 479ms（相对实验起点）
```

失效时刻 `T` 只能被夹在一个区间里，两个边界都按「服务端什么时候做判断」取：

- 下界：最后一次成功的请求在 [发出, 返回] 之间被判为有效
  ⇒ `T > 0ms`（严格大于**发出**时刻）。
- 上界：第一次失败的请求在 [发出, 返回] 之间被判为无效
  ⇒ `T ≤ 750ms`（小于等于**返回**时刻）。

因此 **`T ∈ (0ms, 750ms]`**，而第二次登录在 **479ms** 返回 —— 落在区间内。

**结论：失效发生在第二次登录返回后至多 `750ms − 479ms = 271ms` 之内。**
这个上界已经小到没有运维含义（重试策略按秒计），因此实践结论是
**没有宽限期**。但严格说，我们**没有证明它是 0** —— 只证明了它不大于 271ms；
要把上界压得更小，只能缩小单次请求的往返（区间宽度就是它）。

> 这里刻意不写「立即失效」：夹逼只给出上界，而区间宽度正是我们自己的请求往返。
> 把「能证明的」与「不能证明的」分开写，比给一个好听但没有依据的结论有用。

> **第一版错在哪**（值得留下）：它把「第二次登录返回后我们第一次去看」的时刻
> 当成了失效时刻，于是打印出「存在 239ms 的宽限期」。问题有两个：
> 一是第二次登录的往返时间（~500ms）被算进了宽限期；
> 二是上界取了请求的**发出**时刻，而服务端完全可能在我们发出之后才处理它 ——
> 那会把「与登录完成同时」误报成「早于登录完成」。
> 现在两个边界分别取「最后一次成功的发出」与「第一次失败的返回」，
> 并且只有当登录返回时刻**落在区间之外**时才敢下方向性结论。

**对部署的含义**（与本文开头「拉锯战」一节一致）：服务端一旦发现 token 失效
就必须**停止重试并报错**，而不是立刻重登。旧的实现曾经在凭据类错误上自动重登，
那会在用户打开 App 时与服务抢同一个会话，把用户反复踢下线。

### 复跑方式

```bash
set -a; . ~/.config/javdb-rss/recon.env; set +a   # JAVDB_USERNAME / JAVDB_PASSWORD

go run ./cmd/contractprobe -wait 2m session   # device_uuid + 单会话时序（会登录两次）
go run ./cmd/contractprobe collected          # /collected 分页契约（会登录一次）
```

两个命令都会把你手机上的 App 会话挤下线（与 `javdb-rss login` 一样），
因此都**不写**你的 token 文件 —— 探针不该改动被观测系统的状态。
若部署需要继续用服务，跑完之后重跑一次 `javdb-rss login` 即可。
