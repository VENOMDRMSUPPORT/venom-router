# Venom Design System

The visual and interaction foundation for **Venom Router** — a private, single-owner AI gateway that pools many LLM provider accounts behind three products (`venom/lite`, `venom/pro`, `venom/max`). This package is the deliverable of the dedicated Design System creation task specified by `docs/07-design-system.md` in the venom-router planning repository (attached as `uploads/venom-router/`; also mounted locally as `venom-router/`).

**Surface it serves:** an owner-only infrastructure control center — a fleet/operations console. One operator, no roles, no teams, no marketing surfaces. The UI's jobs: authenticate the owner, enroll and monitor providers/accounts, inspect models/offerings/certification, watch quota, explain routing decisions, manage Venom API keys, backup/restore, settings.

**Identity:** precise · technical · controlled · trustworthy · high-signal · calm under failure. Truthful state above all — `unknown`, `stale`, and `degraded` read as clearly as `healthy`, never faked as a confident number or conveyed by color alone. The system should communicate: **Venom decides. The transport executes.**

**Explicitly avoided:** gradients-as-decoration, glassmorphism, neon cyberpunk, purple-AI branding, oversized marketing type, card soup, meaningless charts, color-only status.

---

## Sources

- `uploads/venom-router/README.md` — product principles + glossary (canonical vocabulary).
- `uploads/venom-router/docs/07-design-system.md` — the authoritative DS brief (tokens, themes, inventory, guardrails).
- `docs/01–06, 08–10` — architecture, domain model (state machines), provider catalog, model intelligence (certification), tier engine (routing states), roadmap, engineering standards, control API (auth/job states), coverage matrix.
- No prior UI exists. The legacy "Hex AIOS" bundle was removed and is **not** referenced. No logo asset was provided — the brand mark is set in plain type (see Iconography); no logo was invented.

## Token architecture (three layers)

1. **Primitive** — raw palette + scales. Authored in `tokens/tokens.primitive.json` (W3C Design Tokens format). Never consumed by components; encodes no product meaning.
2. **Semantic** — roles (`surface.*`, `text.*`, `border.*`, `status.*`, `tier.*`, `action.*`, `focus.*`, `viz.*`). One complete mapping **per theme**: `tokens/tokens.semantic.venom-dark.json` and `…venom-light.json`. Every theme defines the identical token set — validated mechanically (`validation/`).
3. **Component** — per-part knobs (`button.primary.bg`, `table.row.hover.bg`, `quota.meter.warning.fg`) in `tokens/tokens.component.json`, derived **only** from semantic tokens.

JSON is the sole authored source. `validation/build-tokens.cjs` compiles it to three outputs: CSS custom properties (`tokens/*.css`, `themes/*.css`), a typed object (`tokens/tokens.ts`), and a Tailwind theme extension (`tokens/tailwind-theme.ts`, exposed as the `@venom/design-system/tailwind` subpath) whose values are `var(--…)` references so utilities stay runtime-themed through `data-theme`. Generated files carry a `GENERATED` header — never hand-edit them. Naming grammar: `category.role.variant.state` → CSS var `--category-role-variant-state`.

## Themes

| Theme | attr | Role |
|---|---|---|
| **Venom Dark** | `data-theme="venom-dark"` (default, also `:root`) | Deep neutral canvas, restrained venom-green accent, calm status hues for long sessions. |
| **Venom Light** | `data-theme="venom-light"` | Full-parity light theme for bright environments/screenshots. |

Density is a second axis: `data-density="comfortable"` (default) / `"compact"` — spacing and control heights switch through tokens only; layouts never fork.

## Visual foundations

