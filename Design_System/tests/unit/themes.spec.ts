import { expect, test } from "@playwright/test";
import { DEFAULT_THEME, THEMES, THEME_LABELS, isThemeName } from "../../src/themes";

test.describe("theme registry", () => {
  test("ships exactly the dark and light themes", () => {
    expect([...THEMES]).toEqual(["venom-dark", "venom-light"]);
    expect(DEFAULT_THEME).toBe("venom-dark");
    expect(THEME_LABELS).toEqual({
      "venom-dark": "Dark",
      "venom-light": "Light",
    });
  });

  test("rejects the retired high-contrast theme", () => {
    expect(isThemeName("venom-dark")).toBe(true);
    expect(isThemeName("venom-light")).toBe(true);
    expect(isThemeName("venom-hc")).toBe(false);
  });
});
