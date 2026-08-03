# Design System Border Unification & Refinement Design

## Problem Description
In both dark (`venom-dark`) and light (`venom-light`) themes, certain component borders are overly stark, jarring, or inconsistent across components:
1. **Accent / Certified Badge Border**: `.vn-badge--accent` (used by `CertificationStateBadge` for `state="certified"`) uses `border-color: var(--accent-default)`. In dark theme, `--accent-default` is pure white (`#FFFFFF`), causing a stark 100% white outline around the badge. In light theme, `--accent-default` is pure black (`#000000`).
2. **Input / Toolbar Search Box Borders**: `"input.border.default"` in `tokens.component.json` is set to `{border.strong}`. In dark mode, `{border.strong}` was `{color.gray.400}` (`#71717A`), causing all idle text inputs, search boxes, selects, and textareas to display a bright, high-contrast outline on dark backgrounds (`#1A1A1E`).
3. **Inconsistent Contrast & Hierarchy**: Dark theme `border.strong` (`gray.400` / `#71717A`) is too high-contrast for hover borders on cards and components.

## Proposed Changes

### 1. New Semantic Token: `accent.border`
- **Dark Theme (`tokens.semantic.venom-dark.json`)**:
  Add `"accent.border": { "$value": "{color.gray.700}" }` (`#3F3F46`).
- **Light Theme (`tokens.semantic.venom-light.json`)**:
  Add `"accent.border": { "$value": "{color.gray.300}" }` (`#A1A1AA`).
- **CSS Class Update (`components-core.css`)**:
  Update `.vn-badge--accent` to use `border-color: var(--accent-border)` instead of `var(--accent-default)`.

### 2. Component Token Adjustment: `input.border.default`
- **Component Tokens (`tokens.component.json`)**:
  Change `"input.border.default"` from `{border.strong}` to `{border.default}`.
  This ensures idle inputs, search inputs, selects, and textareas use a subtle, refined border (`#2E2E33` in dark theme, `#D4D4D8` in light theme), matching cards and panels.
  On hover, inputs transition to `var(--border-strong)`. On focus, they use `var(--input-border-focus)`.

### 3. Semantic Token Refinement: `border.strong`
- **Dark Theme (`tokens.semantic.venom-dark.json`)**:
  Refine `border.strong` from `{color.gray.400}` (`#71717A`) to `{color.gray.600}` (`#52525B`). This provides smooth, natural contrast for hover states and secondary elements without harsh outlines.

### 4. Build Pipeline & Validation
- Run token compilation (`node Design_System/scripts/validate.js`) to update generated CSS files (`themes/venom-dark.css`, `themes/venom-light.css`, `tokens/components.css`, `tokens/tokens.ts`, `tokens/tailwind-theme.ts`).
- Execute Design System and Dashboard test suites to ensure 100% test passing and zero regressions.

## Verification Plan
1. **Token Build & Guardrails**: Run `npm run validate` inside `Design_System`.
2. **Dashboard Integration**: Verify that `dashboard` compiles cleanly and passes unit tests (`npm test` in `dashboard`).
