// The deterministic control-plane stub the browser suites run against.
//
// The suites drive the REAL built dashboard (vite preview serves dist/), so
// everything above the network boundary is production code. Only the control
// plane is replaced, by intercepting `/api/control/v1/*` and `/v1/*` with the
// shared fixture table in ./fixtures.ts.
//
// That boundary is the point: these suites prove the APP's flows, a11y and
// rendering. The backend's behaviour is proven by the Go suites, and mixing
// the two would make a browser test fail for reasons that have nothing to do
// with the browser.

import { expect, type Page, type Route } from "@playwright/test";
import { CONTROL_ROUTES, FROZEN_NOW_MS, matchRoute } from "./fixtures";

/** Requests the stub refused to answer. A spec asserts this is empty, so an
 * endpoint nobody stubbed fails the test LOUDLY instead of leaving a surface
 * rendering its empty state while axe happily passes over nothing. */
export interface StubReport {
  readonly unhandled: string[];
}

/**
 * Installs the control-plane stub plus every determinism control the visual
 * baselines depend on. Call before `page.goto`.
 *
 * Determinism, in the order it matters:
 *   1. a FIXED clock, so any "5 minutes ago" renders identically every run;
 *   2. fixture bodies with fixed ids and timestamps (see fixtures.ts);
 *   3. animations and transitions disabled, so a screenshot never catches a
 *      component mid-tween;
 *   4. caret hidden, so a focused input does not blink one pixel-diff at a
 *      time.
 */
export async function installControlStub(page: Page): Promise<StubReport> {
  const unhandled: string[] = [];

  // A fixed clock must be installed BEFORE any app code runs.
  await page.clock.install({ time: new Date(FROZEN_NOW_MS) });

  await page.addStyleTag({
    content: `
      *, *::before, *::after {
        animation-duration: 0s !important;
        animation-delay: 0s !important;
        transition-duration: 0s !important;
        transition-delay: 0s !important;
        scroll-behavior: auto !important;
      }
      * { caret-color: transparent !important; }
    `,
  }).catch(() => {
    // addStyleTag before the first navigation throws; the init script below
    // is what actually applies for the initial load. Swallowing here keeps
    // the helper callable in either order.
  });

  await page.addInitScript(() => {
    const style = document.createElement("style");
    style.textContent =
      "*,*::before,*::after{animation-duration:0s !important;animation-delay:0s !important;" +
      "transition-duration:0s !important;transition-delay:0s !important;scroll-behavior:auto !important}" +
      "*{caret-color:transparent !important}";
    document.documentElement.appendChild(style);
  });

  await page.route("**/api/control/v1/**", async (route: Route) => {
    const request = route.request();
    const { pathname } = new URL(request.url());
    const stub = matchRoute(request.method(), pathname, CONTROL_ROUTES);

    if (stub === undefined) {
      unhandled.push(`${request.method()} ${pathname}`);
      // Fail CLOSED with a typed envelope rather than aborting: the app then
      // renders its real error state, which is itself worth being accessible,
      // and the spec's assertion on `unhandled` is what fails the run.
      await route.fulfill({
        status: 501,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "fixture_missing",
            message: `no fixture for ${request.method()} ${pathname}`,
            request_id: "stub",
            retryable: false,
          },
        }),
      });
      return;
    }

    await route.fulfill({
      status: stub.status,
      contentType: "application/json",
      body: JSON.stringify(stub.body),
    });
  });

  // The Playground posts to the public data plane, which is not a control
  // route. It gets an OpenAI-shaped completion so the surface has something
  // real to render.
  await page.route("**/v1/chat/completions", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        id: "chatcmpl-fixture-0001",
        object: "chat.completion",
        created: Math.floor(FROZEN_NOW_MS / 1000),
        model: "venom/pro",
        choices: [
          { index: 0, message: { role: "assistant", content: "Fixture reply." }, finish_reason: "stop" },
        ],
        usage: { prompt_tokens: 8, completion_tokens: 3, total_tokens: 11 },
      }),
    });
  });

  return { unhandled };
}

/**
 * Navigates to the app and waits for the authenticated shell.
 *
 * `path` is a real URL PATH (the shell became path-routed in the SPA-routing
 * change); callers that want a specific page pass `pathForRoute(...)` from
 * src/shell/route so a test can never drift from the mapping the app itself
 * uses — the earlier hash form silently stopped resolving and three specs
 * failed on a missing row rather than on the routing change that caused it.
 *
 * Waits on RENDERED STATE (the primary nav), never on a timer — there is no
 * `waitForTimeout` in this suite, by policy, because a sleep long enough to
 * be reliable on a loaded CI host is a sleep that makes every run slower and
 * still occasionally flakes.
 */
export async function gotoShell(page: Page, path = "/"): Promise<void> {
  await page.goto(path);
  await page.getByRole("navigation", { name: /primary/i }).waitFor();
  // Webfonts must have settled before any screenshot, or the first capture
  // races the fallback face and every baseline is a coin flip.
  await page.evaluate(() => document.fonts.ready);
}

/**
 * Waits until nothing left on the page can still change its layout.
 *
 * Fonts alone are not enough: provider cards render logo images, and an
 * <img> that is still decoding occupies a different box than one that has
 * loaded. That shifts the grid below it, which is how a full-page screenshot
 * ends up ~1% different from its own baseline on the very next run.
 *
 * Both waits are on STATE (fonts.ready, img.complete), never on a duration —
 * a sleep long enough to be reliable here would slow every capture and still
 * flake on a loaded CI host.
 */
export async function waitForVisualsSettled(page: Page): Promise<void> {
  await page.evaluate(() => document.fonts.ready);
  await page.waitForFunction(() => Array.from(document.images).every((img) => img.complete));
}

/** Clicks a primary-nav destination and waits for it to become current. */
export async function gotoNav(page: Page, label: string | RegExp): Promise<void> {
  const nav = page.getByRole("navigation", { name: /primary/i });
  const link = nav.getByRole("link", { name: label, exact: typeof label === "string" });
  await link.click();
  await link.and(page.locator("[aria-current='page']")).waitFor();
}

/** Selects one of the Providers page's two auth tabs and waits for it to take
 * effect. The page opens on "OAuth Providers", so a flow that works with a
 * key-authenticated provider has to click across first — exactly as an owner
 * does. */
export async function selectAuthTab(
  page: Page,
  label: "OAuth Providers" | "API Key Providers",
): Promise<void> {
  const tab = page.getByRole("button", { name: label, exact: true });
  await tab.click();
  await expect(tab).toHaveAttribute("aria-pressed", "true");
}
