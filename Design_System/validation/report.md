# Validation report — Venom Design System

Run at `2026-08-07T05:34:32.435Z` (finished `2026-08-07T05:34:51.411Z`) against package `@venom/design-system@1.0.0` by `scripts/validate.js`. Every row below is an actual gate output from this run — not a hand-maintained estimate. Regenerate with `npm run validate`; never hand-edit this file.

## Gate results

| # | Gate | Status | Command |
|---|------|--------|---------|
| 1 | Token build (schema, completeness, reference legality) | **PASS** | `buildVenomTokens(io)` |
| 2 | Contrast (WCAG AA across dark and light) | **PASS** | `checkVenomContrast(io)` |
| 3 | Guardrails (raw-color, terminology, secrets, icon map, state coverage, story coverage, CDN scan, required components) | **PASS** | `checkVenomGuardrails(io)` |
| 4 | Type check (strict) | **PASS** | `npx tsc -p tsconfig.json --noEmit` |
| 5 | Declaration generation (components/**/*.tsx -> sibling .d.ts) | **PASS** | `npx tsc -p tsconfig.declarations.json` |
| 6 | Declaration drift check (checked-in .d.ts vs freshly generated) | **PASS** | `diff before/after tsc -p tsconfig.declarations.json` |
| 7 | Declaration generation (package entry points -> dist/types) | **PASS** | `npx tsc -p tsconfig.build.json` |
| 8 | Explorer production build (Vite, no in-browser transpilation) | **PASS** | `npx vite build --config vite.explorer.config.ts` |
| 9 | Library build (Vite, dist/*.mjs + dist/*.cjs) | **PASS** | `npx vite build` |
| 10 | Package export completeness (every dist/*.mjs entry point is importable and non-empty) | **PASS** | `node dist\.export-check.mjs` |
| 11 | No stale validation artifacts | **PASS** | `ls('validation') + filename check` |
| 12 | Handoff manifest (classification, canonical sources, generated artifacts, stale duplicates, exclusions) | **PASS** | `checkVenomHandoff(io)` |

12/12 gates passed.

## Gate detail

### Token build (schema, completeness, reference legality)

- Status: **PASS**
- Command: `buildVenomTokens(io)`
- Started: 2026-08-07T05:34:32.439Z · Finished: 2026-08-07T05:34:32.451Z
- Summary: `{"venom-dark":{"count":98,"missing":[],"extra":[]},"venom-light":{"count":98,"missing":[],"extra":[]}}`
- Log: OK: 171 primitives, 98 semantic tokens x 2 themes, 69 component tokens, 16 density tokens, 5 accents (7 override blocks), 17 tailwind theme groups.

### Contrast (WCAG AA across dark and light)

- Status: **PASS**
- Command: `checkVenomContrast(io)`
- Started: 2026-08-07T05:34:32.451Z · Finished: 2026-08-07T05:34:32.452Z
- Summary: `{"summary":{"checked":78,"failed":0,"failures":[]},"results":[{"theme":"venom-dark","pair":"text.primary on surface.canvas","ratio":19.9,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.secondary on surface.canvas","ratio":13.46,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.muted on surface.canvas","ratio":7.76,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.primary on surface.primary","ratio":17.35,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.secondary on surface.primary","ratio":11.74,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.muted on surface.primary","ratio":6.77,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.primary on surface.secondary","ratio":16.58,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.secondary on surface.secondary","ratio":11.22,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.muted on surface.secondary","ratio":6.47,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.primary on surface.raised","ratio":15.82,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.secondary on surface.raised","ratio":10.7,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.muted on surface.raised","ratio":6.17,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.link on surface.canvas","ratio":19.9,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.link on surface.primary","ratio":17.35,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.on-accent on accent.default","ratio":21,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"accent.text on surface.canvas","ratio":19.9,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"accent.text on surface.primary","ratio":17.35,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.healthy.fg on status.healthy.bg","ratio":7.57,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.healthy.fg on surface.primary","ratio":9.8,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.degraded.fg on status.degraded.bg","ratio":6.98,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.degraded.fg on surface.primary","ratio":8.37,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.warning.fg on status.warning.bg","ratio":7.61,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.warning.fg on surface.primary","ratio":10.05,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.critical.fg on status.critical.bg","ratio":6.84,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.critical.fg on surface.primary","ratio":7.91,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.info.fg on status.info.bg","ratio":6.92,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.info.fg on surface.primary","ratio":8.15,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.unknown.fg on status.unknown.bg","ratio":10.72,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.unknown.fg on surface.primary","ratio":11.74,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.inactive.fg on status.inactive.bg","ratio":6.77,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.inactive.fg on surface.primary","ratio":6.77,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"tier.lite.fg on tier.lite.bg","ratio":7.35,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"tier.pro.fg on tier.pro.bg","ratio":6.53,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"tier.max.fg on tier.max.bg","ratio":7.73,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"action.primary.fg on action.primary.bg","ratio":21,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"action.secondary.fg on action.secondary.bg","ratio":16.58,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"action.destructive.fg on action.destructive.bg","ratio":6.15,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"border.strong on surface.primary","ratio":3.59,"need":3,"pass":true,"label":"control boundary"},{"theme":"venom-dark","pair":"focus.ring on surface.canvas","ratio":19.9,"need":3,"pass":true,"label":"focus ring"},{"theme":"venom-light","pair":"text.primary on surface.canvas","ratio":15.48,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.secondary on surface.canvas","ratio":9.5,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.muted on surface.canvas","ratio":5.43,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.primary on surface.primary","ratio":17.01,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.secondary on surface.primary","ratio":10.44,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.muted on surface.primary","ratio":5.97,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.primary on surface.secondary","ratio":16.3,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.secondary on surface.secondary","ratio":10.01,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.muted on surface.secondary","ratio":5.72,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.primary on surface.raised","ratio":17.01,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.secondary on surface.raised","ratio":10.44,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.muted on surface.raised","ratio":5.97,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.link on surface.canvas","ratio":15.48,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.link on surface.primary","ratio":17.01,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.on-accent on accent.default","ratio":21,"need":4.5,"pass":true},{"theme":"venom-light","pair":"accent.text on surface.canvas","ratio":19.11,"need":4.5,"pass":true},{"theme":"venom-light","pair":"accent.text on surface.primary","ratio":21,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.healthy.fg on status.healthy.bg","ratio":5.7,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.healthy.fg on surface.primary","ratio":6.6,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.degraded.fg on status.degraded.bg","ratio":6.39,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.degraded.fg on surface.primary","ratio":7.79,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.warning.fg on status.warning.bg","ratio":5.67,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.warning.fg on surface.primary","ratio":6.67,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.critical.fg on status.critical.bg","ratio":6.88,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.critical.fg on surface.primary","ratio":8.51,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.info.fg on status.info.bg","ratio":7.12,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.info.fg on surface.primary","ratio":8.7,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.unknown.fg on status.unknown.bg","ratio":8.23,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.unknown.fg on surface.primary","ratio":10.44,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.inactive.fg on status.inactive.bg","ratio":7.03,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.inactive.fg on surface.primary","ratio":7.73,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"tier.lite.fg on tier.lite.bg","ratio":6.68,"need":4.5,"pass":true},{"theme":"venom-light","pair":"tier.pro.fg on tier.pro.bg","ratio":7.54,"need":4.5,"pass":true},{"theme":"venom-light","pair":"tier.max.fg on tier.max.bg","ratio":6.09,"need":4.5,"pass":true},{"theme":"venom-light","pair":"action.primary.fg on action.primary.bg","ratio":21,"need":4.5,"pass":true},{"theme":"venom-light","pair":"action.secondary.fg on action.secondary.bg","ratio":17.01,"need":4.5,"pass":true},{"theme":"venom-light","pair":"action.destructive.fg on action.destructive.bg","ratio":6.15,"need":4.5,"pass":true},{"theme":"venom-light","pair":"border.strong on surface.primary","ratio":4.83,"need":3,"pass":true,"label":"control boundary"},{"theme":"venom-light","pair":"focus.ring on surface.canvas","ratio":19.11,"need":3,"pass":true,"label":"focus ring"}]}`
- Log: CONTRAST: 78 pairings checked, 0 failures

### Guardrails (raw-color, terminology, secrets, icon map, state coverage, story coverage, CDN scan, required components)

- Status: **PASS**
- Command: `checkVenomGuardrails(io)`
- Started: 2026-08-07T05:34:32.452Z · Finished: 2026-08-07T05:34:32.925Z
- Summary: `{"lintFiles":137,"terminologyFiles":199,"iconGlyphsDefined":80,"iconGlyphsUsed":41,"stateChecks":97,"componentFileCount":62,"cdnScanFiles":208,"requiredComponentsChecked":4,"anyScannedFiles":125,"errors":[]}`
- Log: GUARDRAILS: 137 lint files, 199 terminology files, 41/80 glyphs, 97 state checks, 62 components, 208 cdn-scanned files, 125 any-scanned files, 0 violations

### Type check (strict)

- Status: **PASS**
- Command: `npx tsc -p tsconfig.json --noEmit`
- Started: 2026-08-07T05:34:32.925Z · Finished: 2026-08-07T05:34:37.156Z

### Declaration generation (components/**/*.tsx -> sibling .d.ts)

