#!/usr/bin/env bash
#
# 一条命令跑完这套页面的全部验证。
#
#   ./tools/verify.sh [origin]
#
# origin 默认取环境变量 ORIGIN（兼容旧的 MOCKUP_ORIGIN），再不行用
# provider=stub 的默认监听地址。
#
# ⚠️ 它打的是**真服务**，所以先把它起起来：
#
#   go run ./cmd/javdb-rss -config <一份 provider: stub 的配置>
#
# 需要：node 与一个能解析的 playwright（本仓库用软链把外面那份接进
# node_modules，见 mockup/README.md）。
#
# CSS 不再在这里编译：Tailwind 产物（internal/webui/assets/app.css）已入库 ——
# 评审与构建都不该先装一遍工具链。
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(cd ../.. && pwd)"

ORIGIN="${1:-${ORIGIN:-${MOCKUP_ORIGIN:-http://127.0.0.1:8080}}}"
echo "origin: $ORIGIN"

echo
echo "── L1 闸门（token-lint：源码里不许出现色值字面量） ────"
# 资产源码 = 页面的 HTML/JS（页面的 CSS 由 mockup/src/input.css 编译而来）。
grep -o '#[0-9a-fA-F]\{3,8\}' \
  "$ROOT/internal/webui/index.html" \
  "$ROOT/internal/webui/assets/app.js" \
  mockup/src/input.css \
  && { echo "✗ 源码里出现了色值字面量"; exit 1; } || echo "✓ 源码无 raw hex"

echo
echo "── 硬闸门：axe / 触摸目标 / 焦点 / 溢出 / 主操作 ─────"
node tools/a11y.mjs "$ORIGIN"

echo
echo "── 行为：逐条断言生成的 URL 与服务契约逐字符相符 ───"
node tools/flows.mjs "$ORIGIN"

echo
echo "── L4 rubric（分在代码里算） ────────────────────────"
node tools/rubric.mjs

echo
echo "全部通过。"
echo "（截图与 mockup 是设计记录，保留在 mockup/ 与 shots/，不在这里重跑。）"
