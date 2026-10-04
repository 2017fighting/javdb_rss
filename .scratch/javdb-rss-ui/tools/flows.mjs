/*
 * 行为验证（评审用，不进产物）。
 *
 *   node tools/flows.mjs <origin>
 *
 * 它打的是 **provider=stub 的真服务**，不是 mockup 那份静态设计稿 ——
 * 于是这些逐字符 URL 断言守的不再是设计稿，而是上线的东西。
 * stub 的收藏 fixture 是 2 女 + 1 男（EvkJ / D2EdJ / PpQ0），
 * 因此条数断言用的是 **fixture 的数**，不是真账号的 144。
 *
 * 本票（03）覆盖：页面骨架 + 收藏女优区 + 待复制 + 复制回退 + 服务地址。
 * 票 04 补上：某位女优的标签区（词表 / 她自己的标签 / 撞号消歧 / 上限 5 /
 * 折叠与搜索 / 整年 / 逐字符 URL / 加载态与失败态 / 手输 id）。
 * 票 05 补上：全站标签模式（片库四选一 / 词表按片库取 / m 总在 main 里 /
 * 月份与时长只在此模式可用 / 模式切换清状态 / 逐字符地不含 filter_by）。
 * 票 06 补上：清单与想看（一清单一链接 / 默认与私有标记 / 私有名字读不到
 * 退回 id / 想看一个显眼的复制按钮 / 两者都能进待复制 / 没 token 时禁用并
 * 说明 token_file，标签区不受连坐）。
 * 状态可见性（07）的断言在那一票里补。
 */
import { chromium } from "playwright";

const origin = (process.argv[2] || "http://127.0.0.1:8080").replace(/\/+$/, "");
const executablePath =
  process.env.CHROME_PATH ||
  `${process.env.HOME}/Library/Caches/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-mac-arm64/chrome-headless-shell`;

const browser = await chromium.launch({ executablePath });

const fails = [];
const ok = [];
function check(name, actual, expected) {
  if (actual === expected) ok.push(name);
  else fails.push(`${name}\n     期望 ${JSON.stringify(expected)}\n     实得 ${JSON.stringify(actual)}`);
}

async function newPage() {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    permissions: ["clipboard-read", "clipboard-write"],
  });
  const page = await ctx.newPage();
  // 页面里任何一个 JS 异常都算失败 —— 否则「点了没反应」会被当成「设计如此」。
  page.on("pageerror", (e) => fails.push(`页面抛了异常: ${e.message}`));
  return { ctx, page };
}

const { ctx, page } = await newPage();

/**
 * 收起底部的待复制面板。
 *
 * 它是**固定**在视口底部的浮层（ui-contract：待复制是常驻输出台），
 * 展开时会盖住页面下缘 —— 想再点页面底部的按钮（比如「加入待复制」）
 * 得先把它收起来，否则点到的是浮层。
 */
async function collapseTray(p) {
  const open = await p.$eval("#tray-panel", (e) => !e.hidden).catch(() => false);
  if (open) await p.click("#tray-toggle");
}
const today = await page.evaluate(() => {
  const d = new Date();
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
});

await page.goto(origin, { waitUntil: "load" });
await page.waitForSelector("#actress-list [data-actress]");

// ── 页面骨架：GET / 是页面，资产同源 ──
const cssHref = await page.getAttribute('link[rel="stylesheet"]', "href");
check("样式是同源资产", cssHref, "/assets/app.css");
check("没有第三方脚本", await page.$$eval("script[src]", (n) => n.map((e) => e.getAttribute("src")).join(",")), "/assets/app.js");

// ── 需求 1：收藏女优，默认只看女优、可切全部、男优被标出来、可搜索 ──
check("默认只看女优（stub fixture）", await page.textContent("#actress-count"), "2");
await page.click('[data-gender="all"]');
check("全部收藏 = 3", await page.textContent("#actress-count"), "3");
check("男优有标记", (await page.textContent("#actress-list")).includes("男优"), true);
check("男优被标在那一条上", await page.$eval('[data-row="PpQ0"]', (e) => e.textContent.includes("男优")), true);
await page.click('[data-gender="female"]');
check("切回只看女优", await page.textContent("#actress-count"), "2");

await page.fill("#actress-search", "EvkJ");
check("可按 id 搜索", await page.textContent("#actress-count"), "1");
await page.fill("#actress-search", "花守");
check("可按名字搜索", await page.textContent("#actress-count"), "1");
await page.fill("#actress-search", "");

// 勾两位：一位追新、一位逐行改成全量
await page.check('[data-actress="EvkJ"]');
await page.check('[data-actress="D2EdJ"]');
await page.click('[data-rowmode="D2EdJ"]'); // D2EdJ 改成全量
const trayRows = await page.$$eval("#tray-list li code", (n) => n.map((e) => e.textContent));
check("勾两位 → 2 条链接", trayRows.length, 2);
check(
  "追新 = since=今天",
  trayRows.includes(`${origin}/rss/actress/EvkJ.xml?since=${today}`),
  true,
);
check(
  "全量 = pages=20（逐行覆盖）",
  trayRows.includes(`${origin}/rss/actress/D2EdJ.xml?pages=20`),
  true,
);
check(
  "待复制里的 URL 不含掩码（掩码只由服务构造）",
  trayRows.every((u) => !u.includes("filter_by") && !u.includes("filter_by_tags")),
  true,
);

// 默认模式切全量：逐行覆盖要被清掉，所有人跟上
await page.click('[data-mode="all"]');
const afterMode = await page.$$eval("#tray-list li code", (n) => n.map((e) => e.textContent));
check("切默认模式后逐行覆盖被清掉", afterMode.every((u) => u.endsWith("?pages=20")), true);
await page.click('[data-mode="new"]');
check(
  "切回默认追新后所有人跟上",
  (await page.$$eval("#tray-list li code", (n) => n.map((e) => e.textContent))).every((u) =>
    u.endsWith(`?since=${today}`),
  ),
  true,
);

