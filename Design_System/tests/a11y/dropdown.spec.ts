import { test, expect } from "@playwright/test";

// DropdownMenu keyboard model — real DOM focus movement, not a painted active index.
test.describe("DropdownMenu", () => {
  async function openMenu(page: import("@playwright/test").Page) {
    await page.goto("/components/overlay/overlay.card.html");
    const trigger = page.getByRole("button", { name: "Account actions" });
    await trigger.focus();
    await page.keyboard.press("ArrowDown");
    await expect(page.getByRole("menu")).toBeVisible();
    return trigger;
  }

  test("ArrowDown on the trigger opens the menu with real focus on the first item", async ({ page }) => {
    await openMenu(page);
    await expect(page.getByRole("menuitem", { name: "Refresh health" })).toBeFocused();
  });

  test("ArrowDown moves focus through items and skips disabled ones", async ({ page }) => {
    await openMenu(page);
    await page.keyboard.press("ArrowDown"); // Run discovery
    await expect(page.getByRole("menuitem", { name: "Run discovery" })).toBeFocused();
    await page.keyboard.press("ArrowDown"); // Reveal credential
    await expect(page.getByRole("menuitem", { name: "Reveal credential" })).toBeFocused();
    await page.keyboard.press("ArrowDown"); // skips disabled "Reauthenticate" -> Stop routing
    await expect(page.getByRole("menuitem", { name: "Stop routing" })).toBeFocused();
  });

  test("Home/End jump to the first/last enabled item", async ({ page }) => {
    await openMenu(page);
    await page.keyboard.press("End");
    await expect(page.getByRole("menuitem", { name: "Disconnect" })).toBeFocused();
    await page.keyboard.press("Home");
    await expect(page.getByRole("menuitem", { name: "Refresh health" })).toBeFocused();
  });

  test("ArrowUp wraps from the first item to the last", async ({ page }) => {
    await openMenu(page);
    await page.keyboard.press("ArrowUp");
    await expect(page.getByRole("menuitem", { name: "Disconnect" })).toBeFocused();
  });

  test("the disabled item is never reachable via keyboard navigation", async ({ page }) => {
    await openMenu(page);
    for (let i = 0; i < 6; i++) await page.keyboard.press("ArrowDown");
    await expect(page.getByRole("menuitem", { name: "Reauthenticate" })).not.toBeFocused();
  });

  test("Enter activates the focused item and closes the menu", async ({ page }) => {
    const trigger = await openMenu(page);
    await page.keyboard.press("Enter");
    await expect(page.getByRole("menu")).toBeHidden();
    await expect(trigger).toBeFocused();
  });

  test("Escape closes the menu and restores focus to the trigger", async ({ page }) => {
    const trigger = await openMenu(page);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu")).toBeHidden();
    await expect(trigger).toBeFocused();
  });
});
