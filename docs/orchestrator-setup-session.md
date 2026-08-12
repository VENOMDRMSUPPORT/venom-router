# Multi-Agent Orchestrator — Setup Session Log

Date: 2026-08-10
Project: venom-router (Desktop)
Goal: Replace the manual copy/paste loop between ChatGPT (reviewer) and Claude
(executor) with a single automated multi-agent pipeline inside Claude Code.

---

## 1. The problem we started from

Previous workflow was fully manual:

1. Ask Claude for a task -> it writes a spec.
2. Copy the spec, paste into ChatGPT to review.
3. Copy ChatGPT's review, paste back into Claude.
4. Loop until the spec is agreed.
5. Claude implements, writes a report.
6. Copy the report into ChatGPT to verify success.

Pain point: constant copy/paste between two desktop apps. The user wanted to
submit one request and watch the agents work together automatically.

---

## 2. Decisions made during the discussion

**n8n?** Considered and rejected. n8n automates via APIs; it cannot drive the
desktop apps, and adding a separate server conflicts with the zero-dependency
philosophy. Not needed for this use case.

**OpenCode vs Claude Code?** The user's key requirement was a genuine second
opinion. OpenCode is multi-provider (could mix Claude + GPT + Gemini). Claude
Code's subagents are Claude-only. The user accepted an all-Claude setup where a
stronger model reviews a weaker one (Opus reviews Sonnet), so we chose Claude
Code for its simpler, more polished experience.

**Human involvement?** The user wants to watch only. The system runs
autonomously and stops to ask ONLY when things drift.

**Model assignment (final):**

- Orchestrator (lead / decision maker): Opus
- Reviewer (independent critic / second opinion): Opus
- Spec-writer: Sonnet
- Planner: Sonnet
- Executor (only agent that edits files): Sonnet

**Placement:** Project-level, inside `venom-router/.claude/` (this project only).

---

## 3. Files created

    venom-router/.claude/
    ├── agents/
    │   ├── spec-writer.md   (Sonnet, read-only)  writes the spec
    │   ├── reviewer.md      (Opus,   read-only + Bash)  independent critic
    │   ├── planner.md       (Sonnet, read-only)  writes the plan
    │   └── executor.md      (Sonnet, full tools)  the only editor
    └── commands/
        └── orchestrate.md   the lead: full loop + stop conditions

## 4. The loop

request -> spec-writer -> reviewer(spec) -> planner -> reviewer(plan)
        -> executor -> reviewer(execution) -> done

Writer and reviewer are always different agents (the second-opinion guarantee).

## 5. Stop conditions (when the lead halts and asks the human)

1. LOOPING — a gate reaches its 3rd CHANGES verdict without APPROVED.
2. SCOPE_CREEP — work grows beyond the original request / spec's In-scope.
3. INVARIANT_BREAK — completing it would break a documented invariant.
4. AMBIGUITY — request is ambiguous and the lead cannot resolve it safely.

## 6. How to run

    cd %USERPROFILE%\Desktop\venom-router
    claude --model opus
    /orchestrate <describe the task>

---

## 7. First test run (proof it works)

Command:

    /orchestrate add a /health endpoint that returns uptime and version

The orchestrator (Opus 5, high effort) ran pre-flight before delegating and
found a real design conflict, then STOPPED correctly instead of blindly
implementing:

- `/health` already exists at `internal/httpapi/health.go:28`, returning
  `{"status":"ok"}` (registered in `controlmux.go:114` via HealthMux / ControlMux).
- `docs/01-architecture.md §6d` documents an invariant: `/health` returns ONLY a
  minimal liveness signal (process up, listener accepting) and NO owner data.
- There is no real source for `version` — it is a `"dev"` placeholder in
  `venomheaders.go:17`; a real value would need build-time injection (ldflags in
  `Taskfile.yml:56`), which overlaps task P8-REL-005.

STOP verdict: AMBIGUITY (with an INVARIANT_BREAK risk).

Options the lead surfaced:

- A) Expand `/health` itself — requires editing the documented §6d contract.
- B) A + inject real version at build time (ldflags) — expands scope, touches P8-REL-005.
- C) Put uptime/version on a separate readiness surface
  (`api/control/v1/health/`) and leave `/health` untouched — breaks no invariant.
- D) Cancel — liveness is already covered.

Outcome: The system behaved exactly as designed. On its very first task it
caught an architectural invariant conflict and paused for a human decision
rather than silently breaking the project's design.

## 8. Next step (pending human decision)

In the Claude Code prompt, type the chosen option (recommended: C) to let the
loop continue: spec -> review -> plan -> review -> execute -> review.