// 服务地址改一行，所有链接当场跟上（不用重新生成）
await page.fill("#base-url", "http://192.168.2.186:8080/");
await page.waitForTimeout(50);
const afterBase = await page.$$eval("#tray-list li code", (n) => n.map((e) => e.textContent));
check("换服务地址后链接重算", afterBase.every((u) => u.startsWith("http://192.168.2.186:8080/")), true);

// 逐条复制：内容 + 「已复制 ✓」反馈（行在折叠面板里，先展开）
await page.click("#tray-toggle");
await page.click("#tray-list [data-copy-row]");
await page.waitForTimeout(120);
check("逐条复制内容", await page.evaluate(() => navigator.clipboard.readText()), `http://192.168.2.186:8080/rss/actress/EvkJ.xml?since=${today}`);
check("复制后有反馈", (await page.textContent("#tray-list [data-copy-row]")).includes("已复制"), true);

// 复制全部：换行分隔的整串
await page.click("#tray-copy-all");
await page.waitForTimeout(120);
const all = await page.evaluate(() => navigator.clipboard.readText());
check("复制全部给的是换行分隔整串", all.split("\n").length, 2);

// ── 复制回退：非安全上下文（局域网 http）下 navigator.clipboard 不存在 ──
const fallback = await page.evaluate(async () => {
  Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  let used = "";
  document.execCommand = (cmd) => {
    used = cmd;
    return true;
  };
  document.getElementById("tray-copy-all").click();
  await new Promise((r) => setTimeout(r, 80));
  return used;
});
check("剪贴板不可用时走 execCommand 回退", fallback, "copy");

// 刷新后：选择与待复制**不复活**，服务地址仍在。
await page.fill("#base-url", "http://192.168.2.186:8080");
await page.waitForTimeout(50);
await page.reload({ waitUntil: "load" });
await page.waitForSelector("#actress-list [data-actress]");
check("刷新后服务地址还在", await page.inputValue("#base-url"), "http://192.168.2.186:8080");
check("刷新后待复制是空的", await page.textContent("#tray-count"), "0");
check("刷新后选择不复活", await page.$$eval("#actress-list [data-actress]:checked", (n) => n.length), 0);
await page.fill("#base-url", origin);

// ── 清空：待复制的唯一性 ──
await page.check('[data-actress="EvkJ"]');
check("勾选后待复制有 1 条", await page.textContent("#tray-count"), "1");
await page.click("#tray-clear");
check("清空后待复制为空", await page.textContent("#tray-count"), "0");
check("清空后选择也松掉", await page.$$eval("#actress-list [data-actress]:checked", (n) => n.length), 0);
await ctx.close();

// ══════════════════════════════════════════════════════════════════════
// 需求 3：某位女优的标签（票 04）
//
// 这一区打的是两个**匿名**发现端点：/tags?type=0（词表）与
// /actress_tags/{id}（她自己的标签）。下面的断言既守页面行为，也守两条
// 正确性规则 —— 「id 撞号按名字消歧」与「基本组只列她支持的那几个」。
// ══════════════════════════════════════════════════════════════════════
const tag = await newPage();
await tag.page.goto(origin, { waitUntil: "load" });
await tag.page.waitForSelector("#actress-list [data-actress]");
await tag.page.waitForFunction(() => document.querySelectorAll("#tag-groups details").length > 0);

check(
  "标签区默认选收藏里的第一位女优",
  await tag.page.inputValue("#tag-picker"),
  "河北彩花（EvkJ）",
);

// 词表的基本组有 6 个字母；她自己的 filter_tags 只有 4 个。
const flags = await tag.page.$$eval("#main-flags [data-flag]", (ns) => ns.map((e) => e.dataset.flag));
check("基本组只列她支持的主属性（按词表 main 组顺序）", flags.join(","), "p,m,c,s");
check(
  "基本组的说明点出「不是词表里全部」",
  (await tag.page.textContent("#main-hint")).includes("全部 6 个"),
  true,
);

// 分组名与顺序原样来自词表；组内标签只来自她自己的 tags[]。
const groupIds = await tag.page.$$eval("#tag-groups details", (ds) => ds.map((d) => d.dataset.group));
check(
  "分组顺序来自词表（只留有她标签的组）",
  groupIds.join(","),
  "subject,role,cloth,body,play_method,category",
);
check(
  "分组名原样来自词表",
  await tag.page.$eval('#tag-groups details[data-group="subject"] summary span', (e) => e.textContent.trim()),
  "主題",
);
// 词表的 服裝 组是 3(眼鏡) + 10(西裝)；她只有眼鏡 —— 所以这条能区分
// 「来自她的 tags[]」与「来自词表」。
check(
  "组内标签只来自她自己的 tags[]",
  (await tag.page.$$eval('#tag-groups details[data-group="cloth"] [data-tag]', (ns) =>
    ns.map((e) => e.dataset.tag),
  )).join(","),
  "3",
);

// id 撞号（月份 1–12 与真标签 id 全撞号）必须按**名字**消歧：
// 她的 id=3 叫「眼鏡」→ 服裝；id=7 叫「處女」→ 主題；id=12 叫「成人電影」→ 類別。
check(
  "撞号 id 3 被消歧到 服裝（不是月份）",
  await tag.page.$eval('#tag-groups [data-tag="3"]', (e) => e.dataset.tagCat),
  "cloth",
);
check(
  "撞号 id 7 被消歧到 主題（名字是處女）",
  await tag.page.$eval('#tag-groups [data-tag="7"]', (e) => e.dataset.tagName),
  "處女",
);
check(
  "撞号 id 12 被消歧到 類別",
  await tag.page.$eval('#tag-groups [data-tag="12"]', (e) => e.dataset.tagCat),
  "category",
);
check(
  "年/月/时长不渲染成可点 chip",
  await tag.page.$$eval(
    '#tag-groups details[data-group="year"], #tag-groups details[data-group="month"], #tag-groups details[data-group="duration"]',
    (ns) => ns.length,
  ),
  0,
);

