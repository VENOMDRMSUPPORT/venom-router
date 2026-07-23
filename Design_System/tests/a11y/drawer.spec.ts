import { test, expect } from "@playwright/test";

// Drawer/Sheet keyboard + focus contract (overlay-stack.ts), exercised against the live
// demo in components/overlay/overlay.card.html — not just read from documentation.
test.describe("Drawer", () => {
  test("moves focus in on open (first focusable — the close button)", async ({ page }) => {
    await page.goto("/components/overlay/overlay.card.html");
    await page.getByRole("button", { name: "Open drawer" }).click();
    await expect(page.getByRole("dialog", { name: "claude-sonnet-4-5" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Close" })).toBeFocused();
  });

  test("Tab wraps forward from the last focusable back to the first", async ({ page }) => {
    await page.goto("/components/overlay/overlay.card.html");
    await page.getByRole("button", { name: "Open drawer" }).click();
    const close = page.getByRole("button", { name: "Close" });
    const done = page.getByRole("button", { name: "Done" });
    await expect(close).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(done).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(close).toBeFocused();
  });

  test("Shift+Tab wraps backward from the first focusable to the last", async ({ page }) => {
    await page.goto("/components/overlay/overlay.card.html");
    await page.getByRole("button", { name: "Open drawer" }).click();
    const close = page.getByRole("button", { name: "Close" });
    const done = page.getByRole("button", { name: "Done" });
    await expect(close).toBeFocused();
    await page.keyboard.press("Shift+Tab");
    await expect(done).toBeFocused();
  });

  test("Escape closes and restores focus to the exact opener", async ({ page }) => {
    await page.goto("/components/overlay/overlay.card.html");
    const opener = page.getByRole("button", { name: "Open drawer" });
    await opener.click();
    await expect(page.getByRole("dialog", { name: "claude-sonnet-4-5" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog", { name: "claude-sonnet-4-5" })).not.toBeVisible();
    await expect(opener).toBeFocused();
  });

  test("honors initialFocusRef instead of the first focusable element", async ({ page }) => {
    await page.goto("/components/overlay/overlay.card.html");
    await page.getByRole("button", { name: "Open sheet (initial focus)" }).click();
    await expect(page.getByRole("dialog", { name: "Edit account" })).toBeVisible();
    await expect(page.getByLabel("Display name")).toBeFocused();
  });

  test("Sheet alias has identical focus-trap + restore behavior", async ({ page }) => {
    await page.goto("/components/overlay/overlay.card.html");
    const opener = page.getByRole("button", { name: "Open Sheet (alias)" });
    await opener.click();
    await expect(page.getByRole("dialog", { name: "Sheet alias" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Close" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog", { name: "Sheet alias" })).not.toBeVisible();
    await expect(opener).toBeFocused();
  });
});
