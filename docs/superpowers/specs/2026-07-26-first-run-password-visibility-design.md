# First-Run Password Visibility Design

**Date:** 2026-07-26  
**Scope:** First-run owner-password setup only

## Goal

Add an independent show/hide control to each password field on the first-run setup screen without changing the screen's established Venom Design System appearance or modifying `Design_System/`.

## Interaction

- Both fields start masked with `type="password"`.
- The owner-password control toggles only the owner-password field.
- The confirmation control toggles only the confirmation field.
- A hidden field uses the Design System `eye` icon; a visible field uses `eye-off`.
- Each control exposes its action through an accessible label and `aria-pressed` state.
- Toggling visibility preserves the entered value and the existing submit, validation, lockout, and secret-clearing behavior.
- Disabled/submitting/locked state applies to both the input and its visibility control.

## Design System Composition

Use only public exports from `@venom/design-system/primitives`:

- `Input` remains the password control.
- `IconButton` is the trailing visibility action with `variant="ghost"` and `size="sm"`.
- `eye` and `eye-off` are already part of the pinned Design System icon set.

The composition uses the dashboard's existing Tailwind preset utilities only: a relative wrapper, token-backed right padding on the input, and token-backed positioning for the trailing button. No new stylesheet, raw visual value, dependency, copied component, or Design System edit is permitted.

## Accessibility

- Button labels are `Show password` / `Hide password` and `Show password confirmation` / `Hide password confirmation`.
- Labels deliberately do not duplicate the fields' accessible names, preserving unambiguous label queries and screen-reader navigation.
- Both controls remain native buttons and keyboard-operable through `IconButton`.
- The visible state is exposed with `aria-pressed`.

## Verification

- Add a focused component test proving masked-by-default behavior, independent toggles, icon-state labels, value preservation, and disabled-state propagation.
- Run the focused test red before implementation and green afterward.
- Run dashboard tests, typecheck, lint, Design System adherence, and production build.
- Inspect the first-run screen in a real browser in both masked and revealed states.

## Non-goals

- No change to the normal sign-in or re-verification screens.
- No changes inside `Design_System/`.
- No new password policy, persistence, API, or submission behavior.
