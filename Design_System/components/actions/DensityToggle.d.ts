import * as React from "react";
export type DensityName = "comfortable" | "compact";
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
export declare function DensityToggle(props: DensityToggleProps): React.JSX.Element;
