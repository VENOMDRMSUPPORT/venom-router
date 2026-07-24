// Wires the dashboard to @venom/design-system's runtime theming (P2a-DS-002,
// 07 §2.3/§3): applies theme/density purely via the package's own
// applyTheme/applyDensity helpers, never hand-rolled attribute writes.
// Persistence is server-driven (PUT /settings, P2b) — until then the
// package defaults are the only source of truth. Browser storage
// (localStorage/sessionStorage) is never touched here — forbidden by the
// DS handoff contract.
import { applyDensity, DEFAULT_DENSITY, DENSITIES, type DensityName } from "@venom/design-system/density";
import { applyTheme, DEFAULT_THEME, THEMES, type ThemeName } from "@venom/design-system/themes";

export { DEFAULT_DENSITY, DEFAULT_THEME, DENSITIES, THEMES };
export type { DensityName, ThemeName };

/** Applies the package defaults (venom-dark / comfortable) to root — call once at startup, before first paint. */
export function initializeThemeRuntime(root: HTMLElement = document.documentElement): void {
  applyTheme(DEFAULT_THEME, root);
  applyDensity(DEFAULT_DENSITY, root);
}

/** Switches the active theme at runtime via the package's own applyTheme. */
export function setTheme(theme: ThemeName, root: HTMLElement = document.documentElement): void {
  applyTheme(theme, root);
}

/** Switches the active density at runtime via the package's own applyDensity. */
export function setDensity(density: DensityName, root: HTMLElement = document.documentElement): void {
  applyDensity(density, root);
}