- **Color:** near-black cool-neutral canvas (`#0C1013` family) with three background depths (canvas → surface → raised). One restrained brand accent — a muted venom green — used for primary actions and active nav only; **the accent never carries status meaning**. Status hues: healthy green, degraded orange, warning amber, critical red, info blue, unknown = neutral slate with a dashed/hollow treatment (missing evidence, not failure), inactive = flat neutral. Tier accents are their own tokens: Lite = sky, Pro = violet, Max = gold — the only way a tier is colored, anywhere.
- **Type:** IBM Plex Sans (UI) + IBM Plex Mono (identifiers, keys, model IDs, traces, numerics). Base UI size 14px; dense data 13px; captions never below 11px. **Tabular figures are mandatory for numeric columns.** Monospace is a deliberate signal: if it's an identifier, a timestamp, an error code, or a quantity, it is mono.
- **Spacing:** base-4 scale (`--space-1..-32`). No off-scale spacing.
- **Radius:** small and disciplined — controls 4px, cards/panels 6–8px, popovers 8px. No pill cards; `full` reserved for status dots and count pills.
- **Elevation:** dark theme layers by surface lightness + hairline borders with subtle shadow; light theme leans on shadow. Semantic steps: card / popover / modal / toast.
- **Borders:** hairlines everywhere (`border.subtle` for dividers, `default` for containers, `strong` for selection). Focus is one global ring token via `:focus-visible` in both themes.
- **Motion:** restrained and functional. `duration.instant|fast|base|slow` (0/100/160/240ms), `ease.standard|emphasized|exit`. Everything collapses to 0ms under `prefers-reduced-motion`. No bounce, no parallax, no decorative animation.
- **Hover:** background steps one surface level (or 4–6% lightness), never color-hue changes. Press: one further step, no scale transforms on controls.
- **Cards:** used only where they express a real boundary (stat tiles, quota windows, enrollment choices). Operational data prefers tables, sections, toolbars, inspectors, drawers, and traces over card grids.
- **Imagery:** none. This is an instrument panel — no illustrations, no stock imagery, no emoji.

## Content fundamentals

- **Casing:** Sentence case for everything — titles, buttons, labels ("Connect provider", not "Connect Provider"). Products are lowercase code identifiers when technical (`venom/lite`) and capitalized names when prose (Lite, Pro, Max).
- **Voice:** terse, factual, operator-to-operator. No exclamation marks, no cheer, no apology theater. "Probe failed: quota exhausted on rolling:3600s window. Retrying in 4m." — not "Oops! Something went wrong."
- **Errors are typed and actionable:** show the stable error code (`funding_locked`, `reverification_required`) in mono next to a human sentence. Never "Something went wrong", "Error occurred", "Not available".
- **Unknown is a first-class word.** Write "Unknown", "No evidence", "Not yet probed" — never a fabricated 0, a blank, or a fake percentage.
- **Secrets stay secret:** key prefixes (`vk_live_3f8a…`) and fingerprints only; reveal is explicit, gated, and cleared on blur. Missing env config lists variable **names**, never values.
- **Canonical vocabulary only:** account, credential, offering, certification, capability truth, funding evidence, quota window, reservation, reconciliation, route attempt, cooldown, provider definition. Never rename casually. Never advertise `venom/standard` or `venom/plus`.
- **Emoji:** never.

## Iconography

One coherent set: **Lucide** (24px grid, 2px stroke, `currentColor`), vendored as static SVGs in `assets/icons/` and exposed through the `Icon` component + generated registry. Icons are always paired with a text label or an accessible name — never color- or icon-only for critical state. The domain icon map (chat, streaming, tools, reasoning, vision, provider, account, model, certification, probe, quota, cooldown, routing, fallback, trace, security, backup, restore, health, latency, cost, free, paid, unknown, …) lives in `icons/icon-map.md`. Capability icons may take controlled accent tints via tokens but never rely on color alone. No logo exists: render "Venom" / "Venom Router" in IBM Plex Sans 600 wherever a mark would go.

## Status semantics (the honest-state rule)

State semantics differ by domain — there is **no** universal green/red mapping. The full domain-state → semantic-token matrix is `states/state-matrix.md` (rendered stories under `states/`). Highlights:

- `unknown` = missing evidence → neutral + dashed border + help icon, never danger, never blank.
- `unsupported` = confirmed absence → quiet definitive treatment, not an alarm.
- `suspended` = temporarily non-routable (paused icon), distinct from `expired` = stale evidence needing refresh.
- `certified` ≠ routable: **routable = certified AND every required capability truth = supported.** The UI must show the conjunction.
- `reconciliation_pending` / `unknown_consumption` = unresolved possible consumption → warning/critical treatment, never neutral or success.
- `free` / `paid` are classifications (funding tokens), not health states. `owner_override` is always visually distinct from provider evidence. Unknown funding ≠ free; unknown quota ≠ unlimited.

## Canonical source of truth

One rule: **`components/` is the only place component behavior is authored.** Every other
directory either re-exports it, composes it, or is generated from it — never a second,
independently maintained copy.

