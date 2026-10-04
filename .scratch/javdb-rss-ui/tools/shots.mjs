/*
 * 截图工具（评审用，不进产物）。
 *
 *   NODE_PATH=<某个装了 playwright 的 node_modules> node tools/shots.mjs <origin> <outDir>
 *
 * score_mockup 需要它自己的 playwright；这里是同一件事的最小版本，
 * 顺带把四个分区与深色模式都拍下来，方便逐个对照 rubric。
 */
import { chromium } from "playwright";
import { mkdirSync } from "node:fs";

const origin = process.argv[2] || "http://localhost:62100";
const outDir = process.argv[3] || "./shots";
mkdirSync(outDir, { recursive: true });

const executablePath =
  process.env.CHROME_PATH ||
  `${process.env.HOME}/Library/Caches/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-mac-arm64/chrome-headless-shell`;

const browser = await chromium.launch({ executablePath });

const shots = [
  { name: "actress", width: 375, height: 900, tab: null, theme: "light" },
  { name: "actress", width: 768, height: 900, tab: null, theme: "light" },
  { name: "actress", width: 1440, height: 900, tab: null, theme: "light" },
  { name: "actress-dark", width: 1440, height: 900, tab: null, theme: "dark" },
  { name: "actress-selected", width: 1440, height: 900, tab: null, theme: "light", select3: true },
  { name: "tags", width: 1440, height: 900, tab: "#tab-tags", theme: "light", pickTags: true },
  { name: "tags-mobile", width: 375, height: 900, tab: "#tab-tags", theme: "light", pickTags: true },
  { name: "tags-dark", width: 1440, height: 900, tab: "#tab-tags", theme: "dark", pickTags: true },
  { name: "tags-site", width: 1440, height: 900, tab: "#tab-tags", theme: "light", siteMode: true },
  { name: "lists", width: 1440, height: 900, tab: "#tab-lists", theme: "light" },
  { name: "want", width: 1440, height: 900, tab: "#tab-want", theme: "light" },
  { name: "want-dark", width: 1440, height: 900, tab: "#tab-want", theme: "dark" },
  { name: "state-notoken", width: 1440, height: 900, tab: null, theme: "light", hash: "#state=notoken" },
  { name: "state-empty", width: 1440, height: 900, tab: null, theme: "light", hash: "#state=empty" },
];

for (const s of shots) {
  const ctx = await browser.newContext({
    viewport: { width: s.width, height: s.height },
    // 1x 而不是 2x：这些图是**给人看的**（浏览器里 1:1 就这么大），
    // 而它们要进仓库 —— 2x 会让 14 张图占 10MB。要更清楚就改这里重跑。
    deviceScaleFactor: 1,
    colorScheme: s.theme,
  });
  const page = await ctx.newPage();
  await page.goto(origin + (s.hash || ""), { waitUntil: "load" });
  if (s.theme === "dark") {
    await page.emulateMedia({ colorScheme: "dark" });
    await page.evaluate(() => document.documentElement.classList.add("dark"));
  }
  if (s.tab) await page.click(s.tab);
  if (s.siteMode) await page.click('[data-source="site"]');
  if (s.select3) {
    for (const id of ["D2EdJ", "Mm5v4", "6595K", "EvkJ"]) {
      await page.check(`[data-actress="${id}"]`);
    }
    await page.click('[data-rowmode="Mm5v4"]');
    await page.click("#tray-toggle");
  }
  if (s.pickTags) {
    await page.click("#tag-expand");
    await page.click('[data-flag="c"]');
    await page.click('[data-tag="17"]'); // 巨乳
    await page.click('[data-tag="23"]'); // 淫亂真實
  }
  await page.waitForTimeout(300);
  await page.screenshot({ path: `${outDir}/${s.name}-${s.width}.png`, fullPage: true });
  await ctx.close();
  console.log("shot", s.name, s.width);
}

await browser.close();
