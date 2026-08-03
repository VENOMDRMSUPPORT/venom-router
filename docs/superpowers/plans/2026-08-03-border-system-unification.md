# Border System Unification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify and refine design system border tokens across light and dark themes to eliminate jarring high-contrast outlines on badges (`Certified` badge), inputs (toolbar search input), and cards.

**Architecture:** Add `accent.border` token to semantic token files for both `venom-dark` and `venom-light`. Update `input.border.default` in `tokens.component.json` to `{border.default}` instead of `{border.strong}`. Soften dark theme `border.strong` to `{color.gray.600}` (`#52525B`). Recompile token CSS and validate across design system and dashboard test suites.

**Tech Stack:** Node.js, JSON W3C Design Tokens, CSS Custom Properties, Vite, TypeScript, Playwright/Vitest.

## Global Constraints
- Every theme file defines EXACTLY the same token set (`venom-dark` and `venom-light` must remain 100% structurally identical in token keys).
- Component tokens must alias semantic tokens only.
- Run `node Design_System/scripts/validate.js` to build and validate design system tokens and guardrails.

---

### Task 1: Update Semantic Tokens & CSS for Accent Badge Border

**Files:**
- Modify: `Design_System/tokens/tokens.semantic.venom-dark.json:33-40`
- Modify: `Design_System/tokens/tokens.semantic.venom-light.json:33-40`
- Modify: `Design_System/css/components-core.css:151`
- Test: `Design_System/scripts/validate.js`

**Interfaces:**
- Consumes: W3C Design Token JSON structure
- Produces: `--accent-border` CSS variable in compiled theme files (`themes/venom-dark.css` and `themes/venom-light.css`)

- [ ] **Step 1: Add `accent.border` to `tokens.semantic.venom-dark.json`**

In `Design_System/tokens/tokens.semantic.venom-dark.json`, update the `"accent"` object to:
```json
  "accent": {
    "$type": "color",
    "default":   { "$value": "{color.gray.0}" },
    "hover":     { "$value": "{color.gray.75}" },
    "active":    { "$value": "{color.gray.100}" },
    "subtle-bg": { "$value": "{color.gray.925}" },
    "text":      { "$value": "{color.gray.0}" },
    "border":    { "$value": "{color.gray.700}" }
  },
```

- [ ] **Step 2: Add `accent.border` to `tokens.semantic.venom-light.json`**

In `Design_System/tokens/tokens.semantic.venom-light.json`, update the `"accent"` object to:
```json
  "accent": {
    "$type": "color",
    "default":   { "$value": "{color.gray.1000}" },
    "hover":     { "$value": "{color.gray.940}" },
    "active":    { "$value": "{color.gray.925}" },
    "subtle-bg": { "$value": "{color.gray.50}" },
    "text":      { "$value": "{color.gray.1000}" },
    "border":    { "$value": "{color.gray.300}" }
  },
```

- [ ] **Step 3: Update `.vn-badge--accent` rule in `Design_System/css/components-core.css`**

In `Design_System/css/components-core.css` line 151, replace:
```css
.vn-badge--accent { background: var(--accent-subtle-bg); color: var(--accent-text); border-color: var(--accent-default); }
```
with:
```css
.vn-badge--accent { background: var(--accent-subtle-bg); color: var(--accent-text); border-color: var(--accent-border); }
```

- [ ] **Step 4: Commit changes**

```bash
git add Design_System/tokens/tokens.semantic.venom-dark.json Design_System/tokens/tokens.semantic.venom-light.json Design_System/css/components-core.css
git commit -m "style(design-system): add accent.border token and update accent badge border styling"
```

---

### Task 2: Refine Component Tokens for Inputs and Dark Theme Border Strong

**Files:**
- Modify: `Design_System/tokens/tokens.component.json:14`
- Modify: `Design_System/tokens/tokens.semantic.venom-dark.json:30`
- Test: `Design_System/scripts/validate.js`

**Interfaces:**
- Consumes: Semantic tokens `{border.default}` and `{color.gray.600}`
- Produces: `--input-border-default` mapped to `var(--border-default)` and `--border-strong` mapped to `#52525B` in dark theme.

- [ ] **Step 1: Update `input.border.default` in `tokens.component.json`**

In `Design_System/tokens/tokens.component.json` line 14, change `"input.border.default"`:
```json
  "input": {
    "bg":            { "$value": "{surface.primary}" },
    "fg":            { "$value": "{text.primary}" },
    "placeholder":   { "$value": "{text.muted}" },
    "border":        { "default": { "$value": "{border.default}" }, "focus": { "$value": "{border.focus}" }, "invalid": { "$value": "{status.critical.border}" } },
    "bg-disabled":   { "$value": "{action.disabled.bg}" }
  },
```

- [ ] **Step 2: Tune `border.strong` in `tokens.semantic.venom-dark.json`**

In `Design_System/tokens/tokens.semantic.venom-dark.json` line 30, change:
```json
  "border": {
    "subtle":  { "$value": "{color.gray.915}" },
    "default": { "$value": "{color.gray.750}" },
    "strong":  { "$value": "{color.gray.600}" },
    "focus":   { "$value": "{color.gray.0}" }
  },
```

- [ ] **Step 3: Commit changes**

```bash
git add Design_System/tokens/tokens.component.json Design_System/tokens/tokens.semantic.venom-dark.json
git commit -m "style(design-system): unify input idle border to border.default and refine dark theme border.strong"
```

---

### Task 3: Build Tokens, Run Validation Harness, and Verify Dashboard

**Files:**
- Generated: `Design_System/themes/venom-dark.css`, `Design_System/themes/venom-light.css`, `Design_System/tokens/components.css`, `Design_System/tokens/tokens.ts`, `Design_System/tokens/tailwind-theme.ts`
- Test: `Design_System/scripts/validate.js`
- Test: `dashboard/src/theme-runtime.test.ts`

- [ ] **Step 1: Run Design System validation script to compile tokens and run gates**

Run: `node Design_System/scripts/validate.js`
Expected: 100% of validation gates PASS.

- [ ] **Step 2: Run dashboard unit tests**

Run: `npm test` in `dashboard` (or `npx vitest run` in `dashboard`)
Expected: All tests PASS.

- [ ] **Step 3: Commit generated files and validation report**

```bash
git add Design_System/themes Design_System/tokens Design_System/validation
git commit -m "build(design-system): recompile tokens and update validation report"
```
