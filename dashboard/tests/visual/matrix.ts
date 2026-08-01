// The appearance matrix the visual-regression suite iterates.
//
// DERIVED, NEVER LISTED. The themes come from @venom/design-system/themes'
// exported THEMES and the densities from @venom/design-system/density's
// exported DENSITIES — the same registries the app itself drives its theme and
// density switches from (see src/theme-runtime.ts, which re-exports both).
//
// Why this matters enough to be its own module: a hardcoded `["venom-dark",
// "venom-light"]` here would not fail when the design system gains a third
// theme. It would keep passing, having quietly stopped covering the new one —
// a suite that reports green while its coverage shrinks. The design system is
// under active development in a parallel workstream right now, which makes
// that failure mode a live risk rather than a hypothetical one.
//
// The parameters exist so the derivation itself is testable: pass a registry
// containing a theme the design system does not ship, and the matrix must
// grow. See src/test/appearanceMatrix.flow.test.tsx.

import { DENSITIES } from "@venom/design-system/density";
import { THEMES } from "@venom/design-system/themes";

export interface AppearanceCell {
  readonly theme: string;
  readonly density: string;
  /** Stable, filesystem-safe name for this cell's screenshot baseline. */
  readonly snapshotName: string;
}

/**
 * The full cross product of themes and densities, in a stable order
 * (theme-major) so baseline filenames never shuffle between runs.
 *
 * Defaults to the design system's own registries. Both parameters are
 * `readonly string[]` rather than the packages' literal union types, because
 * the point of the test that exercises this function is to pass a theme name
 * the union does not contain.
 */
export function appearanceMatrix(
  themes: readonly string[] = THEMES,
  densities: readonly string[] = DENSITIES,
): AppearanceCell[] {
  const cells: AppearanceCell[] = [];
  for (const theme of themes) {
    for (const density of densities) {
      cells.push({ theme, density, snapshotName: `${theme}--${density}` });
    }
  }
  return cells;
}

/** The registries as the design system currently ships them, re-exported so a
 * spec can assert against the source of truth without importing the package
 * twice under two different names. */
export { DENSITIES, THEMES };