// 折叠：默认全折，搜索时全展开，清掉搜索回到默认。
check("分组默认全部折叠", await tag.page.$$eval("#tag-groups details[open]", (ns) => ns.length), 0);
await tag.page.fill("#tag-search", "眼鏡");
check(
  "搜索时命中所在的组被展开",
  await tag.page.$$eval("#tag-groups details[open]", (ns) => ns.length),
  1,
);
check(
  "搜索只留下命中的组",
  await tag.page.$$eval("#tag-groups details", (ds) => ds.map((d) => d.dataset.group).join(",")),
  "cloth",
);
await tag.page.fill("#tag-search", "");
check("清掉搜索后回到默认折叠", await tag.page.$$eval("#tag-groups details[open]", (ns) => ns.length), 0);
await tag.page.click("#tag-expand");
check("可全部展开", await tag.page.$$eval("#tag-groups details:not([open])", (ns) => ns.length), 0);

// URL 逐字符：乱序点三颗，URL 仍按**词表顺序（组顺序 + id）**拼。
await tag.page.click('#tag-groups [data-tag="28"]'); // 體型
await tag.page.click('#tag-groups [data-tag="3"]'); // 服裝
await tag.page.click('#tag-groups [data-tag="68"]'); // 主題
check(
  "标签按词表顺序（组顺序 + id）拼进 URL",
  await tag.page.textContent("#tag-url"),
  `${origin}/rss/actress/EvkJ.xml?since=${today}&tags=68,3,28`,
);

// 主属性按词表 main 组顺序（p,m,c,s），不是点击顺序。
await tag.page.click('#main-flags [data-flag="m"]');
await tag.page.click('#main-flags [data-flag="c"]');
check(
  "主属性按词表 main 组顺序拼进 URL",
  await tag.page.textContent("#tag-url"),
  `${origin}/rss/actress/EvkJ.xml?since=${today}&main=m,c&tags=68,3,28`,
);
check(
  "标签链接里只有语义参数（没有掩码）",
  (await tag.page.textContent("#tag-url")).includes("filter_by"),
  false,
);

// 上限 5：第 6 个点不动，并说明是上游限制；每组带已选计数。
await tag.page.click('#tag-groups [data-tag="161"]');
await tag.page.click('#tag-groups [data-tag="7"]');
check("已选计数到 5", await tag.page.textContent("#tag-count"), "5");
check(
  "触顶后未选中的点不动",
  await tag.page.$eval('#tag-groups [data-tag="45"]', (e) => e.disabled),
  true,
);
check(
  "触顶的说明点出是上游限制",
  (await tag.page.textContent("#tag-selected")).includes("上游的硬限制"),
  true,
);
check(
  "每组带已选计数",
  (await tag.page.textContent('#tag-groups details[data-group="subject"] summary')).includes("已选 2"),
  true,
);
// 取消一个，第 6 个又能点（上限是「同时 5 个」，不是「最多点 5 次」）。
await tag.page.click('#tag-selected [data-unselect="7"]');
check(
  "取消一个后第 6 个恢复可点",
  await tag.page.$eval('#tag-groups [data-tag="45"]', (e) => e.disabled),
  false,
);

// 年份：选项文字**不带「起」**（它是整年，不是下界）；选了它 since 让位。
const yearText = await tag.page.$$eval("#year-select option", (os) =>
  os.map((o) => o.textContent).join("|"),
);
check("年份选项不带「起」", yearText.includes("起"), false);
check("年份选项是整年文字", yearText.includes("2025 年"), true);
await tag.page.selectOption("#year-select", "2025");
const yearUrl = await tag.page.textContent("#tag-url");
check("选了年份后 year 进 URL", yearUrl.includes("year=2025"), true);
check("选了年份后 since 让位", yearUrl.includes("since="), false);
check(
  "页面说明为什么让位",
  (await tag.page.textContent("#tag-mode-hint")).includes("空 feed"),
  true,
);
await tag.page.selectOption("#year-select", "");

// 手输一个**不在收藏里**的 id：标签与链接都要出得来（标签筛选是匿名的）。
await tag.page.fill("#tag-picker", "ZZZZ9");
await tag.page.press("#tag-picker", "Enter");
await tag.page.waitForFunction(
  () =>
    document.getElementById("tag-picker-status").textContent.includes("上游给了她") &&
    document.querySelectorAll("#tag-groups details").length > 0,
);
check("手输 id 仍能出标签", await tag.page.$eval("#tag-groups [data-tag]", (e) => !!e), true);
check(
  "手输 id 的链接用它",
  (await tag.page.textContent("#tag-url")).startsWith(`${origin}/rss/actress/ZZZZ9.xml`),
  true,
);

// 切人：取她的标签是有加载态的（不是永远显示「?」）。
// 让这一次取数慢下来，好确定性地看到加载态。
await tag.page.route("**/actress_tags/*", async (route) => {
  await new Promise((r) => setTimeout(r, 300));
  await route.continue();
});
await tag.page.fill("#tag-picker", "EvkJ");
await tag.page.press("#tag-picker", "Enter");
check("切人时有加载态", (await tag.page.textContent("#tag-picker-status")).includes("正在读取"), true);
await tag.page.waitForFunction(() =>
  document.getElementById("tag-picker-status").textContent.includes("河北彩花"),
);
check("切人后旧的标签选择被清掉", await tag.page.textContent("#tag-count"), "0");

