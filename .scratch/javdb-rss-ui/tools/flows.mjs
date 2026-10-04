/*
 * 行为验证（评审用，不进产物）。
 *
 *   node tools/flows.mjs <origin>
 *
 * 闸门过了不等于能用。这里把四个需求逐条走一遍，断言生成的 URL
 * **逐字符**等于服务真实契约：
 *   /rss/actress/{id}.xml?since=YYYY-MM-DD
 *   /rss/actress/{id}.xml?pages=20
 *   /rss/actress/{id}.xml?since=…&filter_by=0:a:{id}:c::&filter_by_tags=68,46
 *   /rss/want.xml
 */
import { chromium } from "playwright";

const origin = process.argv[2] || "http://localhost:62100";
const executablePath =
  process.env.CHROME_PATH ||
  `${process.env.HOME}/Library/Caches/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-mac-arm64/chrome-headless-shell`;

const browser = await chromium.launch({ executablePath });
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  permissions: ["clipboard-read", "clipboard-write"],
});
const page = await ctx.newPage();

// 页面里任何一个 JS 异常都算失败 —— 否则“点了没反应”会被当成“设计如此”。
const pageErrors = [];
page.on("pageerror", (e) => pageErrors.push(e.message));

const fails = [];
const ok = [];
function check(name, actual, expected) {
  if (actual === expected) ok.push(name);
  else fails.push(`${name}\n     期望 ${JSON.stringify(expected)}\n     实得 ${JSON.stringify(actual)}`);
}

const today = await page.evaluate(() => {
  const d = new Date();
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
});

await page.goto(origin, { waitUntil: "load" });

// ── 需求 1：收藏女优，挑几个，追新 / 全量，一人一条链接 ──
check("默认只看女优", await page.textContent("#tab-actress-count"), "138");
await page.click('[data-gender="all"]');
check("全部演员 = 144", await page.textContent("#tab-actress-count"), "144");
check("男优有标记", (await page.textContent("#actress-list")).includes("男优"), true);
await page.click('[data-gender="female"]');

await page.fill("#actress-search", "EvkJ");
await page.check('[data-actress="EvkJ"]');
await page.fill("#actress-search", "Mm5v4");
await page.check('[data-actress="Mm5v4"]');
await page.click('[data-rowmode="Mm5v4"]'); // Mm5v4 改成全量
await page.fill("#actress-search", "");
const trayRows = await page.$$eval("#tray-list li code", (n) => n.map((e) => e.textContent));
check("2 条链接", trayRows.length, 2);
check(
  "追新 = since=今天",
  trayRows.includes(`http://127.0.0.1:8080/rss/actress/EvkJ.xml?since=${today}`),
  true,
);
check(
  "全量 = pages=20",
  trayRows.includes("http://127.0.0.1:8080/rss/actress/Mm5v4.xml?pages=20"),
  true,
);

// 默认模式切全量：逐行覆盖要被清掉，所有人跟上
await page.click('[data-mode="all"]');
const afterMode = await page.$$eval("#tray-list li code", (n) => n.map((e) => e.textContent));
check(
  "切默认模式后逐行覆盖被清掉",
  afterMode.every((u) => u.endsWith("?pages=20")),
  true,
);
await page.click('[data-mode="new"]');

// 服务地址改一行，所有链接当场跟上（不用重新生成）
await page.fill("#base-url", "http://192.168.2.186:8080/");
await page.waitForTimeout(50);
const afterBase = await page.$$eval("#tray-list li code", (n) => n.map((e) => e.textContent));
check(
  "换服务地址后链接重算",
  afterBase.every((u) => u.startsWith("http://192.168.2.186:8080/")),
  true,
);
await page.fill("#base-url", "http://127.0.0.1:8080");
await page.waitForTimeout(50);

// ── 需求 2：想看，单独一个复制按钮 ──
await page.click("#tab-want");
check("想看 URL", await page.textContent("#want-url"), "http://127.0.0.1:8080/rss/want.xml");
await page.click("#want-copy");
await page.waitForTimeout(120);
check("想看复制内容", await page.evaluate(() => navigator.clipboard.readText()), "http://127.0.0.1:8080/rss/want.xml");
check("复制后有反馈", (await page.textContent("#want-copy")).includes("已复制"), true);

