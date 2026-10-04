/*
 * 硬闸门验证（评审用，不进产物）。
 *
 *   node tools/a11y.mjs <origin>
 *
 * 干三件 score_mockup 在这台机器上做不到的事：
 *   1. 真跑 axe-core（WCAG 2 A/AA）—— 明暗两套、三个断点。
 *   2. 量触摸目标尺寸（WCAG 2.5.8 AA ≥24px）。
 *   3. 量焦点可见性、横向溢出、reduced-motion 是否被尊重。
 * 结果算在代码里，不靠「看起来不错」。
 *
 * 它打的是 **provider=stub 的真服务**。票 03 只有「收藏女优」一个分区，
 * 票 04 补回了「某位女优的标签」分区（展开分组 + 选中一颗标签也算进"有内容"态）。
 * 票 06 补上「清单」与「想看」：卡片与它们在待复制里的行也进 axe。
 */
import { chromium } from "playwright";
import { readFileSync } from "node:fs";

const origin = (process.argv[2] || "http://127.0.0.1:8080").replace(/\/+$/, "");
const axe = readFileSync(
  process.env.AXE_PATH ||
    "/Users/raincore/.pi/agent/npm/node_modules/axe-core/axe.min.js",
  "utf8",
);

const executablePath =
  process.env.CHROME_PATH ||
  `${process.env.HOME}/Library/Caches/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-mac-arm64/chrome-headless-shell`;

const browser = await chromium.launch({ executablePath });
const failures = [];
const notes = [];