| Directory | Role | Authored or generated? |
|---|---|---|
| `components/` | The component implementations themselves (`Name.tsx`). This is the only place component logic lives. | **Authored** (source of truth) |
| `src/` | Thin barrel/re-export layer (`primitives.ts`, `domain.ts`, `icons.ts`, `tokens.ts`, `themes.ts`, `density.ts`, `index.ts`) that defines the package's public import surface. Contains no component logic of its own — it only imports from `components/` and `tokens/` and re-exports. | **Authored**, but re-export-only |
| `storybook/` | The explorer hub page (`index.html`) that lists and frames every card/state/composition. Navigation/presentation only — renders nothing of its own. | **Authored** (thin) |
| `ui_kits/venom-console/` | Page-level *compositions* (`screens-*.tsx`, `index.entry.tsx`) that assemble `components/` into the 17 representative surfaces. This is application-shell example code, not component source. | **Authored** |
| `dist/` | Library build output (`vite build`) — `*.mjs`/`*.cjs` + `dist/types/*.d.ts`. Fully regenerated by `npm run build:lib`; deleting it and rebuilding must reproduce it byte-for-byte from `src/` + `components/`. | **Generated — never hand-edit, never commit as a source artifact** |
| `dist-explorer/` | Explorer build output (`vite build --config vite.explorer.config.ts`) — the static, deployable explorer site. Fully regenerated by `npm run build:explorer`. | **Generated — never hand-edit** |

Each component's sibling `Name.d.ts` is also generated (`npm run build:declarations`, from the
`.tsx` — see "Extend" below) — it is not an independently maintained type definition.

## Index

- `styles.css` — global entry (imports all tokens + themes + fonts + base).
- `tokens/` — authored JSON (3 layers) + generated CSS/TS/Tailwind outputs.
- `themes/` — generated per-theme semantic CSS.
- `foundations/` — specimen cards (type, color, spacing, elevation, motion, density, focus).
- `assets/icons/` + `icons/` — vendored Lucide subset, registry, domain icon map.
- `components/` — primitives (`actions/ forms/ display/ navigation/ containers/ feedback/ overlay/ data/ icons/`) and domain components (`domain-provider/ domain-model/ domain-quota/ domain-routing/ domain-security/ domain-diagnostics/`). Each: `Name.tsx` + generated `Name.d.ts` + `Name.prompt.md` + a dense card (`*.card.html` + `*.entry.tsx`).
- `src/` — the package's public entry points (re-exports only — see "Canonical source of truth").
- `states/` — domain state-matrix stories + `state-matrix.md`.
- `patterns/` — composition rules (app shell, page anatomy, forms, feedback, density, loading, async jobs, destructive confirm, secret reveal).
- `ui_kits/venom-console/` — the 17 representative surface compositions (interactive shell).
- `accessibility/` — the a11y contract + keyboard models.
- `storybook/` — explorer hub (theme × density switching over every card).
- `validation/` — token build + mechanical gates (theme completeness, contrast, state coverage, terminology, secret canary, CDN scan, handoff manifest) + `report.md`/`report.json`, run via `scripts/validate.js`; plus the handoff contract and machine-readable `handoff-manifest.json`.
- `tests/` — Playwright a11y (axe-core) + visual-regression suites.
- `dist/`, `dist-explorer/` — generated build output (see "Canonical source of truth"; not part of the authored surface).
- `SKILL.md` — agent-facing usage skill.

## Repository map

