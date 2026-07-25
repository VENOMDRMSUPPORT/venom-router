# First-Run Password Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add independent, accessible show/hide eye controls to the two first-run password fields.

**Architecture:** Keep the feature local to `FirstRunSetup.tsx` as a small composition of the public Design System `Input` and `IconButton`. The composition forwards the `id`, invalid state, and ARIA description attributes injected by `FormField` to the real input so existing label and error wiring remains intact.

**Tech Stack:** React 18, TypeScript, Vitest, Testing Library, Tailwind using `venomTailwindPreset`, `@venom/design-system@1.0.0`.

## Global Constraints

- Do not modify `Design_System/`.
- Use only public Design System primitives and the existing `eye` / `eye-off` icons.
- Add no dependency, stylesheet, raw visual value, or custom token mapping.
- Limit behavior changes to the first-run setup screen.
- Preserve validation, submission, lockout, autocomplete, and secret clearing.

---

### Task 1: First-run password visibility controls

**Files:**
- Create: `dashboard/src/auth/FirstRunSetup.test.tsx`
- Modify: `dashboard/src/auth/FirstRunSetup.tsx`

**Interfaces:**
- Consumes: `Input`, `IconButton`, and `FormField` from `@venom/design-system/primitives`.
- Produces: two independently controlled password inputs with dynamic accessible labels and `aria-pressed`.

- [ ] **Step 1: Write the failing interaction test**

Create `dashboard/src/auth/FirstRunSetup.test.tsx`:

```tsx
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import FirstRunSetup from "./FirstRunSetup";

afterEach(cleanup);

describe("FirstRunSetup password visibility", () => {
  it("keeps both passwords masked by default and toggles each field independently", () => {
    render(<FirstRunSetup onComplete={vi.fn()} />);

    const password = screen.getByLabelText(/owner password/i) as HTMLInputElement;
    const confirmation = screen.getByLabelText(/confirm password/i) as HTMLInputElement;
    fireEvent.change(password, { target: { value: "owner-password-value" } });
    fireEvent.change(confirmation, { target: { value: "confirmation-value" } });

    expect(password.type).toBe("password");
    expect(confirmation.type).toBe("password");

    const passwordToggle = screen.getByRole("button", { name: "Show password" });
    expect(passwordToggle.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(passwordToggle);

    expect(password.type).toBe("text");
    expect(password.value).toBe("owner-password-value");
    expect(confirmation.type).toBe("password");
    expect(screen.getByRole("button", { name: "Hide password" }).getAttribute("aria-pressed")).toBe("true");

    fireEvent.click(screen.getByRole("button", { name: "Show password confirmation" }));
    expect(password.type).toBe("text");
    expect(confirmation.type).toBe("text");
    expect(confirmation.value).toBe("confirmation-value");

    fireEvent.click(screen.getByRole("button", { name: "Hide password" }));
    expect(password.type).toBe("password");
    expect(confirmation.type).toBe("text");
  });
});
```

- [ ] **Step 2: Run the focused test and verify the red state**

Run from `dashboard/`:

```powershell
npm test -- --run src/auth/FirstRunSetup.test.tsx
```

Expected: FAIL because no button named `Show password` exists.

- [ ] **Step 3: Implement the minimal Design System composition**

In `FirstRunSetup.tsx`, import `ComponentProps` and `IconButton`. Add a local `PasswordInput` extending `Omit<ComponentProps<typeof Input>, "type">`; it accepts `revealed`, `revealLabel`, and `onRevealChange`, forwards remaining props to the real input, and renders:

```tsx
<div className="relative">
  <Input {...inputProps} type={revealed ? "text" : "password"} className="pr-10" />
  <IconButton
    icon={revealed ? "eye-off" : "eye"}
    label={`${revealed ? "Hide" : "Show"} ${revealLabel}`}
    variant="ghost"
    size="sm"
    aria-pressed={revealed}
    disabled={inputProps.disabled}
    onClick={onRevealChange}
    className="absolute right-1 top-1/2 -translate-y-1/2"
  />
</div>
```

Add independent `passwordRevealed` and `confirmationRevealed` state. Replace only the two first-run inputs with this composition, using `revealLabel="password"` and `revealLabel="password confirmation"`. Preserve their current autocomplete, value, disabled, and change props exactly.

- [ ] **Step 4: Run the focused test and verify the green state**

```powershell
npm test -- --run src/auth/FirstRunSetup.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Run dashboard verification**

```powershell
npm test
npm run typecheck
npm run lint
npm run check:ds-adherence
npm run build
```

Expected: every command exits zero and adherence confirms `Design_System/` is untouched.

- [ ] **Step 6: Verify the rendered interaction**

Open the first-run screen in a real browser. Confirm both fields start masked, each eye changes only its field and becomes `eye-off` while visible, Space/Enter works with a visible focus ring, values survive toggles, and no layout overlap or size drift appears.

- [ ] **Step 7: Commit the implementation**

```powershell
git add -- dashboard/src/auth/FirstRunSetup.tsx dashboard/src/auth/FirstRunSetup.test.tsx docs/superpowers/plans/2026-07-26-first-run-password-visibility.md
git commit -m "feat(auth): add first-run password visibility controls"
```
