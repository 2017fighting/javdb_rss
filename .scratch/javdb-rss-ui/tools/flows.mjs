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
 * 标签（04/05）、清单与想看（06）、状态可见性（07）的断言在各自的票里补。
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
check("全部演员 = 3", await page.textContent("#actress-count"), "3");
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

await browser.close();

console.log(fails.length ? `FAIL (${fails.length})` : `PASS (${ok.length} 项)`);
for (const f of fails) console.log("  ✗ " + f);
process.exit(fails.length ? 1 : 0);
