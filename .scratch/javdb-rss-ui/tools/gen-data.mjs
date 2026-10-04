/*
 * 把探针抓下来的上游原始响应变成 mockup 用的数据文件。
 *
 *   node tools/gen-data.mjs
 *
 * 输入：evidence/（contractprobe 落盘的原始响应，见 notes/tag-vocabulary.md）
 * 输出：mockup/src/data.js
 *
 * 为什么不让 mockup 直接 fetch 上游：设计稿要能离线打开、要能被 Playwright
 * 反复截图，而且不该在评审时打真实上游。数据是**真的**，只是冻在了这里。
 */
import { readFileSync, writeFileSync, readdirSync } from "node:fs";

const E = new URL("../evidence/", import.meta.url);
const read = (p) => JSON.parse(readFileSync(new URL(p, E), "utf8"));

// ── 1. 收藏的演员（144 位，真实数据）────────────────────────────────
// gender 来自**上游**（/api/v1/users/collected_actors 每项带 gender）；
// 服务的 /collected 目前把它丢掉了 —— 这是本设计稿要指出的一处缺口。
const pages = readdirSync(new URL("collected_actors_pages/", E)).sort(
  (a, b) => Number(a.match(/\d+/)[0]) - Number(b.match(/\d+/)[0]),
);
const actors = [];
const seen = new Set();
for (const f of pages) {
  for (const a of read(`collected_actors_pages/${f}`).actors ?? []) {
    if (seen.has(a.id)) continue;
    seen.add(a.id);
    actors.push({
      id: a.id,
      name: a.name,
      videos: a.videos_count ?? 0,
      // 0 = 女优，1 = 男优（实测 144 位里 138 女 / 6 男）
      gender: a.gender ?? 0,
    });
  }
}

// ── 2. 标签词表（/api/v2/tags?type=0，11 组 355 个标签，真实数据）────
const vocab = read("tags_type0.json").tags.map((g) => ({
  categoryId: g.category_id,
  category: g.category,
  tags: g.tags.map((t) => ({ id: String(t.id), name: t.name })),
}));

// ── 3. 每位女优自己的标签（/api/v1/actors/{id} 的顶层 tags[]）────────
const actressTags = {};
for (const f of readdirSync(E).filter((f) => f.startsWith("actor_"))) {
  const id = f.replace(/^actor_|\.json$/g, "");
  const tags = read(f).tags ?? [];
  if (tags.length) {
    actressTags[id] = tags.map((t) => ({
      id: String(t.id),
      name: t.name,
      count: t.videos_count ?? 0,
    }));
  }
}

// ── 4. 清单：真实数据（服务已经实现了 /collected_lists 与 /rss/list/{id}.xml）──
// 原始响应是上游 /api/v1/lists/simple（见 evidence/lists/simple.json）；
// 服务的 /collected_lists 就是把它映射一遍再加个 feed 路径。
const lists = (read("lists/simple.json").lists ?? []).map((l) => ({
  id: l.id,
  name: l.name,
  count: l.movies_count ?? 0,
  isDefault: !!l.is_default,
  privacy: l.privacy ?? "",
}));

const out = `/*
 * 由 tools/gen-data.mjs 生成 —— 不要手改。
 * 来源：evidence/ 下 contractprobe 抓到的**真实上游响应**。
 * 每一样都是真的（演员、标签词表、每位女优的标签、清单）。
 */
window.JAVDB_DATA = ${JSON.stringify({ actors, tagVocab: vocab, actressTags, lists }, null, 0)};
`;

writeFileSync(new URL("../mockup/src/data.js", import.meta.url), out);
console.log(
  `data.js: ${actors.length} 位演员（${actors.filter((a) => a.gender === 0).length} 女 / ${actors.filter((a) => a.gender === 1).length} 男）` +
    `, ${vocab.length} 个标签组 / ${vocab.reduce((n, g) => n + g.tags.length, 0)} 个标签` +
    `, ${Object.keys(actressTags).length} 位女优的标签` +
    `, ${lists.length} 份真实清单`,
);
