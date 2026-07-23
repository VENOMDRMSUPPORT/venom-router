# 07 — Design System requirements & acceptance contract

> **STATUS — read first.**
> - The dedicated Design System task is complete. Its implementation lives in `Design_System/` as
>   the versioned `@venom/design-system@1.0.0` package; the legacy "Hex AIOS" bundle remains removed
>   and is not a source of tokens, components, or product assumptions.
> - This document remains the authoritative requirements and acceptance contract. The package's
>   `README.md`, `SKILL.md`, and `validation/handoff-contract.md` define its operational consumption
>   and maintenance rules.
> - The package has passed its mechanical acceptance gate (`npm run validate`: 12/12). Roadmap
>   Phase 2a must re-run that gate and prove dashboard consumption across the required themes and
>   densities before any production Venom UI surface is built.

The visual and interaction foundation for the Venom control dashboard. This doc is a spec, not a
mood board: it defines a **product identity**, a **token architecture**, a **multi-theme contract**,
a **fixed component inventory**, the **domain state coverage**, and the **guardrails that make drift
impossible**. If a screen needs a color, a spacing, a font size, or a shadow, it comes from here —
never from a literal in a component.

Read [README](../README.md) principle #10 first. The design system exists to serve one goal: a
dashboard that looks and behaves like a real enterprise product on day one and stays that way as
it grows, because every surface is assembled from the same small set of tokens and components.

---

## 0. Product identity & context (what the DS is *for*)

The dedicated task must design to this identity — not a generic dashboard, and explicitly **not**
the removed "Hex AIOS" agent-console aesthetic.

- **Product:** Venom Router — a **private, single-owner AI gateway** that pools many LLM provider
  accounts behind three tiers (`venom/lite|pro|max`).
- **Surface:** an **owner-only infrastructure control center** (a fleet/operations console), not a
  consumer app and not a multi-user SaaS. There is exactly one operator; there are no end-user
  roles, no marketing surfaces, no onboarding funnels.
- **Primary jobs the UI serves:** **authenticate the owner** (first-run password setup, login/
  logout, and re-verification for sensitive actions — one owner, no roles); enroll and monitor
  providers/accounts (the Provider Fleet); inspect models, offerings, capabilities, and
  certification; watch quota/consumption; understand routing decisions ("why this route?"); manage
  Venom API keys; run backup/restore; configure settings. All against the loopback **control API**
  ([09-control-api](09-control-api.md)). Owner authentication is a single-owner **login**, not a
  multi-user sign-up funnel and not a role system.
- **Tone:** precise, dense, calm, trustworthy — an operator tool for long monitoring sessions.
  Truthful state above all: **unknown/degraded/stale must read as clearly as healthy** (never faked
  as a confident number or color). Dark-first, fully accessible, enterprise-grade by construction.
