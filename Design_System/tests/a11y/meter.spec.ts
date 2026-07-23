import { test, expect } from "@playwright/test";

// Meter's unknown/unavailable branches must never expose a fake numeric value, must keep
// an accessible name, and must be structurally distinguishable from a real (possibly
// zero) numeric meter and from each other.
test.describe("Meter — unknown-state semantics", () => {
  test("unknown renders role=img with an accessible name containing 'Unknown', no aria-valuenow", async ({ page }) => {
    await page.goto("/components/feedback/feedback.card.html");
    const unknown = page.getByRole("img", { name: "Provider quota: Unknown" });
    await expect(unknown).toBeVisible();
    await expect(unknown).not.toHaveAttribute("aria-valuenow");
    await expect(unknown).toHaveClass(/is-unknown/);
  });

  test("unavailable renders role=img with an accessible name containing 'Unavailable', distinct from unknown", async ({ page }) => {
    await page.goto("/components/feedback/feedback.card.html");
    const unavailable = page.getByRole("img", { name: "Not metered: Unavailable" });
    await expect(unavailable).toBeVisible();
    await expect(unavailable).not.toHaveAttribute("aria-valuenow");
    await expect(unavailable).toHaveClass(/is-unavailable/);
    await expect(unavailable).not.toHaveClass(/is-unknown/);
  });

  test("a real numeric meter (including zero/exhausted) stays role=meter with a real aria-valuenow", async ({ page }) => {
    await page.goto("/components/feedback/feedback.card.html");
    const known = page.getByRole("meter", { name: "5-hour window" });
    await expect(known).toBeVisible();
    await expect(known).toHaveAttribute("aria-valuenow", "82");
  });
});
