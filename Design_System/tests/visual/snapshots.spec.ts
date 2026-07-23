import { test, expect } from "@playwright/test";
import { THEMES, setThemeDensity } from "../pages";

// Deterministic screenshot + comparison workflow. Fixed viewport (playwright.config.ts),
// fixed fixtures (every card/state page uses static mock data — no Date.now()/Math.random()
// in any component or demo), stored baselines under tests/visual/snapshots.spec.ts-snapshots/.
// Update baselines deliberately with `npm run test:visual:update`; any other diff fails CI
// with a non-zero exit code and a diff image alongside the report.
const REPRESENTATIVE_PAGES = [
  { name: "actions", url: "/components/actions/actions.card.html" },
  { name: "forms", url: "/components/forms/forms.card.html" },
  { name: "feedback", url: "/components/feedback/feedback.card.html" },
  { name: "overlay", url: "/components/overlay/overlay.card.html" },
  { name: "quota", url: "/components/domain-quota/quota.card.html" },
  { name: "certification-states", url: "/states/certification.html" },
  { name: "routing-outcomes", url: "/states/routing-outcomes.html" },
];

test.describe("Visual regression — representative pages x 3 themes", () => {
  for (const { name, url } of REPRESENTATIVE_PAGES) {
    for (const theme of THEMES) {
      test(`${name} @ ${theme}`, async ({ page }) => {
        await page.goto(url);
        await setThemeDensity(page, theme);
        await expect(page).toHaveScreenshot(`${name}-${theme}.png`, { fullPage: true });
      });
    }
  }
});

// Density parity for a couple of representative surfaces — spacing/control heights only,
// layouts never fork.
const DENSITY_SAMPLES = [
  { name: "forms", url: "/components/forms/forms.card.html" },
  { name: "quota", url: "/components/domain-quota/quota.card.html" },
];

test.describe("Visual regression — density (comfortable vs compact)", () => {
  for (const { name, url } of DENSITY_SAMPLES) {
    for (const density of ["comfortable", "compact"] as const) {
      test(`${name} @ ${density}`, async ({ page }) => {
        await page.goto(url);
        await setThemeDensity(page, "venom-dark", density);
        await expect(page).toHaveScreenshot(`${name}-${density}.png`, { fullPage: true });
      });
    }
  }
});
