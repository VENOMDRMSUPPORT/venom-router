import { afterEach, describe, expect, it, vi } from "vitest";
import {
  DEFAULT_DENSITY,
  DEFAULT_THEME,
  DENSITIES,
  THEMES,
  initializeThemeRuntime,
  setDensity,
  setTheme,
} from "./theme-runtime";

function freshRoot(): HTMLElement {
  return document.createElement("html");
}

describe("initializeThemeRuntime", () => {
  it("applies the package defaults (venom-dark / comfortable) to the given root", () => {
    const root = freshRoot();

    initializeThemeRuntime(root);

    expect(root.getAttribute("data-theme")).toBe("venom-dark");
    expect(root.getAttribute("data-density")).toBe("comfortable");
    expect(DEFAULT_THEME).toBe("venom-dark");
    expect(DEFAULT_DENSITY).toBe("comfortable");
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

    expect(setItemSpy).not.toHaveBeenCalled();
  });
});
