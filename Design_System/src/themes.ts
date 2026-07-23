/**
 * Public entry point: theme registry metadata.
 * Themes are delivered as generated CSS (see `themes/*.css`, compiled by
 * `validation/build-tokens.js`); this module exposes the typed registry consumers use to
 * drive a theme switcher without hardcoding theme names in application code.
 */

import type { ThemeName } from "../components/actions/ThemeSwitcher";
export type { ThemeName };

/** The three shipped themes, in the canonical order. Dark is default (also bound to `:root`). */
export const THEMES = ["venom-dark", "venom-light", "venom-hc"] as const;

export const DEFAULT_THEME: ThemeName = "venom-dark";

export const THEME_LABELS: Record<ThemeName, string> = {
  "venom-dark": "Dark",
  "venom-light": "Light",
  "venom-hc": "High contrast",
};

/** Relative path (from the package root) to each theme's generated CSS. Already bundled by `styles.css`; exposed for tooling that needs to load a single theme in isolation. */
export const THEME_CSS_PATH: Record<ThemeName, string> = {
  "venom-dark": "./themes/venom-dark.css",
  "venom-light": "./themes/venom-light.css",
  "venom-hc": "./themes/venom-hc.css",
};

/** Apply a theme by setting `data-theme` on the given root element (defaults to `document.documentElement`). Persistence is the consuming app's responsibility (server-side settings, never browser storage — see SKILL.md). */
export function applyTheme(theme: ThemeName, root: HTMLElement = document.documentElement): void {
  root.setAttribute("data-theme", theme);
}

export function isThemeName(value: string): value is ThemeName {
  return (THEMES as readonly string[]).includes(value);
}
