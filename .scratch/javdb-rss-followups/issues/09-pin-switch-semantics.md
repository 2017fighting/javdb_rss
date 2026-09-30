# 09 — pin 的切换语义

**What to build:** ticket 04 把选择改成「按 `created_at` 取最新、cnsub 优先」，并持久化钉住，**只有「新增了 cnsub」才切换**。本票把这句精确定义到可写测试：什么算新 cnsub、pin 指向上游消失怎么办、同日多条怎么定序、首次选定怎么选。

**Type:** grilling（交付是切换规则的精确决定）

**Blocked by:** None — can start immediately.（与 [08](08-pin-store.md) 存储票无先后）

**Status:** ready-for-agent

- [ ] **「新增了 cnsub」的精确定义**：
  - pin 不是 cnsub、现在有 cnsub → 切换到「最新的 cnsub」？（这是主要动机）
  - pin 是 cnsub A、上游出现更新的 cnsub B → 切换到 B？
  - pin 是 cnsub A、上游出现更旧的 cnsub C → 不切？（`created_at` 日粒度）
- [ ] **pin 指向上游消失时**（被删/失效）：继续返回它（可能给出死链）还是回退重选？两者的可见性各是什么？
- [ ] **同日定序**：`created_at` 相同（同日）的多条 cnsub / 多条候选，用 infohash 字典序？还是别的稳定键？
- [ ] **首次选定**（无 pin）：纯函数规则 = 最新的 cnsub，否则最新的候选 —— 与 ticket 04 一致，要能被单测直接构造
- [ ] **与初次交付地图 ticket 09「不引入缓存」的关系**：pin 已经是有状态，是否顺便把候选列表也缓存下来（会改写成本模型）？还是严格只存一个 infohash？
- [ ] **切换要可见**：pin 变更时是否记日志/暴露计数，好让「切换」本身可被观察（否则又回到不可见）

## 为什么单列一票

`Select` 的排序规则是纯函数、好测；但「什么时候把 pin 从旧值换成新值」是**有状态的策略**，边界比排序多（消失、同日、旧 cnsub 回潮）。混在一起写会让纯函数测试被状态问题污染。
