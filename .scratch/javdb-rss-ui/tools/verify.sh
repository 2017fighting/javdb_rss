#!/usr/bin/env bash
#
# 一条命令跑完这套 mockup 的全部验证。
#
#   ./tools/verify.sh [origin]
#
# origin 默认取环境变量 MOCKUP_ORIGIN，再不行用 serve_mockup 给的本地地址。
# 需要：node、已装好的 tailwindcss（npm i）、以及一个能解析的 playwright
# （本仓库用软链把外面那份接进 node_modules，见 README「怎么跑」）。
set -euo pipefail
cd "$(dirname "$0")/.."

ORIGIN="${1:-${MOCKUP_ORIGIN:-http://localhost:62100}}"
echo "origin: $ORIGIN"

echo
echo "── 构建 CSS ──────────────────────────────────────────"
./node_modules/.bin/tailwindcss -i mockup/src/input.css -o mockup/assets/app.css --minify

echo
echo "── L1/L2 闸门（token-lint + 对比度） ─────────────────"
grep -o '#[0-9a-fA-F]\{3,8\}' mockup/index.html mockup/app.js mockup/src/input.css \
  && { echo "✗ 源码里出现了色值字面量"; exit 1; } || echo "✓ 源码无 raw hex"

echo
echo "── 硬闸门：axe / 触摸目标 / 焦点 / 溢出 / 主操作 ─────"
node tools/a11y.mjs "$ORIGIN"

echo
echo "── 行为：四个需求逐条断言 URL 与服务契约逐字符相符 ───"
node tools/flows.mjs "$ORIGIN"

echo
echo "── L4 rubric（分在代码里算） ────────────────────────"
node tools/rubric.mjs

echo
echo "── 截图（人看用） ──────────────────────────────────"
node tools/shots.mjs "$ORIGIN" ./shots

echo
echo "全部通过。"
