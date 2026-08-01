// P6-TEST-001 (1a): flow-level accessibility across the owner's real journey.
//
// Each test here is a SEQUENCE, not a render. axe runs at every step, and the
// shared no-secret canary runs alongside it, because both classes of defect
// live in the transitions: focus that lands nowhere after a dialog closes, a
// table that loses its accessible name once populated, a raw key that survives
// its one-time reveal.
//
// COVERAGE HONESTY: `color-contrast` does NOT run here. jsdom has no layout or
// paint engine, so axe-core cannot compute rendered colours and src/test/axe.ts
// disables that rule. Contrast is covered in a real browser by
// tests/e2e/a11y.spec.ts. Nothing in this file should be cited as
// contrast coverage.

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createElement } from "react";
import { assertNoAxeViolations } from "./axe";
import { assertNoSecretsRendered, SENTINELS } from "./noSecrets";
import { gotoNav, installControlFetch, loginToShell, NO_LIVE_SESSION, renderShell } from "./flows";
import { apiError, REQUEST_ID } from "../../tests/e2e/fixtures";
import AuthGate from "../auth/AuthGate";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** The secrets that must never appear once the owner is inside the shell.
 *
 * `accountExternalID` is deliberately ABSENT from this list: the Fleet surface
 * renders it on purpose as the owner's handle on an account (AccountRow's
 * AccountIdentity), and it is non-secret by design. Banning it globally would
 * make the canary fire on correct shipped behaviour. It IS declared on the
 * surfaces that have no business showing it — see the cross-surface test at
 * the bottom of this file, which is where that check has teeth. */
const SHELL_SECRETS = [
  { label: "owner password", value: SENTINELS.ownerPassword },
  { label: "provider credential", value: SENTINELS.providerCredential },
  { label: "raw Venom API key", value: SENTINELS.rawVenomKey },
];

/**
 * Per-test timeout for the axe-heavy journeys.
 *
 * These tests are COMPUTE-bound, not race-bound: each step runs axe-core over
 * the entire shell DOM, and a multi-step journey does that four times. Alone
 * the longest takes ~1s; sharing a CPU with the other 28 test files it can
 * exceed vitest's 5s default and fail for no reason but load.
 *
 * Raising the bound is the correct fix and the opposite of a sleep — there is
 * no `waitForTimeout` anywhere in this suite, and every wait is on rendered
 * state. A bound that only contention can break is a flake generator, and this
 * repo has already been bitten by exactly that shape elsewhere.
 */
const AXE_JOURNEY_TIMEOUT_MS = 30_000;

/** Expands a provider card so its account rows render. The Fleet surface
 * keeps accounts collapsed behind a per-card disclosure (ProviderCard's
 * "Expand {name} accounts" control), so a journey that wants a populated
 * account list has to open it the way an owner does. */
async function expandProviderCard(providerName: string): Promise<void> {
  const toggle = await screen.findByRole("button", { name: new RegExp(`Expand ${providerName} accounts`, "i") });
  fireEvent.click(toggle);
}

describe("journey — sign in, then move through the shell", () => {
  it("stays accessible and secret-free at every step from login to the operate surfaces", async () => {
    const { view } = await loginToShell();
    const { container } = view;

    // Step 1 — the shell's landing surface, immediately after a real login.
    // The password was typed into a live form one step ago; it must not have
    // survived into the authenticated DOM.
    await assertNoAxeViolations(container);
    assertNoSecretsRendered(container, SHELL_SECRETS);

    // Step 2 — Providers: a POPULATED table (one connected account with a
    // quota window), which is a different a11y problem from an empty one.
    // The account rows live inside the provider card and only render once it
    // is expanded, so the expansion is part of the journey, not a shortcut.
    await gotoNav("Providers");
    await expandProviderCard("OpenCode Zen");
    // AccountRow identifies an account by email/external id (AccountIdentity),
    // not by display_name — so this is what "the table is populated" looks
    // like on this surface.
    await screen.findByText(new RegExp(SENTINELS.accountExternalID));
    await assertNoAxeViolations(container);
    assertNoSecretsRendered(container, SHELL_SECRETS);

    // Step 3 — Models, reached by navigation rather than by remounting.
    await gotoNav("Models");
    await assertNoAxeViolations(container);
    assertNoSecretsRendered(container, SHELL_SECRETS);

    // Step 4 — Diagnostics: the route-decision table, populated.
    await gotoNav("Diagnostics");
    await assertNoAxeViolations(container);
    assertNoSecretsRendered(container, SHELL_SECRETS);
  }, AXE_JOURNEY_TIMEOUT_MS);

  // One test PER surface rather than one loop over all of them: a loop stops
  // at the first violation and hides the rest, so the report says "a surface
  // is broken" when what a reviewer needs is "which ones".
  it.each([
    "Overview",
    "Providers",
    "Models",
    "Playground",
    "Usage & Analytics",
    "Quota & Limits",
    "Token Health",
    "Diagnostics",
    "Routing",
    "Settings",
    "API Keys",
  ])(
    "reaches %s by navigation with zero axe violations and no secrets rendered",
    async (label) => {
      const { view } = await renderShell();
      const { container } = view;

      await gotoNav(label);
      await assertNoAxeViolations(container);
      assertNoSecretsRendered(container, SHELL_SECRETS);
    },
    AXE_JOURNEY_TIMEOUT_MS,
  );
});

