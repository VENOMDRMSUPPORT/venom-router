// EnterpriseCustomizer's non-component constants, in their own module so
// the component file exports only the component (react-refresh's
// only-export-components guardrail).
import { DEFAULT_ACCENT, DEFAULT_RADIUS_PX, DEFAULT_SPACING_SCALE, type AccentName, type ThemeName } from "../theme-runtime";

/** The four appearance fields the customizer owns (density stays on the
 * Settings page's DensityToggle; venom-hc stays on the Settings page's
 * ThemeSwitcher — when the current theme is venom-hc, neither Light nor
 * Dark segment shows active in the widget). */
export interface CustomizerValue {
  theme: ThemeName;
  accent: AccentName;
  radiusPx: number;
  spacingScale: number;
}

/** Reset restores the server defaults: dark theme, mono accent, 6 px
 * radius, 100% spacing (density untouched — it is not this widget's). */
export const CUSTOMIZER_RESET: CustomizerValue = {
  theme: "venom-dark",
  accent: DEFAULT_ACCENT,
  radiusPx: DEFAULT_RADIUS_PX,
  spacingScale: DEFAULT_SPACING_SCALE,
};

/** How long after the last slider change the persist fires (ms) — a
 * trailing debounce covering both drag-release and keyboard-arrow bursts. */
export const SLIDER_PERSIST_DEBOUNCE_MS = 400;
