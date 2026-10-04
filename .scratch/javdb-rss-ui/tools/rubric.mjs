/*
 * L4 rubric 算分（评审用，不进产物）。
 *
 *   node tools/rubric.mjs
 *
 * 模型只负责逐条判 PASS/FAIL 并给理由；分数在这里算，不由模型报一个浮点。
 */
import { readFileSync } from "node:fs";

const { checks } = JSON.parse(readFileSync(new URL("./rubric.json", import.meta.url), "utf8"));

let pass = 0;
for (const c of checks) {
  if (c.pass) pass++;
  console.log(
    `${c.pass ? "PASS" : "FAIL"}  ${c.id.padEnd(16)} ${c.needsHuman ? "[还需要人眼定稿] " : ""}${c.reason}`,
  );
  console.log(`      ↳ ${c.evidence}`);
}
const score = pass / checks.length;
console.log(
  `\nrubric ${pass}/${checks.length} = ${(score * 100).toFixed(0)}%` +
    (checks.some((c) => c.needsHuman) ? "（含未人眼确认项）" : ""),
);
process.exit(pass === checks.length ? 0 : 1);
