# 主动推送模式：本次不做，但形状已定

**状态**：⛔ 本次不做（2026-10-04，用户在 grilling 中决定）。
**但**：决定之前已经把该问的都问完了，形状与 API 契约都在下面 ——
将来重开这件事时**不要从零再来一遍**。

## 要做的两件事（当初的需求）

1. 新增「我想看的」接口 → 生成一个 feed。
2. 配了 qbt endpoint（账号密码 optional，qbt 可对某些 IP 免鉴权）就换成**主动推送**：
   轮询「想看」，某番号有磁力就推给 qbt，然后把该番号在 App 里改成「我看过的」。

**第 1 件已交付**（`GET /rss/want.xml`，见 README）。第 2 件不做。

## 已定的形状（用户逐条回答过，别再问一遍）

| 问题 | 决定 |
|---|---|
| 推送循环住哪儿 | **进程内常驻 goroutine**（照 `health.Run` 的形状），周期由配置给、SIGHUP 可改；不引入 cron / 外部端点 |
| 「已推送但写回失败」的真相在哪儿 | **App 列表即队列** —— 「想看」里还留着 = 还没成功；写回成功即出队。不引入第二份持久状态（pin 之外） |
| 推送成功后番号变成什么 | **标成 App 的「看过」**（用户明确要求，尽管这让「看过」同时表示「机器已交给 qbt」） |
| qbt 连不上时 | **拒绝启动（fail fast）**：配了 endpoint 却连不上就报错退出。注意这与 pin 的处理一致，但把一个可选外部依赖变成了硬依赖 |
| 推送时给 qbt 带什么元数据 | 未定（用户在问到这里时改了主意）—— `category` / tag / savepath / 是否 paused 都还没拍 |

**顺序必须是：先推 qbt，成功后再写回 App。** 反过来会丢件（写回成功但推送失败 = 这部
片永远不会再被推）。写回失败时下一轮重推是**无害**的 —— qbt 按 infohash 天然去重
（本次验收实测：同一条磁链重复添加不会产生第二个种子）。

## API 契约（读的已实测；写的来自先例，未实测）

读「想看」/「看过」：

```
GET /api/v2/users/review_movies?status=want_watch|watched&page=N
→ data.movies[]（movieSlim：id/number/title/release_date/has_cnsub/magnets_count）
   每页 10 条，空页 = 到底
```

✅ 2026-10-04 用真 token 实测过（见 `internal/appapi/want.go`）。

写「看过」（**本仓库未实测**，契约来自先例
[`FlanChanXwO/javdb-cli`](https://github.com/FlanChanXwO/javdb-cli)
的 `internal/javdb/appapi/endpoint/user/user.go`）：

```
POST /api/v1/movies/{id}/reviews     表单：status=watched|want_watch&score=<int>&content=<string>
DELETE /api/v1/movies/{id}/reviews/{reviewId}      取消标记
```

⚠️ 三个坑：① 写的是 **movie id**（不是番号）；② `reviewId` 是大整数，用
`float64` 解出来再格式化会变成科学计数法（先例为此专门写了 `reviewID()`）；
③ 这三个写操作都会改动用户的真实账号状态 —— 第一次跑必须在真实账号上小心验证。

## qBittorrent 侧实测事实（2026-10-04，v5.2.4，Docker）

真实验收 `/rss/want.xml` 时顺带刻下来的，推送模式会用到：

- **`POST /api/v2/rss/addFeed`** —— 5.x 把 4.x 的 `rss/add` **改名**了，
  用旧名字会得到 `404 Endpoint does not exist`。
- WebUI 的两个「拦路」校验（都会表现为 401）：
  ① **Host 头的端口必须与 WebUI 监听端口一致**（端口映射后 `Host: <host>:<映射端口>`
     会被拒，日志里写 `Invalid Host header, port mismatch`）；
  ② POST 需要 `Referer` 与 Host 同源。
  配置项 `WebUI\AuthSubnetWhitelist` + `AuthSubnetWhitelistEnabled` 可对某些网段免鉴权
  （用户提到的「qbt 可以 bypass 某些 ip」），但**必须在 qbt 停止时改 conf** ——
  它启动时会重写配置文件，热改会被丢掉。
- **每 feed 只保留 50 篇文章**（`RSS\MaxArticlesPerFeed` 默认 50）。
  对本次的实测数据（222 条条目）意味着 **qbt 只看得到其中 50 条**；
  要用 RSS 方式消化整张清单，得在 qbt 设置里调大这个值。
- **qbt 拿不到 channel description。** 它的 `GET /api/v2/rss/items` 响应里
  feed 对象的键是 `articles / hasError / isLoading / lastBuildDate / title / uid / url`
  —— **没有 description**。因此任何要写给 qbt 看的信号只能放**标题**
  （这也是 `/rss/want.xml` 把「待磁链」计数写进标题的原因）。
- `POST /api/v2/torrents/add`（`urls=<magnet>&stopped=true`）接受本服务生成的磁链：
  `success_count: 1`，infohash 与 feed 的 guid 逐位一致。

## 成本（本次实测，供将来的推送循环估算）

234 部「想看」+ 上游当前约 0.8–0.9s/请求 ⇒ 一次完整渲染约 **20 秒**
（24 页清单 + 222 次磁链请求，磁链并发受 `app_api.magnet_concurrency` 控制，默认 8）。
推送循环每轮都要付这个代价 —— 这是它值得单独想清楚的原因之一。
