# Validation report — Venom Design System

Run at `2026-07-23T00:28:49.844Z` (finished `2026-07-23T00:29:00.464Z`) against package `@venom/design-system@1.0.0` by `scripts/validate.js`. Every row below is an actual gate output from this run — not a hand-maintained estimate. Regenerate with `npm run validate`; never hand-edit this file.

## Gate results

| # | Gate | Status | Command |
|---|------|--------|---------|
| 1 | Token build (schema, completeness, reference legality) | **PASS** | `buildVenomTokens(io)` |
| 2 | Contrast (WCAG AA; AAA core text in venom-hc) | **PASS** | `checkVenomContrast(io)` |
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
- Started: 2026-07-23T00:28:49.846Z · Finished: 2026-07-23T00:28:49.856Z
- Summary: `{"venom-dark":{"count":93,"missing":[],"extra":[]},"venom-light":{"count":93,"missing":[],"extra":[]},"venom-hc":{"count":93,"missing":[],"extra":[]}}`
- Log: OK: 164 primitives, 93 semantic tokens x 3 themes, 73 component tokens, 16 density tokens, 17 tailwind theme groups.

### Contrast (WCAG AA; AAA core text in venom-hc)

- Status: **PASS**
- Command: `checkVenomContrast(io)`
- Started: 2026-07-23T00:28:49.856Z · Finished: 2026-07-23T00:28:49.857Z
- Summary: `{"summary":{"checked":119,"failed":0,"failures":[]},"results":[{"theme":"venom-dark","pair":"text.primary on surface.canvas","ratio":17.78,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.secondary on surface.canvas","ratio":11.98,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.muted on surface.canvas","ratio":8.52,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.primary on surface.primary","ratio":17.08,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.secondary on surface.primary","ratio":11.51,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.muted on surface.primary","ratio":8.18,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.primary on surface.secondary","ratio":16.16,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.secondary on surface.secondary","ratio":10.89,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.muted on surface.secondary","ratio":7.74,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.primary on surface.raised","ratio":14.43,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.secondary on surface.raised","ratio":9.72,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.muted on surface.raised","ratio":6.91,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.link on surface.canvas","ratio":10.8,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.link on surface.primary","ratio":10.37,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"text.on-accent on accent.default","ratio":8.14,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"accent.text on surface.canvas","ratio":10.8,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"accent.text on surface.primary","ratio":10.37,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.healthy.fg on status.healthy.bg","ratio":7.57,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.healthy.fg on surface.primary","ratio":10.37,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.degraded.fg on status.degraded.bg","ratio":6.98,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.degraded.fg on surface.primary","ratio":8.85,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.warning.fg on status.warning.bg","ratio":7.61,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.warning.fg on surface.primary","ratio":10.63,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.critical.fg on status.critical.bg","ratio":6.84,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.critical.fg on surface.primary","ratio":8.36,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.info.fg on status.info.bg","ratio":6.92,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.info.fg on surface.primary","ratio":8.62,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.unknown.fg on status.unknown.bg","ratio":9.72,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.unknown.fg on surface.primary","ratio":11.51,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"status.inactive.fg on status.inactive.bg","ratio":7.74,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"status.inactive.fg on surface.primary","ratio":8.18,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-dark","pair":"tier.lite.fg on tier.lite.bg","ratio":7.35,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"tier.pro.fg on tier.pro.bg","ratio":6.53,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"tier.max.fg on tier.max.bg","ratio":7.73,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"action.primary.fg on action.primary.bg","ratio":8.14,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"action.secondary.fg on action.secondary.bg","ratio":14.43,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"action.destructive.fg on action.destructive.bg","ratio":6.15,"need":4.5,"pass":true},{"theme":"venom-dark","pair":"border.strong on surface.primary","ratio":5.16,"need":3,"pass":true,"label":"control boundary"},{"theme":"venom-dark","pair":"focus.ring on surface.canvas","ratio":10.8,"need":3,"pass":true,"label":"focus ring"},{"theme":"venom-light","pair":"text.primary on surface.canvas","ratio":17.36,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.secondary on surface.canvas","ratio":8.84,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.muted on surface.canvas","ratio":5.83,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.primary on surface.primary","ratio":16.89,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.secondary on surface.primary","ratio":8.6,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.muted on surface.primary","ratio":5.67,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.primary on surface.secondary","ratio":16.16,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.secondary on surface.secondary","ratio":8.22,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.muted on surface.secondary","ratio":5.42,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.primary on surface.raised","ratio":17.36,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.secondary on surface.raised","ratio":8.84,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.muted on surface.raised","ratio":5.83,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.link on surface.canvas","ratio":6.6,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.link on surface.primary","ratio":6.42,"need":4.5,"pass":true},{"theme":"venom-light","pair":"text.on-accent on accent.default","ratio":6.6,"need":4.5,"pass":true},{"theme":"venom-light","pair":"accent.text on surface.canvas","ratio":6.6,"need":4.5,"pass":true},{"theme":"venom-light","pair":"accent.text on surface.primary","ratio":6.42,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.healthy.fg on status.healthy.bg","ratio":5.7,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.healthy.fg on surface.primary","ratio":6.42,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.degraded.fg on status.degraded.bg","ratio":6.39,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.degraded.fg on surface.primary","ratio":7.58,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.warning.fg on status.warning.bg","ratio":5.67,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.warning.fg on surface.primary","ratio":6.49,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.critical.fg on status.critical.bg","ratio":6.88,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.critical.fg on surface.primary","ratio":8.28,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.info.fg on status.info.bg","ratio":7.12,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.info.fg on surface.primary","ratio":8.47,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.unknown.fg on status.unknown.bg","ratio":7.72,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.unknown.fg on surface.primary","ratio":8.6,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"status.inactive.fg on status.inactive.bg","ratio":5.42,"need":4.5,"pass":true},{"theme":"venom-light","pair":"status.inactive.fg on surface.primary","ratio":5.67,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-light","pair":"tier.lite.fg on tier.lite.bg","ratio":6.68,"need":4.5,"pass":true},{"theme":"venom-light","pair":"tier.pro.fg on tier.pro.bg","ratio":7.54,"need":4.5,"pass":true},{"theme":"venom-light","pair":"tier.max.fg on tier.max.bg","ratio":6.09,"need":4.5,"pass":true},{"theme":"venom-light","pair":"action.primary.fg on action.primary.bg","ratio":6.6,"need":4.5,"pass":true},{"theme":"venom-light","pair":"action.secondary.fg on action.secondary.bg","ratio":17.36,"need":4.5,"pass":true},{"theme":"venom-light","pair":"action.destructive.fg on action.destructive.bg","ratio":6.15,"need":4.5,"pass":true},{"theme":"venom-light","pair":"border.strong on surface.primary","ratio":3.46,"need":3,"pass":true,"label":"control boundary"},{"theme":"venom-light","pair":"focus.ring on surface.canvas","ratio":4.63,"need":3,"pass":true,"label":"focus ring"},{"theme":"venom-hc","pair":"text.primary on surface.canvas","ratio":21,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.secondary on surface.canvas","ratio":18.35,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.muted on surface.canvas","ratio":16.39,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.primary on surface.primary","ratio":21,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.secondary on surface.primary","ratio":18.35,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.muted on surface.primary","ratio":16.39,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.primary on surface.secondary","ratio":19.11,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.secondary on surface.secondary","ratio":16.69,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.muted on surface.secondary","ratio":14.91,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.primary on surface.raised","ratio":18.35,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.secondary on surface.raised","ratio":16.03,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.muted on surface.raised","ratio":14.32,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.link on surface.canvas","ratio":14.27,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.link on surface.primary","ratio":14.27,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"text.on-accent on accent.default","ratio":11.87,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"accent.text on surface.canvas","ratio":15.21,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"accent.text on surface.primary","ratio":15.21,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"status.healthy.fg on status.healthy.bg","ratio":9.7,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"status.healthy.fg on surface.primary","ratio":15.21,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-hc","pair":"status.degraded.fg on status.degraded.bg","ratio":9.34,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"status.degraded.fg on surface.primary","ratio":13.54,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-hc","pair":"status.warning.fg on status.warning.bg","ratio":9.43,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"status.warning.fg on surface.primary","ratio":15.07,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-hc","pair":"status.critical.fg on status.critical.bg","ratio":9.17,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"status.critical.fg on surface.primary","ratio":12.84,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-hc","pair":"status.info.fg on status.info.bg","ratio":9.51,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"status.info.fg on surface.primary","ratio":13.55,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-hc","pair":"status.unknown.fg on status.unknown.bg","ratio":13.54,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"status.unknown.fg on surface.primary","ratio":18.35,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-hc","pair":"status.inactive.fg on status.inactive.bg","ratio":10.89,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"status.inactive.fg on surface.primary","ratio":13.17,"need":3,"pass":true,"label":"icon/graphic on surface"},{"theme":"venom-hc","pair":"tier.lite.fg on tier.lite.bg","ratio":9.73,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"tier.pro.fg on tier.pro.bg","ratio":9.42,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"tier.max.fg on tier.max.bg","ratio":9.69,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"action.primary.fg on action.primary.bg","ratio":11.87,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"action.secondary.fg on action.secondary.bg","ratio":18.35,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"action.destructive.fg on action.destructive.bg","ratio":9.57,"need":4.5,"pass":true},{"theme":"venom-hc","pair":"border.strong on surface.primary","ratio":16.39,"need":3,"pass":true,"label":"control boundary"},{"theme":"venom-hc","pair":"focus.ring on surface.canvas","ratio":14.81,"need":3,"pass":true,"label":"focus ring"},{"theme":"venom-hc","pair":"text.primary on surface.canvas","ratio":21,"need":7,"pass":true,"label":"AAA"},{"theme":"venom-hc","pair":"text.secondary on surface.canvas","ratio":18.35,"need":7,"pass":true,"label":"AAA"}]}`
- Log: CONTRAST: 119 pairings checked, 0 failures

