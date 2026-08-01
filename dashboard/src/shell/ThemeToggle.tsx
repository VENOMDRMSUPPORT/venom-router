import { IconButton } from "@venom/design-system/primitives";
import type { ThemeName } from "../theme-runtime";

export interface ThemeToggleProps {
  /** The theme currently applied to the document root. */
  theme: ThemeName;
  /** Called with the toggled theme — the shell applies and persists it
   * through the existing settings flow, exactly like every other
   * appearance control. */
  onChange: (next: ThemeName) => void;
}

/**
 * The header's sun/moon theme toggle (legacy console pattern): one button
 * that flips light <-> dark, wired to the existing server-persisted theme
 * setting. venom-hc counts as a dark appearance here (the toggle offers
 * light); high-contrast itself is selected elsewhere, never destroyed by
 * this control unless the owner explicitly toggles.
 */
export default function ThemeToggle(props: ThemeToggleProps) {
  const { theme, onChange } = props;
  const isDark = theme !== "venom-light";

  return (
    <IconButton
      icon={isDark ? "sun" : "moon"}
      label={isDark ? "Switch to light mode" : "Switch to dark mode"}
      variant="ghost"
      onClick={() => onChange(isDark ? "venom-light" : "venom-dark")}
    />
  );
}
