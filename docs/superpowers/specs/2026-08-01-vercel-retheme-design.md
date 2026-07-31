# Vercel-Style Retheme + Enterprise Customizer — Design

**Date:** 2026-08-01
**Status:** Approved by owner (scope: full system; keep HC; server-side persistence)
**Scope:** Standalone owner-priority task, NOT among the 179 tracker tasks.

## Goal

Match the current Design System's look to the legacy app at `G:\Venom-Router`
(screenshots approved 2026-08-01): Vercel-style neutral palette in BOTH light
and dark themes, a monochrome default accent with five optional accents, and a
floating bottom-right "Enterprise Customizer" (theme mode, accent, border
radius 0–16px, layout spacing 75–125%).

**Legacy sources to port from (READ-ONLY — never modify anything under G:\):**
- `G:\Venom-Router\web\src\styles.css` lines ~95–412 (light `:root`, `.dark`,
  and `[data-accent=…]` blocks — the authoritative color values)
- `G:\Venom-Router\web\src\components\layout\customizer.tsx` (the widget UI)
- `G:\Venom-Router\web\src\lib\use-theme.tsx` (state model; persistence model
  is NOT ported — see below)

## Decisions (owner-approved)

1. **Full system**: neutrals + mono default accent + 5 accents
   (blue/violet/amber/emerald/rose, emerald & rose have distinct dark values)
   + the floating customizer with all four controls.
2. **venom-hc stays** as a third theme: it adopts the new neutral identity but
   keeps maximized contrast. The customizer shows only Light/Dark (1:1 with
   the screenshot); HC remains selectable from the Settings page as today.
3. **Server-side persistence only** (per DS SKILL.md invariant): the owner
   settings API (`internal/httpapi/settings.go`, currently theme+density)
   gains `accent`, `radius_px`, `spacing_scale`. Migration `00013`. No
   localStorage anywhere (that part of the legacy hook is deliberately NOT
   ported).

## Architecture

### Batch A — Design System (`Design_System/`)
- **Neutrals**: retarget `tokens.semantic.venom-light.json` and
  `tokens.semantic.venom-dark.json` surface/text/border mappings to the
  legacy zinc-neutral values (add primitives if the existing gray ramp lacks
  a needed stop; keep primitive-reference discipline — semantic files never
  hold raw hex unless already conventional, e.g. scrim).
  Key targets — light: canvas `#f4f4f5`, primary/card `#ffffff`, text
  `#1c1c1e`, muted text `#71717a`, border `#e4e4e7`; dark: canvas `#09090b`,
  card `#1a1a1e`, text `#ffffff`, muted `#a1a1aa`, border `#222226`.
- **Accent dimension**: default accent becomes MONO (light: black accent /
  white text; dark: white accent / black text) by remapping `accent.*`,
  `focus.ring`, `selection.*`, `text.link*`, `border.focus` in the two theme
  files. Five optional accents ship as `[data-accent="…"]` override blocks in
  a new generated `tokens/accents.css` (built from a new
  `tokens.accents.json`), overriding ONLY the accent-derived custom
  properties. `data-accent` absent or `mono` = base themes. Legacy hex values
  are authoritative (incl. emerald/rose dark variants). HC: accents also
  apply, but HC's focus ring/borders stay HC-owned (accents must not lower
  HC's contrast guarantees — the contrast gate is the arbiter).
- **Radius & spacing hooks**: radius tokens route through ONE base custom
  property (default 6px, matching the screenshot) so a consumer can set
  `--vn-radius-base: NNpx`; component spacing routes through a multiplier
  custom property (default 1) applied to the density-resolved spacing scale.
  Both default to today's visual output when unset (aside from the deliberate
  new 6px default).
- **New public API** (src/, mirroring `applyTheme`/`applyDensity`):
  `ACCENTS`, `DEFAULT_ACCENT`, `applyAccent(name, root?)`, `isAccentName`,
  `applyRadius(px, root?)` (clamped 0–16), `applySpacing(scale, root?)`
  (clamped 0.75–1.25), each setting `data-accent` / the custom properties.
- **Gates**: `npm run validate` 12/12 stays green (contrast gate may force
  small value tuning — tune within the legacy family, never below AA); a11y
  suite green; **visual baselines regenerate once** as the intended, reviewed
  change of this spec (DS invariant explicitly permits this case).

### Batch B — Backend settings + Dashboard customizer
- **Migration `00013_owner_settings_customizer.sql`**: add `accent TEXT NOT
  NULL DEFAULT 'mono'`, `radius_px INTEGER NOT NULL DEFAULT 6`,
  `spacing_scale REAL NOT NULL DEFAULT 1.0` to the owner settings table
  (follow 00005's shape/naming).
- **API** (`internal/httpapi/settings.go`): GET/PUT gain the three fields;
  validation fail-closed: accent ∈ {mono,blue,violet,amber,emerald,rose},
  radius_px ∈ [0,16] integer, spacing_scale ∈ [0.75,1.25] (step-free server
  side); unknown values → 400 naming the field. Same auth/audit behavior as
  the existing theme/density fields.
- **Dashboard** (`dashboard/`): port `EnterpriseCustomizer` 1:1 visually
  (floating 48px round button, bottom-right, slider icon; expanding card with
  Theme Mode toggle, 6-swatch accent grid with per-theme swatch hex, radius
  slider 0–16 labeled `0px (Sharp) / 8px / 16px (Round)`, spacing slider
  75–125% labeled `Compact (75%) / 100% / Cozy (125%)`, Reset, click-outside
  close) — rebuilt on OUR DS components/tokens, consuming the new DS apply
  functions, reading/writing the owner settings API (optimistic apply,
  persisted via the existing settings client pattern in
  `dashboard/src/theme-runtime.ts`). Reset = server defaults
  (dark/mono/6px/100%).
- **Boot**: theme-runtime applies all five settings (theme, density, accent,
  radius, spacing) from the settings payload before first paint, as it does
  for theme/density today.

## Out of scope
- The tracker/roadmap (unchanged; this is standalone).
- Tray, Go routing/engine code beyond the settings surface.
- Legacy repo `G:\Venom-Router` — read-only reference.

## Testing
- DS: validate 12/12, a11y suite, regenerated visual baselines reviewed
  against the legacy screenshots; unit tests for the new apply* functions
  (clamping, attribute/property effects, unknown-name rejection).
- Go: settings round-trip + validation-rejection tests per field; migration
  up test in the existing migration-test style.
- Dashboard: customizer interaction tests (open/close, optimistic apply,
  persistence call, reset) in the existing vitest setup; ds-adherence check
  stays green.
