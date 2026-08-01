// Wires the dashboard to @venom/design-system's runtime theming (P2a-DS-002,
// 07 §2.3/§3): applies theme/density/accent/radius/spacing purely via the
// package's own applyTheme/applyDensity/applyAccent/applyRadius/applySpacing
// helpers, never hand-rolled attribute writes. Persistence is server-driven
// (GET/PUT /settings) — the settings payload is applied through
// applyAppearanceSettings before the shell's first content paint. Browser
// storage (localStorage/sessionStorage) is never touched here — forbidden by
// the DS handoff contract.
import {
  ACCENTS,
  ACCENT_LABELS,
  DEFAULT_ACCENT,
  DEFAULT_RADIUS_PX,
  DEFAULT_SPACING_SCALE,
  applyAccent,
  applyRadius,
  applySpacing,
  isAccentName,
  type AccentName,
} from "@venom/design-system/customizer";
import {
  applyDensity,
  DEFAULT_DENSITY,
  DENSITIES,
  DENSITY_LABELS,
  type DensityName,
} from "@venom/design-system/density";
import { applyTheme, DEFAULT_THEME, THEMES, THEME_LABELS, type ThemeName } from "@venom/design-system/themes";

export {
  ACCENTS,
  ACCENT_LABELS,
  DEFAULT_ACCENT,
  DEFAULT_DENSITY,
  DEFAULT_RADIUS_PX,
  DEFAULT_SPACING_SCALE,
  DEFAULT_THEME,
  DENSITIES,
  DENSITY_LABELS,
  isAccentName,
  THEMES,
  THEME_LABELS,
};
export type { AccentName, DensityName, ThemeName };

/** The five appearance fields exactly as GET /settings serves them (snake_case
 * wire names). accent/radius_px/spacing_scale are optional so a payload from
 * a server predating migration 00013 still applies cleanly — absent fields
 * resolve to the frozen defaults (mono / 6px / 1.0). */
export interface AppearanceSettingsPayload {
  theme: ThemeName;
  density: DensityName;
  accent?: string;
  radius_px?: number;
  spacing_scale?: number;
}

/** Applies the package defaults (venom-dark / comfortable / mono / 6px / 1)
 * to root — call once at startup, before first paint. */
export function initializeThemeRuntime(root: HTMLElement = document.documentElement): void {
  applyTheme(DEFAULT_THEME, root);
  applyDensity(DEFAULT_DENSITY, root);
  applyAccent(DEFAULT_ACCENT, root);
  applyRadius(DEFAULT_RADIUS_PX, root);
  applySpacing(DEFAULT_SPACING_SCALE, root);
}

/** Applies ALL five settings from a GET /settings payload in one shot —
 * the boot path (and the shell's restore) call this so the saved appearance
 * lands before the content area's first paint. Unknown/absent accent names
 * fall back to mono rather than throwing; radius/spacing clamping is the
 * package helpers' own job. */
export function applyAppearanceSettings(settings: AppearanceSettingsPayload, root: HTMLElement = document.documentElement): void {
  applyTheme(settings.theme, root);
  applyDensity(settings.density, root);
  const accent: AccentName = settings.accent !== undefined && isAccentName(settings.accent) ? settings.accent : DEFAULT_ACCENT;
  applyAccent(accent, root);
  applyRadius(settings.radius_px ?? DEFAULT_RADIUS_PX, root);
  applySpacing(settings.spacing_scale ?? DEFAULT_SPACING_SCALE, root);
}

/** Switches the active theme at runtime via the package's own applyTheme. */
export function setTheme(theme: ThemeName, root: HTMLElement = document.documentElement): void {
  applyTheme(theme, root);
}

/** Switches the active density at runtime via the package's own applyDensity. */
export function setDensity(density: DensityName, root: HTMLElement = document.documentElement): void {
  applyDensity(density, root);
}

/** Switches the active accent at runtime via the package's own applyAccent. */
export function setAccent(accent: AccentName, root: HTMLElement = document.documentElement): void {
  applyAccent(accent, root);
}

/** Sets the base corner radius at runtime via the package's own applyRadius
 * (rounded + clamped to [0, 16] there). */
export function setRadius(px: number, root: HTMLElement = document.documentElement): void {
  applyRadius(px, root);
}

/** Sets the layout spacing multiplier at runtime via the package's own
 * applySpacing (clamped to [0.75, 1.25] there). */
export function setSpacing(scale: number, root: HTMLElement = document.documentElement): void {
  applySpacing(scale, root);
}
