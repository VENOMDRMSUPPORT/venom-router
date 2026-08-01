/**
 * Public entry point: customizer registry metadata (accent, radius, spacing).
 * Mirrors `applyTheme` (src/themes.ts) and `applyDensity` (src/density.ts).
 *
 * - Accents are delivered as generated CSS override blocks
 *   (`tokens/accents.css`, compiled from `tokens/tokens.accents.json` by
 *   `validation/build-tokens.cjs`) keyed off `data-accent` on the root
 *   element. `data-accent` absent or `"mono"` keeps the base monochrome
 *   themes.
 * - Radius routes every radius token through the `--vn-radius-base` custom
 *   property (default 6px); spacing multiplies the density-resolved spacing
 *   values through `--vn-spacing-scale` (default 1).
 *
 * Persistence is the consuming app's responsibility (server-side settings,
 * never browser storage — see SKILL.md).
 */

/** The six accent identities, in the canonical order. Mono is the default (no override block — the base themes ARE the mono identity). */
export const ACCENTS = ["mono", "blue", "violet", "amber", "emerald", "rose"] as const;

export type AccentName = (typeof ACCENTS)[number];

export const DEFAULT_ACCENT: AccentName = "mono";

export const ACCENT_LABELS: Record<AccentName, string> = {
  mono: "Mono",
  blue: "Blue",
  violet: "Violet",
  amber: "Amber",
  emerald: "Emerald",
  rose: "Rose",
};

/** The custom property `applyRadius` writes; every radius token routes through it. */
export const RADIUS_BASE_PROPERTY = "--vn-radius-base";
/** The custom property `applySpacing` writes; density-resolved spacing values multiply by it. */
export const SPACING_SCALE_PROPERTY = "--vn-spacing-scale";

export const DEFAULT_RADIUS_PX = 6;
export const RADIUS_MIN_PX = 0;
export const RADIUS_MAX_PX = 16;

export const DEFAULT_SPACING_SCALE = 1;
export const SPACING_SCALE_MIN = 0.75;
export const SPACING_SCALE_MAX = 1.25;

/** Apply an accent by setting `data-accent` on the given root element (defaults to `document.documentElement`). `"mono"` is a valid value and resolves to the base theme (no override block matches it). */
export function applyAccent(accent: AccentName, root: HTMLElement = document.documentElement): void {
  root.setAttribute("data-accent", accent);
}

export function isAccentName(value: string): value is AccentName {
  return (ACCENTS as readonly string[]).includes(value);
}

/** Apply a base corner radius in px. Rounded to an integer and clamped to [0, 16] (infinities clamp to the nearest bound); NaN falls back to the 6px default. Writes `--vn-radius-base` on the given root element. */
export function applyRadius(px: number, root: HTMLElement = document.documentElement): void {
  const wanted = Number.isNaN(px) ? DEFAULT_RADIUS_PX : Math.round(px);
  const clamped = Math.min(RADIUS_MAX_PX, Math.max(RADIUS_MIN_PX, wanted));
  root.style.setProperty(RADIUS_BASE_PROPERTY, `${clamped}px`);
}

/** Apply a layout spacing multiplier. Clamped to [0.75, 1.25] (infinities clamp to the nearest bound); NaN falls back to 1. Writes `--vn-spacing-scale` on the given root element. */
export function applySpacing(scale: number, root: HTMLElement = document.documentElement): void {
  const wanted = Number.isNaN(scale) ? DEFAULT_SPACING_SCALE : scale;
  const clamped = Math.min(SPACING_SCALE_MAX, Math.max(SPACING_SCALE_MIN, wanted));
  root.style.setProperty(SPACING_SCALE_PROPERTY, String(clamped));
}