// 加入待复制：标签链接也是一条待复制（待复制仍然只有一份 URL 列表）。
await tag.page.click("#tag-expand");
await tag.page.click('#tag-groups [data-tag="3"]');
await tag.page.click("#tag-add");
check("加入待复制后有 1 条", await tag.page.textContent("#tray-count"), "1");
check(
  "待复制里那条就是标签链接",
  await tag.page.$eval("#tray-list li code", (e) => e.textContent),
  `${origin}/rss/actress/EvkJ.xml?since=${today}&tags=3`,
);
await tag.page.click("#tray-list [data-copy-row]");
await tag.page.waitForTimeout(120);
check(
  "待复制里的标签链接能复制",
  await tag.page.evaluate(() => navigator.clipboard.readText()),
  `${origin}/rss/actress/EvkJ.xml?since=${today}&tags=3`,
);

// 换一位女优再加：两条标签链接并存（不同的人互不影响）；
// 同一位再点一次是同一条链的新版本，不是又堆一条。
await collapseTray(tag.page);
await tag.page.fill("#tag-picker", "D2EdJ");
await tag.page.press("#tag-picker", "Enter");
await tag.page.waitForFunction(() =>
  document.getElementById("tag-picker-status").textContent.includes("上游给了她"),
);
await tag.page.click("#tag-expand");
await tag.page.click('#tag-groups [data-tag="3"]');
await tag.page.click("#tag-add");
check("换一位女优再加 → 两条标签链接并存", await tag.page.textContent("#tray-count"), "2");
await collapseTray(tag.page);
await tag.page.click("#tag-add");
check("同一位再加不重复追加", await tag.page.textContent("#tray-count"), "2");
await tag.ctx.close();

// ── 「有已选」的组默认展开（其余默认折叠）──
// 搜索会把所有组都展开（否则命中被藏在折叠里）。清掉搜索时，只有
// **有已选**的那个组该留下来，因为那正是重渲染时的默认规则。
const defaults = await newPage();
await defaults.page.goto(origin, { waitUntil: "load" });
await defaults.page.waitForSelector("#actress-list [data-actress]");
await defaults.page.waitForFunction(
  () => document.querySelectorAll("#tag-groups details").length > 0,
);
await defaults.page.fill("#tag-search", "眼鏡");
await defaults.page.click('#tag-groups [data-tag="3"]');
await defaults.page.fill("#tag-search", "");
check(
  "有已选的组默认展开、其余仍折叠",
  await defaults.page.$$eval("#tag-groups details[open]", (ds) =>
    ds.map((d) => d.dataset.group).join(","),
  ),
  "cloth",
);
await defaults.ctx.close();

// ── 取不到她的标签时，页面要照实说出服务那句话（不是永远显示「?」） ──
const broken = await newPage();
await broken.page.route("**/actress_tags/*", (route) =>
  route.fulfill({
    status: 502,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify({ error: "读取女优标签失败：上游炸了" }),
  }),
);
await broken.page.goto(origin, { waitUntil: "load" });
await broken.page.waitForSelector("#actress-list [data-actress]");
await broken.page.waitForFunction(() =>
  document.getElementById("tag-empty").textContent.includes("上游炸了"),
);
check(
  "取不到她的标签时说出服务那句话",
  (await broken.page.textContent("#tag-empty")).includes("上游炸了"),
  true,
);
check(
  "取不到她的标签时给出下一步",
  await broken.page.$eval("#tag-empty [data-tag-retry]", (e) => !!e),
  true,
);
check(
  "取不到时不显示「?」",
  (await broken.page.textContent("#tag-picker-status")).includes("读不到"),
  true,
);
await broken.ctx.close();

// ── 状态：读不到收藏时，照实说服务给的那句话（不是空列表） ──
// 服务的 /collected 在没 token 时返回 503 + 那句指向 token_file 的话。
// stub provider 没有 token 概念，所以这里用路由拦截喂**真实服务的那段文案**
// （服务自己那侧由 internal/httpapi 的测试覆盖），验的是页面照实呈现它。
const notokenBody = {
  error:
    "尚未配置 token，读不到 App 里的收藏女优。请从 App 导出后配置 app_api.token_file（见 README）。" +
    "注意：番号订阅与女优订阅不需要 token，不受此影响。",
};

for (const [name, status, body, markers] of [
  ["没 token", 503, notokenBody, ["token_file", "不需要 token"]],
  ["上游出错", 502, { error: "读取收藏女优失败：上游炸了" }, ["上游炸了"]],
  ["空收藏", 200, { actresses: [] }, ["收藏列表是空的"]],
]) {
  const { ctx: c, page: p } = await newPage();
  await p.route("**/collected", (route) =>
    route.fulfill({
      status,
      contentType: "application/json; charset=utf-8",
      body: JSON.stringify(body),
    }),
  );
  await p.goto(origin, { waitUntil: "load" });
  await p.waitForFunction(() => !document.getElementById("actress-empty").classList.contains("hidden"));
  const text = await p.textContent("#actress-empty");
  for (const m of markers) {
    check(`${name} 时页面说出「${m}」`, text.includes(m), true);
  }
  await c.close();
}

