import * as React from "react";
import { Icon } from "../icons/Icon";

export type DensityName = "comfortable" | "compact";

interface DensityOption {
  value: DensityName;
  label: string;
  icon?: string;
}

const DENSITY_OPTIONS: DensityOption[] = [
  { value: "comfortable", label: "Comfortable" },
  { value: "compact", label: "Compact", icon: "rows-3" },
];

export interface DensityToggleProps {
  /** The density currently applied to the document root. */
  value: DensityName;
  /** Called with the newly chosen density. No hidden storage — same controlled contract as `ThemeSwitcher`. */
  onChange: (density: DensityName) => void;
  label?: string;
  className?: string;
}

/**
 * DensityToggle — a controlled picker for exactly `comfortable` / `compact`. Density is
 * a token-driven mode switch (`data-density` on the root), never a layout fork; this
 * component only reports the choice, it never applies or persists it itself.
 */
export function DensityToggle(props: DensityToggleProps) {
  const { value, onChange, label = "Density", className = "" } = props;
  const refs = React.useRef<Array<HTMLButtonElement | null>>([]);

  const move = (index: number, e: React.KeyboardEvent) => {
    let next = -1;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") next = (index + 1) % DENSITY_OPTIONS.length;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") next = (index - 1 + DENSITY_OPTIONS.length) % DENSITY_OPTIONS.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = DENSITY_OPTIONS.length - 1;
    if (next !== -1) {
      e.preventDefault();
      onChange(DENSITY_OPTIONS[next].value);
      refs.current[next]?.focus();
    }
  };

  return (
    <div className={("vn-segmented vn-density-toggle " + className).trim()} role="radiogroup" aria-label={label}>
      {DENSITY_OPTIONS.map((opt, i) => (
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
          {opt.icon ? <Icon name={opt.icon} size={13} /> : null}
          <span className="vn-density-toggle-label">{opt.label}</span>
        </button>
      ))}
    </div>
  );
}
