# CLAUDE.md

This file points Claude Code (claude.ai/code) at the canonical agent guide.

**Read [`AGENTS.md`](./AGENTS.md) first** — it is the source of truth for this
repository's stack, commands, architecture, security rules, and conventions.
When the two disagree, **AGENTS.md wins**.

## Agent Onboarding (MANDATORY — run on session start)

```bash
# 1. Check if the knowledge graph exists
if [ ! -f graphify-out/graph.json ]; then
  echo "Knowledge graph missing — rebuilding..."
  graphify .
fi

# 2. Verify it's fresh (rebuilt within last 30 days)
graphify --update . 2>/dev/null || true
```

**Do NOT read source files without querying the graph first.**
**After significant changes (5+ files or new modules):** run `graphify --update .`
before committing. A post-commit hook handles smaller updates automatically.
See `## Knowledge Graph (graphify)` in AGENTS.md for commands.

Quick reference (details in AGENTS.md):

- Dev server: `bun dev` → http://localhost:8084 (strictPort)
- Verify before declaring done: `tsc --noEmit`, `bun lint`, `bun test`, `bun build`
- One REST transport under `/api/*` dispatched from `src/server.ts` — no `createServerFn`
- Never log secrets — use `src/lib/logger.ts` (`createLogger`), not `console.*`
- `VENOM_ENCRYPTION_KEY` is required; there is no fallback
- See `.env.example` for all environment variables

## Knowledge Graph

This project has a knowledge graph in `graphify-out/` (gitignored). **Query
the graph before reading source files** to save context tokens:

```bash
graphify query "question"        # BFS broad context
graphify query "question" --dfs  # DFS specific path
graphify explain "Concept"       # single-node explanation
```

After significant code changes, rebuild: `graphify --update .`

See the **Knowledge Graph (graphify)** section in AGENTS.md for full details.
