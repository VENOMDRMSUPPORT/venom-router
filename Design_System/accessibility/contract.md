# Accessibility contract (WCAG 2.2 AA minimum)

Mechanically checked where possible (`validation/`); the rest is a binding review rule.

## Contrast
Every text/background token pairing meets ≥ 4.5:1 (body) / ≥ 3:1 (large text, UI boundaries) in both themes. Enforced by `validation/check-contrast.js` over the resolved theme manifest — pairings validated: text.{primary,secondary,muted} × surface.{canvas,primary,secondary,raised}; status.*.fg × status.*.bg and × surfaces; tier.*.fg × tier.*.bg; action fg × bg; text.link × surfaces; text.on-accent × accent.default. `text.disabled` is exempt per WCAG (disabled controls) but still tuned for legibility.

## Keyboard
- Everything interactive is reachable and operable: buttons/links native; menus and comboboxes use Arrow/Home/End/Enter/Escape with roving tabindex; tabs arrow-navigate; clickable table rows take Enter/Space (tabIndex=0).
- One global visible focus ring (`focus.ring`, `:focus-visible`, 2px — 3px in HC); inset variants for rows/tabs. Logical tab order follows DOM order; nothing positive-tabindex.
- Dialogs/Drawers trap Tab (wrap first↔last), close on Escape, and **restore focus to the opener**. AlertDialog requires an explicit choice (no scrim dismiss).
- Nothing depends on hover alone: tooltips open on focus; row actions are real buttons; truncated cells have full values in the row Drawer.

## Semantics
- Status is never color-only: StatusBadge and every domain state = icon + text label (+ title/tooltip detail). `unknown` additionally uses a dashed border (shape channel); meters use hatching for unknown.
- Roles/labels: `role="alert"` for critical inline errors, `role="status"`/`aria-live="polite"` for toasts and job progress; `aria-invalid` + `aria-describedby` wiring via FormField; `aria-sort` on sorted headers; meters/progress expose `aria-valuenow/valuetext` ("unknown" is a value text, never a fake number); icon-only controls REQUIRE `label` (IconButton enforces).
- Tables: real `<table>` semantics, sticky `<th>` headers, caption via `aria-label`; row expansion/detail is reachable via keyboard (row Enter → Drawer).

## Motion & zoom
`prefers-reduced-motion` collapses all animation/transitions to 0.01ms globally (no component opts out). Layouts are fluid; 200% zoom keeps all controls usable (desktop-first grid collapses; tables scroll horizontally with sticky identity column strategy). Pointer targets ≥ 24×24 CSS px in compact, ≥ 32 in comfortable for primary controls.

## Screen-reader specifics per domain
- Secret reveal: revealed value announced once; hide clears the DOM node (no stale secret in the accessibility tree).
- Countdown chips (cooldown, session expiry) update at most every second and are `aria-live="off"` with an accessible absolute time — no announcement spam.
- Route traces: exclusion codes are text; the chosen row carries "chosen" as text, not just emphasis.

## Verification
- Automated: contrast matrix gate (CI-style script in `validation/`), structural lint for icon-only labels and color-only status (`validation/checks`), axe-core run over `storybook/index.html` and cards recommended in the consuming repo's CI (documented in the handoff contract).
- Manual keyboard walkthrough checklist: `accessibility/keyboard-checklist.md`.

## Known limitations (V1 of the DS package)
- axe/visual-regression run as documented CI recipes for the consuming repo; this package ships the mechanical token/contrast/coverage gates it can run standalone.
- RTL: layouts use logical flex/grid ordering and avoid directional icons for meaning, but full RTL mirroring is untested (documented as future work, per brief §9 extensibility).
