# Integration handoff contract

How the Venom dashboard (roadmap Phase 2a consumer) consumes this package.

## Import surface
- **Global CSS:** link `styles.css` (imports tokens → themes → components CSS → icons). One entry point; everything it `@import`s ships with it.
- **Tokens:** authored source `tokens/tokens.*.json` (W3C format). Generated outputs — `tokens/*.css`, `themes/*.css`, `tokens/tokens.ts`, and the Tailwind theme extension `tokens/tailwind-theme.ts` — carry a GENERATED header and are rebuilt by `validation/build-tokens.cjs`; hand-editing them is forbidden.
- **Tailwind:** the theme extension is a **generated artifact of this package** — the consuming app must never hand-author or duplicate a token → Tailwind mapping. In `tailwind.config`: `import { venomTailwindPreset } from "@venom/design-system/tailwind"` and set `presets: [venomTailwindPreset]` (handoff-copy mode ships the same module as `dist/tailwind.mjs`/`.cjs`). Every generated value is a `var(--…)` reference to the CSS custom properties, so utilities resolve to tokens (never literals) and theme/density switching stays runtime-driven through `data-theme`/`data-density`; breakpoints (`screens`) are the one literal exception because media queries cannot read CSS custom properties. Token changes flow to Tailwind by re-running `validation/build-tokens.cjs` — never by editing the artifact.
- **Components:** `components/**/<Name>.tsx` with sibling `.d.ts` prop contracts. They are presentational, typed via the `.d.ts` files, consume only semantic/component tokens through the `vn-*` classes + CSS variables, and never fetch — feed them view models from the control API.

## Theming & density
- `data-theme="venom-dark" | "venom-light"` on the root element (dark is also the `:root` default); `data-density="comfortable" | "compact"` alongside it.
- Theme/density choice persists **server-side** via `PUT /settings` (control API) — not browser storage — and is applied on boot before first paint.
- Adding a theme = one new complete semantic JSON + registry entry; `validation/build-tokens.cjs` fails the build if the token set diverges. No component changes.

## Change policy (tokens are API)
- Token edits are reviewed, semver'd changes with a changelog entry; renames ship with a codemod, never a silent edit.
- New components enter the inventory only with: all states, keyboard model, ARIA notes, a card/story, and a `.d.ts` contract — then screens may import them.

## CI gates to wire in the consuming repo
1. `npm run validate` (`node scripts/validate.js`) — runs `build-tokens.cjs` (theme completeness + reference legality), `check-contrast.cjs` (AA matrix across dark and light), `check-guardrails.cjs` (raw-value lint, terminology scan, secret canary, icon-map + state-coverage checks, forbidden-CDN scan, required-component presence), the strict type check, both declaration-generation passes, the Vite library build, and the Vite explorer production build — all blocking, all from one command, exit code 0 only if every gate passes.
2. `npm run test:a11y` — Playwright + axe-core over the explorer's cards, state matrices, and the `ui_kits/venom-console` compositions; keyboard flows per `accessibility/keyboard-checklist.md`.
3. `npm run test:visual` — screenshot-diff every representative surface across the 2 themes × 2 densities (baseline: `tests/visual/snapshots.spec.ts-snapshots/`, the Playwright snapshot directory; update only on an intended, reviewed visual change with `npm run test:visual:update`). `validation/shots/` holds illustrative evidence screenshots for documentation — it is **not** the comparison baseline.

## Offline packaging (resolved, not a flagged substitution)
This package makes no runtime network requests. Webfonts fall back to the documented system stack (`tokens/fonts.css`) rather than fetching IBM Plex from Google Fonts; the pinned Lucide icon set (`lucide-static@0.453.0`) is vendored into `assets/icons/` and referenced locally by `icons/icons.css` (`npm run vendor:icons` regenerates both from the pinned devDependency). The explorer and every composition example build and run with the network disabled. To restore the IBM Plex webfonts, vendor licensed woff2 files into this package and add local `@font-face` rules to `tokens/fonts.css` — never reintroduce a CDN `@import`.
