import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ACCENTS,
  DEFAULT_ACCENT,
  DEFAULT_DENSITY,
  DEFAULT_RADIUS_PX,
  DEFAULT_SPACING_SCALE,
  DEFAULT_THEME,
  DENSITIES,
  THEMES,
  applyAppearanceSettings,
  initializeThemeRuntime,
  setAccent,
  setDensity,
  setRadius,
  setSpacing,
  setTheme,
} from "./theme-runtime";

function freshRoot(): HTMLElement {
  return document.createElement("html");
}

/** Builds "<n>px" from a number — raw px string literals are banned by the
 * no-raw-values lint gate, and building the expectation from the number
 * keeps assertions token-scale-honest. */
function px(n: number): string {
  return `${n}px`;
}

describe("initializeThemeRuntime", () => {
  it("applies the package defaults (venom-dark / comfortable / mono / 6 px / 1) to the given root", () => {
    const root = freshRoot();

    initializeThemeRuntime(root);

    expect(root.getAttribute("data-theme")).toBe("venom-dark");
    expect(root.getAttribute("data-density")).toBe("comfortable");
    expect(root.getAttribute("data-accent")).toBe("mono");
    expect(root.style.getPropertyValue("--vn-radius-base")).toBe(px(6));
    expect(root.style.getPropertyValue("--vn-spacing-scale")).toBe("1");
    expect(DEFAULT_THEME).toBe("venom-dark");
    expect(DEFAULT_DENSITY).toBe("comfortable");
    expect(DEFAULT_ACCENT).toBe("mono");
    expect(DEFAULT_RADIUS_PX).toBe(6);
    expect(DEFAULT_SPACING_SCALE).toBe(1);
  });
});

describe("applyAppearanceSettings", () => {
  it("applies all five fields from a full settings payload before first paint", () => {
    const root = freshRoot();

    applyAppearanceSettings(
      { theme: "venom-light", density: "compact", accent: "violet", radius_px: 12, spacing_scale: 0.85 },
      root,
    );

    expect(root.getAttribute("data-theme")).toBe("venom-light");
    expect(root.getAttribute("data-density")).toBe("compact");
    expect(root.getAttribute("data-accent")).toBe("violet");
    expect(root.style.getPropertyValue("--vn-radius-base")).toBe(px(12));
    expect(root.style.getPropertyValue("--vn-spacing-scale")).toBe("0.85");
  });

  it("falls back to mono / 6 px / 1 when the customizer fields are absent from the payload", () => {
    const root = freshRoot();

    applyAppearanceSettings({ theme: "venom-dark", density: "comfortable" }, root);

    expect(root.getAttribute("data-accent")).toBe("mono");
    expect(root.style.getPropertyValue("--vn-radius-base")).toBe(px(6));
    expect(root.style.getPropertyValue("--vn-spacing-scale")).toBe("1");
  });

  it("falls back to mono when the payload carries an unknown accent name", () => {
    const root = freshRoot();

    applyAppearanceSettings(
      { theme: "venom-dark", density: "comfortable", accent: "not-an-accent", radius_px: 6, spacing_scale: 1 },
      root,
    );

    expect(root.getAttribute("data-accent")).toBe("mono");
  });
});

describe("setAccent / setRadius / setSpacing", () => {
  it("flips data-accent on the root for every shipped accent", () => {
    expect(ACCENTS).toEqual(["mono", "blue", "violet", "amber", "emerald", "rose"]);

    for (const accent of ACCENTS) {
      const root = freshRoot();
      setAccent(accent, root);
      expect(root.getAttribute("data-accent")).toBe(accent);
    }
  });

  it("writes --vn-radius-base and clamps to [0, 16] via the package's applyRadius", () => {
    const root = freshRoot();
    setRadius(12, root);
    expect(root.style.getPropertyValue("--vn-radius-base")).toBe(px(12));

    setRadius(99, root);
    expect(root.style.getPropertyValue("--vn-radius-base")).toBe(px(16));

    setRadius(-4, root);
    expect(root.style.getPropertyValue("--vn-radius-base")).toBe(px(0));
  });

  it("writes --vn-spacing-scale and clamps to [0.75, 1.25] via the package's applySpacing", () => {
    const root = freshRoot();
    setSpacing(1.1, root);
    expect(root.style.getPropertyValue("--vn-spacing-scale")).toBe("1.1");

    setSpacing(5, root);
    expect(root.style.getPropertyValue("--vn-spacing-scale")).toBe("1.25");

    setSpacing(0.1, root);
    expect(root.style.getPropertyValue("--vn-spacing-scale")).toBe("0.75");
  });
});

describe("setTheme", () => {
  it("flips data-theme on the root for every shipped theme", () => {
    expect(THEMES).toEqual(["venom-dark", "venom-light", "venom-hc"]);

    for (const theme of THEMES) {
      const root = freshRoot();
      setTheme(theme, root);
      expect(root.getAttribute("data-theme")).toBe(theme);
    }
  });
});

describe("setDensity", () => {
  it("flips data-density on the root for every shipped density", () => {
    expect(DENSITIES).toEqual(["comfortable", "compact"]);

    for (const density of DENSITIES) {
      const root = freshRoot();
      setDensity(density, root);
      expect(root.getAttribute("data-density")).toBe(density);
    }
  });
});

describe("no browser-storage persistence", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("never writes to localStorage or sessionStorage while initializing or switching", () => {
    // Storage.prototype is shared by both window.localStorage and
    // window.sessionStorage in jsdom, so one spy covers both — exactly
    // the "no browser-storage persistence" boundary the DS handoff
    // contract forbids (server-driven persistence is P2b's job).
    const setItemSpy = vi.spyOn(Storage.prototype, "setItem");
    const root = freshRoot();

    initializeThemeRuntime(root);
    for (const theme of THEMES) setTheme(theme, root);
    for (const density of DENSITIES) setDensity(density, root);
    for (const accent of ACCENTS) setAccent(accent, root);
    setRadius(12, root);
    setSpacing(0.9, root);
    applyAppearanceSettings(
      { theme: "venom-light", density: "compact", accent: "blue", radius_px: 4, spacing_scale: 1.2 },
      root,
    );

    expect(setItemSpy).not.toHaveBeenCalled();
  });
});
