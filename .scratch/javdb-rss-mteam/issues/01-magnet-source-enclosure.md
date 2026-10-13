# 01: 领域模型能表达「这条候选来自哪个来源」，feed 据此选交付物

**What to build:** 磁链候选记住自己来自哪个数据来源；feed 条目按来源决定 `<enclosure>` ——
上游来源发磁链（现状一字不改），M-Team 来源发**本服务的种子地址**。
这是一张 prefactor：**用户可见行为零变化**，它只是把「交付物由来源决定」这条缝先开出来，
好让后面那张票只做「把候选池换掉」这一件事。

**Blocked by:** None (can start immediately).

**Status:** resolved（2026-10-13）

- [x] `catalog.Magnet` 增加来源字段，且**零值必须是上游** —— 既有构造点一行都不用改
      （`MagnetSource` + `SourceUpstream`/`SourceMTeam`；appapi / stub / 各层测试零改动）
- [x] `feed.Item` 按来源决定 enclosure 的 URL 与 `type`：上游 → 磁链（`magnet:?xt=urn:btih:…`）；
      M-Team → `{feed.base_url}/torrent/{infohash}.torrent`
- [x] 两种来源的 enclosure `type` 都是 `application/x-bittorrent`
- [x] `GUID()` 不变：两种来源都是**纯 infohash**
- [x] `feed` 的表驱动测试新增「种子 URL」那一支（含「零值就是上游」与「末尾斜杠不拼出双斜杠」）
- [x] **上游来源的输出逐字节未变** —— 新增一个黄金串测试钉住完整 XML。
      ⚠️ 订正票面原措辞：既有用例本身是**结构断言**（`xml.Unmarshal` 反解），
      不是字节比对 —— 所以「逐字节」这件事是由那个新用例**单独**承担的。
      评审两轴都指出了这处夸大，改在这里而不是含糊过去。
- [x] `make check` 与 `make race` 全绿

## Answer（2026-10-13）

**落在三个文件**：`catalog/model.go`（`MagnetSource` + `Magnet.Source`）、
`feed/item.go`（`Item.EnclosureURL(baseURL)` + 导出的 `TorrentPathPrefix`）、
`feed/rss.go`（`Meta.BaseURL`；`Render` 用 `EnclosureURL` 同时填 `<link>` 与 `<enclosure url>`）。

⭐ **两个与票面不同的判断（记在这里）**：

1. **`TorrentPathPrefix` 导出，给路由注册用。** 生成 URL 的地方（`feed`）与注册路由的地方
   （`httpapi`，票 02 才加）必须一致，而两处各写一遍字符串的失败方式是**静默的** ——
   feed 发出一个指向 404 的地址，客户端只会说「加不上」，没有任何地方会响。
2. **`Meta.BaseURL` 在 `renderItems` 这个唯一漏斗里填，不在五个调用点各填一次。**
   五个调用点（番号 / 女优 / 清单 / 标签 / 想看）漏一处就是「那个 feed 的种子地址是相对的」，
   同样是静默失效。漏斗里填一次，漏不掉。

**测试（`feed` 3 条 + `httpapi` 2 条）**：

| 测的 | 为什么值得测 |
|---|---|
| `Item.EnclosureURL` 四种输入（上游 / 零值 / M-Team / 末尾斜杠） | 它是「谁决定交付物」的唯一表述，输出形状就是契约 |
| 渲染出的 M-Team 条目：`enclosure` 是本服务地址、`link` 一致、`type` 正确、`guid` 未变 | 三件事分属不同关心点，任一条错了都是静默失败（重复下载 / 加不上 / 根本不认） |
| 上游来源的**黄金串**逐字节比对 | 这一票的全部风险就是「为了支持第二种交付物而顺手改动了第一种」；结构断言对那种改动不敏感 |
| `httpapi`：想看 feed 里 M-Team 来源 → 绝对地址；上游来源 → 磁链 | 地址有一半来自**配置**，feed 的单测看不到；两张图摆在一起才是完整表述 |

**黄金串是独立真值，不是照抄实现**：`2026-09-27` 用 `date` 确认是周日、
`3110 MiB` 用 `bc` 确认是 `3261071360` 字节。另做了一次**变异检验**
（把 `Render` 换回 `MagnetURI()`）确认那张网真的会响。

**⚠️ 一个环境坑（不是代码问题）**：`make race` 第一次失败，原因是 `/tmp` 是 3.4G 的 tmpfs
且已 96% 满，而 Go 的 `-race` 工作目录默认在 `/tmp`。用
`TMPDIR=/var/tmp/gobuild make race`（根分区 800G 空闲）即全绿。

## Comments

本票原计划还包含「`pin.Store` 增加只读访问器」，但探查后发现 **`pin.Store.Get(id)` 已经存在**
（`internal/pin/store.go`），所以那半张票取消 —— 票 02 的 M-Team 装饰器直接用它。