- **Non-goals for V1:** no image-generation screens (future scope, [05 §9](05-tier-engine.md#9-future-scope-non-v1));
  no agent-orchestration metaphors; no consumer theming/branding beyond the three shipped themes.

---

## 1. Principles

1. **Tokens are the only source of truth.** Every visual value is a named token. A raw `#hex`,
   `rgb()`, `px` color, or magic spacing number inside a component is a lint error, not a style.
2. **Three-layer token pipeline.** *Primitive* (raw palette + scales) → *semantic* (roles like
   `surface`, `text-primary`, `border-strong`) → *component* (e.g. `button-primary-bg`).
   Components consume **semantic or component** tokens only, never primitives. Themes re-map the
   semantic layer; primitives and components rarely change.
3. **Theme = a complete semantic mapping.** A theme is valid only if it defines **every** semantic
   token. A CI check fails the build if any theme is missing a token or introduces one no other
   theme has. No theme may reference a primitive a component can see.
4. **Accessible by construction.** Every text/background pairing in every theme meets WCAG 2.1 AA
   (≥ 4.5:1 body, ≥ 3:1 large text and UI boundaries). Contrast is validated in CI, not by eye.
5. **One component, all states.** A component ships with every state defined (default, hover,
   active, focus-visible, disabled, loading, error, empty, skeleton). Screens compose components;
   they never restyle them.
6. **Density is a mode, not a rewrite.** A `comfortable`/`compact` density switch changes spacing
   and control heights through tokens only; layouts don't fork.
7. **Motion is subtle, consistent, and reducible.** All animation uses the motion tokens and
   honors `prefers-reduced-motion`. No bespoke easings or durations in components.
8. **The system is headless-friendly and swappable.** Behavior (a headless primitive) is separated
   from skin (tokens). Skins can be re-themed or replaced without touching component logic.

---

## 2. Token architecture

### 2.1 Layers

```
Primitive tokens        Semantic tokens              Component tokens
(raw, theme-agnostic)   (role, theme-mapped)         (part, derived)
─────────────────────   ──────────────────────────   ──────────────────────
color.slate.900         color.bg.canvas              button.primary.bg
color.slate.800     ─▶  color.bg.surface        ─▶  button.primary.bg.hover
color.indigo.500        color.text.primary           card.bg
space.0..32             color.border.subtle          input.border.focus
radius.xs..full         color.accent.default         table.row.hover.bg
font.size.xs..7xl       color.status.success.fg      tier.max.badge.bg
shadow.0..5             color.status.danger.bg       nav.item.active.fg
z.0..max                elevation.card / .popover     …
duration.*/ease.*       focus.ring                    (only when a part needs
                        density.control.height        a value no semantic token
                        …                             already expresses)
```

Rules: components import from the **semantic** layer by default and from the **component** layer
only for parts that genuinely need their own knob. **No component ever imports a primitive.** This
is what lets a new theme restyle everything by re-mapping semantics alone.

### 2.2 Naming grammar

`category.role.variant.state` — lowercase, dot-delimited, no abbreviations that aren't in the
glossary. Examples: `color.text.primary`, `color.border.strong`, `color.status.warning.bg`,
`button.primary.bg.hover`, `tier.pro.accent`. Every token name is stable API: renaming one is a
breaking change with a codemod, never a silent edit.

### 2.3 Delivery

- Tokens are authored **once** in a platform-neutral source (`tokens/*.json`, W3C Design Tokens
  format) and compiled (Style Dictionary or equivalent) into: CSS custom properties (the runtime),
  a TypeScript `tokens` object (for typed access + Storybook), and a Tailwind theme extension.
- The dashboard is **React + Vite + TypeScript** (embedded via `go:embed`, see
  [01-architecture](01-architecture.md)); Tailwind maps utilities onto the CSS variables so class
  names resolve to tokens, never to literals. There is exactly one `tokens/` source; the CSS, TS,
  and Tailwind outputs are generated, never hand-edited.
- Themes are applied by a single `data-theme` attribute on the root plus a `data-density`
  attribute. Switching a theme swaps the semantic custom-property block; no component re-renders
  its logic. Theme choice persists per the dashboard's own settings store (server-side, not
  browser storage — the dashboard runs against the local control API).

---

## 3. The themes we ship

Venom is a dark-first operations dashboard, so **Dark is the default**. Three themes ship in v1;
the token architecture supports unlimited additional themes with **zero component changes**.

| Theme | `data-theme` | Role |
|---|---|---|
| **Venom Dark** | `venom-dark` | Default. Deep neutral canvas, restrained accent, calm status colors for long monitoring sessions. |
| **Venom Light** | `venom-light` | Full parity light theme for bright environments and screenshots. |
| **High Contrast** | `venom-hc` | Accessibility theme: maximized contrast, thicker borders, stronger focus rings; meets AAA where feasible. |

Every theme defines the identical semantic token set — that is the contract in §1.3. Adding a
"Venom Midnight" or a brand theme later means authoring one more semantic mapping file and adding
it to the theme registry; nothing else moves.

### 3.1 Palette intent (primitive → semantic, illustrative)

Exact values live in `tokens/`; this is the *intent* each theme must satisfy, not the hex list.

- **Canvas / surface / raised** — three background depths so panels read as layered without heavy
  borders. Dark: near-black neutral → elevated panels one step lighter. Light: paper white →
  faint gray panels.
- **Text** — `primary` (high-emphasis), `secondary` (labels/help), `disabled`, `inverse` (on
  accent fills). All validated ≥ AA on their intended background.
- **Border** — `subtle` (dividers), `default`, `strong` (focus/selected), `focus` (the ring).
- **Accent** — one restrained brand accent (`accent.default` + `hover`/`active`/`subtle-bg`) used
  for primary actions and active nav. The accent is *not* a status color and never carries
  meaning like success/danger.
- **Status** — `success`, `warning`, `danger`, `info`, each with `fg` / `bg` / `border` /
  `subtle-bg`, tuned per theme so a green in dark and a green in light are equally legible and
  equally AA.
- **Tier accents** — `tier.lite`, `tier.pro`, `tier.max` each get a distinct, consistent hue used
  wherever a tier is shown (badges, charts, status). This mapping is a token, so the three
  products are always visually distinguishable and never re-picked ad hoc.
- **Data-viz ramp** — a categorical set (provider/tier series) and a sequential set (quota/usage
  heat), both defined as tokens and both required to stay distinguishable in all three themes and
  under common color-vision deficiencies.

---

## 4. Foundations (the scales)

All scales are tokenized; components reference steps, never raw values.

- **Spacing** — a single base-4 scale (`space.0` … `space.32`), used for padding, gaps, and
  layout. No off-scale spacing. Density mode scales control paddings via tokens.
- **Typography** — one primary UI typeface (a clean, legible sans; system stack fallback) and one
  monospace (for keys, model IDs, code, and numeric tables). A modular type scale
  (`font.size.xs` … `7xl`) with defined `line-height` and `weight` tokens. Tabular figures are
  mandatory for any numeric column (quota %, tokens, latency).
- **Radius** — `radius.xs` … `radius.2xl` + `full`; controls, cards, and popovers each map to a
  fixed step.
- **Elevation** — `shadow.0` … `shadow.5` mapped to semantic `elevation.card`,
  `elevation.popover`, `elevation.modal`, `elevation.toast`. Dark theme leans on surface-lightness
  steps plus subtle shadow; light theme leans on shadow.
- **Z-index** — a named `z.*` ladder (base, sticky, dropdown, overlay, modal, toast, max) so stack
  order is never guessed.
- **Motion** — `duration.instant/fast/base/slow` + `ease.standard/emphasized/exit`; all component
  transitions pick from these and collapse to 0ms under reduced-motion.
- **Breakpoints** — a small set (`sm/md/lg/xl/2xl`) tokenized; the dashboard targets desktop first
  (it's an operator tool) but must stay usable down to a narrow laptop width.
- **Focus** — one `focus.ring` token (color + width + offset) applied via `:focus-visible`
  everywhere; keyboard focus is always clearly visible in every theme.
- **Iconography** — a single icon set (one library, consistent stroke/size grid); icons inherit
  `currentColor` and size tokens, never hardcoded dimensions.

---

## 5. Component inventory

The dashboard is built **only** from this inventory. Adding a screen means composing these; it
does not mean inventing a widget. Anything genuinely new is added to the inventory first (with all
states + a Storybook entry + a11y notes), then used.

**Primitives:** Button (primary / secondary / ghost / danger / icon), Link, Input, Textarea,
Select, Combobox, Checkbox, Radio, Switch, Slider, Badge/Chip, Tag, Avatar/ProviderLogo, Tooltip,
Icon, Spinner, ProgressBar, Skeleton, Kbd, Code/Copyable (with reveal-then-clear for secrets).

**Composite:** Card, StatCard (the Providers / Accounts / Healthy / Models tiles), Table (sortable,
sticky header, row states, empty/loading, tabular numerics), DataList/KeyValue, Tabs, Accordion,
Dialog/Modal, Drawer/Sheet, Popover, DropdownMenu, Toast/Notification, Banner/Alert (incl. the
review-queue banner), Breadcrumbs, Pagination, EmptyState, ErrorState, FormRow/Field (label +
help + error), SearchField, FilterBar, ThemeSwitcher, DensityToggle.

**Domain components (Venom-specific, still token-built):**
- **TierBadge** — renders `lite/pro/max` with the tier accent token; the single way a tier is
  labeled anywhere.
- **FundingBadge** — `free` / `paid` / `unknown` with status tokens; lives on the account row, per
  the domain rule that funding is account-scoped (see [02-domain-model](02-domain-model.md)).
- **HealthDot / StateChip** — renders the account's **derived `display_status`** and offering state,
  mapped to status tokens with a text label (never color-only). It must cover every value of the
  multi-axis account model ([02 §3](02-domain-model.md)): `connecting`, `healthy`, `degraded`,
  `unavailable`, `expired` (credential), `cooling_down`, `reauthenticating`, `stopped`,
  `disconnected`, `unknown`. The single mapping from axes → chip lives here (no screen re-derives it).
- **ProviderFleet row set** — provider row → expandable account rows → per-account
  status/funding/models, the core Phase 2 surface.
- **QuotaMeter** — per-account usage %, remaining, and reset, in provider-native units; sequential
  data-viz ramp; honest handling of unknown/low-confidence quota (rendered distinctly, never faked
  as a number).
- **CertificationState** — the `discovered→…→certified` lifecycle chip with the review-reason
  tooltip.
- **RouteExplain** — the "why this route?" diagnostics view (candidate set, exclusion reason codes,
  chosen route, scores) built from Table + Badge + Tooltip.
> **Future / optional — explicitly NOT part of the V1 inventory or the V1 acceptance gate:**
> **TopologyGraph** (a read-only live provider topology panel from the OmniRoute analysis: hub +
> provider nodes, states = active/recent/error, fed by the control API). The dedicated Design
> System task is **not** required to design it for V1. If it is ever adopted, it enters through
> the standard inventory process (all states + Storybook + a11y) first, like any new component.

Every component entry in the inventory carries: purpose, props/variants, all interaction states,
keyboard model, ARIA roles, and the tokens it consumes. That entry — in Storybook — is the
component's contract.

---

## 5a. Domain state coverage (every state the DS must render)

Beyond the generic per-component states in §1.5, the dedicated task must design an explicit,
distinct, accessible rendering for **every domain state below**. "Truthful state" is the rule:
`unknown`, `stale`, and `degraded` must be visually unmistakable and never faked as a confident
value or conveyed by color alone (always icon **+ text**).

| Domain concept | States to render distinctly | Primary component(s) | Rendering rule |
|---|---|---|---|
| **Account display status** (derived, multi-axis — [02 §3](02-domain-model.md)) | `connecting`, `healthy`, `degraded`, `unavailable`, `expired`, `cooling_down`, `reauthenticating`, `stopped`, `disconnected`, `unknown` | HealthDot / StateChip, ProviderFleet row | status token + text; `cooling_down` shows retry-after; `expired`/`reauthenticating` prompt the fix action |
| **Funding** | `free`, `paid`, `unknown`, plus a `locked` indicator (provider_policy) | FundingBadge | status tokens; `unknown` is visually distinct (not a neutral blank); `locked` shows a lock affordance and disables override |
| **Certification** (state × capability truth — [04 §5](04-model-intelligence.md)) | state: `discovered`→`observed`→`probing`→`certified`, `suspended`, `expired`; capability truth: `unknown`/`supported`/`unsupported`; + review reason | CertificationState chip | lifecycle chip + capability-truth sub-state + review-reason tooltip; `certified+unknown` must read as "not routable yet" |
| **Quota** ([05 §4](05-tier-engine.md)) | `available`, `insufficient`, `exhausted`, `unknown`, `stale`; + low-confidence; + provider-native unit | QuotaMeter | sequential ramp for known values; `unknown`/`stale` rendered as a distinct non-numeric treatment, never a fabricated %; shows reset window |
| **Routing decision** ([05 §2, §7](05-tier-engine.md)) | chosen route; candidates; excluded candidates with **typed reason codes** (`funding_unknown`, `context_unverified`, `capability_not_certified`, `no_healthy_account`, `quota_exhausted`, `cooling_down`, …); clamp notes (thinking) | RouteExplain (Table + Badge + Tooltip) | one row per candidate with score + exclusion reason; chosen row emphasized; reasons are the typed codes verbatim |
| **Provider (integration) row** | `setup_required` (missing OAuth client-secret env — names only, never values), `active` (≥1 account), `no_accounts` | ProviderFleet row, Banner/Alert | "Setup required" banner lists missing env var **names**; never render a secret value |
| **Tier** | `lite` / `pro` / `max` accents | TierBadge | single tier accent token; the only way a tier is labeled |
| **Model / offering** | `available`, `withdrawn`, `catalog_only` (media/image-only), context `unknown` | Table cells, Badge | `catalog_only` clearly "not in a tier"; `unknown` context never shown as `0` |
| **Owner authentication & session** ([09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)) | `first_run_setup` (create password), `login`, `invalid_credentials`, `locked_out` (with retry-after), `session_active`, `session_idle_warning`, `session_expired`, `reverification_required` (sensitive-action prompt), `reverified` | Auth screens (FormRow + Button + Banner), re-verify Dialog | single-owner login (never a sign-up funnel or role picker); password fields masked, never echoed; `locked_out` shows retry-after; `session_expired` routes to login preserving no secret; `reverification_required` is a modal password prompt gating the sensitive action for 5 min |
| **Secret reveal** | hidden, revealed (with `no-store`), cleared-on-blur; **blocked pending re-verification** | Code/Copyable, re-verify Dialog | reveal-then-clear; masked by default; never persisted in the DOM after hide; reveal is gated on a fresh (≤ 5 min) re-verification |
| **Generic async** | loading/skeleton, empty, error, partial/degraded | Skeleton, EmptyState, ErrorState, Banner | no layout shift skeleton→loaded; every empty/error state has written copy |

The dedicated task turns each cell into a Storybook story (all sub-states) with an a11y annotation
before any screen may use it.

---

## 6. Patterns (composition rules)

- **App shell** — persistent left navigation (grouped, collapsible, with an active-item accent),
  top bar (environment/health summary, theme + density switch, owner menu), and a content area
  with a consistent page header (title, description, primary action, breadcrumbs).
- **Page anatomy** — every page = header → optional FilterBar → content (Table/Cards/Detail) →
  consistent empty/loading/error states. No page invents its own header or spacing.
- **Forms** — FormRow everywhere (label + control + help + inline error); primary action bottom-
  right; destructive actions require confirmation and use the danger token; secrets use the reveal
  component with `no-store` and clear-on-blur (mirrors the security model in
  [01-architecture](01-architecture.md) §8).
- **Feedback** — toasts for transient results, inline banners for persistent conditions (e.g.
  "Setup required" when an OAuth client secret env var is missing; the review-queue count).
- **Data density** — tables default to comfortable, switchable to compact; numerics right-aligned
  and tabular; status always icon **+ text**, never color alone.
- **Loading** — skeletons for first paint, inline spinners for in-place refresh; never a layout
  shift between skeleton and loaded.

---

## 7. Accessibility contract

- **Contrast:** every theme passes WCAG 2.1 AA for all token pairings; High Contrast targets AAA
  where feasible. Enforced by a CI contrast test over the token matrix, not by review.
- **Keyboard:** every interactive element is reachable and operable by keyboard; visible
  `:focus-visible` ring from the focus token; logical tab order; dialogs trap and restore focus.
- **Semantics:** correct roles/labels/`aria-*`; status conveyed by text/icon in addition to color;
  live regions for async results and toasts.
- **Motion & targets:** honor `prefers-reduced-motion`; pointer targets meet minimum size in
  comfortable density; nothing depends on hover alone.
- **Content:** all UX copy goes through the copy guidelines (clear, terse, consistent casing);
  every empty/error state is written, not left blank.

---

## 8. Drift-prevention guardrails (how this stays enforced)

The design system is only real if the build refuses to accept violations. These gates live with
the dashboard package and run in CI (cross-referenced by
[08-engineering-standards](08-engineering-standards.md)):

1. **No raw values lint** — an ESLint/Stylelint rule bans hex/rgb/hsl colors, raw `px` for spacing,
   raw shadows, and off-scale numbers in components. Values must come from tokens/Tailwind-mapped
   classes. CI-blocking.
2. **Theme completeness test** — a test asserts every theme defines exactly the semantic token set
   (no missing, no extra) and that no component references a primitive.
3. **Contrast test** — automated AA (AAA for HC) contrast check across all foreground/background
   token pairs in all themes.
4. **Component-inventory gate** — a new visual component can't be imported by a screen until it has
   a Storybook story covering all states and an a11y annotation; a check enforces "screens import
   only inventory components."
5. **Visual regression** — Storybook + snapshot/visual diffs on the component library, run in each
   theme + density, so an unintended visual change is caught before merge.
6. **Token-change review** — any edit under `tokens/` is a reviewed, semver'd change with a
   changelog entry; renames ship with a codemod. Tokens are treated as API.
7. **Accessibility CI** — automated a11y checks (axe) on component stories and key page flows.

The net effect: a contributor *cannot* introduce a one-off color, an inaccessible pairing, an
un-themed value, or a bespoke widget without CI stopping them. That is what "prevents drift" means
here — it's mechanical, not a style guide people are asked to remember.

---

## 9. Extensibility (ready for the future)

- **New theme:** author one semantic mapping file, register it, pass the completeness + contrast
  tests. No component edits. Unlimited themes by design.
- **New surface/feature:** compose existing inventory components inside the standard page anatomy;
  wire to the control API. New data → new domain component *added to the inventory first*.
- **New tier or status:** add its accent/status token and it renders consistently everywhere the
  TierBadge/StateChip are used — because those are the single rendering path.
- **Rebrand:** re-map the accent + primitive palette; semantics and components are untouched.
- **Density/locale/RTL:** density is already a token mode; the type and spacing scales and the
  logical-property layout leave room to add RTL and localization without structural change.

The design system ships as its own versioned package inside the repo so the dashboard, Storybook,
and the token build all consume one artifact — the foundation is genuinely reusable, not copied
per screen.

---

## 10. Deliverables for the dedicated Design System task

The separate Design System creation task (not this planning task) must produce **all** of the
following and pass the acceptance gate before any Venom UI is built. Nothing here was created during
planning remediation.

**Artifacts**
1. **Token source of truth** — one `tokens/*.json` (W3C Design Tokens format), the sole authored
   source; hand-editing generated outputs is forbidden.
2. **Compiled outputs (generated)** — CSS custom properties (runtime), a typed TypeScript `tokens`
   object, and a Tailwind theme extension.
3. **Themes** — Venom Dark (default), Venom Light, High Contrast — each a complete semantic mapping
   satisfying the completeness contract (§1.3).
4. **Base components** — the primitives + composites in §5, each with all interaction states, a
   keyboard model, ARIA roles, and a Storybook story.
5. **Venom domain components** — TierBadge, FundingBadge, HealthDot/StateChip, ProviderFleet row
   set, QuotaMeter, CertificationState, RouteExplain, and any component required by the domain
   state matrix (§5a). Derived from the final Venom domain/pages — **no image-generation components
   in V1** ([05 §9](05-tier-engine.md#9-future-scope-non-v1)).
6. **Page-level patterns** — the app shell, page anatomy, forms, feedback, density, and loading
   patterns in §6.
7. **Accessibility validation** — the CI contrast matrix (AA; AAA for HC), axe checks on stories,
   keyboard/focus verification.
8. **Visual regression baseline** — Storybook snapshots per theme × density.
9. **Integration handoff contract** — how the app consumes the versioned package (import surface,
   `data-theme`/`data-density` attributes, control-API-driven theming persistence — server-side per
   §2.3), and the semver/changelog/codemod policy for token changes.

**Acceptance gate (the DS task's own gate, consumed by roadmap Phase 2a):** tokens compile to all
three outputs; all three themes render the full inventory in Storybook; the no-raw-values lint,
theme-completeness test, contrast test, component-inventory gate, visual-regression, and axe checks
all pass in CI. Only then may dashboard implementation begin.

**Explicitly out of scope for the DS task in V1:** image-generation screens, agent-orchestration
metaphors, and any consumer/marketing theming beyond the three shipped themes.
