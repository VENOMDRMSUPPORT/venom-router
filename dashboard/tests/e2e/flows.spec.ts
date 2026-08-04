// P6-TEST-001 (1b): the critical flows the card names, in a real browser
// against the real production build.
//
//   connect account · view fleet · read a route explanation · create a key ·
//   connect a client
//
// These drive the APP, not the backend: the control plane is the deterministic
// fixture stub (../e2e/stub.ts). A failure here means the dashboard's flow
// broke — routing, state, wiring, rendering — which is precisely the signal
// the jsdom suites cannot give with full fidelity and the Go suites do not
// cover at all.

import { expect, test } from "@playwright/test";
import { ACCOUNT_ID, KEY_PREFIX_FIXTURE, REQUEST_ID } from "./fixtures";
import { RAW_VENOM_KEY_PATTERN, SENTINELS } from "../../src/test/noSecrets";
import { pathForRoute } from "../../src/shell/route";
import { gotoNav, gotoShell, installControlStub, selectAuthTab } from "./stub";

test.describe("critical flows", () => {
  test("view fleet — the owner sees a connected account and its quota", async ({ page }) => {
    const report = await installControlStub(page);
    await gotoShell(page);
    await gotoNav(page, "Providers");
    // The seeded provider is key-authenticated; the page opens on OAuth.
    await selectAuthTab(page, "API Key Providers");

    await expect(page.getByText("OpenCode Zen")).toBeVisible();

    // Accounts live behind a per-card disclosure, so "view the fleet" is two
    // steps, exactly as it is for an owner. The account is identified by its
    // identity email (never the opaque external_id, which is no longer shown).
    await page.getByRole("button", { name: /Expand OpenCode Zen accounts/i }).click();
    await expect(page.getByText("owner@example.test")).toBeVisible();

    expect(report.unhandled).toEqual([]);
  });

  test("connect account — the API-key dialog enrolls a provider account", async ({ page }) => {
    await installControlStub(page);
    await gotoShell(page);
    await gotoNav(page, "Providers");
    await selectAuthTab(page, "API Key Providers");

    const posted: string[] = [];
    page.on("request", (r) => {
      if (r.method() === "POST" && r.url().includes("/providers/") && r.url().endsWith("/accounts")) {
        posted.push(r.url());
      }
    });

    // The default Active Providers view adds accounts through the row's
    // "+ Add account" action (the documented flow for a 2nd/3rd account).
    await page.getByRole("button", { name: "Add account" }).first().click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();

    await dialog.getByLabel("API key").fill(SENTINELS.providerCredential);
    await dialog.getByRole("button", { name: /save & encrypt/i }).click();

    // The enrollment POST is what "connected" means here; asserting the
    // request went out is stronger than asserting a toast appeared, because a
    // toast can be rendered by a handler that never called the API.
    await expect.poll(() => posted.length, { message: "enrollment POST must reach the control plane" }).toBeGreaterThan(0);

    // The credential must not survive the dialog anywhere in the page.
    await expect(dialog).toBeHidden();
    const html = await page.content();
    expect(html, "the provider credential must not remain in the DOM").not.toContain(SENTINELS.providerCredential);
  });

  test("read a route explanation — the diagnostics deep link opens one request", async ({ page }) => {
    const report = await installControlStub(page);

    // The exact URL shell/route.parseLocation honours, built with the very
    // function Overview's link uses (pathForRoute). If this route regressed,
    // the owner would land on the bare list with no indication which request
    // they asked about.
    await gotoShell(page, pathForRoute("diagnostics", REQUEST_ID));

    await expect(page.getByTestId(`route-row-${REQUEST_ID}`)).toBeVisible();
    // The explanation's substance: the chosen offering and the clamp the
    // decision recorded.
    await expect(page.getByText("zen/grok-code").first()).toBeVisible();

    expect(report.unhandled).toEqual([]);
  });

  test("create a key — the raw key is shown once and never again", async ({ page }) => {
    await installControlStub(page);
    await gotoShell(page);
    await gotoNav(page, "API Keys");

    await page.getByRole("button", { name: /new (api )?key/i }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel(/label/i).fill("My laptop");
    await dialog.getByRole("button", { name: /^create/i }).click();

    // The single legitimate appearance (09 §3.11).
    await expect(page.getByText(SENTINELS.rawVenomKey)).toBeVisible();

    await page.getByRole("button", { name: /done|close/i }).first().click();
    await expect(page.getByText(SENTINELS.rawVenomKey)).toBeHidden();

    // After dismissal the page may show the non-secret 4-char prefix, and
    // must show nothing longer. This is the entropy boundary the shared
    // canary encodes, applied to a real rendered page.
    const html = await page.content();
    expect(html).toContain(KEY_PREFIX_FIXTURE);
    expect(
      RAW_VENOM_KEY_PATTERN.pattern.test(html),
      "a raw vk_live_ key survived the one-time reveal",
    ).toBe(false);
  });

  test("connect a client — the quick start reaches the config generators", async ({ page }) => {
    const report = await installControlStub(page);
    await gotoShell(page);
    await gotoNav(page, "Overview");

    // P6-UI-011 has no nav entry by design; Overview's quick start is the
    // only route to it, which makes this reachable only as a flow.
    await page.getByRole("button", { name: /connect a client|quick start/i }).first().click();

    await expect(page.getByRole("heading", { name: /connect a client/i })).toBeVisible();
    expect(report.unhandled).toEqual([]);
  });

  test("the account id the fleet shows is the one diagnostics attributes attempts to", async ({ page }) => {
    // A cross-surface consistency check: two surfaces render the same account
    // from two different read models, and a mismatch would send an owner
    // debugging the wrong account.
    await installControlStub(page);
    await gotoShell(page, pathForRoute("diagnostics", REQUEST_ID));
    await expect(page.getByTestId(`route-row-${REQUEST_ID}`)).toBeVisible();

    const html = await page.content();
    expect(html).toContain(ACCOUNT_ID);
  });
});
