/*
 * javdb-rss-ui mockup — 交互。
 *
 * 数据是**真的**：src/data.js 由 tools/gen-data.mjs 从 evidence/ 里
 * contractprobe 抓到的上游原始响应生成。定稿之后才谈接进 Go 服务。
 *
 * 所有 URL 都按服务真实契约拼：
 *   /rss/actress/{id}.xml?since=YYYY-MM-DD | pages=20
 *   /rss/actress/{id}.xml?filter_by=0:a:{id}:c,m::&filter_by_tags=68,46
 *   /rss/want.xml
 *
 * 刻意实现的两件事（设计稿里就要证明它们成立）：
 *   1. 复制在 http（局域网）下没有 navigator.clipboard —— 走 execCommand 回退，
 *      再失败就把链接摊开让人手动选。粘不出来比看起来差点严重得多。
 *   2. 生成不出来的时候说「为什么」，不放一条会 404 或筛错东西的链接。
 */
(() => {
  "use strict";

  const D = window.JAVDB_DATA;

  // ───────────────────────── 标签词表索引 ─────────────────────────
  // 上游 /api/v2/tags?type=0 给的 11 组。我们只用其中**能真筛**的组：
  //   main → 走 filter_by（不是 filter_by_tags）
  //   subject/role/cloth/body/behavior/play_method/category → 走 filter_by_tags
  //   year/month/duration → id 不是标签 id，实测筛不了（见 index.html 的说明）
  // 这三组的 id 不是标签 id（月份 1–12 与真标签 id 全撞号），因此不进 filter_by_tags。
  // year/month 由本服务本地筛（现有 since=）；duration 这一版不做。
  const UNSUPPORTED = new Set(["year", "month", "duration"]);
  const MAIN_GROUP = D.tagVocab.find((g) => g.categoryId === "main");
  const TAG_GROUPS = D.tagVocab.filter(
    (g) => g.categoryId !== "main" && !UNSUPPORTED.has(g.categoryId),
  );
  // 年/月/时长：上游给了词表，但**没有过滤通道**（实测 30 个参数名全被静默忽略，
  // 而且月份 id 1–12 与真标签 id 全撞号）。因此它们不走 filter_by_tags，
  // 改由本服务按 release_date / duration 本地筛。
  const YEAR_GROUP = D.tagVocab.find((g) => g.categoryId === "year");
  const MONTH_GROUP = D.tagVocab.find((g) => g.categoryId === "month");
  const DURATION_GROUP = D.tagVocab.find((g) => g.categoryId === "duration");
  const GROUP_ORDER = new Map(TAG_GROUPS.map((g, i) => [g.categoryId, i]));

// 四个片库。名字与「实测四个库返回不同集合」这件事都写在 index.html 的说明里。
const ZONES = [
  { id: 0, name: "有码" },
  { id: 1, name: "无码" },
  { id: 2, name: "欧美" },
  { id: 3, name: "FC2" },
];

// 时长档位：id 是上游给的四个，人读文字是我们拆出边界的。
const DURATION_LABEL = {
  "lt-45": "45 分钟以内",
  "45-90": "45–90 分钟",
  "90-120": "90–120 分钟",
  "gt-120": "120 分钟以上",
};

  // id → 候选组。**同一个 id 会出现在多个组里**（月份 1–12 与真实标签 id 全撞），
  // 所以挑选时必须用**名字**消歧：女优自己的 tags[] 带的是真实名字。
  const BY_ID = new Map();
  D.tagVocab.forEach((g, gi) => {
    for (const t of g.tags) {
      if (!BY_ID.has(t.id)) BY_ID.set(t.id, []);
      BY_ID.get(t.id).push({ gi, categoryId: g.categoryId, category: g.category, name: t.name });
    }
  });

  /** 把一个「女优的标签」落到分组上。名字对不上又有多义时返回 null（宁可不显示）。 */
  function locate(id, name) {
    const cands = BY_ID.get(String(id)) || [];
    const hit = cands.find((c) => c.name === name) || (cands.length === 1 ? cands[0] : null);
    if (!hit || UNSUPPORTED.has(hit.categoryId) || hit.categoryId === "main") return null;
    return hit;
  }

  // ───────────────────────────── DOM ─────────────────────────────

  const $ = (id) => document.getElementById(id);
  const el = {
    html: document.documentElement,
    base: $("base-url"),
    themeToggle: $("theme-toggle"),
    themeIcon: $("theme-icon"),
    upstreamChip: $("upstream-chip"),
    upstreamText: $("upstream-text"),
    tabs: $("tabs"),
    actressSearch: $("actress-search"),
    actressList: $("actress-list"),
    actressEmpty: $("actress-empty"),
    actressStatus: $("actress-status"),
    actressCount: $("tab-actress-count"),
    selectAll: $("actress-select-all"),
    clearSel: $("actress-clear"),
    genderGroup: $("gender-group"),
    modeGroup: $("mode-group"),
    todayEcho: $("today-echo"),
    sourceGroup: $("tag-source-group"),
    siteNote: $("site-note"),
    pickerWrap: $("actress-picker-wrap"),
    picker: $("actress-picker"),
    pickerOptions: $("actress-options"),
    pickerStatus: $("picker-status"),
    mainFlags: $("main-flags"),
    tagModeGroup: $("tag-mode-group"),
    tagSearch: $("tag-search"),
    tagSelected: $("tag-selected"),
    tagGroups: $("tag-groups"),
    tagEmpty: $("tag-empty"),
    tagCount: $("tag-count"),
    tagExpand: $("tag-expand"),
    zoneGroup: $("zone-group"),
    durationGroup: $("duration-group"),
    durationHint: $("duration-hint"),
    yearSelect: $("year-select"),
    monthGroup: $("month-group"),
    yearHint: $("year-hint"),
    listsCount: $("tab-lists-count"),
    tagUrl: $("tag-url"),
    tagUrlHint: $("tag-url-hint"),
    tagAdd: $("tag-add"),
    tagCopy: $("tag-copy"),
    listCards: $("list-cards"),
    wantUrl: $("want-url"),
    wantOpen: $("want-open"),
    wantCopy: $("want-copy"),
    wantAdd: $("want-add"),
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
    demo: "ok", // ok | empty | notoken | error（用 URL hash 切换，供评审看状态）
    gender: "female", // female | all —— 收藏里真的有 6 位男优
    mode: "new", // new | all —— 女优区新链接的默认模式，逐行可覆盖
    rowMode: new Map(), // id -> new | all
    selected: new Set(),
    search: "",
    source: "actress", // actress | site
    picker: D.actors[0]?.id ?? "",
    flags: new Set(), // filter_by 主属性字母
    tagMode: "new",
    tags: new Map(), // id -> {id, name, categoryId}
    tagSearch: "",
    // 时间与时长（本地过滤维度，单选；null = 不限）。
    year: null,
    month: null,
    duration: null,
    // 全站模式的片库号。实测四个库返回**不同**集合，写错不报错只会给别的作品。
    zone: 0,
    // 标签组的展开状态（用户显式点过的才记在这里；其余按「有已选」推定）。
    groupOpen: new Map(),
    links: [], // 待复制。只存「怎么生成」，URL 每次现算 —— 换服务地址要立刻跟上
  };

  // ─────────────────────────── 小工具 ───────────────────────────

  function feedBase() {
    const raw = el.base.value.trim().replace(/\/+$/, "");
    return raw || "http://127.0.0.1:8080";
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

  const actorById = (id) => D.actors.find((a) => a.id === id);

  function optionLabel(a) {
    return `${a.name}（${a.id}）`;
  }

  function resolveActor(text) {
    const t = String(text || "").trim();
    if (!t) return null;
    const low = t.toLowerCase();
    return (
      D.actors.find(
        (a) => optionLabel(a) === t || a.name === t || a.id === t || a.id.toLowerCase() === low,
      ) || null
    );
  }

  /** 这位女优自己的标签（上游 /api/v1/actors/{id} 的顶层 tags[]）。没取到就是 null。 */
  function ownTags(id) {
    return D.actressTags[id] || null;
  }

  // ─────────────────────────── URL 构造 ───────────────────────────

  function actressUrl(id, mode) {
    const path = `${feedBase()}/rss/actress/${encodeURIComponent(id)}.xml`;
    return mode === "all" ? `${path}?pages=20` : `${path}?since=${todayISO()}`;
  }

  /**
   * 标签按**词表顺序**排，不按点击顺序 —— 同样的选择永远得到同样的 URL，
   * 这样两条链接能直接比对是不是一回事。
   */
  function sortedTags() {
    return [...state.tags.values()].sort(
      (a, b) =>
        (GROUP_ORDER.get(a.categoryId) ?? 99) - (GROUP_ORDER.get(b.categoryId) ?? 99) ||
        Number(a.id) - Number(b.id),
    );
  }

  /** filter_by 的字母按词表顺序给，而不是点击顺序 —— 同样的选择永远得到同样的 URL。 */
  function sortedFlags() {
    return (MAIN_GROUP?.tags ?? []).map((t) => t.id).filter((id) => state.flags.has(id));
  }

  /**
   * 年/月选中的下界（YYYY-MM-DD）。两者都没选就是 null。
   *
   * 为什么只能给下界：服务目前只有 `since=`。要「只取这一年/这一个月」还需要
   * 一个 `until=`（未实现）—— 而这是**本地过滤**，不是上游通道（上游没有）。
   */
  function timeSince() {
    if (state.year && state.month) return `${state.year}-${String(state.month).padStart(2, "0")}-01`;
    if (state.year) return `${state.year}-01-01`;
    return null;
  }

  function tagQuery() {
    const qs = new URLSearchParams();
    // ⚠️ 选了年份就**不能**再带 since：`since` 是本服务的本地过滤，
    // 而 `year` 是上游筛选。两个一起发的话 since=<今天> 会把 year=2021 的结果
    // 全部筛掉，表现为「选了年份反而是空的」。年份本身就是范围，链接范围那栏让位。
    if (state.year) {
      if (state.tagMode === "all") qs.set("pages", "20");
    } else if (state.tagMode === "all") {
      qs.set("pages", "20");
    } else {
      qs.set("since", todayISO());
    }

    // 主属性、年份、标签都用**语义参数**给服务，让服务去拼掩码 ——
    // 掩码的段数是有讲究的（多一段会让年份被静默丢弃），不该由前端拼。
    const letters = sortedFlags();
    if (letters.length) qs.set("main", letters.join(","));
    if (state.year) qs.set("year", state.year);
    const ids = sortedTags().map((t) => t.id);
    if (ids.length) qs.set("tags", ids.join(","));
    return qs;
  }

  /** 全站订阅的 URL。形态与女优订阅**不同**：片库在路径里，筛选在 query 里。 */
  function siteUrl() {
    const qs = new URLSearchParams();
    // m（含磁鏈）**总是**并进去（不是"没有别的才用 m"）：实测不发它时浏览返回的
    // 50 部 magnets_count 全是 0，而没有磁链的条目发不出去 —— 服务那侧也是这么补的。
    const letters = sortedFlags();
    qs.set("main", letters.includes("m") ? letters.join(",") : [...letters, "m"].join(","));
    const ids = sortedTags().map((t) => t.id);
    if (ids.length) qs.set("tags", ids.join(","));
    if (state.year) qs.set("year", state.year);
    if (state.month) qs.set("month", state.month);
    // 时长必须与年份一起给（实测单独给会被上游静默忽略）。
    if (state.duration && state.year) qs.set("duration", state.duration);
    return `${feedBase()}/rss/tags/${state.zone}.xml?${qs.toString()}`;
  }

  function tagUrl() {
    if (state.source === "site") return siteUrl();
    if (!actorById(state.picker)) return "";
    return `${feedBase()}/rss/actress/${encodeURIComponent(state.picker)}.xml?${tagQuery().toString()}`;
  }

  function wantUrl() {
    return `${feedBase()}/rss/want.xml`;
  }

  function linkUrl(link) {
    if (link.kind === "want") return wantUrl();
    if (link.kind === "tags") {
      const qs = new URLSearchParams();
      if (link.mode === "all") qs.set("pages", "20");
      else qs.set("since", todayISO());
      if (link.flags.length) qs.set("filter_by", `0:a:${link.id}:${link.flags.join(",")}::`);
      if (link.tags.length) qs.set("filter_by_tags", link.tags.join(","));
      return `${feedBase()}/rss/actress/${encodeURIComponent(link.id)}.xml?${qs.toString()}`;
    }
    if (link.kind === "list") return listUrl(link.id);
    return actressUrl(link.id, link.mode);
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
    }, 1800);
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
    const value = (b) => b.dataset.mode ?? b.dataset.source ?? b.dataset.tagmode ?? b.dataset.gender;
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
    // 初始 tabindex：只让选中那个进 tab 序。
    btns.forEach((b) => {
      b.tabIndex = b.getAttribute("aria-checked") === "true" ? 0 : -1;
    });
    return select;
  }

  // ─────────────────────────── 女优列表 ───────────────────────────

  function visibleActors() {
    if (state.demo === "empty") return [];
    const q = state.search.trim().toLowerCase();
    return D.actors.filter((a) => {
      if (state.gender === "female" && a.gender !== 0) return false;
      if (!q) return true;
      return a.name.toLowerCase().includes(q) || a.id.toLowerCase().includes(q);
    });
  }

  const modeOf = (id) => state.rowMode.get(id) || state.mode;

  function renderActors() {
    const rows = visibleActors();
    el.actressCount.textContent =
      state.demo === "empty" ? "" : String(rows.length === D.actors.length ? D.actors.length : rows.length);

    // 空 / 错误状态：说清是什么、为什么、下一步做什么（H9）。
    if (state.demo !== "ok") {
      el.actressList.innerHTML = "";
      el.actressEmpty.classList.remove("hidden");
      if (state.demo === "empty") {
        el.actressEmpty.className =
          "rounded-lg border border-dashed border-border px-4 py-10 text-center";
        el.actressEmpty.innerHTML = `
          <p class="text-sm font-medium">收藏里一个人也没有</p>
          <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-muted-foreground">
            服务读到了你的账号，但收藏列表是空的 —— 还没在 JavDB App 里点过「收藏」。
            收藏之后刷新这一页就行。
          </p>
          <button class="btn btn-md btn-primary mt-4" type="button" data-retry>重新读取</button>`;
      } else {
        const isToken = state.demo === "notoken";
        el.actressEmpty.className =
          "rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-6 text-left";
        el.actressEmpty.innerHTML = `
          <p class="flex items-start gap-2 text-sm font-medium">
            <span aria-hidden="true">⛔</span>
            ${isToken ? "读不到收藏：还没配置 token" : "读不到收藏：上游出错了"}
          </p>
          <p class="mt-2 text-xs leading-relaxed text-muted-foreground">
            ${
              isToken
                ? '服务返回 <code class="font-mono">503</code>：<span class="font-mono">尚未配置 token，读不到 App 里的收藏女优</span>。从 App 导出后配置 <code class="font-mono">app_api.token_file</code>，或者跑一次 <code class="font-mono">javadb-rss login</code>。'
                : '服务返回 <code class="font-mono">502</code>：上游请求失败。这通常是临时的，直接重试；一直失败就看 <code class="font-mono">/healthz/upstream</code>。'
            }
          </p>
          <p class="mt-2 text-xs leading-relaxed text-muted-foreground">
            番号订阅与女优订阅不需要 token，不受影响 —— 你可以继续用「标签筛选」。
          </p>
          <button class="btn btn-md btn-primary mt-4" type="button" data-retry>重试</button>`;
      }
      el.actressEmpty.querySelector("[data-retry]").addEventListener("click", () => {
        location.hash = "state=ok";
      });
      syncSelectionSummary();
      return;
    }

    if (!rows.length) {
      el.actressList.innerHTML = "";
      el.actressEmpty.classList.remove("hidden");
      el.actressEmpty.className =
        "rounded-lg border border-dashed border-border px-4 py-10 text-center";
      el.actressEmpty.innerHTML = `
        <p class="text-sm font-medium">没有匹配的演员</p>
        <p class="mt-1.5 text-xs text-muted-foreground">换个名字或 id 试试，或者把筛选放宽到「全部演员」。</p>
        <button class="btn btn-md btn-outline mt-4" type="button" data-reset>重置筛选</button>`;
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
        <li data-row="${a.id}" class="flex items-center gap-3 px-3 py-1 transition-colors hover:bg-accent/40">
          <label class="flex min-w-0 flex-1 cursor-pointer items-center gap-3 py-1.5">
            <input type="checkbox" class="size-4 shrink-0 accent-[hsl(var(--primary))]"
              data-actress="${a.id}" ${on ? "checked" : ""} />
            <span class="min-w-0 truncate text-sm">
              <span class="font-medium">${a.name}</span>
              <span class="ms-1.5 font-mono text-xs text-muted-foreground">${a.id}</span>
              ${male ? '<span class="ms-1.5 rounded-sm border border-border px-1 text-2xs text-muted-foreground">男优</span>' : ""}
            </span>
          </label>
          <span class="hidden w-16 shrink-0 text-right text-xs text-muted-foreground tnum md:block">${a.videos} 部</span>
          <button type="button" class="btn btn-sm btn-outline w-20 shrink-0 md:w-24"
            data-rowmode="${a.id}"
            aria-label="${a.name} 的链接模式现在是${mode === "all" ? "全量" : "追新"}，点击切换"
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
      el.actressStatus.textContent = state.demo === "ok" ? "勾选后链接进入下方待复制区" : "";
      return;
    }
    el.actressStatus.textContent = `已选 ${n} 位 · ${all ? `${all} 条全量（较慢）` : "全部追新"}`;
  }

  function syncTrayFromSelection() {
    const keep = state.links.filter((l) => l.kind !== "actress");
    const picked = [...state.selected]
      .map(actorById)
      .filter(Boolean)
      .sort((a, b) => D.actors.indexOf(a) - D.actors.indexOf(b))
      .map((a) => ({
        key: `actress:${a.id}`,
        kind: "actress",
        id: a.id,
        mode: modeOf(a.id),
        label: a.name,
        sub: "收藏女优",
      }));
    state.links = [...keep, ...picked];
    renderTray();
  }

  // ─────────────────────────── 标签筛选 ───────────────────────────

  /** 当前该显示哪些组、每组哪些标签。 */
  function currentTagGroups() {
    if (state.source === "site") {
      return TAG_GROUPS.map((g) => ({
        categoryId: g.categoryId,
        category: g.category,
        tags: g.tags,
      }));
    }
    const own = ownTags(state.picker);
    if (!own) return null; // 没取到这位的标签（示例数据的限制）
    const buckets = new Map();
    for (const t of own) {
      const hit = locate(t.id, t.name);
      if (!hit) continue;
      if (!buckets.has(hit.categoryId)) {
        buckets.set(hit.categoryId, { categoryId: hit.categoryId, category: hit.category, tags: [] });
      }
      buckets.get(hit.categoryId).tags.push({ id: t.id, name: t.name, count: t.count });
    }
    // 组按上游顺序，组内按上游词表顺序（拿词表顺序做键，App 里就是这个序）。
    const order = new Map(TAG_GROUPS.map((g, i) => [g.categoryId, i]));
    return [...buckets.values()].sort((a, b) => order.get(a.categoryId) - order.get(b.categoryId));
  }

  function renderPicker() {
    el.pickerOptions.innerHTML = D.actors
      .map((a) => `<option value="${optionLabel(a)}" label="${optionLabel(a)} · ${a.videos} 部"></option>`)
      .join("");
    const a = actorById(state.picker);
    el.picker.value = a ? optionLabel(a) : "";
  }

  function renderFlags() {
    // 基本组直接来自上游词表（main），标签顺序与名字都不是我们编的。
    el.mainFlags.innerHTML = (MAIN_GROUP?.tags ?? [])
      .map(
        (f) => `
      <button type="button" class="chip" data-flag="${f.id}" aria-pressed="${state.flags.has(f.id)}"
        title="主属性字母 ${f.id}">
        ${f.name} <span class="font-mono text-2xs opacity-70">${f.id}</span>
      </button>`,
      )
      .join("");
  }

  // ───────────────────── 时间与时长（本地过滤） ─────────────────────

  /** 一排单选 chip，第一个是「不限」。 */
  function radioChips(name, current, options) {
    const one = (value, label, extra = "") =>
      `<button type="button" class="chip" role="radio" data-time="${name}" data-value="${value}"
        aria-checked="${value === (current ?? "")}" tabindex="${value === (current ?? "") ? 0 : -1}"
        ${extra}>${label}</button>`;
    return one("", "不限") + options.map((o) => one(o.value, o.label, o.extra)).join("");
  }

  function renderTime() {
    const site = state.source === "site";
    renderZones();
    // 年份用原生 select：26 个选项铺成一排 chip 会让这一屏被年份淹没
    // （Hick's Law），而 select 自带键盘跳转与手机滚轮，不用自己实现弹层。
    // 顺序保留词表的倒序（2026, 2025, …）—— 「最近的在前」正是选的时候要的。
    el.yearSelect.innerHTML =
      '<option value="">不限</option>' +
      (YEAR_GROUP?.tags ?? [])
        // 「起」不是装饰：服务只有 since=（下界），没有 until=。把语义写进选项，
        // 比只写在下面那行提示里可靠 —— 提示可以不被读，选项一定会被读到。
        .map((t) => `<option value="${t.id}">${t.name} 年起</option>`)
        .join("");
    el.yearSelect.value = state.year ?? "";
    // 月份反过来升序：月份是周期量，按日历排才符合直觉（Jakob's Law）。
    // 女优模式下禁用 —— App 的面板里没有它，掩码里也没有它的位置。
    el.monthGroup.innerHTML = radioChips(
      "month",
      site ? state.month : null,
      (MONTH_GROUP?.tags ?? [])
        .map((t) => ({ value: t.id, label: `${t.name} 月起` }))
        .sort((a, b) => Number(a.value) - Number(b.value))
        .map((o) => ({ ...o, extra: site ? "" : 'disabled aria-disabled="true" title="女优订阅不支持月份"' })),
    );
    // 时长：全站模式可用（掩码第 5 槽）；女优模式禁用 —— 那个槽位还没验出来。
    el.durationGroup.innerHTML = radioChips(
      "duration",
      site ? state.duration : null,
      (DURATION_GROUP?.tags ?? []).map((t) => ({
        value: t.id,
        label: DURATION_LABEL[t.id] ?? t.name,
        extra: site
          ? ""
          : 'disabled aria-disabled="true" title="女优订阅不支持时长"',
      })),
    );
    el.durationHint.textContent = site
      ? "必须与年份一起给 —— 实测单独给时长的结果与不筛逐条相同（服务那侧会把这种写法定成 400）。"
      : "女优订阅不支持时长（App 的筛选面板里也没有它）。实测掩码里多写一段会让**年份被静默丢弃**，所以服务直接返回 400。";

    renderTimeHint();
  }

  function renderZones() {
    el.zoneGroup.innerHTML = ZONES.map(
      (z) => `<button type="button" class="chip" role="radio" data-zone="${z.id}"
        aria-checked="${z.id === state.zone}" tabindex="${z.id === state.zone ? 0 : -1}">${z.name}</button>`,
    ).join("");
  }

  /** 年/月/时长这两组只在全站模式下可用。 */
  function timeControlsEnabled() {
    return state.source === "site";
  }

  function renderTimeHint() {
    if (state.source === "site") {
      // 全站形态：年/月/时长各占掩码一个槽位。
      el.yearHint.textContent = state.year
        ? `掩码第 5 槽 = ${state.year}（实测是**整年**：升序第 1 页是 1 月、降序第 9 页是 12 月）。`
        : "不选就是「不限」。掩码第 5 槽空着；时长那一排这时不可用（它必须与年份一起给）。";
      return;
    }
    // 女优形态：**只有年份**（掩码第 5 段），而且它一出现，追新那栏就得让位。
    el.yearHint.textContent = state.year
      ? `掩码会写成 0:a:${state.picker}:{主属性}:${state.year}，实测是**整年**。` +
        "⚠️ 这时「链接范围」那栏的追新让位 —— 年份本身就是范围；" +
        "两个一起发的话，本服务的 since 会把年份的结果全筛掉。"
      : "不选就是「不限」，链接用上面的链接范围（追新 / 全量）。";
  }

  function renderTagSelection() {
    const picked = sortedTags();
    el.tagCount.textContent = String(picked.length);
    if (!picked.length) {
      el.tagSelected.innerHTML = `<p class="text-xs text-muted-foreground">还没选标签。选定后按 <code class="font-mono">filter_by_tags</code> 的逗号顺序发出去（多个标签是交集）。</p>`;
      return;
    }
    el.tagSelected.innerHTML = `
      <div class="flex flex-wrap items-center gap-2 rounded-md border border-border bg-muted/40 px-3 py-2">
        <span class="text-xs text-muted-foreground">已选</span>
        ${picked
          .map(
            (t) => `
          <button type="button" class="chip" data-unselect="${t.id}" aria-pressed="true"
            title="点一下取消 ${t.name}">
            ${t.name} <span class="font-mono text-2xs opacity-70">×</span>
          </button>`,
          )
          .join("")}
        <button type="button" class="btn btn-sm btn-ghost" data-clear-tags>全清</button>
      </div>`;
  }

  function renderTagGroups() {
    const groups = currentTagGroups();
    const q = state.tagSearch.trim().toLowerCase();

    if (groups === null) {
      el.tagGroups.innerHTML = "";
      showTagNote(
        "示例数据只带了 22 位女优的标签（真实实现里这是每次按需取一位的一次上游请求）。换一位试，或者切到「全站标签」看完整词表。",
        true,
      );
      return;
    }

    const shown = groups
      .map((g) => ({
        ...g,
        tags: q
          ? g.tags.filter((t) => t.name.toLowerCase().includes(q) || String(t.id).includes(q))
          : g.tags,
      }))
      .filter((g) => g.tags.length);

    if (!shown.length) {
      el.tagGroups.innerHTML = "";
      showTagNote(
        q ? `没有匹配「${el.tagSearch.value.trim()}」的标签。` : "这位女优没有任何可筛的标签。",
        true,
      );
      return;
    }
    el.tagEmpty.classList.add("hidden");

    const full = state.tags.size >= MAX_TAGS;
    el.tagGroups.innerHTML = shown
      .map((g) => {
        const pickedInGroup = g.tags.filter((t) => state.tags.has(t.id)).length;
        // 折叠形式与 App 一致。默认只展开「有已选」的组；搜索时全展开，
        // 否则搜出来的命中被藏在折叠里，等于没搜。
        const open = q ? true : state.groupOpen.get(g.categoryId) ?? pickedInGroup > 0;
        return `
        <details class="tag-group overflow-hidden rounded-md border border-border" data-group="${g.categoryId}" ${open ? "open" : ""}>
          <summary
            class="flex cursor-pointer flex-wrap items-baseline gap-x-2 px-3 py-2 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <span class="text-xs font-medium text-foreground">${g.category}</span>
            <span class="font-mono text-2xs text-muted-foreground">${g.categoryId}</span>
            <span class="text-2xs text-muted-foreground tnum">${g.tags.length}</span>
            ${pickedInGroup ? `<span class="text-2xs font-medium text-foreground tnum">已选 ${pickedInGroup}</span>` : ""}
          </summary>
          <div class="flex flex-wrap gap-2 border-t border-border px-3 py-3">
            ${g.tags
              .map((t) => {
                const on = state.tags.has(t.id);
                const blocked = full && !on;
                return `<button type="button" class="chip" data-tag="${t.id}"
                  data-tag-name="${t.name}" data-tag-cat="${g.categoryId}"
                  aria-pressed="${on}"
                  ${blocked ? 'disabled aria-disabled="true"' : ""}
                  title="${blocked ? `最多选 ${MAX_TAGS} 个（上游硬限制），先取消一个` : `标签 id ${t.id}${t.count ? ` · 据上游 ${t.count} 部` : ""}`}">
                  ${t.name}${t.count ? ` <span class="tnum opacity-70">${t.count}</span>` : ""}
                </button>`;
              })
              .join("")}
          </div>
        </details>`;
      })
      .join("");

    if (full) {
      showTagNote(`已经选满 ${MAX_TAGS} 个。要换就点掉一个已选的（带 ✓ 的那个）。`, false);
    }
  }

  function showTagNote(text, dashed) {
    el.tagEmpty.textContent = text;
    el.tagEmpty.className = dashed
      ? "mt-3 rounded-md border border-dashed border-border px-3 py-6 text-center text-xs text-muted-foreground"
      : "note mt-3";
    el.tagEmpty.classList.remove("hidden");
  }

  function renderTagUrl() {
    const site = state.source === "site";
    const a = actorById(state.picker);
    // 全站模式不再依赖女优 —— 它自己就是一条完整的订阅。
    const canBuild = site || !!a;

    el.tagUrl.textContent = canBuild ? tagUrl() : "先从上面选一位女优。";

    const parts = [];
    if (site) {
      parts.push(`掩码 ${state.zone}:t:{主属性}:{标签}:{年}:{时长}:{月}（片库在路径里）`);
      parts.push("主属性里的 m 是自动带的 —— 不发它时上游返回的 50 部全都没有磁链");
      const ids = sortedTags().map((t) => t.id);
      if (ids.length) parts.push(`${ids.length} 个标签（上限 5 是上游硬限制）`);
      if (state.duration && !state.year) parts.push("⚠️ 时长必须与年份一起给，否则不会生效");
    } else if (canBuild) {
      const since = timeSince();
      if (since) {
        parts.push(
          `since=${since} + pages=20 —— 年/月由本服务本地按 release_date 筛，不是上游通道`,
        );
      } else {
        parts.push(
          state.tagMode === "all" ? "pages=20 取整个片单" : `since=${todayISO()} 只取这之后发行的`,
        );
      }
      if (state.flags.size) parts.push(`filter_by 只看 ${[...state.flags].join("、")}（交集）`);
      const picked = sortedTags();
      if (picked.length) {
        parts.push(`filter_by_tags 只看 ${picked.map((t) => t.name).join(" + ")}（交集）`);
      } else if (!state.flags.size && !since) {
        parts.push("⚠️ 一个筛选都没选 —— 这条等于该女优的全部作品，和「收藏女优」里那条重复");
      }
    }
    el.tagUrlHint.textContent = parts.join(" · ");

    el.tagAdd.disabled = !canBuild;
    el.tagCopy.disabled = !canBuild;
  }

  // ─────────────────────────── 清单 ───────────────────────────

  function renderLists() {
    el.listsCount.textContent = String(D.lists.length);
    el.listCards.innerHTML = D.lists
      .map(
        (l, i) => `
      <li class="flex flex-col rounded-lg border border-border bg-card p-4">
        <p class="flex flex-wrap items-baseline gap-1.5">
          <span class="text-sm font-medium">${l.name}</span>
          ${l.isDefault ? '<span class="rounded-sm border border-border px-1 text-2xs text-muted-foreground">默认</span>' : ""}
          ${l.privacy === "own" ? '<span class="rounded-sm border border-border px-1 text-2xs text-muted-foreground">私有</span>' : ""}
        </p>
        <p class="mt-1 font-mono text-xs text-muted-foreground">${l.id} · ${l.count} 部</p>
        <code class="mt-3 block rounded-md bg-muted px-2 py-1.5 font-mono text-xs break-all">${listUrl(l.id)}</code>
        <div class="mt-3 flex flex-wrap gap-2">
          <button class="btn btn-md btn-outline" type="button" data-list-copy="${i}">复制</button>
          <button class="btn btn-md btn-ghost" type="button" data-list-add="${i}">加入待复制</button>
        </div>
      </li>`,
      )
      .join("");
  }

  function listUrl(id) {
    return `${feedBase()}/rss/list/${encodeURIComponent(id)}.xml`;
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
            去「收藏女优」里勾几位，或者在「标签筛选」里做一条 —— 生成的链接都会落到这里。
          </p>
          <button class="btn btn-md btn-outline mt-4" type="button" data-goto="tab-actress">去挑女优</button>
        </li>`;
      el.trayList.querySelector("[data-goto]").addEventListener("click", (e) => {
        switchTab(e.currentTarget.dataset.goto, true);
      });
      return;
    }

    el.trayList.innerHTML = state.links
      .map(
        (l, i) => `
      <li class="flex items-start gap-3 py-3">
        <div class="min-w-0 flex-1">
          <p class="flex flex-wrap items-center gap-1.5">
            <span class="text-sm font-medium">${l.label}</span>
            <span class="rounded-sm border border-border px-1 font-mono text-2xs text-muted-foreground">
              ${l.kind === "want" ? "固定路径" : l.mode === "all" ? "全量" : "追新"}
            </span>
            <span class="text-xs text-muted-foreground">${l.sub}</span>
          </p>
          <code class="mt-1 block font-mono text-xs break-all text-muted-foreground select-all">${linkUrl(l)}</code>
        </div>
        <div class="flex shrink-0 gap-1">
          <button class="btn btn-sm btn-outline" type="button" data-copy-row="${i}">复制</button>
          <button class="btn btn-sm btn-ghost" type="button" data-remove-row="${i}"
            aria-label="移除 ${l.label}">移除</button>
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

  // ─────────────────────────── 分区标签页 ───────────────────────────

  const MAX_TAGS = 5;
  const TAB_IDS = ["tab-actress", "tab-tags", "tab-lists", "tab-want"];

  function switchTab(id, focus) {
    TAB_IDS.forEach((tid) => {
      const tab = $(tid);
      const panel = $(tab.getAttribute("aria-controls"));
      const on = tid === id;
      tab.setAttribute("aria-selected", String(on));
      tab.tabIndex = on ? 0 : -1;
      panel.hidden = !on;
      if (on && focus) tab.focus();
    });
  }

  // ─────────────────────────── 事件 ───────────────────────────

  const selectGender = radioGroup(el.genderGroup, (v) => {
    state.gender = v;
    // 被筛掉的男优如果已在选择里，要跟着松掉 —— 否则「看不见却还算数」。
    if (v === "female") {
      for (const id of [...state.selected]) {
        const a = actorById(id);
        if (a && a.gender !== 0) state.selected.delete(id);
      }
    }
    renderActors();
    syncTrayFromSelection();
  });

  radioGroup(el.modeGroup, (v) => {
    state.mode = v;
    // 默认模式变了，逐行的覆盖就没意义了 —— 清掉，让所有人跟上新默认。
    state.rowMode.clear();
    renderActors();
    syncTrayFromSelection();
  });

  radioGroup(el.sourceGroup, (v) => {
    state.source = v;
    el.siteNote.classList.toggle("hidden", v !== "site");
    el.pickerWrap.classList.toggle("hidden", v === "site");
    state.tagSearch = "";
    el.tagSearch.value = "";
    renderTagGroups();
    renderTagSelection();
    refreshTime();
    renderTagUrl();
  });

  radioGroup(el.tagModeGroup, (v) => {
    state.tagMode = v;
    renderTagUrl();
  });

  el.tabs.addEventListener("click", (e) => {
    const tab = e.target.closest('[role="tab"]');
    if (tab) switchTab(tab.id, false);
  });
  el.tabs.addEventListener("keydown", (e) => {
    const idx = TAB_IDS.indexOf(document.activeElement.id);
    if (idx < 0) return;
    let next = null;
    if (e.key === "ArrowRight") next = (idx + 1) % TAB_IDS.length;
    else if (e.key === "ArrowLeft") next = (idx - 1 + TAB_IDS.length) % TAB_IDS.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = TAB_IDS.length - 1;
    if (next === null) return;
    e.preventDefault();
    switchTab(TAB_IDS[next], true);
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
    syncTrayFromSelection();
  });

  el.actressList.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-rowmode]");
    if (!btn) return;
    const id = btn.dataset.rowmode;
    const next = modeOf(id) === "all" ? "new" : "all";
    state.rowMode.set(id, next);
    // 同上：只改这一个按钮，不动 DOM 的其余部分。
    const name = actorById(id).name;
    btn.textContent = next === "all" ? "全量" : "追新";
    btn.setAttribute(
      "aria-label",
      `${name} 的链接模式现在是${next === "all" ? "全量" : "追新"}，点击切换`,
    );
    syncSelectionSummary();
    syncTrayFromSelection();
    announce(`${name} 改为${next === "all" ? "全量" : "追新"}`);
  });

  el.selectAll.addEventListener("click", () => {
    visibleActors().forEach((a) => state.selected.add(a.id));
    renderActors();
    syncTrayFromSelection();
    announce(`已选 ${state.selected.size} 位`);
  });

  el.clearSel.addEventListener("click", () => {
    state.selected.clear();
    state.rowMode.clear();
    renderActors();
    syncTrayFromSelection();
    announce("已清空选择");
  });

  el.picker.addEventListener("change", () => {
    const a = resolveActor(el.picker.value);
    if (!a) return;
    state.picker = a.id;
    state.tags.clear();
    state.tagSearch = "";
    el.tagSearch.value = "";
    renderTagGroups();
    renderTagSelection();
    refreshTime();
    renderTagUrl();
    el.pickerStatus.textContent = describePicker(a);
  });

  el.picker.addEventListener("input", () => {
    const a = resolveActor(el.picker.value);
    el.pickerStatus.textContent = a
      ? describePicker(a)
      : el.picker.value.trim()
        ? "找不到这位女优 —— 名字或 id 要完全一致，或者从下拉里挑一个。"
        : "还没选女优。";
  });

  function describePicker(a) {
    const n = ownTags(a.id)?.length;
    return `${a.name} · ${a.videos} 部 · 上游给了她 ${n ?? "?"} 个标签`;
  }

  el.mainFlags.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-flag]");
    if (!btn) return;
    const k = btn.dataset.flag;
    if (state.flags.has(k)) state.flags.delete(k);
    else state.flags.add(k);
    btn.setAttribute("aria-pressed", String(state.flags.has(k)));
    renderTagUrl();
  });

  el.tagSearch.addEventListener("input", () => {
    state.tagSearch = el.tagSearch.value;
    renderTagGroups();
  });

  el.tagGroups.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-tag]");
    if (!btn) return;
    toggleTag(btn.dataset.tag, btn.dataset.tagName, btn.dataset.tagCat);
  });

  // 原生 <details> 自己管展开，这里只把状态记下来，好在重渲染后保持（H3 用户控制）。
  el.tagGroups.addEventListener("toggle", (e) => {
    const d = e.target.closest("details[data-group]");
    if (!d) return;
    state.groupOpen.set(d.dataset.group, d.open);
    syncExpandButton();
  }, true);

  el.tagExpand.addEventListener("click", () => {
    const open = el.tagGroups.querySelectorAll("details:not([open])").length > 0;
    el.tagGroups.querySelectorAll("details[data-group]").forEach((d) => {
      state.groupOpen.set(d.dataset.group, open);
    });
    renderTagGroups();
    syncExpandButton();
    announce(open ? "已展开全部标签组" : "已收起全部标签组");
  });

  function syncExpandButton() {
    const collapsed = el.tagGroups.querySelectorAll("details:not([open])").length > 0;
    el.tagExpand.textContent = collapsed ? "全部展开" : "全部收起";
  }

  // 年/月/时长（单选）。时长那排是禁用的，点不到。
  el.yearSelect.addEventListener("change", () => {
    state.year = el.yearSelect.value || null;
    renderTime();
    renderTagUrl();
  });
  el.monthGroup.addEventListener("click", (e) => onTimePick(e, "month"));
  el.durationGroup.addEventListener("click", (e) => onTimePick(e, "duration"));
  el.zoneGroup.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-zone]");
    if (!btn) return;
    state.zone = Number(btn.dataset.zone);
    renderTime();
    renderTagUrl();
  });

  function onTimePick(e, name) {
    const btn = e.target.closest("[data-time]");
    if (!btn) return;
    state[name] = btn.dataset.value || null;
    renderTime();
    renderTagUrl();
  }

  /** 时间维度的可用性随模式变，切换模式时要整块重渲染。 */
  function refreshTime() {
    if (!timeControlsEnabled()) {
      // 切回女优模式时清掉这三个 —— 它们在那条 URL 上根本不出现，
      // 留着会让「界面上选着、链接里没有」这种最坏的不一致出现。
      state.year = null;
      state.month = null;
      state.duration = null;
    }
    renderTime();
  }

  el.listCards.addEventListener("click", (e) => {
    const copy = e.target.closest("[data-list-copy]");
    if (copy) {
      const l = D.lists[Number(copy.dataset.listCopy)];
      if (l) doCopy(copy, listUrl(l.id), `清单「${l.name}」的链接`);
      return;
    }
    const add = e.target.closest("[data-list-add]");
    if (add) {
      const l = D.lists[Number(add.dataset.listAdd)];
      if (!l) return;
      if (!state.links.some((x) => x.kind === "list" && x.id === l.id)) {
        state.links.push({ key: `list:${l.id}`, kind: "list", id: l.id, label: l.name, sub: "清单" });
      }
      renderTray();
      setTrayOpen(true);
      announce("已加入待复制");
    }
  });

  el.tagSelected.addEventListener("click", (e) => {
    const clear = e.target.closest("[data-clear-tags]");
    if (clear) {
      state.tags.clear();
      renderTagGroups();
      renderTagSelection();
      renderTagUrl();
      announce("已清空标签");
      return;
    }
    const un = e.target.closest("[data-unselect]");
    if (!un) return;
    const id = un.dataset.unselect;
    state.tags.delete(id);
    renderTagGroups();
    renderTagSelection();
    renderTagUrl();
    // 被取消的按钮已经不在 DOM 里了，把焦点还给标签区第一个同类控件。
    const next = el.tagGroups.querySelector("[data-tag]") || el.tagSearch;
    next.focus();
  });

  function toggleTag(id, name, categoryId) {
    if (state.tags.has(id)) {
      state.tags.delete(id);
    } else {
      if (state.tags.size >= MAX_TAGS) {
        announce(`最多选 ${MAX_TAGS} 个标签`);
        return;
      }
      state.tags.set(id, { id, name, categoryId });
    }
    renderTagGroups();
    renderTagSelection();
    renderTagUrl();
    if (state.tags.size === MAX_TAGS) announce(`标签已选满 ${MAX_TAGS} 个`);
  }

  el.tagAdd.addEventListener("click", () => {
    const a = actorById(state.picker);
    if (!a) return;
    const ids = sortedTags().map((t) => t.id);
    state.links = state.links.filter((l) => l.kind !== "tags");
    state.links.push({
      key: `tags:${a.id}`,
      kind: "tags",
      id: a.id,
      mode: state.tagMode,
      flags: sortedFlags(),
      tags: ids,
      label: `${a.name} · 标签筛选`,
      sub: ids.length ? `${ids.length} 个标签` : "无标签",
    });
    renderTray();
    setTrayOpen(true);
    announce("已加入待复制");
  });

  el.tagCopy.addEventListener("click", () => {
    doCopy(el.tagCopy, tagUrl(), "链接");
  });

  el.wantCopy.addEventListener("click", () => {
    doCopy(el.wantCopy, wantUrl(), "「想看」链接");
  });

  el.wantAdd.addEventListener("click", () => {
    if (!state.links.some((l) => l.kind === "want")) {
      state.links.push({ key: "want", kind: "want", label: "想看", sub: "App 里的标记" });
    }
    renderTray();
    setTrayOpen(true);
    announce("已加入待复制");
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
      state.links.splice(i, 1);
      // 从待复制里移除的女优，选择状态也要跟着松掉，否则两处说法不一致。
      if (l.kind === "actress") {
        state.selected.delete(l.id);
        const box = el.actressList.querySelector(`[data-actress="${l.id}"]`);
        if (box) box.checked = false;
        syncSelectionSummary();
      }
      renderTray();
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
    renderActors();
    renderTray();
    announce("已清空待复制");
  });

  el.base.addEventListener("input", () => {
    renderTagUrl();
    renderLists();
    el.wantUrl.textContent = wantUrl();
    el.wantOpen.href = wantUrl();
    renderTray();
  });

  // 主题
  function syncThemeButton() {
    const dark = el.html.classList.contains("dark");
    el.themeToggle.setAttribute("aria-pressed", String(dark));
    el.themeIcon.textContent = dark ? "◑" : "◐";
  }
  el.themeToggle.addEventListener("click", () => {
    const dark = !el.html.classList.contains("dark");
    el.html.classList.toggle("dark", dark);
    try {
      localStorage.setItem("javdb-rss-theme", dark ? "dark" : "light");
    } catch (e) {}
    syncThemeButton();
  });

  // 演示状态（URL hash）
  function readDemo() {
    const m = /state=(ok|empty|notoken|error)/.exec(location.hash || "");
    state.demo = m ? m[1] : "ok";
    const chip = {
      ok: ["上游正常", false],
      empty: ["上游正常", false],
      notoken: ["未配置 token", true],
      error: ["上游出错", true],
    }[state.demo];
    el.upstreamText.textContent = chip[0];
    el.upstreamChip.classList.toggle("text-destructive", chip[1]);
  }
  window.addEventListener("hashchange", () => {
    readDemo();
    state.selected.clear();
    state.links = [];
    renderActors();
    renderTray();
  });

  // ─────────────────────────── 启动 ───────────────────────────

  readDemo();
  el.todayEcho.textContent = todayISO();
  el.wantUrl.textContent = wantUrl();
  el.wantOpen.href = wantUrl();
  renderPicker();
  renderFlags();
  renderTagGroups();
  renderTagSelection();
  renderTime();
  renderTagUrl();
  renderLists();
  renderActors();
  renderTray();
  syncThemeButton();
  syncExpandButton();
  const first = actorById(state.picker);
  if (first) el.pickerStatus.textContent = describePicker(first);
  // 把「收藏里有几位男优」写在开关旁边 —— 不然这个开关看上去没由来。
  const males = D.actors.filter((a) => a.gender !== 0).length;
  if (males) {
    $("gender-label").textContent = `收藏里有 ${males} 位男优`;
    // 把数据来源挂在控件上：promote 时这个开关靠的就是这一个字段。
    el.genderGroup.title = "/collected 的 gender 字段：0 = 女优，1 = 男优（上游直接给，服务透出）";
  }
})();
