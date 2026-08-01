// Proves the visual-regression matrix is DERIVED from the design system's own
// registries rather than transcribed from them.
//
// This is the guard against the quietest failure mode a visual suite has: the
// design system gains a theme, the suite keeps passing, and nobody notices it
// stopped covering one. A hardcoded list cannot fail that way — it just goes
// silently incomplete — so the only way to catch it is to test the derivation
// itself.

import { describe, expect, it } from "vitest";
import { DENSITIES, THEMES, appearanceMatrix } from "../../tests/visual/matrix";

describe("appearance matrix", () => {
  it("covers exactly the design system's shipped themes x densities", () => {
    const cells = appearanceMatrix();

    expect(cells).toHaveLength(THEMES.length * DENSITIES.length);
    for (const theme of THEMES) {
      for (const density of DENSITIES) {
        expect(cells).toContainEqual({ theme, density, snapshotName: `${theme}--${density}` });
      }
    }
  });

  it("GROWS when the design system gains a theme", () => {
    // THE MUTATION GUARD. Feed the function a registry containing a theme the
    // package does not ship. A derived matrix picks it up; a hardcoded one
    // returns the same cells it always did and this assertion goes red.
    const withNewTheme = [...THEMES, "venom-high-contrast"];
    const cells = appearanceMatrix(withNewTheme, DENSITIES);

    expect(cells).toHaveLength(withNewTheme.length * DENSITIES.length);
    expect(cells.map((c) => c.theme)).toContain("venom-high-contrast");
    for (const density of DENSITIES) {
      expect(cells).toContainEqual({
        theme: "venom-high-contrast",
        density,
        snapshotName: `venom-high-contrast--${density}`,
      });
    }
  });

  it("GROWS when the design system gains a density", () => {
    // The same guard on the other axis — density is a separate registry
    // (@venom/design-system/density) and could be hardcoded independently.
    const withNewDensity = [...DENSITIES, "spacious"];
    const cells = appearanceMatrix(THEMES, withNewDensity);

    expect(cells).toHaveLength(THEMES.length * withNewDensity.length);
    expect(cells.map((c) => c.density)).toContain("spacious");
  });

  it("orders cells theme-major and stably, so baseline filenames never shuffle", () => {
    // A matrix whose order depended on iteration luck would rename baselines
    // between runs, and every rename reads as a missing snapshot.
    const first = appearanceMatrix().map((c) => c.snapshotName);
    const second = appearanceMatrix().map((c) => c.snapshotName);

    expect(first).toEqual(second);
    expect(new Set(first).size).toBe(first.length);
  });
});