// ── 「对不上就不显示」（宁缺勿错）：撞号而名字对不上的标签必须被丢掉 ──
// 词表里 id=3 有两个候选（服裝:眼鏡 / 月份:3），而她这条的名字两个都对不上
// —— 把一个月份 id 当成真标签发出去会静默筛出另一批作品，所以宁可不显示。
const ambiguous = await newPage();
await ambiguous.page.route("**/actress_tags/*", (route) =>
  route.fulfill({
    status: 200,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify({
      id: "EvkJ",
      name: "河北彩花",
      videos_count: 1,
      main: [{ id: "p", name: "可播放" }],
      tags: [
        { id: "3", name: "月份：3", videos_count: 1 }, // 撞号且名字对不上 → 丢
        { id: "7", name: "處女", videos_count: 1 }, // 撞号但名字对得上 → 留
        { id: "999999", name: "词表里没有", videos_count: 1 }, // 词表里没有 → 丢
      ],
    }),
  }),
);
await ambiguous.page.goto(origin, { waitUntil: "load" });
await ambiguous.page.waitForSelector("#actress-list [data-actress]");
await ambiguous.page.waitForFunction(
  () => document.querySelectorAll("#tag-groups details").length > 0,
);
check(
  "撞号但名字对不上 → 不显示",
  await ambiguous.page.$$eval('#tag-groups [data-tag="3"]', (ns) => ns.length),
  0,
);
check(
  "词表里没有的 id → 不显示",
  await ambiguous.page.$$eval('#tag-groups [data-tag="999999"]', (ns) => ns.length),
  0,
);
check(
  "能对上的照旧落到真分组上",
  await ambiguous.page.$eval('#tag-groups [data-tag="7"]', (e) => e.dataset.tagCat),
  "subject",
);
await ambiguous.ctx.close();

// ── 没配 token（收藏读不到）时，标签区照旧能用手输 id 用起来 ──
// 标签词表与女优标签都是**匿名可读**的，不该被「收藏读不到」连坐。
const notokenTag = await newPage();
await notokenTag.page.route("**/collected", (route) =>
  route.fulfill({
    status: 503,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify(notokenBody),
  }),
);
await notokenTag.page.goto(origin, { waitUntil: "load" });
// 收藏读不到（503）：这里等的不是列表，而是服务那句指向 token_file 的话。
await notokenTag.page.waitForFunction(() =>
  document.getElementById("actress-empty").textContent.includes("token_file"),
);
await notokenTag.page.fill("#tag-picker", "EvkJ");
await notokenTag.page.press("#tag-picker", "Enter");
await notokenTag.page.waitForFunction(() =>
  document.getElementById("tag-picker-status").textContent.includes("上游给了她"),
);
check(
  "没 token 时手输 id 仍能出标签",
  await notokenTag.page.$eval("#tag-groups [data-tag]", (e) => !!e),
  true,
);
check(
  "没 token 时标签链接照旧可生成",
  (await notokenTag.page.textContent("#tag-url")).startsWith(
    `${origin}/rss/actress/EvkJ.xml?since=${today}`,
  ),
  true,
);
await notokenTag.ctx.close();

// ══════════════════════════════════════════════════════════════════════
// 需求 3b：全站标签（票 05）
//
// 全站模式不挂实体：片库在 URL 路径里，标签/年/月/时长走 filter_by 掩码的
// 槽位（掩码由服务构造）。这一区只发语义参数。最要紧的两条断言是
// 「m 总在 main 里」与「换片库换词表」—— 两者错了都不报错，只静默给错东西。
// ══════════════════════════════════════════════════════════════════════
const site = await newPage();
// 页面到底按哪个片库取了词表 —— 「词表按片库取，不是写死快照」只能这样验。
const vocabCalls = [];
await site.page.route(/\/tags\?type=/, (route) => {
  vocabCalls.push(new URL(route.request().url()).searchParams.get("type"));
  route.continue();
});
await site.page.goto(origin, { waitUntil: "load" });
await site.page.waitForSelector("#actress-list [data-actress]");
await site.page.waitForFunction(() => document.querySelectorAll("#tag-groups details").length > 0);

// ── 女优模式：月份与时长在界面上，但被禁用并说明原因（不是藏起来）──
check(
  "女优模式下月份控件可见但禁用",
  await site.page.$$eval(
    "#month-group [data-month]",
    (ns) => ns.length > 0 && ns.every((n) => n.disabled),
  ),
  true,
);
check(
  "女优模式下时长控件可见但禁用",
  await site.page.$$eval(
    "#duration-group [data-duration]",
    (ns) => ns.length > 0 && ns.every((n) => n.disabled),
  ),
  true,
);
check(
  "女优模式的月份说明点出服务会判 400",
  (await site.page.textContent("#month-hint")).includes("400"),
  true,
);
check(
  "女优模式的月份 chip 带说明 title",
  (await site.page.$eval('#month-group [data-month="3"]', (e) => e.title)).includes(
    "女优订阅不支持月份",
  ),
  true,
);
check("女优模式不显示片库选择", await site.page.$eval("#site-note", (e) => e.hidden), true);
check(
  "女优模式的说明讲的是她自己 tags[]",
  (await site.page.textContent("#tag-note")).includes("她自己"),
  true,
);

// ── 切到全站模式 ──
await site.page.click('[data-source="site"]');
check("全站模式显示片库选择", await site.page.$eval("#site-note", (e) => !e.hidden), true);
check("全站模式隐藏女优输入框", await site.page.$eval("#tag-picker-wrap", (e) => e.hidden), true);
check(
  "全站模式给出四个片库",
  await site.page.$$eval("#zone-group [data-zone]", (ns) =>
    ns.map((n) => n.dataset.zone).join(","),
  ),
  "0,1,2,3",
);
check(
  "全站模式的说明点出「走掩码槽位、不是 filter_by_tags」",
  (await site.page.textContent("#tag-note")).includes("掩码槽位") &&
    (await site.page.textContent("#tag-note")).includes("filter_by_tags"),
  true,
);

// ── 默认链接：追新 + m（m 总是并进 main，不是「没有别的才用 m」）──
check(
  "全站模式默认把 m 并进 main",
  await site.page.textContent("#tag-url"),
  `${origin}/rss/tags/0.xml?since=${today}&main=m`,
);
check(
  "全站模式的 URL 里没有掩码",
  (await site.page.textContent("#tag-url")).includes("filter_by"),
  false,
);
check(
  "全站模式下月份可用",
  await site.page.$$eval(
    '#month-group [data-month]:not([data-month=""]):not([disabled])',
    (ns) => ns.length,
  ),
  3,
);
check(
  "全站模式下没选年份时时长仍禁用",
  await site.page.$$eval(
    '#duration-group [data-duration]:not([data-duration=""]):not([disabled])',
    (ns) => ns.length,
  ),
  0,
);

