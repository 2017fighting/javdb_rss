# 08 — 「钉住」磁链的存储与失效

**What to build:** ticket 04 的决策要求服务端**持久化**「每个作品 id 已选中的 infohash」（pin），只有出现新 cnsub 才切换。这是本服务引入的**第一个持久状态**，直接抵触地图里已定的「无状态：服务端不持久化已下发」。本票把这个新状态设计清楚，让「钉住」在裸二进制、容器、k8s 三种部署下都有明确行为。

设计上，pin 天然落在既有的 `catalog.Source` 边界上 —— 与 `dedupe` 同层，做成一个装饰器；`Select` 保持为纯函数（ticket 04 的排序规则）。本票只定「状态存哪、怎么活」。

**Type:** grilling（交付是存储与失效的设计决定）

**Blocked by:** None — can start immediately.

**Status:** resolved（2026-09-30，grilling with user；实现见下）

- [x] **介质与位置**：单个 JSON 文件 + 原子替换；位置由新的 `app_api.pin_file` 决定，**留空时默认配置文件同目录的 `pin.json`**（与 `token_file` 留空时的规则完全一致）。
- [x] **重启语义**：丢 pin 就是丢 guid。上游无法重建出同一选择（04 已实测：上游顺序不是时间序、不可用任何字段重放），因此重启后按纯函数重选会让约 1/4 作品换磁链 → qBittorrent 重下。pin 因此**故意不可关闭**、不可丢弃。
- [x] **多副本 / 并发**：`flock`（`LOCK_EX|LOCK_NB`）强制单写者，第二个指向同一文件的实例**拒绝启动**。三套部署当前已是单副本（k8s `replicas: 1` + `Recreate`），本票把隐含约束变成强制且失败可见。
- [x] **增长与淘汰**：**不设 TTL、不设上限**，只把条数记进日志。淘汰任何一条都会让它退回纯函数重选（== 本票要消除的抖动）。一条约 150 字节；一个女优约 230 部，万级也就 1.5MB。
- [x] **键的选择**：上游 **movie id**，一张全局表。番号不唯一（合集/同名），用它做键会让两部不同作品互相钉死。需要给 `catalog.Work` 补一个 `ID` 字段（此前 `appapi` 把线格式的 `id` 丢掉了）。
- [x] **跨 feed 共享**：全局单表天然满足。已写进测试（同一个 `Work.ID` 经两条路由得到同一个 pin，因而同一个 guid）。
- [x] **写失败**：**fail fast**。启动时不可写/损坏/版本不认识 → 拒绝启动；运行中写失败 → 记 ERROR 并让进程退出（交由 systemd/k8s/compose 重启），**不静默降级、不带一半内存表继续服务**。
- [x] **与 k8s 清单的一致性**：四处都补可写状态目录（见下）。k8s 不再依赖只读根文件系统上的空白处。

## Answer（2026-09-30，grilling with user）

**决策：pin 是「上游 movie id → 一条磁链记录」的全局表，落在单个 JSON 文件里，原子替换；`flock` 强制单写者；任何读写失败都 fail fast；每请求合并写一次。**

### 介质与位置

- **介质：单个 JSON 文件 + 原子替换。** 复用 `config.SaveToken` 已验证过的模式：`pin.json.tmp` → `chmod 0600` → `rename`。零新依赖、人可读、可用文本工具直接看。内容是一张 `movieID -> record` 表，启动时全量读进内存，写时整体重写。候选过的行式日志（需处理半截行 + 压缩）与 bbolt/SQLite（依赖）都被排除：访问模式只是几千条小键值的点查与全量遍历。

- **位置：新增 `app_api.pin_file`，留空默认配置文件同目录的 `pin.json`。** 与 `Holder.TokenPath()` 同一套规则（「配置在哪儿，它的状态就在哪儿」）。裸二进制开箱即用；容器/k8s/systemd 显式指向可写卷。三份示例配置同步加键，由 `internal/config/examples_sync_test.go` 强制一致。

```json
{
  "version": 1,
  "pins": {
    "aBc123": {
      "infohash": "0e8f4789...",
      "name": "KV-328",
      "size_mb": 3110,
      "cnsub": false,
      "created_at": "09/27/2026",
      "pinned_at": "2026-09-30T12:00:00Z"
    }
  }
}
```

**为什么记录里带磁链快照**：pin 指向上游消失时，[09](09-pin-switch-semantics.md) 可能需要「继续返回它（死链）」。只存 infohash 的话那条路走不通（没有 name/size 就渲染不出 item），到时要么改 schema 要么只能重选 —— 两者都是二次伤害。快照约 150 字节/条，万级 1.5MB，代价可忽略。

### 重启语义：丢 pin 就是丢 guid

上游**无法**重建出同一选择（04 实测：顺序是策展档位、不可用任何字段重放）。因此：

- 丢 pin → 退回纯函数重选 → 约 1/4 作品的 guid 变化 → qBittorrent 各多下一份。
- 失败方式是**不可见的**，正是本 effort 开票的原因。

据此，pin **故意不可关闭**：`pin_file` 留空是「用默认路径」，不是「禁用钉住」。升级会要求状态目录可写（旧部署若配置目录只读会启动失败 —— 这是有意的，见「写失败」）。

### 单写者

`flock` 加在 pin 文件旁（`pin.json.lock`），`LOCK_EX|LOCK_NB`。第二个实例指向同一文件时**拒绝启动**并打清楚原因。三套部署当前已是单副本，本票把隐含约束变成强制。