- Status: **PASS**
- Command: `npx tsc -p tsconfig.declarations.json`
- Started: 2026-08-07T05:34:37.186Z · Finished: 2026-08-07T05:34:40.085Z

### Declaration drift check (checked-in .d.ts vs freshly generated)

- Status: **PASS**
- Command: `diff before/after tsc -p tsconfig.declarations.json`
- Started: 2026-08-07T05:34:40.085Z · Finished: 2026-08-07T05:34:40.107Z
- Summary: `{"declarationFilesChecked":63,"drifted":0}`

### Declaration generation (package entry points -> dist/types)

- Status: **PASS**
- Command: `npx tsc -p tsconfig.build.json`
- Started: 2026-08-07T05:34:40.122Z · Finished: 2026-08-07T05:34:43.946Z

### Explorer production build (Vite, no in-browser transpilation)

- Status: **PASS**
- Command: `npx vite build --config vite.explorer.config.ts`
- Started: 2026-08-07T05:34:43.946Z · Finished: 2026-08-07T05:34:47.533Z
- Output: `[36mvite v5.4.21 [32mbuilding for production...[36m[39m transforming... [32m✓[39m 213 modules transformed. rendering chunks... computing gzip size... [2mdist-explorer/[22m[32mcomponents/icons/icon.card.html                                    [39m[1m[2m  0.65 kB[22m[1m[22m[2m │ gzip:  0.43 kB[22m [2mdist-explorer/[22m[32mcomponents/domain-routing/routing.card.html                        [39m[1m[2m  0.84 kB[22m[1m[22m[2m │ gzip:  0.47 kB[22m [2mdist-explorer/[22m[32m`