// ── 主属性：c → c,m（并进去，不是覆盖）──
await site.page.click('#main-flags [data-flag="c"]');
check(
  "全站模式主属性 c → c,m",
  await site.page.textContent("#tag-url"),
  `${origin}/rss/tags/0.xml?since=${today}&main=c,m`,
);
check(
  "m 那颗 chip 总是按下且不可取消",
  await site.page.$eval(
    '#main-flags [data-flag="m"]',
    (e) => e.getAttribute("aria-pressed") === "true" && e.disabled,
  ),
  true,
);

// ── 标签来自**片库词表**（不是某位女优的 tags[]）──
check(
  "全站模式的标签组就是词表里除 main/年/月/时长之外的全部",
  await site.page.$$eval("#tag-groups details", (ds) =>
    ds.map((d) => d.dataset.group).join(","),
  ),
  "subject,role,cloth,body,behavior,play_method,category",
);
await site.page.click("#tag-expand");
await site.page.click('#tag-groups [data-tag="3"]'); // 服裝
await site.page.click('#tag-groups [data-tag="68"]'); // 主題
check(
  "全站模式标签按词表顺序（组顺序 + id）拼进 URL",
  await site.page.textContent("#tag-url"),
  `${origin}/rss/tags/0.xml?since=${today}&main=c,m&tags=68,3`,
);

// ── 换片库：路径换、词表跟着换、旧选择清掉 ──
await site.page.click('#zone-group [data-zone="2"]');
await site.page.waitForFunction(
  () => document.querySelectorAll('#tag-groups details[data-group="place"]').length > 0,
);
check(
  "换片库后路径换到 2",
  (await site.page.textContent("#tag-url")).startsWith(`${origin}/rss/tags/2.xml?`),
  true,
);
check("换片库后按 type=2 取了另一份词表", vocabCalls.includes("2"), true);
check(
  "换片库后出现 type=0 词表里没有的组（地點）",
  await site.page.$eval('#tag-groups details[data-group="place"]', (e) => !!e),
  true,
);
check(
  "换片库时旧标签被清掉（不留看不见却生效的 id）",
  (await site.page.textContent("#tag-url")).includes("tags="),
  false,
);
check(
  "换片库后标签区说明写的是新片库",
  (await site.page.textContent("#tag-note")).includes("type=2"),
  true,
);

// ── 换回 0（等一个**只属于 zone 0 词表**的 tag，免得拿上一份词表做断言）──
await site.page.click('#zone-group [data-zone="0"]');
await site.page.waitForFunction(
  () => document.querySelectorAll('#tag-groups [data-tag="312"]').length > 0,
);
const siteYearText = await site.page.$$eval("#year-select option", (os) =>
  os.map((o) => o.textContent).join("|"),
);
check("全站模式的年份选项不带「起」", siteYearText.includes("起"), false);
check("全站模式的年份选项是整年文字", siteYearText.includes("2025 年"), true);
await site.page.selectOption("#year-select", "2025");
check(
  "全站模式选了年份后 year 进 URL、since 让位",
  await site.page.textContent("#tag-url"),
  `${origin}/rss/tags/0.xml?main=m&year=2025`,
);
check(
  "全站模式下选了年份后时长恢复可用",
  await site.page.$$eval(
    '#duration-group [data-duration]:not([data-duration=""]):not([disabled])',
    (ns) => ns.length,
  ),
  4,
);
await site.page.click('#duration-group [data-duration="gt-120"]');
check(
  "全站模式选了时长后 duration 进 URL",
  await site.page.textContent("#tag-url"),
  `${origin}/rss/tags/0.xml?main=m&year=2025&duration=gt-120`,
);
await site.page.click('#month-group [data-month="3"]');
check(
  "全站模式月份进 URL（在时长之前）",
  await site.page.textContent("#tag-url"),
  `${origin}/rss/tags/0.xml?main=m&year=2025&month=3&duration=gt-120`,
);
await site.page.click("#tag-expand");
await site.page.click('#tag-groups [data-tag="68"]');
check(
  "全站模式标签与年/月/时长同时进 URL（逐字符）",
  await site.page.textContent("#tag-url"),
  `${origin}/rss/tags/0.xml?main=m&tags=68&year=2025&month=3&duration=gt-120`,
);
check(
  "全站模式的链接里仍然没有 filter_by",
  (await site.page.textContent("#tag-url")).includes("filter_by"),
  false,
);

// 全量（pages=20）不能吞掉年份/月份那几项：链接与提示都得同时说它们。
await site.page.click('[data-tagmode="all"]');
check(
  "全站模式全量 + 年/月/时长仍然逐字符正确",
  await site.page.textContent("#tag-url"),
  `${origin}/rss/tags/0.xml?pages=20&main=m&tags=68&year=2025&month=3&duration=gt-120`,
);
const allHint = await site.page.textContent("#tag-url-hint");
check("全量模式的提示仍然提到年份", allHint.includes("year=2025"), true);
check("全量模式的提示仍然提到月份", allHint.includes("month=3"), true);
check(
  "全量模式不再讲「since 让位」（它本来就不发 since）",
  (await site.page.textContent("#tag-mode-hint")).includes("让位"),
  false,
);
await site.page.click('[data-tagmode="new"]');

