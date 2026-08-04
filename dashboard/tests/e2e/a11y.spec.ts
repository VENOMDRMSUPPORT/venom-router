// P6-TEST-001 (1b): accessibility in a REAL browser.
//
// WHY THIS SUITE EXISTS AT ALL, given src/test/journey.flow.test.tsx already
// runs axe over the same surfaces: jsdom has no layout and no paint engine, so
// axe-core's `color-contrast` rule cannot run there and src/test/axe.ts
// disables it. Contrast is not a minor extra — it is one of the most commonly
// failed WCAG criteria and the single most likely thing a theme change breaks.
// A real browser is the only place it can be evaluated, so this is the suite
// that evaluates it.
//
// Everything else axe checks runs in both layers, deliberately: the jsdom one
// is fast and blocks every `npm test`, this one is authoritative.

import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { gotoNav, gotoShell, installControlStub } from "./stub";
import { REQUEST_ID } from "./fixtures";
import { pathForRoute } from "../../src/shell/route";
import { NAV } from "../../src/shell/nav";

/** Every primary-nav destination, by its visible label — DERIVED from the
 * nav registry, never restated. A hardcoded copy of these labels silently
 * stopped covering a surface the moment one was renamed ("Models" →
 * "Live Models"): the label no longer matched any link, so the test failed
 * on a 60s locator timeout instead of on an accessibility violation. Reading
 * NAV also keeps this suite TOTAL — a newly added destination gets an a11y
 * test automatically rather than being silently uncovered. */
const SURFACES = NAV.map((item) => item.label);

/**
 * Runs axe over the whole page and asserts two separate things:
 *
 *   1. that `color-contrast` ACTUALLY EXECUTED, and
 *   2. that there are no violations.
 *
 * The first assertion is not redundant. axe reports a disabled rule by simply
 * omitting it, so a suite that only checked `violations` would stay green
 * forever if someone disabled contrast — which is exactly the mutation this
 * check has to survive. A rule that ran appears in passes, violations, or
 * incomplete; a rule that was disabled appears in none of them.
 */
async function expectAccessible(page: import("@playwright/test").Page, label: string): Promise<void> {
  const results = await new AxeBuilder({ page }).analyze();

  const contrastRan = [...results.passes, ...results.violations, ...results.incomplete].some(
    (r) => r.id === "color-contrast",
  );
  expect(
    contrastRan,
    `color-contrast did not execute on ${label}. This suite exists to run that rule in a real ` +
      `browser — if it is disabled or skipped, the suite is claiming coverage it does not have.`,
  ).toBe(true);

  const summary = results.violations.map((v) => `[${v.id}] ${v.help} -> ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
  expect(summary, `axe violations on ${label}`).toEqual([]);
}

test.describe("accessibility in a real browser (color-contrast included)", () => {
  for (const label of SURFACES) {
    test(`${label} has zero axe violations with color-contrast evaluated`, async ({ page }) => {
      const report = await installControlStub(page);
      await gotoShell(page);
      await gotoNav(page, label);
      await expectAccessible(page, label);
      expect(report.unhandled, "every endpoint the surface called must have a fixture").toEqual([]);
    });
  }

  test("the route-explanation deep link has zero axe violations", async ({ page }) => {
    const report = await installControlStub(page);
    // The one deep link the shell honours (shell/route.parseLocation) — a
    // different entry path than clicking through the nav, and therefore a
    // different initial DOM. The URL comes from the app's own pathForRoute so
    // this test cannot drift from the mapping the shell parses.
    await gotoShell(page, pathForRoute("diagnostics", REQUEST_ID));
    await page.getByTestId(`route-row-${REQUEST_ID}`).waitFor();
    await expectAccessible(page, "diagnostics deep link");
    expect(report.unhandled).toEqual([]);
  });

  test("the API-key create dialog has zero axe violations while open", async ({ page }) => {
    await installControlStub(page);
    await gotoShell(page);
    await gotoNav(page, "API Keys");
    await page.getByRole("button", { name: /new (api )?key/i }).click();
    await page.getByRole("dialog").waitFor();
    await expectAccessible(page, "API keys create dialog");
  });

  test("the login screen has zero axe violations", async ({ page }) => {
    await installControlStub(page);
    // Force the unauthenticated path: no live session means AuthGate renders
    // LoginScreen, which the authenticated navigation above never reaches.
    await page.route("**/api/control/v1/auth/session", (route) =>
      route.fulfill({
        status: 401,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "session_expired", message: "no live session", request_id: "stub", retryable: false },
        }),
      }),
    );
    await page.goto("/");
    await page.getByLabel(/owner password/i).waitFor();
    await page.evaluate(() => document.fonts.ready);
    await expectAccessible(page, "login");
  });
});
