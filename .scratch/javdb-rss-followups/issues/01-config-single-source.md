# 01 — 配置单一来源

**What to build:** 三份配置（用户示例、容器示例、k8s 内嵌 ConfigMap）表达同一套设置，但彼此独立维护 —— 已经因此漂移过一次（k8s 那份漏了 `device_uuid`，而配置项有 10+ 个）。这张票让「三处不一致」变得不可能，或至少一改就报错。

**Blocked by:** None — can start immediately.

**Status:** resolved（走的是引言允许的「一改就报错」分支；第 4 条未做生成，见下）

- [x] 修改配置结构时，三份文件的不一致会被**自动化检查**发现，而不是靠人记得改三处
- [x] 该检查在「故意只从其中一份删掉一个键」时确实失败（证明它不是空转 —— 本项目已出现过多次假通过的测试）
- [x] k8s 的 ConfigMap 与另外两份的键集合一致，含此前漂移掉的 `device_uuid`
- [ ] 新增一个配置项时只需在一处添加，其余两处由机制保证同步 —— **未采用生成方案**，见下方「关于第 4 条」

## 实测与实现（2026-09-30）

**检查落点是 `internal/config/examples_sync_test.go`**，随 `go test ./...` 与
CI 的 `make race` 一起跑。它做三层校验，全部围绕着「键集合」而非取值
（三份文件的取值本就不同：`listen`、`base_url`、`token_file`）：

1. **两两对齐** —— `config.example.yaml`、`deploy/config.docker.yaml`、
   `deploy/k8s.yaml` 内嵌的 ConfigMap 摊平成点号键路径（`app_api.device_uuid`），
   三份必须完全相同。这一层主要拦「可选段（`feeds`）在某一两份里有、另一两份没有」。
2. **覆盖 schema** —— 同一组键必须等于 `Config` 结构体（yaml tag）声明的 schema。
   这一层是关键：只让三份文件互相看齐的话，三份一起漏掉同一个键时没人会发现；
   把结构体当单一来源后，「往结构体加字段却忘了写进示例」会立刻失败。
   `feeds` 白名单段在示例里整段注释掉，属于**可选段**：允许整体缺席，不允许只写一半。
3. **元测试** —— 遍历「任意一份 × 任意一个键」把它删掉，断言检查必须报错
   （`TestKeyCheckCatchesDrift`）；另有一条 `TestKeyCheckCatchesKeyRemovedFromSource`
   直接改 `config.example.yaml` 的文本再走一遍完整的「读文件 → 解析 → 比对」链路。

**人工验收（本次实现时实跑，可复现）**：

- 从 `deploy/k8s.yaml` 删掉 `device_uuid: ""` → `TestExampleConfigsAreInSync` 失败，
  报 `config.example.yaml 有而 deploy/k8s.yaml (ConfigMap) 没有: app_api.device_uuid`。
- 往 `Config` 临时加一个 `quux` 字段（不动任何示例）→ 三份文件同时报
  `缺少 schema 里的键: quux`。

两处都验证后已还原。

## code-review 补修（2026-09-30）

两轴评审各发现一条真问题，已修：

1. **反射漏了切片元素（Spec 轴）** —— `schemaKeyPaths` 只在字段类型是结构体时下钻，
   `feeds.actresses` 是 `[]ActressSub`，于是 `feeds.actresses.id` / `.params` /
   `.since` **没有进 schema**。一旦有人把示例里的 `feeds` 段取消注释，检查会把这些
   合法键误报成「schema 里没有的键」。已加 `indirectType` 剥掉指针/切片/数组，
   并加 `TestSchemaCoversSliceElements` 钉住。
2. **非空转证明不够（Spec 轴）** —— 原 `TestKeyCheckCatchesDrift` 删任何必填键都会
   同时触发 schema 那层，所以它**无法证明「两两对齐」层本身有效**（就算把对齐层删掉，
   这个测试照样过）。已新增 `TestCrossFileCheckCatchesFeedsDrift`：把完整的 `feeds`
   段只加进其中一份，三份都满足 schema，只有对齐层能发现 —— 单独证明对齐层非空转。
   同时补了 `TestKeyCheckAcceptsFullFeeds` / `TestKeyCheckCatchesPartialFeeds`
   覆盖 feeds 的完整与半写路径。

Standards 轴的三条都是判断性意见，保留并说明：`TestK8sConfigHasDeviceUUID` 是有意的
回归钉子；`feeds` 半写校验现已有用例覆盖，不再是未执行分支；「加配置项要动四处」
正是第 4 条的取舍，见下。

## 关于第 4 条「只需在一处添加」

本票采用的是引言里**允许的**「一改就报错」分支，而不是「从一份生成另外两份」，
因此第 4 条**保持未勾选**：

- 单一来源是 `Config` 结构体 —— 加一个字段（一处）后，检查会指名道姓地列出三份示例
  各自缺了哪个键，不同步就无法通过 `make test` / CI。机制**保证的是「同步否则失败」**，
  不是「自动改写另外两份」。
- 之所以不做生成：三份文件的取值与注释都不同（本地 / 容器 / k8s 各有各的说明），
  生成会牺牲它们作为文档的价值，而 `go generate` + 在 `deploy/k8s.yaml` 里插标记注释
  的复杂度，收益不抵成本（ticket 04 的审查记录也把它记为「另一个 effort 的量级」）。

**同步更新的文档**：`config.example.yaml` / `deploy/config.docker.yaml` /
`deploy/k8s.yaml` 顶部各指向了这条检查；`README.md` 的部署一节也说明了
「三处同步，否则 `make test` 失败」。
