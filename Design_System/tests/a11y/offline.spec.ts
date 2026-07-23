import { test, expect } from "@playwright/test";
import { ALL_CARD_PAGES, STATE_MATRICES, COMPOSITIONS } from "../pages";

// Offline verification: block every cross-origin request (anything not served by the
// local dev/preview server) and confirm representative surfaces still load and render.
// A CDN dependency would show up here as a blocked request the page actually needed.
test.describe("Offline (network disabled except localhost)", () => {
  const SAMPLE = [...ALL_CARD_PAGES.slice(0, 3), ...STATE_MATRICES.slice(0, 2), ...COMPOSITIONS];

  for (const url of SAMPLE) {
    test(`${url} loads with all cross-origin requests blocked`, async ({ page, baseURL }) => {
      const blocked: string[] = [];
      await page.route("**/*", (route) => {
        const reqUrl = route.request().url();
        if (baseURL && reqUrl.startsWith(baseURL)) return route.continue();
        if (reqUrl.startsWith("data:") || reqUrl.startsWith("blob:")) return route.continue();
        blocked.push(reqUrl);
        return route.abort("blockedbyclient");
      });

      const consoleErrors: string[] = [];
      page.on("console", (msg) => {
        if (msg.type() === "error") consoleErrors.push(msg.text());
      });

      await page.goto(url);
      await page.waitForSelector("#root, body");
      // The page must have rendered real content, not an empty/error shell.
      const bodyText = await page.locator("body").innerText();
      expect(bodyText.trim().length).toBeGreaterThan(0);
      expect(blocked, "page attempted a cross-origin request while network was disabled").toEqual([]);
    });
  }
});
