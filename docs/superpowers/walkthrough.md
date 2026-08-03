# Border System Unification Walkthrough

## Summary of Changes
Unified border design patterns across the design system to eliminate harsh, high-contrast borders on badges, inputs, and cards in both dark (`venom-dark`) and light (`venom-light`) themes.

### Key Changes
1. **New `accent.border` Semantic Token**:
   - `tokens.semantic.venom-dark.json`: Added `accent.border` mapped to `{color.gray.700}` (`#3F3F46`).
   - `tokens.semantic.venom-light.json`: Added `accent.border` mapped to `{color.gray.300}` (`#A1A1AA`).
   - `components-core.css`: Updated `.vn-badge--accent` (used by `CertificationStateBadge` for `state="certified"`) to use `border-color: var(--accent-border)` instead of `var(--accent-default)`.

2. **Refined Input & Toolbar Search Borders**:
   - `tokens.component.json`: Changed `"input.border.default"` from `{border.strong}` to `{border.default}`.
   - Toolbar search input and form controls now present a smooth, subtle border (`#2E2E33` in dark theme, `#D4D4D8` in light theme) during idle state, transitioning smoothly on hover and focus.

3. **Design System Token Compilation**:
   - Recompiled all CSS artifacts (`themes/venom-dark.css`, `themes/venom-light.css`, `tokens/components.css`, `tokens/tokens.ts`, `tokens/tailwind-theme.ts`).

## Verification Results
- **Design System Gates (`node scripts/validate.js`)**: 12/12 gates passed (Token schema, WCAG AA contrast, guardrails, type check, declaration drift check, explorer build, library build, package exports, handoff manifest).
- **Dashboard Unit Tests (`vitest`)**: 485/485 tests passed across 38 test files.