// ── 需求 3：标签选择器（女优 × 标签），按上游分组，最多 5 个 ──
await page.click("#tab-tags");
await page.fill("#actress-picker", "河北彩花（EvkJ）");
await page.dispatchEvent("#actress-picker", "change");
check("基本组来自上游词表", await page.$$eval("#main-flags [data-flag]", (n) => n.map((e) => e.dataset.flag).join(",")), "p,m,c,s,i,v");
check(
  "分组与名称原样取自上游",
  await page.$$eval("#tag-groups details summary span:first-child", (n) => n.map((e) => e.textContent).join(",")),
  "主題,角色,服裝,體型,行爲,玩法,類別",
);
check(
  "年份/月份/時長 不作为可点标签出现（它们走本地过滤那一节）",
  await page.$$eval("#tag-groups details", (n) => n.map((e) => e.dataset.group).join(",")),
  "subject,role,cloth,body,behavior,play_method,category",
);
check("默认全部折叠（与 App 一致）", await page.$$eval("#tag-groups details[open]", (n) => n.length), 0);
await page.click("#tag-expand");
check(
  "全部展开按钮生效",
  await page.$$eval("#tag-groups details[open]", (n) => n.length),
  7,
);

// 年/月/时长：上游没有通道，改成服务本地过滤
check(
  "年份是原生 select（26 个选项不铺成 chip）",
  await page.$$eval("#year-select option", (n) => n.slice(0, 3).map((e) => e.textContent.trim()).join(",")),
  "不限,2026 年起,2025 年起",
);
check(
  "月份按日历升序，且标签写明只有下界",
  await page.$$eval("#month-group [data-time]", (n) => n.slice(0, 4).map((e) => e.textContent.trim()).join(",")),
  "不限,1 月起,2 月起,3 月起",
);
await page.selectOption("#year-select", "2024");
check(
  "选年份 -> since= + pages=20（本地筛，强制拉全量）",
  await page.textContent("#tag-url"),
  `http://127.0.0.1:8080/rss/actress/EvkJ.xml?since=2024-01-01&pages=20`,
);
check("年份提示说明只有下界", (await page.textContent("#year-hint")).includes("下界"), true);
await page.selectOption("#year-select", "");

await page.click('[data-flag="c"]'); // 中文字幕
await page.click('[data-tag="46"]'); // 顏射
await page.click('[data-tag="68"]'); // 潮吹（先点 46 后点 68，验证 URL 不按点击顺序）
check(
  "标签 URL（分组词表 id + 稳定排序）",
  await page.textContent("#tag-url"),
  `http://127.0.0.1:8080/rss/actress/EvkJ.xml?since=${today}&filter_by=0%3Aa%3AEvkJ%3Ac%3A%3A&filter_by_tags=46%2C68`,
);
check("已选区可取消", await page.$$eval("#tag-selected [data-unselect]", (n) => n.length), 2);

// 触顶：第 6 个点不动，并且说明为什么
for (const id of [48, 148, 161]) await page.click(`[data-tag="${id}"]`);
check("标签上限 5", await page.textContent("#tag-count"), "5");
check("触顶后第 6 个被禁用", await page.isDisabled('[data-tag="212"]'), true);
check("触顶有解释", (await page.textContent("#tag-empty")).includes("已经选满 5 个"), true);
check("每组带已选计数", (await page.textContent("#tag-groups")).includes("已选"), true);

// 全站模式：标签走**掩码槽位**（抓包反推的那条），不是 filter_by_tags
await page.click('[data-source="site"]');
check("片库四选一", await page.$$eval("#zone-group [data-zone]", (n) => n.length), 4);
// 先清掉女优模式留下的选择，才谈得上「裸」URL
await page.click("[data-clear-tags]");
await page.click('[data-flag="c"]');
check(
  "裸全站 URL 自动带上 m（不发它上游返回的全都没有磁链）",
  await page.textContent("#tag-url"),
  "http://127.0.0.1:8080/rss/tags/0.xml?main=m",
);
check(
  "全站词汇表包含 7 个可筛组共 307 个标签（折叠时也在 DOM 里）",
  await page.$$eval("#tag-groups [data-tag]", (n) => n.length),
  307,
);
// 全站模式下 年/月/时长 全部可用。先选回 c，验证 m 是被「并进去」而不是覆盖。
await page.click('[data-flag="c"]');
if (await page.$$eval("#tag-groups details:not([open])", (n) => n.length)) {
  await page.click("#tag-expand");
}
await page.click('[data-tag="68"]');
await page.click('[data-tag="46"]');
await page.selectOption("#year-select", "2020");
await page.click('#month-group [data-value="3"]');
await page.click('#duration-group [data-value="gt-120"]');
await page.click('#zone-group [data-zone="2"]');
check(
  "全站完整 URL（掩码的六个槽位齐了）",
  await page.textContent("#tag-url"),
  "http://127.0.0.1:8080/rss/tags/2.xml?main=c%2Cm&tags=46%2C68&year=2020&month=3&duration=gt-120",
);
check("全站模式下时长可用（0 个禁用）", await page.$$eval("#duration-group [data-time]", (n) => n.filter((e) => e.disabled).length), 0);

