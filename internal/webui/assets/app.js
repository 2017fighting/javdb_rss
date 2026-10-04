/*
 * 订阅链接生成器 —— 页面交互。
 *
 * 页面是**服务自己的一个端点**，不是另一套前端工程：
 *   · 数据只从服务自己的发现端点来（/collected）；
 *   · 生成的链接只发**语义参数**，掩码一律由服务构造（前端不拼 filter_by）；
 *   · 除了「服务地址 + 主题」，什么都不持久化 —— 选择与待复制只活在内存里。
 *
 * 刻意实现的两件事（设计稿里就要证明它们成立）：
 *   1. 复制在 http（局域网）下没有 navigator.clipboard —— 走 execCommand 回退，
 *      再失败就把链接摊开让人手动选。粘不出来比看起来差点严重得多。
 *   2. 读不到收藏时**照实说出服务给的那句话**，而不是给一个空列表 ——
 *      空列表会被理解成「你没收藏任何人」，而真相是「服务读不到」。
 */
(() => {
  "use strict";

  const BASE_KEY = "javdb-rss-base";
  const THEME_KEY = "javdb-rss-theme";
  // 动作反馈的时长。与 ui-contract.md 的「1.8s」一致。
  const FEEDBACK_MS = 1800;

  const $ = (id) => document.getElementById(id);
  const el = {
    html: document.documentElement,
    base: $("base-url"),
    themeToggle: $("theme-toggle"),
    themeIcon: $("theme-icon"),
    actressSearch: $("actress-search"),
    actressList: $("actress-list"),
    actressEmpty: $("actress-empty"),
    actressStatus: $("actress-status"),
    actressCount: $("actress-count"),
    selectAll: $("actress-select-all"),
    clearSel: $("actress-clear"),
    genderGroup: $("gender-group"),
    genderLabel: $("gender-label"),
    modeGroup: $("mode-group"),
    todayEcho: $("today-echo"),
    trayCount: $("tray-count"),
    trayNote: $("tray-note"),
    trayToggle: $("tray-toggle"),
    trayPanel: $("tray-panel"),
    trayList: $("tray-list"),
    trayCopyAll: $("tray-copy-all"),
    trayClear: $("tray-clear"),
    live: $("live"),
  };

  // ───────────────────────────── 状态 ─────────────────────────────

  const state = {
    // 收藏里有男优，所以默认只看女优（服务透出的 gender：0 = 女优，1 = 男优）。
    gender: "female", // female | all
    // 女优区新链接的默认模式，逐行可覆盖。
    mode: "new", // new | all
    rowMode: new Map(), // id -> new | all
    selected: new Set(),
    search: "",
    // 从 /collected 读到的收藏。loading / error 是它的两种非正常态。
    actresses: [],
    loading: true,
    error: null, // {status, message}
    // 待复制。只存「怎么生成」，URL 每次现算 —— 换服务地址要立刻跟上。
    links: [],
  };

  // ─────────────────────────── 小工具 ───────────────────────────

  const ESCAPES = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };
  /** 名字与 id 来自上游，拼进 innerHTML 之前必须转义。 */
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ESCAPES[c]);

  /** 服务地址。留空时退回打开页面的那个源 —— 它总是对的。 */
  function base() {
    return el.base.value.trim().replace(/\/+$/, "") || location.origin;
  }

  function todayISO() {
    const d = new Date();
    const p = (n) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  }

  function announce(msg) {
    el.live.textContent = "";
    window.setTimeout(() => {
      el.live.textContent = msg;
    }, 30);
  }

  const actressById = (id) => state.actresses.find((a) => a.id === id);
  const modeOf = (id) => state.rowMode.get(id) || state.mode;

  // ─────────────────────────── URL 构造 ───────────────────────────
  //
  // 前端只发**语义参数**：追新 = since=<今天>，全量 = pages=20。
  // filter_by / filter_by_tags 一律不出现 —— 掩码由服务构造（掩码的段数
  // 是个「多一段就静默失效」的坑，不该有两处实现）。

  function actressUrl(id, mode) {
    const path = `${base()}/rss/actress/${encodeURIComponent(id)}.xml`;
    return mode === "all" ? `${path}?pages=20` : `${path}?since=${todayISO()}`;
  }

  /** 待复制里的每条 URL 都在渲染时现算。 */
  function linkUrl(link) {
    if (link.kind === "actress") return actressUrl(link.id, link.mode);
    return "";
  }

  // ─────────────────────────── 复制 ───────────────────────────
  // http 局域网下 navigator.clipboard 不存在（非安全上下文），必须有回退。

  async function copyText(text) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch (e) {
      /* 落到回退 */
    }
    try {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed";
      ta.style.top = "-1000px";
      document.body.appendChild(ta);
      ta.select();
      ta.setSelectionRange(0, text.length);
      const ok = document.execCommand("copy");
      ta.remove();
      return ok;
    } catch (e) {
      return false;
    }
  }

  function flash(btn, label) {
    if (btn.dataset.busy === "1") return;
    btn.dataset.busy = "1";
    const original = btn.textContent;
    btn.textContent = label;
    btn.classList.add("border-foreground");
    window.setTimeout(() => {
      btn.textContent = original;
      btn.classList.remove("border-foreground");
      delete btn.dataset.busy;
    }, FEEDBACK_MS);
  }

  async function doCopy(btn, text, what) {
    const ok = await copyText(text);
    if (ok) {
      flash(btn, "已复制 ✓");
      announce(`${what}已复制到剪贴板`);
      return true;
    }
    // 复制失败不能只是静默 —— 把链接摊开，让人能手动选中。
    setTrayOpen(true);
    announce("复制失败：浏览器不给写剪贴板。链接已展开，请手动选中复制。");
    const warn = document.createElement("p");
    warn.className =
      "mx-1 mt-1 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-foreground";
    warn.setAttribute("role", "alert");
    warn.textContent =
      "复制失败：当前页面不是 https，浏览器不给写剪贴板。下面这条链接可以直接选中复制。";
    el.trayPanel.prepend(warn);
    window.setTimeout(() => warn.remove(), 6000);
    return false;
  }

  // ────────────────────── 单选框组（分段控件） ──────────────────────

  function radioGroup(container, onPick) {
    const btns = [...container.querySelectorAll('[role="radio"]')];
    const value = (b) =>
      b.dataset.mode ?? b.dataset.gender ?? b.dataset.source ?? b.dataset.tagmode;
    const select = (v, focus) => {
      btns.forEach((b) => {
        const on = value(b) === v;
        b.setAttribute("aria-checked", String(on));
        b.tabIndex = on ? 0 : -1;
        if (on && focus) b.focus();
      });
      onPick(v);
    };
    btns.forEach((b, i) => {
      b.addEventListener("click", () => select(value(b), false));
      b.addEventListener("keydown", (e) => {
        const step =
          e.key === "ArrowRight" || e.key === "ArrowDown"
            ? 1
            : e.key === "ArrowLeft" || e.key === "ArrowUp"
              ? -1
              : e.key === "Home"
                ? -Infinity
                : e.key === "End"
                  ? Infinity
                  : 0;
        if (!step) return;
        e.preventDefault();
        const next =
          step === -Infinity
            ? 0
            : step === Infinity
              ? btns.length - 1
              : (i + step + btns.length) % btns.length;
        select(value(btns[next]), true);
      });
    });
    btns.forEach((b) => {
      b.tabIndex = b.getAttribute("aria-checked") === "true" ? 0 : -1;
    });
    return select;
  }

  // ─────────────────────────── 数据读取 ───────────────────────────

  /**
   * 读 /collected。失败时**照实保留服务给的那句话** —— 它才是用户要照做的
   * 下一步（比如去配 token_file），而不是这里编一句「加载失败」。
   */
  async function loadCollected() {
    state.loading = true;
    state.error = null;
    renderActors();
    try {
      const res = await fetch("/collected", { headers: { accept: "application/json" } });
      const body = await res.json().catch(() => null);
      if (!res.ok) {
        state.actresses = [];
        state.error = {
          status: res.status,
          message:
            body && typeof body.error === "string" && body.error
              ? body.error
              : `服务返回 ${res.status}`,
        };
      } else {
        state.actresses = Array.isArray(body?.actresses) ? body.actresses : [];
      }
    } catch (e) {
      state.actresses = [];
      state.error = { status: 0, message: `读不到服务：${e.message}` };
    }
    state.loading = false;
    // 服务换了之后，选择里可能有已经不存在的 id —— 清掉，免得待复制里
    // 留着一条指向幽灵的链接。
    const known = new Set(state.actresses.map((a) => a.id));
    for (const id of [...state.selected]) {
      if (!known.has(id)) state.selected.delete(id);
    }
    renderAll();
  }

  // ─────────────────────────── 女优列表 ───────────────────────────

  function visibleActors() {
    const q = state.search.trim().toLowerCase();
    return state.actresses.filter((a) => {
      if (state.gender === "female" && a.gender !== 0) return false;
      if (!q) return true;
      return (
        String(a.name || "").toLowerCase().includes(q) ||
        String(a.id || "").toLowerCase().includes(q)
      );
    });
  }

  function showEmpty(html, className) {
    el.actressList.innerHTML = "";
    el.actressEmpty.className = className;
    el.actressEmpty.innerHTML = html;
    el.actressEmpty.classList.remove("hidden");
  }

  function renderActors() {
    const rows = visibleActors();
    el.actressCount.textContent = String(rows.length);

    if (state.loading) {
      showEmpty(
        `<p class="text-sm font-medium">正在读取收藏…</p>
         <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-muted-foreground">从服务的 <code class="font-mono">/collected</code> 读。</p>`,
        "rounded-lg border border-dashed border-border px-4 py-10 text-center",
      );
      syncSelectionSummary();
      return;
    }

    if (state.error) {
      const title =
        state.error.status === 503
          ? "读不到收藏：还没配置 token"
          : state.error.status >= 500
            ? "读不到收藏：上游出错了"
            : "读不到收藏";
      showEmpty(
        `<p class="flex items-start gap-2 text-sm font-medium">
           <span aria-hidden="true">⛔</span>${esc(title)}
         </p>
         <p class="mt-2 text-xs leading-relaxed text-muted-foreground" data-server-message></p>
         <button class="btn btn-md btn-primary mt-4" type="button" data-retry>重新读取</button>`,
        "rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-6 text-left",
      );
      // 服务的原话原样显示（含它给的下一步动作），不在这里改写。
      el.actressEmpty.querySelector("[data-server-message]").textContent = state.error.message;
      el.actressEmpty
        .querySelector("[data-retry]")
        .addEventListener("click", () => loadCollected());
      syncSelectionSummary();
      return;
    }

    if (!state.actresses.length) {
      showEmpty(
        `<p class="text-sm font-medium">收藏里一个人也没有</p>
         <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-muted-foreground">
           服务读到了你的账号，但收藏列表是空的 —— 还没在 JavDB App 里点过「收藏」。
           收藏之后刷新这一页就行。
         </p>
         <button class="btn btn-md btn-primary mt-4" type="button" data-retry>重新读取</button>`,
        "rounded-lg border border-dashed border-border px-4 py-10 text-center",
      );
      el.actressEmpty
        .querySelector("[data-retry]")
        .addEventListener("click", () => loadCollected());
      syncSelectionSummary();
      return;
    }

    if (!rows.length) {
      showEmpty(
        `<p class="text-sm font-medium">没有匹配的演员</p>
         <p class="mt-1.5 text-xs text-muted-foreground">换个名字或 id 试试，或者把筛选放宽到「全部演员」。</p>
         <button class="btn btn-md btn-outline mt-4" type="button" data-reset>重置筛选</button>`,
        "rounded-lg border border-dashed border-border px-4 py-10 text-center",
      );
      el.actressEmpty.querySelector("[data-reset]").addEventListener("click", () => {
        el.actressSearch.value = "";
        state.search = "";
        selectGender("all");
      });
      syncSelectionSummary();
      return;
    }

    el.actressEmpty.classList.add("hidden");
    el.actressList.innerHTML = rows
      .map((a) => {
        const on = state.selected.has(a.id);
        const mode = modeOf(a.id);
        const male = a.gender !== 0;
        return `
        <li data-row="${esc(a.id)}" class="flex items-center gap-3 px-3 py-1 transition-colors hover:bg-accent/40">
          <label class="flex min-w-0 flex-1 cursor-pointer items-center gap-3 py-1.5">
            <input type="checkbox" class="size-4 shrink-0 accent-[hsl(var(--primary))]"
              data-actress="${esc(a.id)}" ${on ? "checked" : ""} />
            <span class="min-w-0 truncate text-sm">
              <span class="font-medium">${esc(a.name)}</span>
              <span class="ms-1.5 font-mono text-xs text-muted-foreground">${esc(a.id)}</span>
              ${male ? '<span class="ms-1.5 rounded-sm border border-border px-1 text-2xs text-muted-foreground">男优</span>' : ""}
            </span>
          </label>
          <span class="hidden w-16 shrink-0 text-right text-xs text-muted-foreground tnum md:block">${Number(a.videos_count) || 0} 部</span>
          <button type="button" class="btn btn-sm btn-outline w-20 shrink-0 md:w-24"
            data-rowmode="${esc(a.id)}"
            aria-label="${esc(a.name)} 的链接模式现在是${mode === "all" ? "全量" : "追新"}，点击切换"
            title="点击切换 追新 / 全量">
            ${mode === "all" ? "全量" : "追新"}
          </button>
        </li>`;
      })
      .join("");

    syncSelectionSummary();
  }

  function syncSelectionSummary() {
    const n = state.selected.size;
    const all = [...state.selected].filter((id) => modeOf(id) === "all").length;
    if (!n) {
      el.actressStatus.textContent = "";
      return;
    }
    el.actressStatus.textContent = `已选 ${n} 位 · ${all ? `${all} 条全量（较慢）` : "全部追新"}`;
  }

  /** 待复制里的女优链接**完全由选择推导** —— 没有第二处存 URL 的地方。 */
  function syncTray() {
    const keep = state.links.filter((l) => l.kind !== "actress");
    const picked = [...state.selected]
      .map(actressById)
      .filter(Boolean)
      .sort((a, b) => state.actresses.indexOf(a) - state.actresses.indexOf(b))
      .map((a) => ({
        key: `actress:${a.id}`,
        kind: "actress",
        id: a.id,
        mode: modeOf(a.id),
        label: a.name,
      }));
    state.links = [...keep, ...picked];
    renderTray();
  }

  // ─────────────────────────── 待复制 ───────────────────────────

  function renderTray() {
    const n = state.links.length;
    const slow = state.links.filter((l) => l.mode === "all").length;

    el.trayCount.textContent = String(n);
    el.trayNote.textContent =
      n === 0 ? "挑好之后一次粘进 qBittorrent" : slow ? `其中 ${slow} 条是全量，会比较慢` : "";
    el.trayCopyAll.disabled = n === 0;
    el.trayClear.disabled = n === 0;

    if (!n) {
      el.trayList.innerHTML = `
        <li class="px-1 py-6 text-center">
          <p class="text-sm font-medium">还没有链接</p>
          <p class="mx-auto mt-1.5 max-w-sm text-xs leading-relaxed text-muted-foreground">
            去上面的「收藏女优」里勾几位，勾中的每一位都会在这里变成一条链接。
          </p>
        </li>`;
      return;
    }

    el.trayList.innerHTML = state.links
      .map(
        (l, i) => `
      <li class="flex items-start gap-3 py-3">
        <div class="min-w-0 flex-1">
          <p class="flex flex-wrap items-center gap-1.5">
            <span class="text-sm font-medium">${esc(l.label)}</span>
            <span class="rounded-sm border border-border px-1 font-mono text-2xs text-muted-foreground">
              ${l.mode === "all" ? "全量" : "追新"}
            </span>
          </p>
          <code class="mt-1 block font-mono text-xs break-all text-muted-foreground select-all">${esc(linkUrl(l))}</code>
        </div>
        <div class="flex shrink-0 gap-1">
          <button class="btn btn-sm btn-outline" type="button" data-copy-row="${i}">复制</button>
          <button class="btn btn-sm btn-ghost" type="button" data-remove-row="${i}"
            aria-label="移除 ${esc(l.label)}">移除</button>
        </div>
      </li>`,
      )
      .join("");
  }

  function setTrayOpen(open) {
    el.trayPanel.hidden = !open;
    el.trayToggle.setAttribute("aria-expanded", String(open));
    el.trayToggle.textContent = open ? "收起" : "查看";
  }

  // ─────────────────────────── 事件 ───────────────────────────

  function selectGender(v) {
    state.gender = v;
    // 被筛掉的男优如果已在选择里，要跟着松掉 —— 否则「看不见却还算数」。
    if (v === "female") {
      for (const id of [...state.selected]) {
        const a = actressById(id);
        if (a && a.gender !== 0) state.selected.delete(id);
      }
    }
    renderActors();
    syncTray();
  }

  radioGroup(el.genderGroup, selectGender);

  radioGroup(el.modeGroup, (v) => {
    state.mode = v;
    // 默认模式变了，逐行的覆盖就没意义了 —— 清掉，让所有人跟上新默认。
    state.rowMode.clear();
    renderActors();
    syncTray();
  });

  el.actressSearch.addEventListener("input", () => {
    state.search = el.actressSearch.value;
    renderActors();
  });

  el.actressList.addEventListener("change", (e) => {
    const box = e.target.closest("[data-actress]");
    if (!box) return;
    if (box.checked) state.selected.add(box.dataset.actress);
    else state.selected.delete(box.dataset.actress);
    // 刻意**不**重渲染整个列表：那会把刚点过的元素换掉，键盘焦点当场丢失。
    syncSelectionSummary();
    syncTray();
  });

  el.actressList.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-rowmode]");
    if (!btn) return;
    const id = btn.dataset.rowmode;
    const next = modeOf(id) === "all" ? "new" : "all";
    state.rowMode.set(id, next);
    // 同上：只改这一个按钮，不动 DOM 的其余部分。
    const a = actressById(id);
    const name = a ? a.name : id;
    btn.textContent = next === "all" ? "全量" : "追新";
    btn.setAttribute(
      "aria-label",
      `${name} 的链接模式现在是${next === "all" ? "全量" : "追新"}，点击切换`,
    );
    syncSelectionSummary();
    syncTray();
    announce(`${name} 改为${next === "all" ? "全量" : "追新"}`);
  });

  el.selectAll.addEventListener("click", () => {
    visibleActors().forEach((a) => state.selected.add(a.id));
    renderActors();
    syncTray();
    announce(`已选 ${state.selected.size} 位`);
  });

  el.clearSel.addEventListener("click", () => {
    state.selected.clear();
    state.rowMode.clear();
    renderActors();
    syncTray();
    announce("已清空选择");
  });

  el.trayToggle.addEventListener("click", () => {
    setTrayOpen(el.trayPanel.hidden);
  });

  el.trayList.addEventListener("click", (e) => {
    const copyBtn = e.target.closest("[data-copy-row]");
    if (copyBtn) {
      const l = state.links[Number(copyBtn.dataset.copyRow)];
      if (l) doCopy(copyBtn, linkUrl(l), `${l.label} 的链接`);
      return;
    }

    const rm = e.target.closest("[data-remove-row]");
    if (rm) {
      const i = Number(rm.dataset.removeRow);
      const l = state.links[i];
      if (!l) return;
      // Actress 链接不是独立实体，而是选择的投影：移除它等于取消勾选，
      // 否则「列表里没有、选择里还在」这种不一致下次重渲染就会自己长回来。
      state.selected.delete(l.id);
      state.rowMode.delete(l.id);
      const box = el.actressList.querySelector(`[data-actress="${CSS.escape(l.id)}"]`);
      if (box) box.checked = false;
      syncSelectionSummary();
      syncTray();
      // 被移除的按钮已经不在了 —— 把焦点接到下一个同类控件上，
      // 否则键盘用户会被扔回 <body>。
      const nextBtn = el.trayList.querySelector("[data-remove-row]") || el.trayToggle;
      nextBtn.focus();
      announce(`已移除 ${l.label}`);
    }
  });

  el.trayCopyAll.addEventListener("click", () => {
    doCopy(el.trayCopyAll, state.links.map(linkUrl).join("\n"), `全部 ${state.links.length} 条链接`);
  });

  el.trayClear.addEventListener("click", () => {
    state.links = [];
    state.selected.clear();
    state.rowMode.clear();
    if (el.actressList) {
      el.actressList.querySelectorAll("[data-actress]").forEach((b) => {
        b.checked = false;
      });
    }
    renderActors();
    renderTray();
    announce("已清空待复制");
  });

  el.base.addEventListener("input", () => {
    try {
      localStorage.setItem(BASE_KEY, el.base.value.trim());
    } catch (e) {}
    renderTray();
  });

  // 主题：跟随系统，可手动覆盖，只有这一项进 localStorage。
  function syncThemeButton() {
    const dark = el.html.classList.contains("dark");
    el.themeToggle.setAttribute("aria-pressed", String(dark));
    el.themeIcon.textContent = dark ? "◑" : "◐";
  }
  el.themeToggle.addEventListener("click", () => {
    const dark = !el.html.classList.contains("dark");
    el.html.classList.toggle("dark", dark);
    try {
      localStorage.setItem(THEME_KEY, dark ? "dark" : "light");
    } catch (e) {}
    syncThemeButton();
  });

  // ─────────────────────────── 启动 ───────────────────────────

  function renderAll() {
    renderActors();
    syncTray();
    // 收藏里到底有几位男优 —— 不然「只看女优 / 全部演员」这个开关看上去没由来。
    const males = state.actresses.filter((a) => a.gender !== 0).length;
    el.genderLabel.textContent = males ? `收藏里有 ${males} 位男优` : "只看女优";
  }

  try {
    el.base.value = localStorage.getItem(BASE_KEY) || location.origin;
  } catch (e) {
    el.base.value = location.origin;
  }
  el.todayEcho.textContent = todayISO();
  syncThemeButton();
  renderTray();
  loadCollected();
})();
