# 07: 状态与运维可见性

**Spec:** [`../spec.md`](../spec.md)

**What to build:** 页面不能长得像一切正常。三件事要在页面上说出来：

1. 收藏列表被翻页上限截断时，列表明说「**已知不完整**」，而不是一份长得像完整的清单；
2. 页面上的上游状态取自服务自己的 `/readyz`（原来只是一个写死的装饰）；
3. `/version` 增三类白名单状态，白名单生效时页面提示「没列出的订阅会 404」—— 否则白名单就表现为「链接看得见、一订就 404」。

**Blocked by:** 03

**Status:** resolved

- [x] `/collected` 报截断（`truncated` / `pages_fetched` / `max_pages`）时页面上可见，措辞是「已知不完整」而不是「加载失败」
- [x] 未截断时**不**出现这条提示（不制造假警告）
- [x] 上游不健康时页面上的状态可见，且与 `/readyz` 的说法一致
- [x] `/version` 返回三类白名单状态；配置了白名单时页面提示「没列出的会 404」
- [x] 没配白名单时不出现这条提示
- [x] handler 层测试覆盖 `/version` 的新字段与截断信号的透出

## Comments

已实现。改动落在五处：

- `internal/config/config.go`：新增 `WhitelistActive()`（= `Feeds != nil`）。
- `internal/httpapi/server.go`：`/version` 从 `map[string]string` 换成带类型的
  `versionBody`，多一个 `whitelist` 对象。
- `internal/httpapi/version_test.go`（新）：没有 feeds 段 → 三类全 false；feeds
  段生效 → 三类全 true；并把 `/version` 的说法与路由层的实际 404 行为钉在
  一起（三类各取一条没放行的订阅，实际都 404，而 `/version` 说它们受限）。
- `internal/webui/index.html` + `assets/app.js`：头部按 mockup 加上游 chip；
  下面三处状态说明由 `/readyz`、`/collected`、`/version` 驱动。
- `.scratch/javdb-rss-ui/tools/flows.mjs`：152 → 171 项断言。

### 需要记下的取舍

- **「三类白名单」= 女优 / 清单 / 全站标签(片库)**（经你确认）。配置里有四份
  白名单，番号被排除是因为**页面上根本不生成番号链接** —— 只有这三类会让页面
  上的某条链接「看得见、一订就 404」。JSON 形状是 `whitelist:{actresses,lists,zones}`。
- **三个布尔现在同值**：`feeds` 段是**整段**闸门（`Allows*` 都先判 `Feeds == nil`），
  所以一个只列了 `codes` 的 feeds 段也会让所有女优/清单/片库订阅 404。按类拆三个
  布尔是为了让页面在闸门将来改成按类独立时不必再动；这条写进了代码注释与
  `version_test.go` 的测试名/注释，免得下一个人以为是 bug。评审把这条记为
  judgement call（Speculative Generality），**刻意保留**。
- **上游状态与 `/readyz` 逐字一致**：chip 的 `title` 就是 `/readyz` 的原文，
  `503` 时内容区再起一条 `#upstream-note` 引用它。chip 沿用 mockup 在 `<md`
  隐藏的取舍，`#upstream-note` 因此补上「任何宽度都看得见」这一条 ——
  否则手机上「上游坏了」这个共同解释就消失了。
- **截断与「加载失败」是两回事**：`#actress-truncated` 只在 `/collected` 给了
  `truncated` 键时出现，说「已知不完整」并带上读了多少页/上限；列表本身照常渲染。
  评审提出一条硬违规：判定写成了 `body.truncated`（看值），而契约是「看键在不在」
  （`omitempty`，README 明写）。已改成 `"truncated" in body` 并保留了原因注释。
- **只动收藏区的截断**，没动 `/collected_lists` 的截断（后者接口层已有信号，
  本票的范围是 `/collected`）。

代码评审（standards + spec 两轴，并行 fresh 子代理）：

- standards：一条硬违规（上面的 `"truncated" in body`）已修；两条 judgement call
  （三个同值布尔、`whitelistActive()` 前端再聚合）**刻意保留** —— 它们直接来自
  你选定的 API 形状，且已在注释里写明。`version_test.go` 里一个被覆盖的重复测试
  已合并。
- spec：无问题。六条验收全部满足，`#upstream-note` 不算范围蔓延（它是小屏可见性的
  必要条件）。

验证：`make check` 全绿（gofmt / vet / 全量测试）；`tools/verify.sh` 全绿
（token-lint / axe+触摸目标+焦点+溢出 / 171 项行为断言 / rubric 7/7）。