// 切回女优模式：时间维度清空（它们在那条 URL 上根本不出现），并禁用
await page.click('[data-flag="c"]');
await page.click("[data-clear-tags]");
await page.click('[data-source="actress"]');
check(
  "切回女优模式后时间维度不残留",
  await page.textContent("#tag-url"),
  `http://127.0.0.1:8080/rss/actress/EvkJ.xml?since=${today}`,
);
check(
  "女优模式下时长禁用（尾部槽位未验证）",
  await page.$$eval("#duration-group [data-time]", (n) => n.filter((e) => e.disabled).length),
  4,
);
// 清掉，免得影响后面的服务地址断言
if (await page.$("[data-clear-tags]")) await page.click("[data-clear-tags]");

// ── 需求 4：清单（后端已实现），每份清单一条可复制的链接 ──
await page.click("#tab-lists");
check("5 份真实清单", await page.$$eval("#list-cards li", (n) => n.length), 5);
check(
  "清单按钮可用（路由已经做了）",
  await page.$$eval("#list-cards [data-list-copy]", (b) => b.every((x) => !x.disabled)),
  true,
);
check(
  "清单 feed 路径",
  await page.$eval("#list-cards code", (e) => e.textContent),
  "http://127.0.0.1:8080/rss/list/k4EVE4.xml",
);
check("默认清单有标记", (await page.textContent("#list-cards")).includes("默认"), true);
await page.click("#list-cards [data-list-copy]");
await page.waitForTimeout(120);
check(
  "清单复制内容",
  await page.evaluate(() => navigator.clipboard.readText()),
  "http://127.0.0.1:8080/rss/list/k4EVE4.xml",
);
await page.click("#list-cards [data-list-add]");
check(
  "清单加入待复制",
  await page.$$eval("#tray-list code", (n) => n.some((e) => e.textContent.includes("/rss/list/"))),
  true,
);
// 清掉，免得影响后面的服务地址断言
await page.click("#tray-clear");

// ── 复制回退：非安全上下文（局域网 http）下 navigator.clipboard 不存在 ──
await page.click("#tab-want");
const fallback = await page.evaluate(async () => {
  const real = Object.getOwnPropertyDescriptor(Navigator.prototype, "clipboard");
  Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  let used = "";
  document.execCommand = (cmd) => {
    used = cmd;
    return true;
  };
  document.getElementById("want-copy").click();
  await new Promise((r) => setTimeout(r, 80));
  if (real) Object.defineProperty(Navigator.prototype, "clipboard", real);
  return used;
});
check("剪贴板不可用时走 execCommand 回退", fallback, "copy");

// ── 状态：空收藏 / 没 token，文案要指向下一步动作 ──
await page.goto(origin + "#state=notoken", { waitUntil: "load" });
const notoken = await page.textContent("#actress-empty");
check("没 token 时提到 token_file", notoken.includes("token_file"), true);
check("没 token 时说明订阅不受影响", notoken.includes("不需要 token"), true);
await page.goto(origin + "#state=empty", { waitUntil: "load" });
check("空收藏是一个独立的说法", (await page.textContent("#actress-empty")).includes("收藏列表是空的"), true);

if (pageErrors.length) {
  fails.push(`页面抛了异常: ${pageErrors.join(" | ")}`);
}

await browser.close();

console.log(fails.length ? `FAIL (${fails.length})` : `PASS (${ok.length} 项)`);
for (const f of fails) console.log("  ✗ " + f);
process.exit(fails.length ? 1 : 0);