### Guardrails (raw-color, terminology, secrets, icon map, state coverage, story coverage, CDN scan, required components)

- Status: **PASS**
- Command: `checkVenomGuardrails(io)`
- Started: 2026-07-23T00:28:49.857Z · Finished: 2026-07-23T00:28:49.981Z
- Summary: `{"lintFiles":131,"terminologyFiles":187,"iconGlyphsDefined":79,"iconGlyphsUsed":41,"stateChecks":97,"componentFileCount":56,"cdnScanFiles":195,"requiredComponentsChecked":4,"anyScannedFiles":113,"errors":[]}`
- Log: GUARDRAILS: 131 lint files, 187 terminology files, 41/79 glyphs, 97 state checks, 56 components, 195 cdn-scanned files, 113 any-scanned files, 0 violations

### Type check (strict)

- Status: **PASS**
- Command: `npx tsc -p tsconfig.json --noEmit`
- Started: 2026-07-23T00:28:49.981Z · Finished: 2026-07-23T00:28:52.349Z

### Declaration generation (components/**/*.tsx -> sibling .d.ts)

- Status: **PASS**
- Command: `npx tsc -p tsconfig.declarations.json`
- Started: 2026-07-23T00:28:52.369Z · Finished: 2026-07-23T00:28:54.604Z

