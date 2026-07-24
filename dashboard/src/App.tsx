import { useState } from "react";
import {
  DEFAULT_DENSITY,
  DEFAULT_THEME,
  DENSITIES,
  THEMES,
  setDensity,
  setTheme,
  type DensityName,
  type ThemeName,
} from "./theme-runtime";

// Minimal runtime theme/density switcher for P2a-DS-002 — proves
// applyTheme/applyDensity wiring through the package's own helpers. The
// real UI shell (navigation, surfaces) is later work (UI-001+).
export default function App() {
  const [theme, setThemeState] = useState<ThemeName>(DEFAULT_THEME);
  const [density, setDensityState] = useState<DensityName>(DEFAULT_DENSITY);

  return (
    <div>
      <p>
        @venom/design-system wired — theme: {theme}, density: {density}
      </p>
      <label>
        Theme:{" "}
        <select
          value={theme}
          onChange={(e) => {
            const next = e.target.value as ThemeName;
            setTheme(next);
            setThemeState(next);
          }}
        >
          {THEMES.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </label>
      <label>
        Density:{" "}
        <select
          value={density}
          onChange={(e) => {
            const next = e.target.value as DensityName;
            setDensity(next);
            setDensityState(next);
          }}
        >
          {DENSITIES.map((d) => (
            <option key={d} value={d}>
              {d}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}