// ── 全站链接也能进待复制并复制（待复制仍然是唯一的输出台）──
await site.page.click("#tag-add");
check("全站链接加入待复制", await site.page.textContent("#tray-count"), "1");
const siteTrayUrl = await site.page.$eval("#tray-list li code", (e) => e.textContent);
check(
  "待复制里那条就是全站链接",
  siteTrayUrl,
  `${origin}/rss/tags/0.xml?main=m&tags=68&year=2025&month=3&duration=gt-120`,
);
await site.page.click("#tray-list [data-copy-row]");
await site.page.waitForTimeout(120);
check(
  "待复制里的全站链接能复制",
  await site.page.evaluate(() => navigator.clipboard.readText()),
  siteTrayUrl,
);

// ── 切回女优模式：另一模式专属的状态被清掉，不留在 URL 里 ──
await site.page.click('[data-source="actress"]');
await site.page.waitForFunction(() => document.querySelectorAll("#tag-groups details").length > 0);
const backToActress = await site.page.textContent("#tag-url");
check(
  "切回女优模式后链接里既没有 month 也没有 duration",
  backToActress.includes("month=") || backToActress.includes("duration=") || backToActress.includes("tags="),
  false,
);
check(
  "切回女优模式后月份控件又禁用",
  await site.page.$$eval("#month-group [data-month]", (ns) => ns.every((n) => n.disabled)),
  true,
);
check("切回女优模式后年份还在（两种模式都有的真筛选）", await site.page.inputValue("#year-select"), "2025");
check(
  "切回女优模式后说明也换回「她自己 tags[]」",
  (await site.page.textContent("#tag-note")).includes("她自己"),
  true,
);
await site.ctx.close();

// ── 片库被 feeds.zones 白名单挡下时：不给一条会 404 的链接 ──
// /tags?type={zone} 的 404 就是那个信号（与 /rss/tags/{zone}.xml 同一套语义）。
const blocked = await newPage();
await blocked.page.route(/\/tags\?type=3/, (route) =>
  route.fulfill({
    status: 404,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify({ error: "未知的订阅" }),
  }),
);
await blocked.page.goto(origin, { waitUntil: "load" });
await blocked.page.waitForSelector("#actress-list [data-actress]");
await blocked.page.waitForFunction(() => document.querySelectorAll("#tag-groups details").length > 0);
await blocked.page.click('[data-source="site"]');
await blocked.page.click('#zone-group [data-zone="3"]');
await blocked.page.waitForFunction(() =>
  document.getElementById("tag-empty").textContent.includes("没被放行"),
);
check(
  "未放行的片库 chip 被禁用",
  await blocked.page.$eval('#zone-group [data-zone="3"]', (e) => e.disabled),
  true,
);
check(
  "未放行的片库不再渲染标签",
  await blocked.page.$$eval("#tag-groups details", (ns) => ns.length),
  0,
);
check(
  "未放行的片库说清是白名单/404 而不是「暂时读不到」",
  (await blocked.page.textContent("#tag-empty")).includes("404"),
  true,
);
check(
  "未放行的片库不给链接",
  (await blocked.page.textContent("#tag-url")).includes("404"),
  true,
);
check(
  "未放行的片库禁用「加入待复制」",
  await blocked.page.$eval("#tag-add", (e) => e.disabled),
  true,
);
// 换回一个放行的片库，链接又回来了。
await blocked.page.click('#zone-group [data-zone="0"]');
await blocked.page.waitForFunction(() => document.querySelectorAll("#tag-groups details").length > 0);
check(
  "换回放行的片库后链接恢复",
  (await blocked.page.textContent("#tag-url")).startsWith(`${origin}/rss/tags/0.xml?`),
  true,
);
await blocked.ctx.close();

// ══════════════════════════════════════════════════════════════════════
// 需求 4：清单与想看（票 06）
//
// 两者读的都是 App 里的标记，所以没 token 时一定 503 —— 那时**不给链接**，
// 而是禁用按钮并说明缺什么（token_file）。这一节同时守「一清单一链接」与
// 「私有清单的名字可能读不到、退回 id」这两条。
// ══════════════════════════════════════════════════════════════════════
const lists = await newPage();
await lists.page.goto(origin, { waitUntil: "load" });
await lists.page.waitForSelector("#list-cards [data-list-copy]");

const listCards = await lists.page.$$eval("#list-cards li", (ns) =>
  ns.map((n) => n.textContent.replace(/\s+/g, " ").trim()),
);
check("清单来自 /collected_lists（stub fixture 2 份）", listCards.length, 2);
check(
  "一清单一链接：名字/条数/链接都在",
  listCards[0].includes("遥控跳弹") &&
    listCards[0].includes("1 部") &&
    listCards[0].includes(`${origin}/rss/list/k4EVE4.xml`),
  true,
);
check("清单标出默认", listCards[1].includes("默认"), true);
check("清单标出私有", listCards[1].includes("私有"), true);
check(
  "私有清单说清名字可能读不到、退回 id、链接仍可用",
  listCards[1].includes("名字可能读不到") &&
    listCards[1].includes("id") &&
    listCards[1].includes("可用"),
  true,
);

await lists.page.click("#list-cards [data-list-copy]");
await lists.page.waitForTimeout(120);
check(
  "清单链接能复制",
  await lists.page.evaluate(() => navigator.clipboard.readText()),
  `${origin}/rss/list/k4EVE4.xml`,
);
check(
  "清单复制后有反馈",
  (await lists.page.textContent("#list-cards [data-list-copy]")).includes("已复制"),
  true,
);

// 「想看」：单独一个显眼的复制按钮（内容区的主操作）。
// 两个发现端点都落地之后才给链接。
await lists.page.waitForSelector("#want-copy:not([disabled])");
check("想看是固定的 /rss/want.xml", await lists.page.textContent("#want-url"), `${origin}/rss/want.xml`);
check(
  "想看的复制按钮是内容区的主操作",
  await lists.page.$eval("#want-copy", (e) => e.classList.contains("btn-primary")),
  true,
);
await lists.page.click("#want-copy");
await lists.page.waitForTimeout(120);
check(
  "想看链接能复制",
  await lists.page.evaluate(() => navigator.clipboard.readText()),
  `${origin}/rss/want.xml`,
);
check(
  "想看复制后有反馈",
  (await lists.page.textContent("#want-copy")).includes("已复制"),
  true,
);

