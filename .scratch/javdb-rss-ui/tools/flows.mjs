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
 * 清单与想看（06）、状态可见性（07）的断言在各自的票里补。
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

await browser.close();

console.log(fails.length ? `FAIL (${fails.length})` : `PASS (${ok.length} 项)`);
for (const f of fails) console.log("  ✗ " + f);
process.exit(fails.length ? 1 : 0);