for (const width of [375, 768, 1440]) {
  for (const theme of ["light", "dark"]) {
    const ctx = await browser.newContext({
      viewport: { width, height: 900 },
      colorScheme: theme,
      reducedMotion: "reduce",
    });
    const page = await ctx.newPage();
    await page.goto(origin, { waitUntil: "load" });
    await page.waitForSelector("#actress-list [data-actress]");
    // 标签区（票 04）：等她的标签落位 —— 否则 axe 量到的是「正在读取」那个态。
    await page.waitForFunction(() => document.querySelectorAll("#tag-groups details").length > 0);
    // 清单区（票 06）：卡片来自 /collected_lists，等它落位再跑 axe。
    await page.waitForSelector("#list-cards [data-list-copy]");
    if (theme === "dark") {
      await page.evaluate(() => document.documentElement.classList.add("dark"));
    }

    // ── 1. axe：空态与填满态都跑一遍 ──
    // 空页面过闸门而填满后不过，是这种页面最容易漏的一种。
    await page.addScriptTag({ content: axe });
    const axeRun = async (label) => {
      const res = await page.evaluate(async () =>
        await window.axe.run(document, {
          runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] },
        }),
      );
      for (const v of res.violations) {
        const nodes = v.nodes
          .slice(0, 3)
          .map((n) => `${n.target.join(" ")} :: ${(n.html || "").slice(0, 90)}`)
          .join(" || ");
        failures.push(
          `axe[${width}/${theme}] ${label} ${v.id} (${v.impact}) ×${v.nodes.length}: ${v.help} — ${nodes}`,
        );
      }
    };
    await axeRun("initial");

    // 先造出「有内容」的状态：选中的行 + 展开的标签分组与选中的标签
    // + 清单与想看各一条 + 展开的待复制面板。空页面过闸门、填满后不过，
    // 是这种页面最容易漏的一种。
    await page.waitForSelector("#want-add:not([disabled])");
    await page.click("#list-cards [data-list-add]");
    await page.click("#tray-toggle"); // 加一条会展开工具条，先收起
    await page.click("#want-add");
    await page.click("#tray-toggle");
    for (const id of ["EvkJ", "D2EdJ"]) await page.check(`[data-actress="${id}"]`);
    await page.click("#tag-expand");
    await page.click('#tag-groups [data-tag="3"]');
    await page.click("#tray-toggle");
    await axeRun("populated");

    // ── 2. 触摸目标尺寸（WCAG 2.5.8 AA ≥24px）──
    // 两个例外照实算，不当成违规：
    //   · 用 <label> 包着的控件 —— 可点区域是整个 label，量它。
    //   · 正文里的行内链接 —— 规范明文豁免（"inline" exception）。
    const small = await page.evaluate(() => {
      const sel =
        'button:not([disabled]), a[href], input:not([type=hidden]), select, [role=radio]';
      const out = [];
      for (const e of document.querySelectorAll(sel)) {
        const r = e.getBoundingClientRect();
        if (r.width === 0 || r.height === 0) continue;
        if (e.classList.contains("sr-only") || e.classList.contains("skip-link")) continue;
        if (getComputedStyle(e).display === "inline") continue;
        const lbl = e.tagName === "INPUT" ? e.closest("label") : null;
        const box = lbl ? lbl.getBoundingClientRect() : r;
        const w = Math.min(box.width, window.innerWidth);
        if (w < 24 || box.height < 24) {
          out.push(
            `${e.tagName.toLowerCase()}#${e.id || "-"} ${Math.round(w)}×${Math.round(box.height)}`,
          );
        }
      }
      return [...new Set(out)];
    });
    if (small.length) failures.push(`target<24px [${width}/${theme}]: ${small.join(" | ")}`);

    // ── 3. 焦点可见性：真的用 Tab 走一圈（:focus-visible 只在键盘触发下成立）──
    const focus = [];
    // 够走到待复制工具条：标签区的牌（chip）不少，8 步已经不购了。
    for (let i = 0; i < 60; i++) {
      await page.keyboard.press("Tab");
      const info = await page.evaluate(() => {
        const e = document.activeElement;
        if (!e || e === document.body) return null;
        const s = getComputedStyle(e);
        const visible =
          (s.outlineStyle !== "none" && parseFloat(s.outlineWidth) > 0) ||
          (s.boxShadow && s.boxShadow !== "none");
        return {
          tag: `${e.tagName.toLowerCase()}#${e.id || "-"}`,
          visible,
          id: e.id || "",
        };
      });
      if (info && !info.visible) focus.push(info.tag);
      if (info && info.id === "tray-toggle") break;
    }
    if (focus.length) {
      failures.push(`no visible focus [${width}/${theme}]: ${focus.join(", ")}`);
    }

    // ── 4. 一屏一个主操作（Von Restorff + H8）──
    // 分两个区域算：内容区（视图）与固定工具条（待复制）。两个区域各最多一个，
    // 但刻意允许共存 —— 规则写在 ui-contract.md 里。
    const primaries = await page.evaluate(() => {
      const vis = [...document.querySelectorAll('[class*="btn-primary"]')].filter(
        (e) => e.offsetParent !== null,
      );
      const inTray = vis.filter((e) => e.closest(".fixed")).length;
      return { view: vis.length - inTray, tray: inTray };
    });
    if (primaries.view > 1) failures.push(`主操作不止一个 [${width}/${theme}]: 内容区 ×${primaries.view}`);
    if (primaries.tray > 1) failures.push(`主操作不止一个 [${width}/${theme}]: 工具条 ×${primaries.tray}`);

    // ── 5. 横向溢出（WCAG 1.4.10 reflow）──
    const o = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      wide: [...document.querySelectorAll("body *")]
        .filter((e) => e.getBoundingClientRect().width > window.innerWidth + 1)
        .map((e) => `${e.tagName.toLowerCase()}#${e.id || "-"}`)
        .slice(0, 5),
    }));
    if (o.doc > 1) failures.push(`横向溢出 [${width}/${theme}]: +${o.doc}px (${o.wide.join(", ")})`);

    // ── 6. reduced-motion 是否被尊重 ──
    const motion = await page.evaluate(() => getComputedStyle(document.querySelector("#tray-copy-all")).transitionDuration);
    notes.push(`reduced-motion transitionDuration [${width}/${theme}] = ${motion}`);

    // ── 7. 全站模式（票 05）：片库 chip、月份/时长 chip、掩码说明都只在
    //       那个模式下可见 —— 只在女优模式下跑 axe 会漏掉它们。──
    const trayOpen = await page.$eval("#tray-panel", (e) => !e.hidden);
    if (trayOpen) await page.click("#tray-toggle");
    await page.click('[data-source="site"]');
    await page.waitForFunction(
      () => document.querySelectorAll('#tag-groups details[data-group="subject"]').length > 0,
    );
    await axeRun("site");

    await ctx.close();
  }
}

await browser.close();

console.log(failures.length ? "FAIL" : "PASS");
for (const f of failures) console.log("  ✗ " + f);
if (!failures.length) console.log("  ✓ axe (a11y AA) / 触摸目标 / 焦点可见 / 无横向溢出");
console.log("notes:");
for (const n of notes.slice(0, 3)) console.log("  · " + n);
process.exit(failures.length ? 1 : 0);