- 已知限制：NFS 上 `flock` 不可靠（k8s PVC 用 `ReadWriteOnce` 单挂载，不跨节点，已够）。`pin_file` 落在本地文件系统上是推荐做法。

### 增长与淘汰

**不设 TTL、不设上限。** 只有条数可观测：启动时 INFO 一条（路径 + 条数），每次落盘 INFO 一条（新增/切换各多少、总计、耗时）。淘汰任何一条 = 把它退回纯函数重选 = 制造抖动，与 pin 的存在理由矛盾；而一条 150 字节，量级根本不构成压力。

### 键与跨 feed 共享

键是 **`catalog.Work.ID`**（上游 movie id）——番号不唯一。一张全局表，任何路由命中的同一部作品都命中同一个 pin，因此同一内容在两个 feed 里是同一个 guid，跨 feed 去重不被破坏。这条由测试钉住。

### 写失败：fail fast

| 时刻 | 情况 | 行为 |
|---|---|---|
| 启动 | 文件不存在 | 正常启动（空表，INFO 报 0 条） |
| 启动 | 损坏 / `version` 不认识 | **拒绝启动**，告诉运维修或删 —— 不能用空表静默盖掉用户状态 |
| 启动 | 目录不可写 | **拒绝启动**（配置错误，不静默退化） |
| 运行 | 写失败（磁盘满 / 只读） | ERROR 日志 + 进程退出，交由编排器重启 —— 可见 |

依据仍是项目原则：**宁可看见错误，不要静默降级**。写失败时若继续用内存表服务，重启后的行为差异无人能解释；退出则 `kubectl get pod` 立刻是 `CrashLoopBackOff`。

### 每请求合并写一次

一次 feed 渲染内先在内存完成所有 pin 变更，渲染结束**只落盘一次**；无变更则零写入（热路径轮询全部命中 → 不碰磁盘）。首次渲染一个 50 部女优 feed = 1 次写，而不是 50 次。

### 与 ticket 09 的边界（实际实现里怎么切）

08 交**存储 + 装饰器骨架**，09 只换策略：

```
internal/pin/
  store.go     格式 / 原子写 / flock / fail-fast / 负载与写入   <- 08
  source.go    catalog.Source 装饰器                            <- 08
  policy.go    Policy 接口 + **临时**实现（= 今天的 catalog.Select，标 TODO(09)） <- 08
```

装饰器把 `Work.Magnets` **改写为单元素**（那条被钉住的）：`feed.Build` 与 `catalog.Select` 一行不改，`magnets[0]` 就是被钉住的那条。纯函数（04 的 `created_at` 规则）与有状态策略（09）都留在 `Policy` 这个接缝后面，互不污染。

**04 的纯函数规则本身留给 09 实现** —— 09 要定「什么算新的 cnsub」，正好需要那个纯函数；08 只留接口与临时实现。

**装配顺序是接线的正确性，不是风格**：`dedupe.New(pin.New(client))` —— dedupe 在外、pin 在内。`dedupe` 把**同一个切片**返回给共享同一次上游调用的所有调用者，而 pin 会原地改写 `works[i].Magnets`；反过来装（`pin.New(dedupe.New(...))`）会让多个 goroutine 并发改同一个切片 —— 实测会被 `-race` 抓到（见 `cmd/javdb-rss` 的 `TestBuildSourceHandlesConcurrentIdenticalRequests`）。

### SIGHUP 重读 pin（超出本票字面的决定，已确认）

`Store.Reload()` 挂在 SIGHUP 上：重载配置时顺便重读 pin 文件。重读前会**先把待落盘的变更刷盘**（否则与重读撞上的那次选择会被覆盖掉，而那些 pin 还没写盘 → 永久丢失）。失败时保留旧表。

它不属于「存哪、怎么活」的字面要求，但是「失效设计」的一部分：运维手工删掉一条 pin（「这个作品想重选」）是个合理操作，不重读就只能重启进程。

### 部署落点（四处都补可写状态目录）

| 目标 | 改动 |
|---|---|
| 裸二进制 | `pin_file: ""` → 配置文件同目录 `pin.json`，开箱即用 |
| systemd | `StateDirectory=javdb-rss`（→ `/var/lib/javdb-rss`，`ProtectSystem=strict` 下 systemd 自动将其列为可写，无需 `ReadWritePaths`）；配置里显式写 `pin_file: "/var/lib/javdb-rss/pin.json"` |
| docker compose | `./state:/state` 卷；`pin_file: "/state/pin.json"` |
| k8s | 新增 `state` PVC（RWO）+ `/state` 挂载，保留 `readOnlyRootFilesystem: true`；`pin_file: "/state/pin.json"` |

三处注释都写明：**丢失 pin 会让 guid 变化、qBittorrent 重复下载**，因此该卷需要持久化；普通 `emptyDir` 在 Pod 重建时会丢（本票选了 PVC，不选 emptyDir）。

### 已知代价

- 升级后**必须**让状态目录可写，否则启动失败（刻意的：默认关闭 pin 会静默退回有抖动的行为）。
- `flock` 在 NFS 上不可靠；文档里写明推荐本地文件系统。
- 快照字段（name/size/cnsub/created_at）与线格式耦合，字段改名时要迁移 —— 有 `version` 字段兜底。

## 与 ticket 09 的边界

本票定**存哪、怎么活**；「什么时候切换 pin」由 [09](09-pin-switch-semantics.md) 定。两者无天然先后，可并行。