| Path | Responsibility | Authored/Generated | Version controlled | Regeneration command |
|---|---|---|---|---|
| `components/` | Canonical component source (`Name.tsx`/`.ts`) + docs (`.prompt.md`) + example cards (`*.card.html`/`*.entry.tsx`) | Authored (sibling `Name.d.ts` generated) | Yes | `.d.ts`: `npm run build:declarations` |
| `src/` | Public package entry points — re-export barrels + the `applyTheme`/`applyDensity`/`applyAccent`/`applyRadius`/`applySpacing` entry helpers. No component logic, ever. | Authored | Yes | — |
| `tokens/tokens.*.json` | Authored token source of truth (W3C format, 3 layers) | Authored | Yes | — |
| `tokens/base.css`, `tokens/fonts.css` | Authored base/type/webfont-fallback CSS | Authored | Yes | — |
| `tokens/primitives.css`, `tokens/components.css`, `tokens/density.css`, `tokens/tokens.ts`, `tokens/tailwind-theme.ts` | Generated token outputs (CSS vars, typed object, Tailwind theme) | Generated (required, committed) | Yes | `npm run validate` (gate 1: `validation/build-tokens.cjs`) |
| `themes/*.css` | Generated per-theme semantic CSS | Generated (required, committed) | Yes | `npm run validate` (gate 1) |
| `assets/icons/`, `icons/icons.css` | Vendored Lucide subset + icon classes (offline, no CDN) | Generated from pinned `lucide-static` (committed) | Yes | `npm run vendor:icons` |
| `icons/icon-map.md` | Domain icon map documentation | Authored | Yes | — |
| `css/` | Authored core + domain component CSS | Authored | Yes | — |
| `styles.css` | Global CSS entry point (`@import` only) | Authored | Yes | — |
| `foundations/` | Foundation specimen pages (docs/examples) | Authored | Yes | — |
| `states/` | Domain state-matrix stories + `state-matrix.md` | Authored | Yes | — |
| `patterns/` | Composition/content pattern documentation | Authored | Yes | — |
| `storybook/` | Thin explorer hub shell (navigation only) | Authored | Yes | — |
| `ui_kits/venom-console/` | The 17 representative surface compositions (examples, not production code) | Authored | Yes | — |
| `accessibility/` | A11y contract + keyboard checklist | Authored | Yes | — |
| `tests/` | Playwright a11y + visual-regression test source | Authored | Yes | — |
| `tests/visual/snapshots.spec.ts-snapshots/` | Visual-regression baselines | Test baseline (committed) | Yes | `npm run test:visual:update` — only on an intended, reviewed visual change |
| `scripts/` | Build/validation/vendoring/migration scripts | Authored | Yes | — |
| `validation/*.cjs`, `handoff-contract.md`, `handoff-manifest.json` | Mechanical gates, handoff contract + manifest | Authored | Yes | — |
| `validation/report.md`, `validation/report.json`, `validation/theme-token-manifest.json` | Generated gate evidence | Generated (required, committed) | Yes | `npm run validate` |
| `validation/shots/` | Illustrative evidence screenshots (docs — **not** the test baseline) | Authored evidence | Yes | — |
| `dist/` | Library build output (`*.mjs`/`*.cjs` + `dist/types/`) | Generated (reproducible) | **No** | `npm run build` |
| `dist-explorer/` | Explorer static build | Generated (reproducible) | **No** | `npm run build:explorer` |
| `test-results/`, `playwright-report/` | Transient Playwright run output | Transient | **No** | any test run |
| `node_modules/` | Local dependency install | Dependency cache | **No** | `npm install` (from `package-lock.json`) |

## Do not edit generated files

Every generated file above is rebuilt mechanically and **must never be hand-edited** — edits are
overwritten on the next build and, where a drift gate exists (declarations, token outputs), a
hand-edit fails `npm run validate`. Generated files carry a `GENERATED` header where the format
allows one. To change a generated output, change its authored source (token JSON, component
`.tsx`, `icons/icons.css` glyph list) and rerun the regeneration command from the table.
Declarations ship from `dist/types/` only; a `.d.ts` file directly inside `src/` is always stale
and fails the handoff gate.

## Clean source handoff

The handoff/version-control set is defined machine-readably in
[`validation/handoff-manifest.json`](validation/handoff-manifest.json) and enforced by the
`check-handoff.cjs` gate inside `npm run validate`.

**Include in a source handoff archive:** everything in the manifest's `authored`,
`generated_required` and `test_baselines` lists — i.e. the whole tree **except** the exclusions
below. Committed generated artifacts (token outputs, theme CSS, vendored icons, sibling `.d.ts`,
validation evidence) ship with the source because package consumers import them directly.

**Exclude from the archive (and from version control — mirrored in `.gitignore`):**
`node_modules/`, `dist/`, `dist-explorer/`, `test-results/`, `playwright-report/`,
`blob-report/`, `.playwright/`, `coverage/`, `.cache/`, `.vite/`, `tmp/`, `temp/`, `*.log`.

**Rebuilding from a clean handoff:** `npm install` → `npm run validate` (regenerates and gates
all committed generated artifacts) → `npm run build` (produces `dist/` + `dist-explorer/`) →
`npm run test:a11y` / `npm run test:visual`. All outputs regenerate deterministically;
`dist/` is required before consuming the package via a `file:` dependency.