// 清单与想看都能加入待复制；待复制仍然只有一份 URL 列表，重复加不堆条目。
await lists.page.click("#list-cards [data-list-add]");
await collapseTray(lists.page);
await lists.page.click("#list-cards [data-list-add]"); // 同一份再点一次
await collapseTray(lists.page);
await lists.page.click("#want-add");
await collapseTray(lists.page);
await lists.page.click("#want-add"); // 想看再点一次
const listTray = await lists.page.$$eval("#tray-list li code", (ns) => ns.map((n) => n.textContent));
check("清单 + 想看重复加不堆条目", listTray.length, 2);
check(
  "待复制里逐条只有一份 URL",
  new Set(listTray).size === listTray.length &&
    listTray.includes(`${origin}/rss/list/k4EVE4.xml`) &&
    listTray.includes(`${origin}/rss/want.xml`),
  true,
);
check(
  "清单/想看的 URL 里没有掩码",
  listTray.every((u) => !u.includes("filter_by")),
  true,
);
await lists.ctx.close();

// ── 确认 token 期间不给链接：一条能手拷的 URL 与一个能点的按钮一样坏 ──
const confirming = await newPage();
await confirming.page.route("**/collected_lists", async (route) => {
  await new Promise((r) => setTimeout(r, 500));
  await route.continue();
});
await confirming.page.goto(origin, { waitUntil: "load" });
check(
  "确认 token 期间不给想看链接",
  (await confirming.page.textContent("#want-url")).includes("/rss/want.xml"),
  false,
);
check(
  "确认 token 期间想看按钮禁用",
  await confirming.page.$eval("#want-copy", (e) => e.disabled),
  true,
);
await confirming.page.waitForSelector("#want-copy:not([disabled])");
check(
  "确认完成后才给出想看链接",
  await confirming.page.textContent("#want-url"),
  `${origin}/rss/want.xml`,
);
await confirming.ctx.close();

// ── 私有清单的名字读不到时：退回 id 当标题，链接仍然可用 ──
const nameless = await newPage();
await nameless.page.route("**/collected_lists", (route) =>
  route.fulfill({
    status: 200,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify({
      lists: [
        { id: "ZZ999", name: "", movies_count: 0, privacy: "own", feed: "/rss/list/ZZ999.xml" },
      ],
    }),
  }),
);
await nameless.page.goto(origin, { waitUntil: "load" });
await nameless.page.waitForSelector("#list-cards [data-list-copy]");
check(
  "名字读不到时退回 id 当标题",
  await nameless.page.$eval("#list-cards li", (e) => e.textContent.includes("ZZ999")),
  true,
);
check(
  "名字读不到时页面说清这件事",
  (await nameless.page.textContent("#list-cards")).includes("退回"),
  true,
);
check(
  "私有清单即使名字为空也标出私有",
  (await nameless.page.textContent("#list-cards")).includes("私有清单"),
  true,
);
check(
  "名字读不到时链接仍然可用",
  await nameless.page.$eval("#list-cards [data-list-copy]", (e) => !e.disabled),
  true,
);
await nameless.ctx.close();

// ── 没 token：清单与想看禁用 + 说明缺什么；标签区不受连坐 ──
const noTokenOut = await newPage();
for (const pattern of ["**/collected", "**/collected_lists"]) {
  await noTokenOut.page.route(pattern, (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json; charset=utf-8",
      body: JSON.stringify(notokenBody),
    }),
  );
}
await noTokenOut.page.goto(origin, { waitUntil: "load" });
await noTokenOut.page.waitForFunction(() =>
  document.getElementById("lists-note").textContent.includes("token_file"),
);
check(
  "没 token 时清单区说明缺 token_file",
  (await noTokenOut.page.textContent("#lists-note")).includes("token_file"),
  true,
);
check(
  "没 token 时清单不给任何可点的输出",
  await noTokenOut.page.$$eval("#list-cards [data-list-copy]:not([disabled])", (ns) => ns.length),
  0,
);
check("没 token 时想看按钮禁用", await noTokenOut.page.$eval("#want-copy", (e) => e.disabled), true);
check(
  "没 token 时想看加入待复制也禁用",
  await noTokenOut.page.$eval("#want-add", (e) => e.disabled),
  true,
);
check(
  "没 token 时不给一条会 503 的想看链接",
  (await noTokenOut.page.textContent("#want-url")).includes("/rss/want.xml"),
  false,
);
check(
  "没 token 时想看说明缺 token_file",
  (await noTokenOut.page.textContent("#want-hint")).includes("token_file"),
  true,
);
// 标签区不受连坐：词表与女优标签匿名可读，手输 id 照样能用。
await noTokenOut.page.fill("#tag-picker", "EvkJ");
await noTokenOut.page.press("#tag-picker", "Enter");
await noTokenOut.page.waitForFunction(() =>
  document.getElementById("tag-picker-status").textContent.includes("上游给了她"),
);
check(
  "没 token 时标签区照旧可用（不被清单/想看连坐）",
  (await noTokenOut.page.textContent("#tag-url")).startsWith(
    `${origin}/rss/actress/EvkJ.xml?since=${today}`,
  ),
  true,
);
await noTokenOut.ctx.close();

await browser.close();

console.log(fails.length ? `FAIL (${fails.length})` : `PASS (${ok.length} 项)`);
for (const f of fails) console.log("  ✗ " + f);
process.exit(fails.length ? 1 : 0);
