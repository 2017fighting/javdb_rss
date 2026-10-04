/*
 * 订阅链接生成器 —— 页面交互。
 *
 * 页面是**服务自己的一个端点**，不是另一套前端工程：
 *   · 数据只从服务自己的发现端点来（/collected、/tags、/actress_tags/{id}）；
 *   · 生成的链接只发**语义参数**，掩码一律由服务构造（前端不拼 filter_by）；
 *   · 除了「服务地址 + 主题」，什么都不持久化 —— 选择与待复制只活在内存里。
 *
 * 刻意实现的三件事（设计稿里就要证明它们成立）：
 *   1. 复制在 http（局域网）下没有 navigator.clipboard —— 走 execCommand 回退，
 *      再失败就把链接摊开让人手动选。粘不出来比看起来差点严重得多。
 *   2. 读不到收藏时**照实说出服务给的那句话**，而不是给一个空列表 ——
 *      空列表会被理解成「你没收藏任何人」，而真相是「服务读不到」。
 *   3. 标签区不靠猜：id 撞号时按**名字**消歧，对不上就不显示（宁缺勿错）；
 *      女优模式的基本组只列她自己支持的字母，不是词表里那一整组。
 *   4. 「全站标签」是**另一种掩码形态**：不挂实体，片库在 URL 路径里，
 *      标签/年/月/时长各占 filter_by 掩码的一个槽位。它是匿名可读的，
 *      而且主属性里**总是**带 m（含磁鏈）—— 不发它的全站 feed 会没有磁链。
 *   5. 「清单」与「想看」读的是 App 里的**标记**，没 token 一定 503 ——
 *      所以拿不到 token 时这两个区禁用并说明缺什么（token_file），
 *      而不是给一条点了就坏的链接。标签区不受影响（匿名可读）。
 */
