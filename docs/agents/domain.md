# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root — this repo's glossary is its `## Language`
  section (there is no separate `GLOSSARY.md`). Terms carry `_Avoid_:` lines:
  those are the synonyms the project deliberately rejects.
- **`docs/adr/`** — read ADRs that touch the area you're about to work in.
- **`.scratch/<effort>/notes/`** — the measured facts about the upstream
  contract (field semantics, mask syntax, silently-ignored parameters). The
  maps say "read it before starting a session"; that applies to agents too.

If any of these files don't exist, **proceed silently**. Don't flag their
absence; don't suggest creating them upfront. The `/domain-modeling` skill
(reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates
them lazily when terms or decisions actually get resolved.

## File structure

Single-context repo:

```
/
├── CONTEXT.md          ← 术语表（`## Language`）
├── docs/adr/           ← 决策记录（尚未创建，按需懒创建）
├── docs/agents/        ← 本目录：技能读的约定
├── internal/           ← 代码
└── .scratch/<effort>/  ← 地图 / spec / ticket / notes / 证据
```

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal,
a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift
to synonyms the glossary explicitly avoids — e.g. 磁链候选 not 种子,
作品 not 影片, 清单 not 片单, 订阅 not 规则, 钉住 not 缓存.

If the concept you need isn't in the glossary yet, that's a signal: either
you're inventing language the project doesn't use (reconsider) or there's a real
gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than
silently overriding:

> _Contradicts ADR-0007 (event-sourced orders), but worth reopening because…_