### Declaration drift check (checked-in .d.ts vs freshly generated)

- Status: **PASS**
- Command: `diff before/after tsc -p tsconfig.declarations.json`
- Started: 2026-07-23T00:28:54.604Z · Finished: 2026-07-23T00:28:54.625Z
- Summary: `{"declarationFilesChecked":57,"drifted":0}`

### Declaration generation (package entry points -> dist/types)

- Status: **PASS**
- Command: `npx tsc -p tsconfig.build.json`
- Started: 2026-07-23T00:28:54.640Z · Finished: 2026-07-23T00:28:57.002Z

### Explorer production build (Vite, no in-browser transpilation)

- Status: **PASS**
- Command: `npx vite build --config vite.explorer.config.ts`
- Started: 2026-07-23T00:28:57.002Z · Finished: 2026-07-23T00:28:58.986Z
- Output: `[36mvite v5.4.21 [32mbuilding for production...[36m[39m transforming... [32m✓[39m 206 modules transformed. rendering chunks... computing gzip size... [2mdist-explorer/[22m[32mcomponents/icons/icon.card.html                                    [39m[1m[2m  0.65 kB[22m[1m[22m[2m │ gzip:  0.44 kB[22m [2mdist-explorer/[22m[32mcomponents/domain-routing/routing.card.html                        [39m[1m[2m  0.84 kB[22m[1m[22m[2m │ gzip:  0.48 kB[22m [2mdist-explorer/[22m[32m`

### Library build (Vite, dist/*.mjs + dist/*.cjs)

- Status: **PASS**
- Command: `npx vite build`
- Started: 2026-07-23T00:28:58.986Z · Finished: 2026-07-23T00:29:00.369Z
- Output: `[36mvite v5.4.21 [32mbuilding for production...[36m[39m transforming... [32m✓[39m 67 modules transformed. rendering chunks... computing gzip size... [2mdist/[22m[36micons.mjs                [39m[1m[2m 0.14 kB[22m[1m[22m[2m │ gzip:  0.14 kB[22m[2m │ map:   0.09 kB[22m [2mdist/[22m[36mdensity.mjs              [39m[1m[2m 0.40 kB[22m[1m[22m[2m │ gzip:  0.27 kB[22m[2m │ map:   1.41 kB[22m [2mdist/[22m[36mthemes.mjs               [39m[1m[2m 0.59 kB[22m[1m[22m[`

### Package export completeness (every dist/*.mjs entry point is importable and non-empty)

- Status: **PASS**
- Command: `node dist\.export-check.mjs`
- Started: 2026-07-23T00:29:00.369Z · Finished: 2026-07-23T00:29:00.448Z
- Output: `index: 148 symbols tokens: 1 symbols themes: 6 symbols density: 5 symbols icons: 2 symbols primitives: 60 symbols domain: 74 symbols tailwind: 2 symbols`

### No stale validation artifacts

- Status: **PASS**
- Command: `ls('validation') + filename check`
- Started: 2026-07-23T00:29:00.448Z · Finished: 2026-07-23T00:29:00.449Z
- Summary: `{"scannedFiles":10,"staleFound":0}`

### Handoff manifest (classification, canonical sources, generated artifacts, stale duplicates, exclusions)

- Status: **PASS**
- Command: `checkVenomHandoff(io)`
- Started: 2026-07-23T00:29:00.449Z · Finished: 2026-07-23T00:29:00.464Z
- Summary: `{"topLevelEntries":30,"canonicalConcerns":10,"componentModulesWithDeclarations":57,"errors":[]}`
- Log: HANDOFF: 30 top-level entries classified, 10 canonical concerns present, 57 component modules with generated declarations, 0 stale duplicates, exclusions covered.

## Not covered by this command

`npm run test:a11y` and `npm run test:visual` (Playwright + axe-core, and the visual-regression suite) are separate commands — they drive a real browser and are not run as part of `npm run validate`. See `tests/` and the accessibility-evidence section of the remediation report for their status.
