# 09 — pin 的切换语义

**What to build:** ticket 04 把选择改成「按 `created_at` 取最新、cnsub 优先」，并持久化钉住，**只有「新增了 cnsub」才切换**。本票把这句精确定义到可写测试：什么算新 cnsub、pin 指向上游消失怎么办、同日多条怎么定序、首次选定怎么选。

**Type:** grilling（交付是切换规则的精确决定）

**Blocked by:** None — can start immediately.（与 [08](08-pin-store.md) 存储票无先后）

**Status:** resolved（2026-09-30，grilling with user；实现见下）

- [x] **「新增了 cnsub」的精确定义**：
  - pin 不是 cnsub、现在有 cnsub → 切换到「最新的 cnsub」？（这是主要动机）—— **是，日期无关**
  - pin 是 cnsub A、上游出现更新的 cnsub B → 切换到 B？—— **是**
  - pin 是 cnsub A、上游出现更旧的 cnsub C → 不切？（`created_at` 日粒度）—— **是，不切**
- [x] **pin 指向上游消失时**（被删/失效）：继续返回它（可能给出死链）还是回退重选？两者的可见性各是什么？—— **继续返回快照 + WARN 日志**（见 `## Answer`）
- [x] **同日定序**：`created_at` 相同（同日）的多条 cnsub / 多条候选，用 infohash 字典序？还是别的稳定键？—— **infohash 字典序（升序）；且同日不触发切换**
- [x] **首次选定**（无 pin）：纯函数规则 = 最新的 cnsub，否则最新的候选 —— 与 ticket 04 一致，要能被单测直接构造 —— **落在 `catalog.Select`，有表驱动单测**
- [x] **与初次交付地图 ticket 09「不引入缓存」的关系**：pin 已经是有状态，是否顺便把候选列表也缓存下来（会改写成本模型）？还是严格只存一个 infohash？—— **严格只存快照，不缓存候选列表**
- [x] **切换要可见**：pin 变更时是否记日志/暴露计数，好让「切换」本身可被观察（否则又回到不可见）—— **逐条 INFO + 落盘的「切换 N」计数**

## 为什么单列一票

`Select` 的排序规则是纯函数、好测；但「什么时候把 pin 从旧值换成新值」是**有状态的策略**，边界比排序多（消失、同日、旧 cnsub 回潮）。混在一起写会让纯函数测试被状态问题污染。

## Answer（2026-09-30，grilling with user）

**决策：`Desired` 是 ticket 04 的纯函数；`KeepOrSwitch` 只在出现 cnsub 时才可能换 pin。**

| 情形 | 行为 |
|---|---|
| 无 pin | 采用 `desired`（首次选定） |
| pin 非 cnsub，`desired` 是 cnsub | **切**（日期无关：cnsub 优先） |
| pin 非 cnsub，`desired` 非 cnsub | 不切（普通候选变化永不动 guid） |
| pin 是 cnsub，`desired` 是 cnsub 且 `created_at` 严格更新 | **切** |
| pin 是 cnsub，`desired` 同日 / 更旧 / 非 cnsub | 不切 |
| pin 指向上游消失 | 继续返回快照（可能死链）+ WARN |

### 1. 「新增了 cnsub」= 当前候选里有 cnsub，且它相对 pin 是新的

- pin 非 cnsub + 任一 cnsub → 切到 `desired`（最新 cnsub）。这是 ticket 04 的主要动机：字幕优先，日期无关。
- pin cnsub A + 更新的 cnsub B（`created_at` 严格更大）→ 切到 B。
- pin cnsub A + 更旧的 cnsub C → 不切。注意 `desired` 本身就是「最新 cnsub」，所以 C 更旧时 `desired` 仍是 A（若 A 还在），切换判断自然为否。

### 2. pin 指向上游消失：继续返回快照

`KeepOrSwitch` **不**检查 pin 是否仍在 `cands` 里，所以快照会被继续下发（记录里带 name/size/cnsub/created_at 就是为了这条路能渲染出 item）。

- 可见性：每次请求一条 **WARN**（movie id、infohash、当前候选数）。
- 「消失」包括上游把这部作品的候选**全部删掉**（候选数为 0）：此时仍返回快照并 WARN，否则作品会静默地从 feed 里消失。
- 为什么不是回退重选：回退会改 guid → qBittorrent 重下，正是 pin 要消除的抖动。稳定的死链至少是可见的（下载会失败/停住），而静默换 guid 不会。
- 代价：pin 永久消失时会**每次轮询**打一条 WARN，直到运维手工删 pin 或上游恢复。这是有意的可见性代价。

### 3. 同日定序：infohash 字典序，且同日不切换

`created_at` 相同（含都为空/都无法解析）→ **infohash 字典序升序**。这个 tie-break 只用于 `Desired` 的确定性（首次选定、以及“最新 cnsub 是谁”）；**同日不触发切换**，否则上游一次同日重排就能让 guid 抖。

### 4. 首次选定：纯函数 `catalog.Select`

有 cnsub → 取 `created_at` 最新的 cnsub；否则 → 取 `created_at` 最新的候选；同日用 infohash 定序。**不读取切片顺序。** 直接表驱动单测（`internal/catalog/select_test.go`）。日期解析与比较集中在 `catalog.ParseCreatedAt` / `catalog.CompareCreatedAt`（`feed` 复用同一份）。

### 5. 不缓存候选列表

pin 仍只存被选中那条的快照（ticket 08 的 schema 不变）。要判断「是否出现新 cnsub」反正每次都得拉上游，缓存候选**省不掉调用**，只会引入陈旧数据与第二个失效面。初次交付地图 ticket 09「不引入缓存」的结论不受影响。

### 6. 切换可见

- 每次切换一条 **INFO**：movie id、旧→新 infohash、旧→新 created_at。
- 落盘日志沿用 ticket 08 的「切换 N」聚合计数。
- 不加 `/metrics`：当前服务形态没有指标端点，日志足够。

### 落地位置

```
internal/catalog/
  select.go      Select = cnsub 优先 + created_at 最新 + infohash 定序   <- 09（04 的规则）
  createdat.go   ParseCreatedAt / CompareCreatedAt                       <- 09
internal/pin/
  policy.go      DefaultPolicy（Desired 委托 catalog.Select；KeepOrSwitch 实现上表） <- 09
  source.go      Source：按策略改写 Magnets + 记切换/消失日志             <- 08 骨架 + 09 日志
```

ticket 08 的 `TemporaryPolicy`（钉住即不切）被删除，`pin.New(..., nil)` 的默认策略变为 `DefaultPolicy`。

### 已知代价

- pin 快照的 `created_at` 为空（旧 schema/上游未给）时，一旦出现带日期的 cnsub 会被判为“更新”而切一次 —— 之后 pin 带上日期即稳定（`CompareCreatedAt` 的退化比较）。
- 「pin 消失」的 WARN 会重复出现（见 §2）。