describe("journey — a dialog open over a surface", () => {
  it("keeps the API-keys surface accessible with its create dialog open", async () => {
    const { view } = await renderShell();
    const { container } = view;

    await gotoNav("API Keys");
    await assertNoAxeViolations(container);

    // The create action lives in the shell's page-context bar, not the
    // surface — opening it is a shell+surface interaction, which is exactly
    // the seam a single-component render cannot exercise.
    fireEvent.click(screen.getByRole("button", { name: /new (api )?key/i }));
    await screen.findByRole("dialog");

    // axe over the WHOLE container with the dialog open: this is where
    // aria-modal, the dialog's accessible name, and the backdrop's handling
    // of the rest of the page are actually testable.
    await assertNoAxeViolations(container);
    assertNoSecretsRendered(container, SHELL_SECRETS);
  }, AXE_JOURNEY_TIMEOUT_MS);
});

describe("journey — a form in an error state", () => {
  it("keeps the login screen accessible when the server rejects the credentials", async () => {
    installControlFetch([
      NO_LIVE_SESSION,
      {
        method: "POST",
        path: "/api/control/v1/auth/login",
        status: 401,
        body: apiError("invalid_credentials", "invalid credentials"),
      },
    ]);

    const { container } = render(createElement(AuthGate));

    const field = await screen.findByLabelText(/owner password/i);
    await assertNoAxeViolations(container);

    fireEvent.change(field, { target: { value: SENTINELS.ownerPassword } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    await screen.findByText(/invalid_credentials/i);

    // The error state is a DIFFERENT DOM: a live region appeared, the submit
    // button re-enabled, and the field kept focus. Re-running axe here is the
    // point of the step.
    await assertNoAxeViolations(container);
  });

  it("keeps a surface accessible when its data endpoint fails", async () => {
    const { view } = await renderShell([
      {
        method: "GET",
        path: "/api/control/v1/usage",
        status: 500,
        body: apiError("internal_error", "usage aggregate unavailable", true),
      },
    ]);
    const { container } = view;

    await gotoNav("Usage & Analytics");
    await screen.findByText(/internal_error/i);
    await assertNoAxeViolations(container);
    assertNoSecretsRendered(container, SHELL_SECRETS);
  }, AXE_JOURNEY_TIMEOUT_MS);
});

describe("journey — the raw API key does not survive its one-time reveal", () => {
  it("shows the raw key once, then leaves no trace of it after the dialog closes", async () => {
    const { view } = await renderShell();
    const { container } = view;

    await gotoNav("API Keys");
    fireEvent.click(screen.getByRole("button", { name: /new (api )?key/i }));
    await screen.findByRole("dialog");

    fireEvent.change(screen.getByLabelText(/label/i), { target: { value: "My laptop" } });
    fireEvent.click(screen.getByRole("button", { name: /^create/i }));

    // The one legitimate appearance: 09 §3.11 says the raw key is shown
    // exactly once, so the canary must NOT be asserted here.
    await screen.findByText(SENTINELS.rawVenomKey);

    // Dismiss the reveal, and the key must be gone from the entire DOM —
    // text, every attribute, and every live form value.
    fireEvent.click(screen.getByRole("button", { name: /done|close/i }));
    await waitFor(() => {
      expect(screen.queryByText(SENTINELS.rawVenomKey)).toBeNull();
    });

    assertNoSecretsRendered(container, SHELL_SECRETS);
  }, AXE_JOURNEY_TIMEOUT_MS);
});

describe("journey — connect a client", () => {
  it("reaches the connect-a-client page from Overview's quick start with zero axe violations", async () => {
    const { view } = await renderShell();
    const { container } = view;

    // P6-UI-011's page has no nav entry by design (AppShell's
    // CONNECT_CLIENT_KEY) — Overview's Quick Start is the only way in, which
    // makes this reachable ONLY as a journey, never as a single render.
    await gotoNav("Overview");
    const quickStart = await screen.findByRole("button", { name: /connect a client|quick start/i });
    fireEvent.click(quickStart);

    await screen.findByText(/connect a client/i);
    await assertNoAxeViolations(container);
    assertNoSecretsRendered(container, SHELL_SECRETS);
  }, AXE_JOURNEY_TIMEOUT_MS);
});

describe("journey — nothing bleeds across surfaces", () => {
  it("does not carry the account external id from Providers into Diagnostics", async () => {
    const { view } = await renderShell();
    const { container } = view;

    // Providers renders the external id on purpose — assert that, so the
    // check below is proven to be about SURVIVAL rather than about a value
    // that was never on screen in the first place.
    await gotoNav("Providers");
    await expandProviderCard("OpenCode Zen");
    await screen.findByText(new RegExp(SENTINELS.accountExternalID));

    await gotoNav("Diagnostics");
    await screen.findByTestId(`route-row-${REQUEST_ID}`);

    // The route-diagnostics payload has no field for an account external id
    // (see controlClient's RouteDecisionEntry doc comment). This proves the
    // previous surface's value did not survive the navigation either.
    assertNoSecretsRendered(container, [
      ...SHELL_SECRETS,
      { label: "account external id", value: SENTINELS.accountExternalID },
    ]);
  });
});
