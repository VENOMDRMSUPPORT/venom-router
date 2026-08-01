import { test, expect } from "@playwright/test";
import {
  ACCENTS,
  ACCENT_LABELS,
  DEFAULT_ACCENT,
  DEFAULT_RADIUS_PX,
  DEFAULT_SPACING_SCALE,
  RADIUS_BASE_PROPERTY,
  SPACING_SCALE_PROPERTY,
  applyAccent,
  applyRadius,
  applySpacing,
  isAccentName,
} from "../../src/customizer";

// Unit tests for the customizer entry point (src/customizer.ts). The DS has no separate
// unit-test framework — Playwright is the one test runner (see package.json) — and these
// functions are plain DOM setters, so they are exercised against a minimal recording root
// rather than a browser page. Attribute/property EFFECTS are asserted on what was written.
function recordingRoot() {
  const attributes: Record<string, string> = {};
  const properties: Record<string, string> = {};
  const root = {
    setAttribute: (name: string, value: string) => {
      attributes[name] = value;
    },
    style: {
      setProperty: (name: string, value: string) => {
        properties[name] = value;
      },
    },
  } as unknown as HTMLElement;
  return { root, attributes, properties };
}

test.describe("accent registry", () => {
  test("canonical registry: six accents, mono first and default, every accent labeled", () => {
    expect([...ACCENTS]).toEqual(["mono", "blue", "violet", "amber", "emerald", "rose"]);
    expect(DEFAULT_ACCENT).toBe("mono");
    for (const accent of ACCENTS) expect(ACCENT_LABELS[accent]).toBeTruthy();
  });

  test("isAccentName accepts every registered accent", () => {
    for (const accent of ACCENTS) expect(isAccentName(accent)).toBe(true);
  });

  test("isAccentName rejects unknown names", () => {
    for (const bad of ["teal", "", "MONO", "Blue", "mono ", "rose-dark"]) {
      expect(isAccentName(bad), `"${bad}" must be rejected`).toBe(false);
    }
  });

  test("applyAccent sets data-accent on the given root", () => {
    for (const accent of ACCENTS) {
      const { root, attributes } = recordingRoot();
      applyAccent(accent, root);
      expect(attributes["data-accent"]).toBe(accent);
    }
  });
});

test.describe("applyRadius", () => {
  test("writes --vn-radius-base as integer px", () => {
    const { root, properties } = recordingRoot();
    applyRadius(DEFAULT_RADIUS_PX, root);
    expect(properties[RADIUS_BASE_PROPERTY]).toBe("6px");
  });

  test("clamps to [0, 16]", () => {
    const cases: Array<[number, string]> = [
      [-5, "0px"],
      [0, "0px"],
      [16, "16px"],
      [20, "16px"],
      [Number.POSITIVE_INFINITY, "16px"],
      [Number.NEGATIVE_INFINITY, "0px"],
    ];
    for (const [input, expected] of cases) {
      const { root, properties } = recordingRoot();
      applyRadius(input, root);
      expect(properties[RADIUS_BASE_PROPERTY], `applyRadius(${input})`).toBe(expected);
    }
  });

  test("rounds fractional input to integer px", () => {
    const cases: Array<[number, string]> = [
      [7.4, "7px"],
      [7.5, "8px"],
      [0.4, "0px"],
      [15.9, "16px"],
    ];
    for (const [input, expected] of cases) {
      const { root, properties } = recordingRoot();
      applyRadius(input, root);
      expect(properties[RADIUS_BASE_PROPERTY], `applyRadius(${input})`).toBe(expected);
    }
  });

  test("NaN falls back to the 6px default", () => {
    const { root, properties } = recordingRoot();
    applyRadius(Number.NaN, root);
    expect(properties[RADIUS_BASE_PROPERTY]).toBe("6px");
  });
});

test.describe("applySpacing", () => {
  test("writes --vn-spacing-scale as a unitless number", () => {
    const { root, properties } = recordingRoot();
    applySpacing(DEFAULT_SPACING_SCALE, root);
    expect(properties[SPACING_SCALE_PROPERTY]).toBe("1");
  });

  test("clamps to [0.75, 1.25] and passes in-range values through", () => {
    const cases: Array<[number, string]> = [
      [0.5, "0.75"],
      [0.75, "0.75"],
      [0.8, "0.8"],
      [1.1, "1.1"],
      [1.25, "1.25"],
      [2, "1.25"],
      [Number.POSITIVE_INFINITY, "1.25"],
      [Number.NEGATIVE_INFINITY, "0.75"],
    ];
    for (const [input, expected] of cases) {
      const { root, properties } = recordingRoot();
      applySpacing(input, root);
      expect(properties[SPACING_SCALE_PROPERTY], `applySpacing(${input})`).toBe(expected);
    }
  });

  test("NaN falls back to the scale-1 default", () => {
    const { root, properties } = recordingRoot();
    applySpacing(Number.NaN, root);
    expect(properties[SPACING_SCALE_PROPERTY]).toBe("1");
  });
});
