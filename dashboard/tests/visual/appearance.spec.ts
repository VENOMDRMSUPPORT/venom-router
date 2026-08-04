// P6-TEST-001 (1b): visual regression across the appearance matrix.
//
// Baselines live HERE, under dashboard/tests/visual/ — never in
// Design_System/. The package's baselines cover the package's own specimens;
// these cover the APP's composed surfaces, and the two drift for entirely
// different reasons. The card is explicit about this split.
//
// The matrix is derived from the design system's exported registries at
// runtime (./matrix.ts) rather than listed here, so a new theme is covered the
// day it ships instead of the day someone remembers to add it. See
// src/test/appearanceMatrix.flow.test.tsx for the test that pins that.

import { expect, test } from "@playwright/test";
import { FULL_SETTINGS, data } from "../e2e/fixtures";
import {
  gotoNav,
  gotoShell,
  installControlStub,
  selectAuthTab,
  waitForVisualsSettled,
} from "../e2e/stub";
import { appearanceMatrix } from "./matrix";

/** The surfaces worth a pixel baseline: the landing surface (shell chrome,
 * nav, cards) and a data-dense one (tables, badges, status colour). Between
 * them they exercise nearly every token a theme or density change touches,
 * without minting a baseline per surface that nobody will ever read. */
const SURFACES = ["Overview", "Providers"];

for (const cell of appearanceMatrix()) {
  test.describe(`${cell.theme} / ${cell.density}`, () => {
    test.beforeEach(async ({ page }) => {
      await installControlStub(page);
      // Drive the appearance through the REAL boot path: the shell restores
      // theme and density from GET /settings before its first content paint
      // (AppShell's restoreSettings). Overriding the payload is therefore a
      // genuine configuration, not a test-only attribute poke — and it proves
      // that path works for every cell in the matrix.
      await page.route("**/api/control/v1/settings", async (route) => {
        if (route.request().method() !== "GET") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify(data(FULL_SETTINGS)),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(data({ ...FULL_SETTINGS, theme: cell.theme, density: cell.density })),
        });
      });
    });

    for (const surface of SURFACES) {
      test(`${surface} matches its baseline`, async ({ page }) => {
        await gotoShell(page);
        await gotoNav(page, surface);

        // The Providers page opens on the OAuth tab, and this fixture's only
        // connected account is key-authenticated — so without this step the
        // baseline photographs an EMPTY state and silently stops covering the
        // account/provider rows, which are the densest thing on the surface
        // (status rails, quota bars, badges, meters) and the whole reason
        // Providers is in SURFACES. Caught while reviewing a regenerated
        // baseline: the empty tab would have become the permanent reference.
        if (surface === "Providers") {
          await selectAuthTab(page, "API Key Providers");
          await expect(page.getByText("OpenCode Zen")).toBeVisible();
        }

        // Assert the appearance actually APPLIED before capturing. Without
        // this the suite would happily photograph the default theme four
        // times and call it a matrix — four identical baselines, all green,
        // covering one cell.
        await expect(page.locator("html")).toHaveAttribute("data-theme", cell.theme);
        await expect(page.locator("html")).toHaveAttribute("data-density", cell.density);

        // Nothing may still be loading when the shutter opens.
        await waitForVisualsSettled(page);

        await expect(page).toHaveScreenshot(`${surface.toLowerCase()}--${cell.snapshotName}.png`, {
          fullPage: true,
        });
      });
    }
  });
}
