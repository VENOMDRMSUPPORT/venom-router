import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { CONSOLE_SCREENS, THEMES, setThemeDensity } from "../pages";

const AXE_TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];
const CONSOLE_URL = "/ui_kits/venom-console/index.html";

// All 17 representative composition surfaces at the default theme...
test.describe("Console — all 17 surfaces (venom-dark)", () => {
  for (const screen of CONSOLE_SCREENS) {
    test(screen, async ({ page }) => {
      await page.goto(CONSOLE_URL + "#" + screen);
      await page.waitForSelector('[data-screen-label="' + screen + '"]');
      const results = await new AxeBuilder({ page }).withTags(AXE_TAGS).analyze();
      expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
    });
  }
});

// ...and a representative subset across all three themes (color/contrast differs per theme;
// running the full 17x3 matrix is redundant once the shared components are already
// axe-clean per-card — this catches composition-level regressions, e.g. a page that
// hardcodes a color the theme can't override).
const REPRESENTATIVE = ["overview", "providers", "diagnostics", "settings"];
test.describe("Console — representative surfaces x 3 themes", () => {
  for (const screen of REPRESENTATIVE) {
    for (const theme of THEMES) {
      test(`${screen} @ ${theme}`, async ({ page }) => {
        await page.goto(CONSOLE_URL + "#" + screen);
        await page.waitForSelector('[data-screen-label="' + screen + '"]');
        await setThemeDensity(page, theme);
        const results = await new AxeBuilder({ page }).withTags(AXE_TAGS).analyze();
        expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
      });
    }
  }
});
