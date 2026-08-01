import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { PRIMITIVE_CARDS, DOMAIN_CARDS, STATE_MATRICES, FOUNDATIONS, THEMES, setThemeDensity } from "../pages";

// Every primitive/domain card and every mandated state matrix, across both themes —
// the "executed automated tests" evidence for the accessibility report. axe-core's
// "best-practice" rules are excluded (they're advisory, not WCAG failures); everything
// else must come back clean.
const AXE_TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

function runAxeSuite(label: string, pages: string[]) {
  test.describe(label, () => {
    for (const url of pages) {
      for (const theme of THEMES) {
        test(`${url} @ ${theme}`, async ({ page }) => {
          await page.goto(url);
          await setThemeDensity(page, theme);
          const results = await new AxeBuilder({ page }).withTags(AXE_TAGS).analyze();
          expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
        });
      }
    }
  });
}

runAxeSuite("Primitive cards", PRIMITIVE_CARDS);
runAxeSuite("Domain cards", DOMAIN_CARDS);
runAxeSuite("State matrices", STATE_MATRICES);

// Foundations are static specimens (color/type/spacing swatches) — one pass at the
// default theme is representative; several already render both themes side by side.
test.describe("Foundations", () => {
  for (const url of FOUNDATIONS) {
    test(url, async ({ page }) => {
      await page.goto(url);
      const results = await new AxeBuilder({ page }).withTags(AXE_TAGS).analyze();
      expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
    });
  }
});
