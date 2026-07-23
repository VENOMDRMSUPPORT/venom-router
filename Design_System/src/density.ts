/**
 * Public entry point: density registry metadata.
 * Density is a token-driven mode switch (`tokens/tokens.density.json` ->
 * `tokens/density.css`), not a layout fork. This module exposes the typed registry.
 */

import type { DensityName } from "../components/actions/DensityToggle";
export type { DensityName };

export const DENSITIES = ["comfortable", "compact"] as const;

export const DEFAULT_DENSITY: DensityName = "comfortable";

export const DENSITY_LABELS: Record<DensityName, string> = {
  comfortable: "Comfortable",
  compact: "Compact",
};

/** Apply a density mode by setting `data-density` on the given root element. */
export function applyDensity(density: DensityName, root: HTMLElement = document.documentElement): void {
  root.setAttribute("data-density", density);
}

export function isDensityName(value: string): value is DensityName {
  return (DENSITIES as readonly string[]).includes(value);
}