(() => {
  "use strict";

  const BASE_KEY = "javdb-rss-base";
  const THEME_KEY = "javdb-rss-theme";
  // 动作反馈的时长。与 ui-contract.md 的「1.8s」一致。
  const FEEDBACK_MS = 1800;
  // 标签上限，**上游的硬限制**：实测第 6 个 id 会被静默丢弃
  // （同一个 id 挪到前 5 位就立刻生效）。不是 UI 口味。
  const MAX_TAGS = 5;
  // 女优订阅的片库号。掩码是 `0:a:{id}:{main}:{year}`（实测），
  // 所以她的标签词表取 type=0 —— 换个片库就是另一份 id 空间。
  const ACTRESS_ZONE = 0;

  // 「想看」是一条**固定路径**：内容跟着 App 里的标记走，URL 里没有任何参数。
  const WANT_PATH = "/rss/want.xml";

  // 全站订阅的四个片库（filter_by 的第一段，也是 /rss/tags/{zone}.xml 的路径段）。
  // 与 catalog.ZoneName 同一份取值：写错片库上游不报错，只会给另一个库的作品，
  // 所以它必须看得见、改得动。
  const SITE_ZONES = [
    { id: 0, name: "有码" },
    { id: 1, name: "无码" },
    { id: 2, name: "欧美" },
    { id: 3, name: "FC2" },
  ];
  const zoneName = (id) => SITE_ZONES.find((z) => z.id === Number(id))?.name ?? `片库 ${id}`;

  // 上游给的四档时长（掩码第 6 槽）。显示名沿用服务端 browse 的 durationName。
  const DURATION_LABELS = {
    "lt-45": "45 分钟以内",
    "45-90": "45–90 分钟",
    "90-120": "90–120 分钟",
    "gt-120": "120 分钟以上",
  };

  // 词表里**不是标签**的那几组（两种模式都是这样）：
  //   main      走 filter_by 的字母位，单独一档（女优的 filter_tags / 全站词表）；
  //   year      走掩码第 5 段（女优）/ 槽（全站），渲染成 select；
  //   month / duration  只有全站形态有对应的槽位；女优订阅遇到它们判 400。
  // 而且年/月/时长的 id **不是标签 id**（月份 1–12 与真标签 id 全撞号），
  // 一旦被当成标签发出去会静默筛出另一批作品 —— 所以它们既不渲染成可点 chip，
  // 也不参与「她的标签 → 分组」的反查。
  const SPECIAL_GROUPS = new Set(["main", "year", "month", "duration"]);

  // 空态/错误态那段说明的样式：虚线框 = 「这里还没有东西」，与有内容的卡片分开。
  const EMPTY_NOTE =
    "mt-3 rounded-md border border-dashed border-border px-3 py-6 text-center text-xs text-muted-foreground";

  const $ = (id) => document.getElementById(id);
  const el = {
    html: document.documentElement,
    base: $("base-url"),
    themeToggle: $("theme-toggle"),
    themeIcon: $("theme-icon"),
    // ── 服务与上游状态 ──
    upstreamChip: $("upstream-chip"),
    upstreamText: $("upstream-text"),
    upstreamNote: $("upstream-note"),
    whitelistNote: $("whitelist-note"),
    actressTruncated: $("actress-truncated"),
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
    // ── 标签区 ──
    tagSourceGroup: $("tag-source-group"),
    tagSourceHint: $("tag-source-hint"),
    siteNote: $("site-note"),
    zoneGroup: $("zone-group"),
    zoneHint: $("zone-hint"),
    pickerWrap: $("tag-picker-wrap"),
    tagNote: $("tag-note"),
    tagPicker: $("tag-picker"),
    actressOptions: $("actress-options"),
    tagPickerStatus: $("tag-picker-status"),
    mainFlags: $("main-flags"),
    mainHint: $("main-hint"),
    tagModeGroup: $("tag-mode-group"),
    tagModeHint: $("tag-mode-hint"),
    tagCount: $("tag-count"),
    tagSearch: $("tag-search"),
    tagExpand: $("tag-expand"),
    tagGroups: $("tag-groups"),
    tagSelected: $("tag-selected"),
    tagEmpty: $("tag-empty"),
    timeHint: $("time-hint"),
    yearSelect: $("year-select"),
    yearHint: $("year-hint"),
    monthGroup: $("month-group"),
    monthHint: $("month-hint"),
    durationGroup: $("duration-group"),
    durationHint: $("duration-hint"),
    tagUrl: $("tag-url"),
    tagUrlHint: $("tag-url-hint"),
    tagAdd: $("tag-add"),
    tagCopy: $("tag-copy"),
    // ── 清单 / 想看 ──
    listsNote: $("lists-note"),
    listCards: $("list-cards"),
    wantUrl: $("want-url"),
    wantCopy: $("want-copy"),
    wantHint: $("want-hint"),
    wantAdd: $("want-add"),
    // ── 待复制 ──
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
    // /collected 的截断信号。**看键在不在**才是判据（服务只在真的触顶时
    // 才给 truncated 这三个键），所以 null = 这份清单是完整的。
    truncated: null, // {pagesFetched, maxPages}

    // ── 标签区 ──

    // 标签从哪儿来：某位女优的 tags[]，还是整个片库的词表。
    tagSource: "actress", // actress | site
    // 全站模式的片库号。四个库是**四份不同的 id 空间**，换库时旧选择必须清掉。
    zone: 0,

    // 选中的人。手输的 id 也在这里（known=false），因此不在收藏列表里的 id
    // 同样能出标签与链接 —— 标签筛选本身是匿名的。
    picker: null, // {id, name, known}
    // /actress_tags/{id} 的结果。它的三个字段（name / main / tags）来自
    // 服务的**同一次**上游读取，所以名字与标签不会各漂各的。
    profile: null,
    profileState: "idle", // idle | loading | loaded | error
    profileError: null, // {status, message}
    profileGen: 0, // 切人比请求先到的守卫（只认最后一次）

    // 标签词表（/tags?type={片库}）。分组名、顺序、组内标签一律原样来自上游。
    // vocabZone 记住这一份是哪個片库的 —— 换库时那份就是另一个 id 空间。
    vocab: null, // {groups:[...]}
    vocabZone: null,
    vocabState: "loading", // loading | loaded | error
    vocabError: null,
    vocabGen: 0, // 换库比请求先到的守卫（只认最后一次）
    // 被 feeds.zones 白名单挡下的片库：/tags?type={zone} 会返回 404。
    // 记住它，就不给一条订不到的链接（“点了就坏”比不给更坏）。
    zoneRejected: new Set(),
    groupIndex: new Map(), // category_id -> 词表里的位置（排序用）
    byId: new Map(), // 标签 id -> 候选 [{categoryId, category, name}]

    flags: new Set(), // 基本组：她支持的 filter_tags 字母
    tagMode: "new", // new | all（标签区自己的链接范围）
    tags: new Map(), // id -> {id, name, categoryId}
    tagSearch: "",
    year: null,
    month: null, // 只有全站模式能用（掩码第 7 槽）
    duration: null, // 只有全站模式能用，且必须与年份同给（掩码第 6 槽）
    // 标签组的展开状态。只记用户**显式点过**的那些，其余按「有已选」推定。
    groupOpen: new Map(),

    // ── 清单与想看 ──
    // /collected_lists 的结果。列清单要 token，所以它（以及 /collected）的
    // 503 同时也是「想看」会不会 503 的信号 —— 两者读的是同一份 App 标记。
    lists: [],
    listsState: "loading", // loading | loaded | error
    listsError: null, // {status, message}

    // 待复制。只存「怎么生成」，URL 每次现算 —— 换服务地址要立刻跟上。
    links: [],

    // ── 服务与上游状态 ──
    // /version 的白名单三类（女优 / 清单 / 全站标签）。null = 还没读到，
    // 那时不输出任何白名单提示（宁可不说，也不编）。
    whitelist: null, // {actresses, lists, zones}
    // /readyz 的结果。unknown = 还没读到或读不到 —— 那时不声称「正常」。
    upstream: { state: "unknown", message: "" }, // unknown | ok | down
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
  const optionLabel = (a) => `${a.name}（${a.id}）`;

  /** 服务给的错误文案（它就是用户要照做的下一步），拿不到才自己编一句。 */
  function serverMessage(body, status) {
    return body && typeof body.error === "string" && body.error
      ? body.error
      : `服务返回 ${status}`;
  }

  // ─────────────────────────── URL 构造 ───────────────────────────
  //
  // 前端只发**语义参数**：追新 = since=<今天>，全量 = pages=20，
  // 主属性 = main=c,m，年份 = year=2021，标签 = tags=68,7。
  // filter_by / filter_by_tags 一律不出现 —— 掩码由服务构造（掩码的段数
  // 是个「多一段就静默失效」的坑，不该有两处实现）。

  /**
   * 把语义参数拼成 query。逗号不转义：它在这儿是**文档化的分隔符**
   * （`main=c,m`、`tags=68,7`），而用户要逐字符比对两条链接是不是一回事。
   */
  function queryString(pairs) {
    if (!pairs.length) return "";
    const s = pairs
      .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
      .join("&");
    return "?" + s.replace(/%2C/g, ",");
  }

  function actressUrl(id, mode) {
    const path = `${base()}/rss/actress/${encodeURIComponent(id)}.xml`;
    return mode === "all" ? `${path}?pages=20` : `${path}?since=${todayISO()}`;
  }

  /**
   * 清单的 feed 路径。优先用服务给的 `feed` —— 那是 /collected_lists 明确
   * 交出来的、可以直接拿去用的路径；自己拼只在它缺席时兜底。
   */
  function listFeed(list) {
    return list.feed || `/rss/list/${encodeURIComponent(list.id)}.xml`;
  }

  /** 标签 id 的排序键：数字 id 按数值，非数字排在后面（词表里只有数字）。 */
  function tagIdKey(id) {
    const n = Number(id);
    return Number.isFinite(n) ? n : Infinity;
  }

  /**
   * 她支持的字母（女优模式）/ 词表基本组全部字母（全站模式），
   * 按**词表 main 组顺序**给（不是点击顺序）。
   */
  function orderedMainLetters() {
    const mainGroup = vocabGroups().find((g) => g.category_id === "main");
    const vocabOrder = (mainGroup?.tags ?? []).map((t) => t.id);
    if (state.tagSource === "site") return vocabOrder;
    const hers = (state.profile?.main ?? []).map((m) => m.id);
    // 词表里没有的字母（上游万一给了新字母）也保留，接在她自己的顺序后面 ——
    // 宁可多列一个，也不能把「她支持」的一个字母藏起来。
    return [
      ...vocabOrder.filter((id) => hers.includes(id)),
      ...hers.filter((id) => !vocabOrder.includes(id)),
    ];
  }

  /**
   * 选中的主属性字母，按词表顺序 —— 同样的选择永远得到逐字符相同的链接。
   *
   * 全站模式里 m（含磁鏈）**总是**在结果里：不发它时上游返回的作品全都没有
   * 磁链，会得到一条看着坏了的空 feed。补齐位置与服务端 addMagnetsFlag 一致
   * （没有 m 就补到最后：`c` → `c,m`），这样页面上显示的与服务实际发的是同一条。
   */
  function sortedFlags() {
    const letters = orderedMainLetters().filter((id) => state.flags.has(id));
    if (state.tagSource === "site" && !letters.includes("m")) letters.push("m");
    return letters;
  }

  /**
   * 选中的标签，按**组顺序 + id** 给（不是点击顺序）。
   *
   * 直接用当前渲染出来的分组做源：它的顺序已经是「组顺序 + id」，
   * 因此页面上的阅读顺序与 URL 里的参数顺序逐字对应。
   */
  function sortedTags() {
    const out = [];
    for (const g of currentTagGroups()) {
      for (const t of g.tags) {
        if (state.tags.has(t.id)) out.push({ id: t.id, name: t.name, categoryId: g.categoryId });
      }
    }
    return out;
  }

  /**
   * 标签区那条链接。它只发语义参数：
   *
   *   女优模式：main=<她支持的字母>  year=<整年>  tags=<id，逗号分隔>
   *   全站模式：main=<字母，总是含 m>  tags=<id>  year / month / duration
   *   + 链接范围（追新 = since=今天；全量 = pages=20）
   *
   * ⚠️ 选了年份（或全站模式选了月份）就**不再发 since**：since 是本服务的
   * 本地下界，year/month 是上游精确筛选，同时发必然得到空 feed。
   */
  function tagQueryPairs(o) {
    const pairs = [];
    if (o.mode === "all") pairs.push(["pages", "20"]);
    else if (!o.year) pairs.push(["since", todayISO()]);
    if (o.flags.length) pairs.push(["main", o.flags.join(",")]);
    if (o.year) pairs.push(["year", o.year]);
    if (o.tags.length) pairs.push(["tags", o.tags.join(",")]);
    return pairs;
  }

  /**
   * 全站订阅的语义参数。形态与女优订阅**不同**：片库在 URL 路径里。
   *
   * 掩码是 `{片库}:t:{主属性}:{标签}:{年}:{时长}:{月}`，由服务从这些参数构造；
   * 这里只发 main / tags / year / month / duration（+ 链接范围）。
   */
  function siteQueryPairs(o) {
    const pairs = [];
    if (o.mode === "all") pairs.push(["pages", "20"]);
    else if (!o.year && !o.month) pairs.push(["since", todayISO()]);
    if (o.flags.length) pairs.push(["main", o.flags.join(",")]);
    if (o.tags.length) pairs.push(["tags", o.tags.join(",")]);
    if (o.year) pairs.push(["year", o.year]);
    if (o.month) pairs.push(["month", o.month]);
    if (o.duration) pairs.push(["duration", o.duration]);
    return pairs;
  }

  function tagUrl() {
    if (state.tagSource === "site") {
      return `${base()}/rss/tags/${state.zone}.xml${queryString(
        siteQueryPairs({
          mode: state.tagMode,
          year: state.year,
          month: state.month,
          duration: state.duration,
          flags: sortedFlags(),
          tags: sortedTags().map((t) => t.id),
        }),
      )}`;
    }
    if (!state.picker) return "";
    const pairs = tagQueryPairs({
      mode: state.tagMode,
      year: state.year,
      flags: sortedFlags(),
      tags: sortedTags().map((t) => t.id),
    });
    return `${base()}/rss/actress/${encodeURIComponent(state.picker.id)}.xml${queryString(pairs)}`;
  }

  /** 待复制里的每条 URL 都在渲染时现算。 */
  function linkUrl(link) {
    if (link.kind === "site") {
      return `${base()}/rss/tags/${link.zone}.xml${queryString(siteQueryPairs(link))}`;
    }
    if (link.kind === "tags") {
      return `${base()}/rss/actress/${encodeURIComponent(link.id)}.xml${queryString(
        tagQueryPairs(link),
      )}`;
    }
    if (link.kind === "list") return `${base()}${link.feed}`;
    if (link.kind === "want") return `${base()}${WANT_PATH}`;
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

  function radioGroup(container, dataKey, onPick) {
    const btns = [...container.querySelectorAll('[role="radio"]')];
    const value = (b) => b.dataset[dataKey];
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
        state.truncated = null;
        state.error = { status: res.status, message: serverMessage(body, res.status) };
      } else {
        state.actresses = Array.isArray(body?.actresses) ? body.actresses : [];
        // 截断信号：**看键在不在**，不看值 —— 服务只在真的触顶时才给
        // truncated / pages_fetched / max_pages 这三个键（`omitempty`）。
        // 写成 `"truncated" in body` 而不是 `body.truncated`：契约说的是
        // 「键存在 = 已知不完整」，一个 `truncated: false` 也应当被当成那个键。
        state.truncated =
          body && "truncated" in body
            ? {
                pagesFetched: Number(body.pages_fetched) || 0,
                maxPages: Number(body.max_pages) || 0,
              }
            : null;
      }
    } catch (e) {
      state.actresses = [];
      state.truncated = null;
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
    // 收藏到手了就给标签区挑一位默认的人 —— 不然这一区开场是空的。
    // 只在她还没选的时候做：手输的 id 不该被收藏列表覆盖。
    // 没配 token（收藏读不到）时不挑，标签区照旧能靠手输 id 用起来。
    if (!state.picker) {
      const first = state.actresses.find((a) => a.gender === 0) || state.actresses[0];
      if (first) {
        el.tagPicker.value = optionLabel(first);
        commitPicker(optionLabel(first));
      }
    }
  }

  /**
   * 读 /collected_lists。与 /collected 同一套规矩：失败时**照实保留**服务给的
   * 那句话（503 里就写着要配 token_file），而不是编一句「加载失败」。
   *
   * 它同时决定「想看」能不能给链接：两者读的是同一份 App 标记，
   * 没 token 时都会 503。
   */
  async function loadLists() {
    state.listsState = "loading";
    state.listsError = null;
    renderLists();
    renderWant();
    try {
      const res = await fetch("/collected_lists", {
        headers: { accept: "application/json" },
      });
      const body = await res.json().catch(() => null);
      if (!res.ok) {
        state.lists = [];
        state.listsState = "error";
        state.listsError = { status: res.status, message: serverMessage(body, res.status) };
      } else {
        state.lists = Array.isArray(body?.lists) ? body.lists : [];
        state.listsState = "loaded";
      }
    } catch (e) {
      state.lists = [];
      state.listsState = "error";
      state.listsError = { status: 0, message: `读不到服务：${e.message}` };
    }
    renderLists();
    renderWant();
  }

  /**
   * 没配 token：/collected 与 /collected_lists 都会 503。页面据此把「清单」与
   * 「想看」的**输出**禁用并说明缺什么 —— 它们读的都是 App 里的标记。
   * 标签区不受影响：词表与女优标签匿名可读。
   */
  function tokenMissing() {
    return state.listsError?.status === 503 || state.error?.status === 503;
  }

  /**
   * 服务在 503 里给的原话（它已经点出 token_file），两个端点谁先到用谁；
   * 一句都没有时才用同一套措辞兜底。
   */
  function tokenMessage() {
    return (
      state.listsError?.message ||
      state.error?.message ||
      "尚未配置 token，读不到 App 里的标记。请从 App 导出后配置 app_api.token_file（见 README）。"
    );
  }

  // ─────────────────── 服务与上游状态（票 07） ───────────────────

  /**
   * 读 /readyz —— 页面上的上游状态**只**来自这里，不是写死的装饰。
   *
   *   200        → 上游正常
   *   503        → 上游不可用（body 就是探针给出的原因）
   *   其它/网络错 → 状态未知（**不**声称「正常」）
   *
   * 「与 /readyz 的说法一致」是这一条的全部意义：chip 与说明都直接用它
   * 给的文案，不另编一句更顺口的。
   */
  async function loadReadyz() {
    let next;
    try {
      const res = await fetch("/readyz", { headers: { accept: "text/plain" } });
      const text = ((await res.text()) || "").trim();
      next =
        res.status === 200
          ? { state: "ok", message: text || "ok" }
          : res.status === 503
            ? { state: "down", message: text || "上游不可用" }
            : { state: "unknown", message: `服务返回 ${res.status}` };
    } catch (e) {
      next = { state: "unknown", message: `读不到服务：${e.message}` };
    }
    state.upstream = next;
    renderUpstream();
  }

  function renderUpstream() {
    const st = state.upstream;
    const down = st.state === "down";
    el.upstreamChip.classList.toggle("text-destructive", down);
    el.upstreamText.textContent =
      st.state === "ok" ? "上游正常" : down ? "上游不可用" : "上游状态未知";
    // /readyz 的原话放在 title 里，鼠标悬停就能看到「探针到底怎么说的」。
    el.upstreamChip.title = st.message;

    // 内容区的说明只在**不健康**时出现。chip 在小屏（<md）是隐藏的，
    // 而「上游坏了」是用户接下来所有异常的共同解释，必须在任何宽度都看得见。
    if (!down) {
      el.upstreamNote.classList.add("hidden");
      el.upstreamNote.textContent = "";
      return;
    }
    el.upstreamNote.innerHTML =
      `<p class="flex items-start gap-2 text-sm font-medium">` +
      `<span aria-hidden="true">⛔</span>上游不可用</p>` +
      `<p class="mt-1.5 leading-relaxed">` +
      `服务的 <code class="font-mono">/readyz</code> 现在返回 503，说明它连不上上游。` +
      `下面已经拿到的订阅链接仍然可用；新内容的读取会失败，直到上游恢复。探针的原话：` +
      `<code class="font-mono">${esc(st.message)}</code></p>`;
    el.upstreamNote.classList.remove("hidden");
  }

  /**
   * 读 /version 的白名单状态。页面据此在白名单生效时说一句「没列出的订阅会 404」——
   * 没有它，白名单就表现为「链接看得见、一订就 404」（契约硬规则 6 要避免的静默）。
   */
  async function loadVersion() {
    try {
      const res = await fetch("/version", { headers: { accept: "application/json" } });
      const body = await res.json().catch(() => null);
      if (res.ok && body) state.whitelist = body.whitelist || null;
    } catch (e) {
      // 读不到就保持 null：宁可不说，也不编一条「白名单已在生效」的提示。
    }
    renderWhitelistNote();
  }

  /** 三类里任意一类生效，就意味着「没列出的订阅会 404」。 */
  function whitelistActive() {
    const w = state.whitelist;
    return !!(w && (w.actresses || w.lists || w.zones));
  }

  function renderWhitelistNote() {
    if (!whitelistActive()) {
      el.whitelistNote.classList.add("hidden");
      el.whitelistNote.textContent = "";
      return;
    }
    const w = state.whitelist;
    const kinds = [];
    if (w.actresses) kinds.push("女优");
    if (w.lists) kinds.push("清单");
    if (w.zones) kinds.push("全站标签");
    el.whitelistNote.innerHTML =
      `<p class="flex items-start gap-2 text-sm font-medium">` +
      `<span aria-hidden="true">⚠</span>服务启用了订阅白名单</p>` +
      `<p class="mt-1.5 leading-relaxed">` +
      `配置里写了 <code class="font-mono">feeds</code> 段，所以` +
      `<b class="font-medium text-foreground">没列出的订阅会返回 404</b>` +
      `（当前受限：${esc(kinds.join(" / "))}）。页面照常把地址交出来，` +
      `但不在白名单里的那些粘进 qBittorrent 只会得到 404 —— 要订就先把它加进配置。</p>`;
    el.whitelistNote.classList.remove("hidden");
  }

  /** 当前模式需要哪个片库的词表：女优订阅固定 type=0，全站模式就是选的片库。 */
  function neededVocabZone() {
    return state.tagSource === "site" ? state.zone : ACTRESS_ZONE;
  }

  /**
   * 读标签词表（匿名可读，不需要 token）。分组名/顺序/组内标签一律原样保留。
   *
   * 词表是**按片库**取的（/tags?type={片库}）：女优订阅用 type=0，全站模式用
   * 用户选的片库 —— 四个库的组数与 id 空间都不同，写成一份快照会让
   * 「换片库换词表」看上去生效、实际没变。
   *
   * 带世代号：换片库比请求先回来是常态，只认最后一次的结果。
   */
  async function loadVocabulary() {
    const zone = neededVocabZone();
    const gen = ++state.vocabGen;
    // 换片库时**先把旧的那份丢掉**：留着它，renderYear/renderMonth 会拿
    // 另一个库的年份/月份去校验当前选择（把刚选的 2025 静默清掉），
    // 页面会在一瞬里显示上一份词表的内容。
    if (state.vocabZone !== zone) {
      state.vocab = null;
      state.vocabZone = null;
      indexVocabulary();
    }
    state.vocabState = "loading";
    state.vocabError = null;
    renderTagArea();
    try {
      const res = await fetch(`/tags?type=${zone}`, {
        headers: { accept: "application/json" },
      });
      const body = await res.json().catch(() => null);
      if (gen !== state.vocabGen) return; // 已经有更新的一次请求了
      if (!res.ok) {
        state.vocabState = "error";
        state.vocabError = { status: res.status, message: serverMessage(body, res.status) };
        // 404 = 这个片库没被放行（与 /rss/tags/{zone}.xml 同一套白名单语义）。
        // 其它错误（502/网络）是**暂时**的，链接本身仍然有效，不能因此禁用。
        if (res.status === 404) state.zoneRejected.add(zone);
        else state.zoneRejected.delete(zone);
      } else {
        state.vocab = body;
        state.vocabZone = zone;
        state.vocabState = "loaded";
        state.zoneRejected.delete(zone);
        indexVocabulary();
      }
    } catch (e) {
      if (gen !== state.vocabGen) return;
      state.vocabState = "error";
      state.vocabError = { status: 0, message: `读不到服务：${e.message}` };
    }
    renderTagArea();
  }

  /** 当前模式要的那份词表已经在手就不重复取；否则去取。 */
  function syncVocabulary() {
    if (state.vocabZone === neededVocabZone() && state.vocabState !== "error") return;
    loadVocabulary();
  }

  /**
   * 读某位女优自己的标签（匿名可读，不需要 token）。
   *
   * 切人比请求先回来是常态（手输 id 时尤其），所以带一个世代号：
   * 只认最后一次发出的那次结果，否则会出现「选了 A、显示 B 的标签」。
   */
  async function loadActressTags(id) {
    const gen = ++state.profileGen;
    state.profile = null;
    state.profileState = "loading";
    state.profileError = null;
    renderTagArea();
    let failure = null;
    let profile = null;
    try {
      const res = await fetch(`/actress_tags/${encodeURIComponent(id)}`, {
        headers: { accept: "application/json" },
      });
      const body = await res.json().catch(() => null);
      if (!res.ok) failure = { status: res.status, message: serverMessage(body, res.status) };
      else profile = body;
    } catch (e) {
      failure = { status: 0, message: `读不到服务：${e.message}` };
    }
    if (gen !== state.profileGen) return; // 已经有更新的一次请求了
    if (failure) {
      state.profileState = "error";
      state.profileError = failure;
    } else {
      state.profile = profile;
      state.profileState = "loaded";
    }
    renderTagArea();
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

  /**
   * /collected 的截断提示。
   *
   * 只在服务真的给了 truncated 键时出现 —— 未截断时它是隐藏的，
   * 不制造一条假的「清单不完整」警告。措辞是「已知不完整」而不是「加载失败」：
   * 列出来的那些都是对的、可用的，只是不保证是全部。
   */
  function renderTruncated() {
    const t = state.truncated;
    if (!t) {
      el.actressTruncated.classList.add("hidden");
      el.actressTruncated.textContent = "";
      return;
    }
    el.actressTruncated.innerHTML =
      `<p class="flex items-start gap-2 text-sm font-medium">` +
      `<span aria-hidden="true">⚠</span>这份清单已知不完整</p>` +
      `<p class="mt-1.5 leading-relaxed">` +
      `服务读到第 ${t.pagesFetched} 页就到了翻页上限（${t.maxPages} 页），收藏里可能还有人没列出来。` +
      `这不是「加载失败」—— 下面列出的都能用，只是不保证是全部。</p>`;
    el.actressTruncated.classList.remove("hidden");
  }

  function renderActors() {
    renderTruncated();
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
         <p class="mt-2 text-xs leading-relaxed text-muted-foreground">
           标签这一区不受影响：词表与女优标签都是匿名可读的，可以直接手输她的 id。
         </p>
         <button class="btn btn-md btn-outline mt-4" type="button" data-retry>重新读取</button>`,
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
           收藏之后刷新这一页就行；标签区也可以直接手输 id。
         </p>
         <button class="btn btn-md btn-outline mt-4" type="button" data-retry>重新读取</button>`,
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
        `<p class="text-sm font-medium">没有匹配的收藏</p>
         <p class="mt-1.5 text-xs text-muted-foreground">换个名字或 id 试试，或者把筛选放宽到「全部收藏」。</p>
         <button class="btn btn-md btn-outline mt-4" type="button" data-reset>重置筛选</button>`,
        "rounded-lg border border-dashed border-border px-4 py-10 text-center",
      );
      el.actressEmpty.querySelector("[data-reset]").addEventListener("click", () => {
        el.actressSearch.value = "";
        state.search = "";
        // 走一遍单选组自己的 click，而不是直接改 state —— 否则控件的
        // aria-checked / tabindex 会与真实筛选不一致（看得见的那个开关在说谎）。
        el.genderGroup.querySelector('[data-gender="all"]').click();
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

  // ─────────────────────────── 标签区 ───────────────────────────

  function vocabGroups() {
    return state.vocab?.groups ?? [];
  }

  /** 建两份索引：分组位置（排序）与 id → 候选组（反查，撞号时靠名字消歧）。 */
  function indexVocabulary() {
    state.groupIndex = new Map(vocabGroups().map((g, i) => [g.category_id, i]));
    state.byId = new Map();
    for (const g of vocabGroups()) {
      for (const t of g.tags) {
        if (!state.byId.has(t.id)) state.byId.set(t.id, []);
        state.byId.get(t.id).push({ categoryId: g.category_id, category: g.category, name: t.name });
      }
    }
  }

  /**
   * 把她的一个标签落到词表的分组上。
   *
   * ⚠️ id **不是全局唯一**：同一个片库里，月份的 1–12 与真标签 id 全部撞号
   * （实测 id=3 同时是「月份:3」与「服裝:眼鏡」）。所以候选多于一个时必须
   * 用**名字**消歧；对不上就返回 null（不显示）—— 宁缺勿错：把一个月份 id
   * 当成真标签发出去，会静默筛出另一批作品。
   */
  function locateTag(id, name) {
    const cands = state.byId.get(String(id)) || [];
    const hit = cands.find((c) => c.name === name) || (cands.length === 1 ? cands[0] : null);
    if (!hit || SPECIAL_GROUPS.has(hit.categoryId)) return null;
    return hit;
  }

  /**
   * 当前该显示哪些组、每组哪些标签。
   *
   *   女优模式：**只来自她自己的 tags[]**，分组名与分组顺序来自词表。
   *   全站模式：直接就是词表里除 main/year/month/duration 之外的每一组 ——
   *             因为它不挂实体，没有「她自己的标签」这回事。
   *
   * 两种都按词表组顺序、组内按 id 排，好让页面上的阅读顺序与 URL 里的
   * 参数顺序逐字对应。
   */
  function currentTagGroups() {
    if (state.tagSource === "site") {
      return vocabGroups()
        .filter((g) => !SPECIAL_GROUPS.has(g.category_id))
        .map((g) => ({
          categoryId: g.category_id,
          category: g.category,
          tags: (g.tags ?? [])
            .map((t) => ({ id: t.id, name: t.name, count: Number(t.videos_count) || 0 }))
            .sort((a, b) => tagIdKey(a.id) - tagIdKey(b.id) || String(a.id).localeCompare(b.id)),
        }))
        .filter((g) => g.tags.length);
    }

    if (!state.profile) return [];
    const buckets = new Map();
    for (const t of state.profile.tags ?? []) {
      const hit = locateTag(t.id, t.name);
      if (!hit) continue;
      if (!buckets.has(hit.categoryId)) {
        buckets.set(hit.categoryId, {
          categoryId: hit.categoryId,
          category: hit.category,
          tags: [],
        });
      }
      buckets.get(hit.categoryId).tags.push({
        id: t.id,
        name: t.name,
        count: Number(t.videos_count) || 0,
      });
    }
    const groups = [...buckets.values()];
    groups.sort(
      (a, b) =>
        (state.groupIndex.get(a.categoryId) ?? 99) - (state.groupIndex.get(b.categoryId) ?? 99),
    );
    for (const g of groups) {
      g.tags.sort((a, b) => tagIdKey(a.id) - tagIdKey(b.id) || String(a.id).localeCompare(b.id));
    }
    return groups;
  }

  /** 从输入框里的文字解析出「要选谁」。认不出就把它当作 id（手输 id 那条路）。 */
  function resolvePicker(text) {
    const t = String(text || "").trim();
    if (!t) return null;
    const low = t.toLowerCase();
    const a = state.actresses.find(
      (x) => optionLabel(x) === t || x.name === t || x.id === t || String(x.id).toLowerCase() === low,
    );
    if (a) return { id: a.id, name: a.name, known: true };
    // 不在收藏里的 id 照样能用：标签是匿名的，服务那侧只按白名单放行。
    return { id: t, name: null, known: false };
  }

  /** 认下输入框里的人，并去取她的标签。 */
  function commitPicker(text) {
    const next = resolvePicker(text);
    if (!next) return;
    // 同一个人不重复取：回车提交后输入框失焦会**再**发一次 change，
    // 而那次重复请求会把已经渲染好的标签区打回加载态（用户看得见一闪）。
    // 上一次是失败的要允许重试。
    if (state.picker && state.picker.id === next.id && state.profileState !== "error") return;
    state.picker = next;
    // 换人就换了一套能力：她的主属性可能完全不同，标签也完全换一批。
    // 留着旧的就是「界面上没有、链接里有」那种最坏的不一致。
    state.flags.clear();
    state.tags.clear();
    state.tagSearch = "";
    el.tagSearch.value = "";
    state.groupOpen.clear();
    renderTagArea();
    loadActressTags(next.id);
  }

  function showTagNote(html, className) {
    el.tagEmpty.className = className;
    el.tagEmpty.innerHTML = html;
    el.tagEmpty.classList.remove("hidden");
  }

  function hideTagNote() {
    el.tagEmpty.classList.add("hidden");
  }

  function tagErrorNote(what, err) {
    const head =
      err.status === 404
        ? "服务说这个订阅不存在"
        : err.status >= 500 || err.status === 0
          ? "上游出错了"
          : "读不到";
    return `<p class="flex items-start gap-2 text-sm font-medium">
        <span aria-hidden="true">⛔</span>${esc(what)}：${esc(head)}
      </p>
      <p class="mt-2 text-xs leading-relaxed text-muted-foreground">${esc(err.message)}</p>
      <button class="btn btn-md btn-outline mt-3" type="button" data-tag-retry>重新读取</button>`;
  }

  /**
   * 片库被白名单挡下时的说明。它与 502 不同：这不是「暂时读不到」，
   * 而是这个订阅本来就不会被服务 —— 所以不给链接、也说清怎么改配置。
   */
  function tagRejectedNote() {
    return `<p class="flex items-start gap-2 text-sm font-medium">
        <span aria-hidden="true">⛔</span>这个片库没被放行
      </p>
      <p class="mt-2 text-xs leading-relaxed text-muted-foreground">
        配置里的 feeds.zones 白名单没列这个片库，所以 /rss/tags/${esc(String(state.zone))}.xml 会返回 404。
        页面因此不渲染它的标签、也不给你一条订不到的链接 —— 换一个片库，或者把它加进白名单。
      </p>`;
  }

  function renderTagArea() {
    renderTagSource();
    renderZones();
    renderPickerStatus();
    renderMainFlags();
    renderTagGroups();
    renderTagSelection();
    renderTime();
    renderTagModeHint();
    renderTagUrl();
    syncExpandButton();
  }

  /**
   * 模式相关的壳与说明：全站模式多一个「片库 + 说明」块、少一个女优选择器。
   * 说明必须跟着模式换 —— 把「全站标签走掩码槽位」写在女优模式里，
   * 或者反过来，就是让下一个人照着错的前提去改。
   */
  function renderTagSource() {
    const site = state.tagSource === "site";
    el.siteNote.hidden = !site;
    el.pickerWrap.hidden = site;
    el.tagSourceHint.textContent = site
      ? "全站标签：不挂任何实体，片库在 URL 路径里，标签/年/月/时长走掩码槽位。"
      : "某位女优的标签：只列她自己的 tags[]，按上游词表分组。";
    el.tagNote.innerHTML = site
      ? `分组名、分组顺序、组内标签全部取自上游词表 <code class="font-mono">/tags?type=${state.zone}</code>（${esc(zoneName(state.zone))}）。全站模式不挂实体，<strong class="font-medium text-foreground">这些就是全部可筛标签</strong>；标签走的是掩码槽位、<strong class="font-medium text-foreground">不是 <code class="font-mono">filter_by_tags</code></strong>。上限 5 个是上游的硬限制，多个标签之间是<strong class="font-medium text-foreground">交集</strong>。`
      : `分组名、分组顺序、组内标签都取自上游词表 <code class="font-mono">/tags?type=0</code>（女优订阅用的就是有码那个库），但<strong class="font-medium text-foreground">只保留她自己 <code class="font-mono">tags[]</code> 里有的那几个</strong>。最多 5 个不是我们的约定 —— 实测第 <strong class="font-medium text-foreground">6 个 id 会被上游静默丢弃</strong>（同一个 id 挪到前 5 位就生效），所以上限只能是 5。多个标签之间是<strong class="font-medium text-foreground">交集</strong>。`;
  }

  /** 片库四选一。写错片库不报错、只会给另一个库的作品，所以必须看得见、改得动。 */
  function renderZones() {
    // 被挡下的片库不可选；选中那一颗恰好被挡下时，把键盘入口让给第一个可用的
    // （否则整组都没有 tabindex=0 的按钮，键盘用户进不来）。
    const usable = SITE_ZONES.find((z) => !state.zoneRejected.has(z.id))?.id;
    const roving = state.zoneRejected.has(state.zone) ? usable : state.zone;
    el.zoneGroup.innerHTML = SITE_ZONES.map((z) => {
      const rejected = state.zoneRejected.has(z.id);
      return `<button type="button" class="chip" role="radio" data-zone="${z.id}"
        aria-checked="${z.id === state.zone}" tabindex="${z.id === roving ? 0 : -1}"
        ${rejected ? 'disabled aria-disabled="true" title="feeds.zones 白名单没放行这个片库：它的订阅会返回 404"' : ""}
        >${esc(z.name)}</button>`;
    }).join("");
    el.zoneHint.textContent = state.zoneRejected.has(state.zone)
      ? `这个片库（${zoneName(state.zone)}）没被 feeds.zones 白名单放行：/tags?type=${state.zone} 与 /rss/tags/${state.zone}.xml 都会 404，所以不给链接。换一个片库，或者改配置里的白名单。`
      : "片库就是 filter_by 的第一段。四个库返回的是四个不同的集合，而且标签 id 空间按片库分 —— 写错片库不报错，只会给另一个库的作品。";
  }

  function renderPickerStatus() {
    if (!state.picker) {
      el.tagPickerStatus.textContent =
        state.error || state.loading
          ? "还没选女优。收藏读不到也不影响这一区 —— 直接填她的 id，回车读取。"
          : "还没选女优。填名字或 id 都行，回车读取。";
      return;
    }
    if (state.profileState === "loading") {
      el.tagPickerStatus.textContent = `正在读取 ${state.picker.id} 的标签…`;
      return;
    }
    if (state.profileState === "error") {
      el.tagPickerStatus.textContent = `读不到 ${state.picker.id} 的标签 —— 详情见下方标签区。`;
      return;
    }
    const p = state.profile;
    if (!p) {
      el.tagPickerStatus.textContent = state.picker.id;
      return;
    }
    const name = p.name || state.picker.id;
    const tags = Array.isArray(p.tags) ? p.tags.length : 0;
    const main = Array.isArray(p.main) ? p.main.length : 0;
    el.tagPickerStatus.textContent = `${name} · ${Number(p.videos_count) || 0} 部 · 上游给了她 ${tags} 个标签、${main} 个主属性`;
  }

  function renderMainFlags() {
    if (state.tagSource === "site") {
      renderSiteMainFlags();
      return;
    }
    if (state.profileState === "loading") {
      el.mainFlags.innerHTML = "";
      el.mainHint.textContent = "正在读取她支持的主属性…";
      return;
    }
    if (state.profileState !== "loaded" || !state.profile) {
      el.mainFlags.innerHTML = "";
      el.mainHint.textContent = state.picker
        ? "还没读到她的主属性。"
        : "先选一位女优（或手输她的 id），这里会列出她支持的那几个主属性。";
      return;
    }
    const letters = orderedMainLetters();
    const byId = new Map((state.profile.main ?? []).map((m) => [m.id, m.name]));
    el.mainFlags.innerHTML = letters
      .map(
        (id) => `
      <button type="button" class="chip" data-flag="${esc(id)}"
        aria-pressed="${state.flags.has(id)}" title="主属性字母 ${esc(id)}">
        ${esc(byId.get(id) || id)} <span class="font-mono text-2xs opacity-70">${esc(id)}</span>
      </button>`,
      )
      .join("");
    // 词表的基本组有 6 个，她支持的可能只有其中几个 —— 这一句就是
    // 「为什么只列这几个」的答案，不然用户会以为词表坏了。
    const vocabMain = vocabGroups().find((g) => g.category_id === "main");
    const vocabCount = vocabMain ? vocabMain.tags.length : 0;
    el.mainHint.textContent = vocabCount
      ? `这 ${letters.length} 个是她自己的 filter_tags（上游给的），不是词表里全部 ${vocabCount} 个 —— 她一个作品都没有的那几个不列。`
      : `这 ${letters.length} 个是她自己的 filter_tags（上游给的）。`;
  }

  /**
   * 全站模式的基本组：就是词表基本组的全部字母（不挂实体，没有「她的 filter_tags」）。
   * m（含磁鏈）**总是**按下去且不可取消 —— 不发它时上游返回的作品全都没有磁链。
   */
  function renderSiteMainFlags() {
    if (state.vocabState === "loading" && !state.vocab) {
      el.mainFlags.innerHTML = "";
      el.mainHint.textContent = "正在读取片库的标签词表…";
      return;
    }
    if (state.vocabState === "error") {
      el.mainFlags.innerHTML = "";
      el.mainHint.textContent = "读不到标签词表，基本组暂时不可选。";
      return;
    }
    const letters = orderedMainLetters();
    const byId = new Map(
      (vocabGroups().find((g) => g.category_id === "main")?.tags ?? []).map((t) => [t.id, t.name]),
    );
    el.mainFlags.innerHTML = letters
      .map((id) => {
        const locked = id === "m";
        return `
      <button type="button" class="chip" data-flag="${esc(id)}"
        aria-pressed="${locked || state.flags.has(id)}"
        ${locked ? 'disabled aria-disabled="true"' : ""}
        title="${
          locked
            ? "全站订阅总是带 m（含磁鏈）：不发它时上游返回的作品全都没有磁链"
            : `主属性字母 ${esc(id)}`
        }">
        ${esc(byId.get(id) || id)} <span class="font-mono text-2xs opacity-70">${esc(id)}</span>
      </button>`;
      })
      .join("");
    el.mainHint.textContent = letters.length
      ? `全站模式的基本组就是词表里全部 ${letters.length} 个主属性（不是某位女优的 filter_tags）。其中 m（含磁鏈）总是带上 —— 不发它时上游返回的作品全都没有磁链，会得到一条看着坏了的空 feed。`
      : "词表里没有基本组，主属性只剩自动带上的 m。";
  }

  function renderTagSelection() {
    const picked = sortedTags();
    el.tagCount.textContent = String(picked.length);
    if (!picked.length) {
      el.tagSelected.innerHTML = `<p class="text-xs text-muted-foreground">还没选标签。选定后按<strong class="font-medium text-foreground">词表顺序</strong>（组顺序 + id）发进 <code class="font-mono">tags=</code>，多个标签之间是交集。</p>`;
      return;
    }
    const full = picked.length >= MAX_TAGS;
    el.tagSelected.innerHTML = `
      <div class="flex flex-wrap items-center gap-2 rounded-md border border-border bg-muted/40 px-3 py-2">
        <span class="text-xs text-muted-foreground">已选 ${picked.length}/${MAX_TAGS}</span>
        ${picked
          .map(
            (t) => `
          <button type="button" class="chip" data-unselect="${esc(t.id)}" aria-pressed="true"
            title="点一下取消 ${esc(t.name)}">
            ${esc(t.name)} <span class="font-mono text-2xs opacity-70">×</span>
          </button>`,
          )
          .join("")}
        <button type="button" class="btn btn-sm btn-ghost" data-clear-tags>全清</button>
      </div>
      ${full ? `<p class="mt-2 text-xs text-muted-foreground">已选满 ${MAX_TAGS} 个 —— 上限是<strong class="font-medium text-foreground">上游的硬限制</strong>（第 ${MAX_TAGS + 1} 个会被静默丢弃），所以没选中的点不动。要换就点掉一个。</p>` : ""}`;
  }

  function renderTagGroups() {
    const q = state.tagSearch.trim().toLowerCase();

    if (state.tagSource === "site") {
      if (state.vocabState === "loading" && !state.vocab) {
        el.tagGroups.innerHTML = "";
        showTagNote("正在读取片库的标签词表…（一次匿名上游请求，不需要 token）", EMPTY_NOTE);
        return;
      }
      if (state.vocabState === "error") {
        el.tagGroups.innerHTML = "";
        showTagNote(
          state.zoneRejected.has(state.zone)
            ? tagRejectedNote()
            : tagErrorNote("读不到标签词表", state.vocabError),
          EMPTY_NOTE,
        );
        return;
      }
      const siteGroups = currentTagGroups();
      if (!siteGroups.length) {
        el.tagGroups.innerHTML = "";
        showTagNote("这个片库的词表里没有可筛的标签组。", EMPTY_NOTE);
        return;
      }
      renderTagGroupList(siteGroups, q);
      return;
    }

    if (!state.picker) {
      el.tagGroups.innerHTML = "";
      showTagNote(
        "先在上面选一位女优，或者直接手输她的 id —— 标签词表与她的标签都是匿名可读的，不需要 token。",
        EMPTY_NOTE,
      );
      return;
    }
    if (state.profileState === "loading" || (state.vocabState === "loading" && !state.vocab)) {
      el.tagGroups.innerHTML = "";
      showTagNote("正在读取她的标签…（词表与她的标签各一次上游请求，都不需要 token）", EMPTY_NOTE);
      return;
    }
    if (state.profileState === "error") {
      el.tagGroups.innerHTML = "";
      showTagNote(tagErrorNote("读不到她的标签", state.profileError), EMPTY_NOTE);
      return;
    }
    if (state.vocabState === "error") {
      el.tagGroups.innerHTML = "";
      showTagNote(tagErrorNote("读不到标签词表", state.vocabError), EMPTY_NOTE);
      return;
    }

    const groups = currentTagGroups();
    if (!groups.length) {
      el.tagGroups.innerHTML = "";
      showTagNote(
        "上游没有给这位女优任何一个能筛的标签：她的 tags[] 里没有一条能与词表按名字对上。对不上的就不显示（宁缺勿错）。",
        EMPTY_NOTE,
      );
      return;
    }

    renderTagGroupList(groups, q);
  }

  /** 把已归好组、排好序的标签画成一排可折叠的组。搜索时只留命中的组并全展开。 */
  function renderTagGroupList(groups, q) {
    const shown = groups
      .map((g) => ({
        ...g,
        tags: q
          ? g.tags.filter(
              (t) => t.name.toLowerCase().includes(q) || String(t.id).toLowerCase().includes(q),
            )
          : g.tags,
      }))
      .filter((g) => g.tags.length);

    if (!shown.length) {
      el.tagGroups.innerHTML = "";
      showTagNote(`没有匹配「${esc(el.tagSearch.value.trim())}」的标签。`, EMPTY_NOTE);
      return;
    }
    hideTagNote();

    const full = state.tags.size >= MAX_TAGS;
    el.tagGroups.innerHTML = shown
      .map((g) => {
        const pickedInGroup = g.tags.filter((t) => state.tags.has(t.id)).length;
        // 默认全部折叠，只展开「有已选」的组；搜索时全展开 ——
        // 命中被藏在折叠里等于没搜。用户显式点过的以用户为准。
        const open = q ? true : (state.groupOpen.get(g.categoryId) ?? pickedInGroup > 0);
        return `
        <details class="tag-group overflow-hidden rounded-md border border-border"
          data-group="${esc(g.categoryId)}" ${open ? "open" : ""}>
          <summary class="flex cursor-pointer flex-wrap items-baseline gap-x-2 px-3 py-2 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <span class="text-xs font-medium text-foreground">${esc(g.category)}</span>
            <span class="font-mono text-2xs text-muted-foreground">${esc(g.categoryId)}</span>
            <span class="text-2xs text-muted-foreground tnum">${g.tags.length}</span>
            ${pickedInGroup ? `<span class="text-2xs font-medium text-foreground tnum">已选 ${pickedInGroup}</span>` : ""}
          </summary>
          <div class="flex flex-wrap gap-2 border-t border-border px-3 py-3">
            ${g.tags
              .map((t) => {
                const on = state.tags.has(t.id);
                const blocked = full && !on;
                return `<button type="button" class="chip" data-tag="${esc(t.id)}"
                  data-tag-name="${esc(t.name)}" data-tag-cat="${esc(g.categoryId)}"
                  aria-pressed="${on}" ${blocked ? 'disabled aria-disabled="true"' : ""}
                  title="${
                    blocked
                      ? `最多选 ${MAX_TAGS} 个（上游硬限制：第 ${MAX_TAGS + 1} 个会被静默丢弃），先取消一个`
                      : `标签 id ${esc(t.id)}${t.count ? ` · 据上游 ${t.count} 部` : ""}`
                  }">
                  ${esc(t.name)}${t.count ? ` <span class="tnum opacity-70">${t.count}</span>` : ""}
                </button>`;
              })
              .join("")}
          </div>
        </details>`;
      })
      .join("");
  }

  // ─────────────────── 时间与时长（年/月/时长） ───────────────────
  //
  // 年：两种模式都是真上游筛选（女优走掩码第 5 段、全站走第 5 槽），精确整年。
  // 月：只有全站模式有对应的槽位（第 7 槽）；女优订阅遇到它判 400。
  // 时长：同样只有全站模式有（第 6 槽），而且**必须与年份同给** ——
  //       实测单独给会被上游静默忽略，那种「看着筛了其实没筛」正是本项目
  //       最不能接受的一类失败（服务那侧会把这种写法定成 400）。

  function renderTime() {
    renderYear();
    renderMonth();
    renderDuration();
    renderTimeHint();
  }

  function renderTimeHint() {
    el.timeHint.textContent =
      state.tagSource === "site"
        ? "全站模式下年/月/时长各占掩码一个槽位（第 5/7/6 段），三个都能用：年份是整年、月份是整月，时长必须与年份一起给。"
        : "女优模式下只有年份（掩码第 5 段，实测整年生效）—— App 的女优筛选面板里没有月和时长，掩码里也没有它们的位置（多写一段会让年份被静默丢弃），服务那侧遇到它们直接返回 400。所以这两样在女优模式下被禁用。";
  }

  function renderYear() {
    const yearGroup = vocabGroups().find((g) => g.category_id === "year");
    const options = yearGroup?.tags ?? [];

    if (state.vocabState === "loading" && !state.vocab) {
      el.yearSelect.innerHTML = '<option value="">不限</option>';
      el.yearSelect.disabled = true;
      el.yearHint.textContent = "正在读取年份…";
      return;
    }
    if (state.vocabState === "error") {
      el.yearSelect.innerHTML = '<option value="">不限</option>';
      el.yearSelect.disabled = true;
      el.yearHint.textContent = "读不到标签词表，年份暂时不可选。";
      return;
    }
    el.yearSelect.disabled = options.length === 0;
    el.yearSelect.innerHTML =
      '<option value="">不限</option>' +
      // ⚠️ 选项文字**不带「起」**：年份落在掩码槽位，实测是**整年**
      // （`::2021` 返回的 19 部全在 2021 内）。带「起」是只有 since=（下界）
      // 时的说法，写错一个字就让人以为自己选了区间。
      options.map((t) => `<option value="${esc(t.id)}">${esc(t.name)} 年</option>`).join("");
    // 词表里没有的年份（换了片库/上游改了）就不要留在选择里。
    if (state.year && !options.some((t) => t.id === state.year)) state.year = null;
    el.yearSelect.value = state.year ?? "";

    if (!options.length) {
      el.yearHint.textContent = "上游词表里没有年份组 —— 年份是词表给的，不是这里编的。";
      return;
    }
    const slot = state.tagSource === "site" ? "掩码第 5 槽" : "掩码第 5 段";
    el.yearHint.textContent = state.year
      ? `${state.year} 年是整年（${slot}），不是「${state.year} 年起」—— 所以这时不发 since。`
      : "不选就是「不限」，链接用上面的链接范围（追新 / 全量）。";
  }

  function renderMonth() {
    const site = state.tagSource === "site";
    // 月份是周期量，按日历升序排才符合直觉（词表的倒序是为了「最近优先」）。
    const options = (vocabGroups().find((g) => g.category_id === "month")?.tags ?? [])
      .slice()
      .sort((a, b) => Number(a.id) - Number(b.id));
    // 女优模式根本没有月份：状态里留着就是「界面上没有、链接里有」。
    if (!site) state.month = null;

    if (state.vocabState === "loading" && !state.vocab) {
      el.monthGroup.innerHTML = "";
      el.monthHint.textContent = "正在读取月份…";
      return;
    }
    if (state.vocabState === "error") {
      el.monthGroup.innerHTML = "";
      el.monthHint.textContent = "读不到标签词表，月份暂时不可选。";
      return;
    }
    if (state.month && !options.some((t) => t.id === state.month)) state.month = null;
    renderRadioChips(
      el.monthGroup,
      "month",
      options.map((t) => ({ value: t.id, label: `${t.name} 月` })),
      site ? state.month : null,
      site ? "" : "女优订阅不支持月份 —— 服务那侧遇到它直接 400",
    );
    el.monthHint.textContent = site
      ? "月份按日历升序。可以单独给（跨年，实测 `0:t:m::::3` 返回各年 3 月）；选中后 since 让位。"
      : "女优订阅不支持月份：App 的女优筛选面板里没有它，掩码里也没有它的位置（多写一段会让年份被静默丢弃），服务那侧遇到它直接 400。";
  }

  function renderDuration() {
    const site = state.tagSource === "site";
    const options = vocabGroups().find((g) => g.category_id === "duration")?.tags ?? [];
    // 女优模式没有它；全站模式没有年份它也不生效（上游会静默忽略）。
    if (!site || !state.year) state.duration = null;

    if (state.vocabState === "loading" && !state.vocab) {
      el.durationGroup.innerHTML = "";
      el.durationHint.textContent = "正在读取时长档位…";
      return;
    }
    if (state.vocabState === "error") {
      el.durationGroup.innerHTML = "";
      el.durationHint.textContent = "读不到标签词表，时长暂时不可选。";
      return;
    }
    // 词表里没有的档位（换了片库/上游改了）就不要留在选择里 —— 否则界面上
    // 没有这颗 chip、链接里却有它的 id（与 renderYear/renderMonth 同一套规矩）。
    if (state.duration && !options.some((t) => t.id === state.duration)) state.duration = null;
    const disabledTitle = !site
      ? "女优订阅不支持时长 —— 服务那侧遇到它直接 400"
      : !state.year
        ? "先选年份：实测单独给时长会被上游静默忽略（服务那侧会把这种写法定成 400）"
        : "";
    renderRadioChips(
      el.durationGroup,
      "duration",
      options.map((t) => ({ value: t.id, label: DURATION_LABELS[t.id] ?? t.name })),
      site ? state.duration : null,
      disabledTitle,
    );
    el.durationHint.textContent = site
      ? state.year
        ? "时长必须与年份一起给 —— 现在年份已选，这一排可用。"
        : "时长必须与年份一起给：实测单独给时长的结果与不筛逐条相同，所以先选年份。"
      : "女优订阅不支持时长（App 的女优筛选面板里也没有它）：掩码里多写一段会让年份被静默丢弃，服务那侧遇到它直接 400。";
  }

  /** 一排单选 chip，带一个「不限」。disabledTitle 非空时整排禁用（含「不限」）。 */
  function renderRadioChips(container, attr, options, current, disabledTitle) {
    const one = (value, label) =>
      `<button type="button" class="chip" role="radio" data-${attr}="${esc(value)}"
        aria-checked="${(current ?? "") === value}"
        tabindex="${(current ?? "") === value ? 0 : -1}"
        ${
          disabledTitle
            ? `disabled aria-disabled="true" title="${esc(disabledTitle)}"`
            : ""
        }>${esc(label)}</button>`;
    container.innerHTML = one("", "不限") + options.map((o) => one(o.value, o.label)).join("");
  }

  function renderTagUrl() {
    const site = state.tagSource === "site";
    const rejected = site && state.zoneRejected.has(state.zone);
    const canBuild = site ? !rejected : !!state.picker;
    el.tagUrl.textContent = rejected
      ? `这个片库没被放行 —— /rss/tags/${state.zone}.xml 会 404。`
      : canBuild
        ? tagUrl()
        : "先从上面选一位女优（或直接手输她的 id）。";

    const parts = [];
    if (canBuild) {
      // 提示要逐条对应 URL 里真发出去的参数：链接范围是一件事，筛选维度是
      // 另一件 —— 「全量」不会吞掉年份/月份那几项。
      if (state.tagMode === "all") parts.push("pages=20 取全部（较慢）");
      else if (!state.year && !(site && state.month)) {
        parts.push(`since=${todayISO()} 只取这之后发行的`);
      }
      if (state.year) parts.push(`year=${state.year}（整年）`);
      if (site && state.month) parts.push(`month=${state.month}（整月）`);
      if ((state.year || (site && state.month)) && state.tagMode !== "all") {
        parts.push("选了这个就不再发 since（本地下界与上游精确值同时发必然空）");
      }
      const letters = sortedFlags();
      if (letters.length) parts.push(`主属性 main=${letters.join(",")}（交集）`);
      if (site && state.duration) parts.push(`时长 ${state.duration}`);
      if (site) parts.push("掩码由服务构造：片库在路径里");
      const picked = sortedTags();
      if (picked.length) {
        parts.push(`标签 ${picked.map((t) => t.name).join(" + ")}（交集）`);
      } else if (!site && !letters.length && !state.year) {
        parts.push("⚠️ 一个筛选都没选 —— 这条等于该女优的全部作品");
      }
    }
    el.tagUrlHint.textContent = parts.join(" · ");
    el.tagAdd.disabled = !canBuild;
    el.tagCopy.disabled = !canBuild;
  }

  function renderTagModeHint() {
    const site = state.tagSource === "site";
    const picked = [state.year && `${state.year} 年`, site && state.month && `${state.month} 月`]
      .filter(Boolean)
      .join(" ");
    if (picked && state.tagMode === "new") {
      // 硬规则：两个过滤维度不能互相抵消。全量模式本来就不发 since，
      // 所以「让位」那句话只对追新成立。
      el.tagModeHint.textContent = `已选 ${picked} —— 这里让位：since= 是本服务的本地下界、year=/month= 是上游精确筛选，同时发必然得到空 feed（服务那侧也会判 400）。`;
      return;
    }
    if (state.tagMode === "all") {
      el.tagModeHint.textContent = picked
        ? `pages=20 取全部，另加 ${picked} 的筛选。`
        : site
          ? "pages=20：把这个片库的结果尽量取完（较慢）。"
          : "pages=20：把这位女优的作品尽量取完（较慢）。";
      return;
    }
    el.tagModeHint.textContent = `since=${todayISO()}：只取这天之后发行的。`;
  }

  function syncExpandButton() {
    const collapsed = el.tagGroups.querySelectorAll("details:not([open])").length > 0;
    el.tagExpand.textContent = collapsed ? "全部展开" : "全部收起";
    el.tagExpand.disabled = el.tagGroups.querySelectorAll("details").length === 0;
  }

  // ─────────────────────────── 清单与想看 ───────────────────────────

  function showListsNote(html) {
    el.listsNote.innerHTML = html;
    el.listsNote.classList.remove("hidden");
  }

  function hideListsNote() {
    el.listsNote.classList.add("hidden");
    el.listsNote.innerHTML = "";
  }

  /**
   * 清单区：一清单一链接，名字/条数/默认/私有都在卡片上。
   *
   * 私有清单的名字**可能读不到**（匿名读取私有清单会 NoPermission，feed 标题
   * 退回 id）—— 卡片上照实写这一句，链接仍然给，因为订阅本身是好的。
   */
  function renderLists() {
    if (state.listsState === "loading") {
      el.listCards.innerHTML = "";
      showListsNote(
        `<p class="text-sm font-medium">正在读取清单…</p>
         <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-muted-foreground">从服务的 <code class="font-mono">/collected_lists</code> 读。</p>`,
      );
      return;
    }

    if (state.listsError) {
      const err = state.listsError;
      const title =
        err.status === 503
          ? "读不到清单：还没配置 token"
          : err.status >= 500
            ? "读不到清单：上游出错了"
            : "读不到清单";
      el.listCards.innerHTML = "";
      showListsNote(
        `<p class="flex items-start gap-2 text-sm font-medium">
           <span aria-hidden="true">⛔</span>${esc(title)}
         </p>
         <p class="mt-2 text-xs leading-relaxed text-muted-foreground" data-server-message></p>
         <p class="mt-2 text-xs leading-relaxed text-muted-foreground">
           列清单读的是 App 里的数据，需要 token；「想看」读的也是它，所以那一区同样没有链接可给。标签区不受影响。
         </p>
         <button class="btn btn-md btn-outline mt-3" type="button" data-lists-retry>重新读取</button>`,
      );
      // 服务的原话原样显示（含它给的下一步动作），不在这里改写。
      el.listsNote.querySelector("[data-server-message]").textContent = err.message;
      el.listsNote.querySelector("[data-lists-retry]").addEventListener("click", () => {
        loadLists();
        loadCollected();
      });
      return;
    }

    if (!state.lists.length) {
      el.listCards.innerHTML = "";
      showListsNote(
        `<p class="text-sm font-medium">一份清单也没有</p>
         <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-muted-foreground">
           服务读到了你的账号，但还没有任何清单 —— 去 JavDB App 里建一份，然后刷新这一页。
         </p>`,
      );
      return;
    }

    hideListsNote();
    el.listCards.innerHTML = state.lists
      .map((l, i) => {
        const isPrivate = l.privacy === "own";
        const title = l.name || l.id;
        // 私有与「读不到名字」这两件事都要说出来（feed 标题会退回 id，
        // 而「为什么标题不是名字」正是用户会自己诊断不出来的那类问题）。
        // 私有优先：它解释了名字为什么会读不到 —— 即使这一次名字是空的、
        // 上面那个分支也能把两件事一并说清。
        const note = isPrivate
          ? "私有清单：名字可能读不到（匿名读取会退回用 id 当 feed 标题），但链接本身照常可用。"
          : !l.name
            ? "上游没给出这份清单的名字 —— feed 的标题会退回用它的 id，链接本身照常可用。"
            : "";
        return `
      <li class="flex flex-col rounded-lg border border-border bg-card p-4">
        <p class="flex flex-wrap items-baseline gap-1.5">
          <span class="text-sm font-medium">${esc(title)}</span>
          ${l.is_default ? '<span class="rounded-sm border border-border px-1 text-2xs text-muted-foreground">默认</span>' : ""}
          ${isPrivate ? '<span class="rounded-sm border border-border px-1 text-2xs text-muted-foreground">私有</span>' : ""}
        </p>
        <p class="mt-1 font-mono text-xs text-muted-foreground">
          ${esc(l.id)} · <span class="tnum">${Number(l.movies_count) || 0}</span> 部
        </p>
        <code class="mt-3 block rounded-md bg-muted px-2 py-1.5 font-mono text-xs break-all text-muted-foreground select-all">${esc(`${base()}${listFeed(l)}`)}</code>
        ${note ? `<p class="mt-2 text-xs leading-relaxed text-muted-foreground">${esc(note)}</p>` : ""}
        <div class="mt-3 flex flex-wrap gap-2">
          <button class="btn btn-md btn-outline" type="button" data-list-copy="${i}">复制</button>
          <button class="btn btn-md btn-ghost" type="button" data-list-add="${i}">加入待复制</button>
        </div>
      </li>`;
      })
      .join("");
  }

  /**
   * 想看区。没有发现端点能直接说「token 在不在」，所以用 /collected 与
   * /collected_lists 的 503 当信号 —— 三条路读的是同一份 App 标记。
   * 拿不到就不给链接：一条点了就 503 的 URL 比灰掉的按钮更坏。
   */
  function renderWant() {
    const blocked = tokenMissing();
    // 两个发现端点（/collected 与 /collected_lists）都还没说话之前，token 在不在
    // 还没定 —— 这时也**不填 URL**：一条能手拷走的链接与一个能点的按钮一样坏。
    const discoveryLoading = (state.loading && !state.error) || state.listsState === "loading";
    if (blocked) {
      el.wantUrl.textContent = "需要 token —— 配好 token_file 之后，这里会给出链接。";
      el.wantHint.textContent = `读不到「想看」：它读的是你在 App 里的标记，需要 token。请配置 app_api.token_file（见 README）。服务说：${tokenMessage()}`;
    } else if (discoveryLoading) {
      el.wantUrl.textContent = "正在确认 token…";
      el.wantHint.textContent =
        "token 在不在定下来之前不给链接 —— 免得给出一条一订就 503 的 URL。";
    } else {
      el.wantUrl.textContent = `${base()}${WANT_PATH}`;
      el.wantHint.textContent =
        "内容跟着你在 App 里的标记走，URL 里没有参数。还没有磁链的作品不发条目，数量写在 feed 标题里。";
    }
    el.wantCopy.disabled = blocked || discoveryLoading;
    el.wantAdd.disabled = blocked || discoveryLoading;
  }

  // ─────────────────────────── 待复制 ───────────────────────────

  /**
   * 待复制里的女优链接**完全由选择推导** —— 没有第二处存 URL 的地方。
   * 标签链接是另一回事：它在点「加入待复制」时快照一次（切换标签不该
   * 悄悄改掉已经放进待复制的那条），所以这里要把非女优的条目留着。
   */
  function syncTrayFromSelection() {
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
        sub: "收藏女优",
      }));
    state.links = [...keep, ...picked];
    renderTray();
  }

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
            去上面的「收藏女优」里勾几位、在「标签筛选」里做一条，
            或者在「清单」「想看」里各挑一条 —— 生成的链接都会落到这里。
          </p>
        </li>`;
      return;
    }

    el.trayList.innerHTML = state.links
      .map((l, i) => {
        const badge =
          l.kind === "want"
            ? "App 标记"
            : l.kind === "list"
              ? "清单"
              : l.mode === "all"
                ? "全量"
                : "追新";
        return `
      <li class="flex items-start gap-3 py-3">
        <div class="min-w-0 flex-1">
          <p class="flex flex-wrap items-center gap-1.5">
            <span class="text-sm font-medium">${esc(l.label)}</span>
            <span class="rounded-sm border border-border px-1 font-mono text-2xs text-muted-foreground">
              ${badge}
            </span>
            <span class="text-xs text-muted-foreground">${esc(l.sub || "")}</span>
          </p>
          <code class="mt-1 block font-mono text-xs break-all text-muted-foreground select-all">${esc(linkUrl(l))}</code>
        </div>
        <div class="flex shrink-0 gap-1">
          <button class="btn btn-sm btn-outline" type="button" data-copy-row="${i}">复制</button>
          <button class="btn btn-sm btn-ghost" type="button" data-remove-row="${i}"
            aria-label="移除 ${esc(l.label)}">移除</button>
        </div>
      </li>`;
      })
      .join("");
  }

  function setTrayOpen(open) {
    el.trayPanel.hidden = !open;
    el.trayToggle.setAttribute("aria-expanded", String(open));
    el.trayToggle.textContent = open ? "收起" : "查看";
  }

  // ─────────────────── 标签区的动作 ───────────────────

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
    // 重渲染换掉了刚点的那颗 chip —— 把焦点接到新的同一颗上，
    // 否则键盘用户每点一个标签就被扔回 <body>。
    const again = el.tagGroups.querySelector(`[data-tag="${CSS.escape(id)}"]`);
    if (again && document.activeElement !== el.tagSearch) again.focus();
    if (state.tags.size === MAX_TAGS) announce(`标签已选满 ${MAX_TAGS} 个（上游硬限制）`);
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
    syncTrayFromSelection();
  }

  radioGroup(el.genderGroup, "gender", selectGender);

  radioGroup(el.modeGroup, "mode", (v) => {
    state.mode = v;
    // 默认模式变了，逐行的覆盖就没意义了 —— 清掉，让所有人跟上新默认。
    state.rowMode.clear();
    renderActors();
    syncTrayFromSelection();
  });

  radioGroup(el.tagModeGroup, "tagmode", (v) => {
    state.tagMode = v;
    renderTagModeHint();
    renderTagUrl();
  });

  /**
   * 切换标签来源。两种模式的控件状态**不共享**：
   *
   *   · 标签与主属性都按「从哪儿来」取候选集 —— 她的 tags[]/filter_tags ↔
   *     整个片库的词表。换模式就换了一套候选，留着旧的会出现「界面上没有、
   *     链接里有」那种最坏的不一致。
   *   · 月份与时长只有全站形态有对应的掩码槽位，切回女优模式必须清掉。
   *
   * 年份是两种模式都有的真筛选，留着 —— renderYear 会用新词表校验它还在不在。
   */
  function switchTagSource(v) {
    if (v === state.tagSource) return;
    state.tagSource = v;
    clearFilterSelection();
    if (v === "actress") {
      state.month = null;
      state.duration = null;
    }
    syncVocabulary();
    renderTagArea();
  }

  /** 清掉标签与主属性的选择（换人 / 换模式 / 换片库时用 —— 它们都属于某个候选集）。 */
  function clearFilterSelection() {
    state.flags.clear();
    state.tags.clear();
    state.tagSearch = "";
    el.tagSearch.value = "";
    state.groupOpen.clear();
  }

  radioGroup(el.tagSourceGroup, "source", switchTagSource);

  /**
   * 把一排 role=radio 的 chip 接上点击与方向键。选中值变了就重渲染，
   * 因而要把焦点还回同值的那一颗 —— 否则键盘用户每选一下就掉回 <body>。
   */
  function bindChipRadio(container, attr, onPick) {
    const activate = (btn) => {
      const value = btn.dataset[attr] || null;
      onPick(value);
      const again = container.querySelector(`[data-${attr}="${CSS.escape(value ?? "")}"]`);
      if (again) again.focus();
    };
    container.addEventListener("click", (e) => {
      const btn = e.target.closest(`[data-${attr}]`);
      if (!btn || btn.disabled) return;
      activate(btn);
    });
    container.addEventListener("keydown", (e) => {
      const step =
        e.key === "ArrowRight" || e.key === "ArrowDown"
          ? 1
          : e.key === "ArrowLeft" || e.key === "ArrowUp"
            ? -1
            : 0;
      if (!step) return;
      const btns = [...container.querySelectorAll(`[data-${attr}]:not([disabled])`)];
      const i = btns.indexOf(document.activeElement);
      if (i < 0) return;
      e.preventDefault();
      activate(btns[(i + step + btns.length) % btns.length]);
    });
  }

  /**
   * 换片库 = 换一套 id 空间（四个库的标签 id 各算各的）。旧选择留在 URL 里
   * 就是「看不见却生效」，所以一律清掉；词表也要跟着重新拉 ——
   * 这正是「词表按片库取、不是写死快照」的用处。
   */
  bindChipRadio(el.zoneGroup, "zone", (v) => {
    const zone = Number(v);
    if (zone === state.zone) return;
    state.zone = zone;
    clearFilterSelection();
    syncVocabulary();
    renderTagArea();
  });

  bindChipRadio(el.monthGroup, "month", (v) => {
    state.month = v;
    renderTime();
    renderTagModeHint();
    renderTagUrl();
  });

  bindChipRadio(el.durationGroup, "duration", (v) => {
    state.duration = v;
    renderTime();
    renderTagUrl();
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
    const a = actressById(id);
    const name = a ? a.name : id;
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

  // ── 标签区 ──

  el.tagPicker.addEventListener("input", () => {
    // 只提示，不取数 —— 每敲一个字就发一次上游请求没有意义。
    el.tagPickerStatus.textContent = el.tagPicker.value.trim()
      ? "回车（或点到别处）读取她的标签。"
      : "还没选女优。填名字或 id 都行，回车读取。";
  });
  el.tagPicker.addEventListener("change", () => {
    if (el.tagPicker.value.trim()) commitPicker(el.tagPicker.value);
  });
  el.tagPicker.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    if (el.tagPicker.value.trim()) commitPicker(el.tagPicker.value);
  });

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
    syncExpandButton();
  });

  // 展开状态只记**用户点过**的那些。
  //
  // 刻意不用 <details> 的 toggle 事件：程序化地设 open（搜索时全展开、
  // 重渲染时恢复）一样会触发它，于是「搜索展开」会被当成用户的选择记下来，
  // 清掉搜索后就再也折不回去了。改成只认 summary 上的真实点击 ——
  // 键盘的 Enter/Space 也会产生 click，所以键盘用户照样算数。
  el.tagGroups.addEventListener("click", (e) => {
    const summary = e.target.closest("summary");
    if (summary) {
      const d = summary.closest("details[data-group]");
      if (d) {
        // 点击的默认动作在监听器之后才翻转 open，所以等一拍再读。
        window.setTimeout(() => {
          state.groupOpen.set(d.dataset.group, d.open);
          syncExpandButton();
        }, 0);
      }
      return;
    }
    const btn = e.target.closest("[data-tag]");
    if (!btn) return;
    toggleTag(btn.dataset.tag, btn.dataset.tagName, btn.dataset.tagCat);
  });

  el.tagExpand.addEventListener("click", () => {
    const open = el.tagGroups.querySelectorAll("details:not([open])").length > 0;
    el.tagGroups.querySelectorAll("details[data-group]").forEach((d) => {
      state.groupOpen.set(d.dataset.group, open);
    });
    renderTagGroups();
    syncExpandButton();
    announce(open ? "已展开全部标签组" : "已收起全部标签组");
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
    // 被取消的按钮已经不在了 —— 把焦点还给标签区第一颗同类控件。
    const next = el.tagGroups.querySelector("[data-tag]") || el.tagSearch;
    next.focus();
  });

  el.yearSelect.addEventListener("change", () => {
    state.year = el.yearSelect.value || null;
    // 时长必须与年份同给：年份一没，它就不生效了 —— 留着就是「选着但链接里没有」。
    if (!state.year) state.duration = null;
    renderTime();
    renderTagModeHint();
    renderTagUrl();
  });

  el.tagEmpty.addEventListener("click", (e) => {
    if (!e.target.closest("[data-tag-retry]")) return;
    if (state.vocabState === "error") loadVocabulary();
    if (state.profileState === "error" && state.picker) loadActressTags(state.picker.id);
  });

  el.tagAdd.addEventListener("click", () => {
    if (state.tagSource === "site") {
      if (state.zoneRejected.has(state.zone)) return; // 不给一条会 404 的链接
      const ids = sortedTags();
      const flags = sortedFlags(); // 总是含 m
      // 同一个片库只留一条：它是一份「当前怎么筛」的快照，重复点就是同一条链的
      // 新版本；不同片库之间互不影响。
      state.links = state.links.filter((l) => l.kind !== "site" || l.zone !== state.zone);
      state.links.push({
        key: `site:${state.zone}`,
        kind: "site",
        zone: state.zone,
        mode: state.tagMode,
        year: state.year,
        month: state.month,
        duration: state.duration,
        flags,
        tags: ids.map((t) => t.id),
        label: `全站 · ${zoneName(state.zone)} · 标签筛选`,
        sub: ids.length ? `${ids.length} 个标签` : "无标签",
      });
      renderTray();
      setTrayOpen(true);
      announce("已加入待复制");
      return;
    }
    if (!state.picker) return;
    const name = state.profile?.name || state.picker.name || state.picker.id;
    const ids = sortedTags();
    const flags = sortedFlags();
    // 同一位女优只留一条标签链接：它是一份「当前怎么筛」的快照，
    // 重复点「加入待复制」是同一条链的新版本，不该在待复制里堆成好几条。
    // 不同女优之间互不影响 —— 挑完 A 再挑 B，A 那条还在。
    state.links = state.links.filter((l) => l.kind !== "tags" || l.id !== state.picker.id);
    state.links.push({
      key: `tags:${state.picker.id}`,
      kind: "tags",
      id: state.picker.id,
      mode: state.tagMode,
      year: state.year,
      flags,
      tags: ids.map((t) => t.id),
      label: `${name} · 标签筛选`,
      sub: ids.length ? `${ids.length} 个标签` : "无标签",
    });
    renderTray();
    setTrayOpen(true);
    announce("已加入待复制");
  });

  el.tagCopy.addEventListener("click", () => {
    const rejected = state.tagSource === "site" && state.zoneRejected.has(state.zone);
    const canBuild = (state.tagSource === "site" && !rejected) || !!state.picker;
    if (!canBuild) return;
    doCopy(el.tagCopy, tagUrl(), state.tagSource === "site" ? "这条全站链接" : "这条标签链接");
  });

  // ── 清单与想看 ──

  el.listCards.addEventListener("click", (e) => {
    const copy = e.target.closest("[data-list-copy]");
    if (copy) {
      const l = state.lists[Number(copy.dataset.listCopy)];
      if (l) doCopy(copy, `${base()}${listFeed(l)}`, `清单「${l.name || l.id}」的链接`);
      return;
    }
    const add = e.target.closest("[data-list-add]");
    if (!add) return;
    const l = state.lists[Number(add.dataset.listAdd)];
    if (!l) return;
    // 同一份清单只留一条：链接跟着 id 走，重复点就是同一条的新版本。
    state.links = state.links.filter((x) => !(x.kind === "list" && x.id === l.id));
    state.links.push({
      key: `list:${l.id}`,
      kind: "list",
      id: l.id,
      feed: listFeed(l),
      label: l.name || l.id,
      sub: `${Number(l.movies_count) || 0} 部`,
    });
    renderTray();
    setTrayOpen(true);
    announce("已加入待复制");
  });

  el.wantCopy.addEventListener("click", () => {
    if (el.wantCopy.disabled) return;
    doCopy(el.wantCopy, `${base()}${WANT_PATH}`, "「想看」链接");
  });

  el.wantAdd.addEventListener("click", () => {
    if (el.wantAdd.disabled) return;
    // 它是一条固定路径：已经在待复制里就不再堆第二条。
    if (state.links.some((l) => l.kind === "want")) {
      setTrayOpen(true);
      announce("「想看」已经在待复制里了");
      return;
    }
    state.links.push({ key: "want", kind: "want", label: "想看" });
    renderTray();
    setTrayOpen(true);
    announce("已加入待复制");
  });

  // ── 待复制 ──

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
      // 女优链接不是独立实体，而是选择的投影：移除它等于取消勾选，
      // 否则「列表里没有、选择里还在」这种不一致下次重渲染就会自己长回来。
      if (l.kind === "actress") {
        state.selected.delete(l.id);
        state.rowMode.delete(l.id);
        const box = el.actressList.querySelector(`[data-actress="${CSS.escape(l.id)}"]`);
        if (box) box.checked = false;
        syncSelectionSummary();
      } else {
        state.links.splice(i, 1);
      }
      if (l.kind === "actress") syncTrayFromSelection();
      else renderTray();
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
    try {
      localStorage.setItem(BASE_KEY, el.base.value.trim());
    } catch (e) {}
    renderTray();
    renderTagUrl();
    // 清单卡片与「想看」也把服务地址写进它们显示的 URL 里，一起重画。
    renderLists();
    renderWant();
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
    syncTrayFromSelection();
    renderTagArea();
    renderOptions();
    // 收藏的 503 也是「想看」能不能给链接的信号，所以这里跟着重画。
    renderWant();
    // 收藏里到底有几位男优 —— 不然「只看女优 / 全部收藏」这个开关看上去没由来。
    const males = state.actresses.filter((a) => a.gender !== 0).length;
    el.genderLabel.textContent = males ? `收藏里有 ${males} 位男优` : "只看女优";
  }

  /** 标签区的女优输入框带一份收藏的候选（datalist）；手输 id 那条路不受它限制。 */
  function renderOptions() {
    el.actressOptions.innerHTML = state.actresses
      .map(
        (a) =>
          `<option value="${esc(optionLabel(a))}" label="${esc(optionLabel(a))} · ${Number(a.videos_count) || 0} 部"></option>`,
      )
      .join("");
  }

  try {
    el.base.value = localStorage.getItem(BASE_KEY) || location.origin;
  } catch (e) {
    el.base.value = location.origin;
  }
  el.todayEcho.textContent = todayISO();
  syncThemeButton();
  renderTray();
  renderTagArea();
  renderTagModeHint();
  renderLists();
  renderWant();
  loadVocabulary();
  loadLists();
  loadCollected();
  // 服务与上游状态（票 07）：/version 给白名单三类，/readyz 给上游健康。
  // 两者都不阻塞收藏列表的渲染 —— 状态说完之前页面照常能用。
  loadVersion();
  loadReadyz();
})();
