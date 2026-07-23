import * as React from "react";
import { Icon } from "../icons/Icon";

export type ThemeName = "venom-dark" | "venom-light" | "venom-hc";

interface ThemeOption {
  value: ThemeName;
  label: string;
  icon: string;
}

const THEME_OPTIONS: ThemeOption[] = [
  { value: "venom-dark", label: "Dark", icon: "moon" },
  { value: "venom-light", label: "Light", icon: "sun" },
  { value: "venom-hc", label: "High contrast", icon: "contrast" },
];

export interface ThemeSwitcherProps {
  /** The theme currently applied to the document root. */
  value: ThemeName;
  /** Called with the newly chosen theme. The component never applies or persists it itself — the app sets `data-theme` and persists the choice (server-side, per SKILL.md), then feeds the resolved value back in as `value`. */
  onChange: (theme: ThemeName) => void;
  label?: string;
  className?: string;
}

/**
 * ThemeSwitcher — a controlled picker for exactly the three shipped themes
 * (`venom-dark` / `venom-light` / `venom-hc`). No hidden storage: this is a pure
 * controlled component, so the host application owns persistence.
 */
export function ThemeSwitcher(props: ThemeSwitcherProps) {
  const { value, onChange, label = "Theme", className = "" } = props;
  const refs = React.useRef<Array<HTMLButtonElement | null>>([]);

  const move = (index: number, e: React.KeyboardEvent) => {
    let next = -1;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") next = (index + 1) % THEME_OPTIONS.length;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") next = (index - 1 + THEME_OPTIONS.length) % THEME_OPTIONS.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = THEME_OPTIONS.length - 1;
    if (next !== -1) {
      e.preventDefault();
      onChange(THEME_OPTIONS[next].value);
      refs.current[next]?.focus();
    }
  };

  return (
    <div className={("vn-segmented vn-theme-switcher " + className).trim()} role="radiogroup" aria-label={label}>
      {THEME_OPTIONS.map((opt, i) => (
        <button
          key={opt.value}
          ref={(node) => {
            refs.current[i] = node;
          }}
          type="button"
          role="radio"
          aria-checked={value === opt.value}
          aria-label={opt.label}
          title={opt.label}
          tabIndex={value === opt.value ? 0 : -1}
          onClick={() => onChange(opt.value)}
          onKeyDown={(e) => move(i, e)}
        >
          <Icon name={opt.icon} size={13} />
          <span className="vn-theme-switcher-label">{opt.label}</span>
        </button>
      ))}
    </div>
  );
}