### Library build (Vite, dist/*.mjs + dist/*.cjs)

- Status: **PASS**
- Command: `npx vite build`
- Started: 2026-08-07T05:34:47.533Z · Finished: 2026-08-07T05:34:51.185Z
- Output: `[36mvite v5.4.21 [32mbuilding for production...[36m[39m transforming... [32m✓[39m 74 modules transformed. rendering chunks... computing gzip size... [2mdist/[22m[36micons.mjs                [39m[1m[2m 0.14 kB[22m[1m[22m[2m │ gzip:  0.14 kB[22m[2m │ map:   0.09 kB[22m [2mdist/[22m[36mdensity.mjs              [39m[1m[2m 0.40 kB[22m[1m[22m[2m │ gzip:  0.27 kB[22m[2m │ map:   1.41 kB[22m [2mdist/[22m[36mthemes.mjs               [39m[1m[2m 0.51 kB[22m[1m[22m[`

### Package export completeness (every dist/*.mjs entry point is importable and non-empty)

- Status: **PASS**
- Command: `node dist\.export-check.mjs`
- Started: 2026-08-07T05:34:51.186Z · Finished: 2026-08-07T05:34:51.386Z
- Output: `index: 172 symbols tokens: 1 symbols themes: 6 symbols density: 5 symbols customizer: 15 symbols icons: 2 symbols primitives: 69 symbols domain: 74 symbols tailwind: 2 symbols`

### No stale validation artifacts

- Status: **PASS**
- Command: `ls('validation') + filename check`
- Started: 2026-08-07T05:34:51.386Z · Finished: 2026-08-07T05:34:51.386Z
- Summary: `{"scannedFiles":10,"staleFound":0}`

### Handoff manifest (classification, canonical sources, generated artifacts, stale duplicates, exclusions)

- Status: **PASS**
- Command: `checkVenomHandoff(io)`
- Started: 2026-08-07T05:34:51.386Z · Finished: 2026-08-07T05:34:51.411Z
- Summary: `{"topLevelEntries":30,"canonicalConcerns":10,"componentModulesWithDeclarations":63,"errors":[]}`
- Log: HANDOFF: 30 top-level entries classified, 10 canonical concerns present, 63 component modules with generated declarations, 0 stale duplicates, exclusions covered.

## Not covered by this command

`npm run test:a11y` and `npm run test:visual` (Playwright + axe-core, and the visual-regression suite) are separate commands — they drive a real browser and are not run as part of `npm run validate`. See `tests/` and the accessibility-evidence section of the remediation report for their status.
