# 03: 页面骨架 + 收藏女优区端到端（含待复制）

**Spec:** [`../spec.md`](../spec.md)

**What to build:** 服务把页面端出来：打开 `http://<服务地址>/` 就是订阅链接生成器。资产内嵌进单二进制（构建与运行都不需要 Node），页面只从服务自己的发现端点取数据，没有任何 CDN 依赖。

这一票打通第一屏的用户价值：看到收藏的女优（默认只看女优，可切全部演员）、搜索、勾选几位、逐行选「追新 / 全量」并有一个默认模式一次覆盖、「待复制」常驻输出台（逐条复制、复制全部、清空）、局域网 http 下剪贴板 API 不存在时的复制回退、服务地址默认取打开页面的那个源并可改且被记住 —— 改一次所有链接当场跟上。链接里**只出现语义参数**，掩码由服务构造；待复制里存的是「怎么生成」（女优 id / 模式），URL 每次派生。

同时把 `.scratch/javdb-rss-ui/tools/flows.mjs` 从静态 mockup **改指向 `provider=stub` 的真服务** —— 那套逐字符 URL 断言从此守的是上线的东西，不是设计稿。

**Blocked by:** None (can start immediately)

**Status:** resolved

- [x] `GET /` 返回 200 + `text/html`；资产走 `/assets/` 前缀，同源、无第三方脚本或字体
- [x] **打错的 feed 路径仍然是 404**：页面只注册在精确的根路径上，不能把 `/rss/want` 这类路径喂成 HTML 页面（qBittorrent 只会说「这不是一个 feed」）
- [x] 收藏列表来自 `/collected`：默认只看女优、可切全部演员、男优被标出来、可按名字或 id 搜索
- [x] 勾选几位 → 每人一条链接：追新 = `since=今天`，全量 = `pages=20`；逐行可覆盖，默认模式可一次覆盖全部并清掉逐行覆盖
- [x] 待复制是**唯一**输出台：别处只放摘要，不放第二份 URL 列表；可逐条复制、可复制全部、可清空
- [x] 复制在剪贴板 API 不可用时走回退链，并给「已复制 ✓」+ 读屏播报
- [x] 服务地址默认是打开页面的那个源，可改、刷新后仍在；改完所有链接当场重算
- [x] 待复制里的每条 URL 里**不出现** `filter_by` / `filter_by_tags`
- [x] 关掉页面重开：选择与待复制不复活（只有服务地址与主题进 localStorage）
- [x] 读不到收藏时照实说出服务给的那句话（指向 `token_file`），不是空列表
- [x] `tools/flows.mjs` 改打真服务并通过（条数断言改用 stub fixture 的数）

## Comments

已实现。页面模块是 `internal/webui`：`embed` 持有 `index.html` 与 `assets/{app.css,app.js}`
（Tailwind 产物入库，构建与运行都不需要 Node），`httpapi` 把 `GET /{$}` 与 `GET /assets/`
挂在服务自己身上。

几处值得记下的取舍：

- **本票只上线「收藏女优」区。** 标签（04/05）、清单与想看（06）、状态可见性（07）**明确由
  后续票实现**，因此本票把 `tools/flows.mjs` 与 `tools/a11y.mjs` 收敛到已上线的这一屏
  （原来的 tag/list/want 断言由各自的票补回来）。`tools/verify.sh` 不再编译 CSS
  （产物入库），也不再重跑截图 —— `mockup/` 与 `shots/` 是冻结的设计记录。
- **stub 的收藏 fixture 补了一位男优**（EvkJ 河北彩花 / D2EdJ 花守夏歩 / PpQ0 森林原人，
  2 女 1 男，名字与条数取自真实收藏的一小截）。没有它，「默认只看女优 / 全部收藏 /
  男优被标出来」这三条在离线的浏览器套件里根本验不到。条数断言因此用 fixture 的数（2/3）。
- **数据只从 `location.origin` 取。** 服务地址（可改、持久化）只影响**生成链接的前缀**：
  页面是服务自己提供的，`/collected` 永远问发这一页的那个源。
- **服务地址的持久化键是 `javdb-rss-base`，主题是 `javdb-rss-theme`** —— 这是 localStorage
  里仅有的两项；选择与待复制只活在内存里。
- **无 token 的文案不在页面里编。** 页面照实渲染 `/collected` 返回的 `error` 字段
  （服务那侧的 503 文案已由 `internal/httpapi` 的测试覆盖）。浏览器套件用 Playwright 的
  路由拦截把那句话喂给页面，验的是「页面照实呈现服务说的话」这条契约。
- 页面模块的测试落在 `internal/httpapi/page_test.go`（`GET /` 的形状、`/assets/` 的内容类型、
  以及最要紧的**打错 feed 路径仍是干净 404** 的回归守卫）。
