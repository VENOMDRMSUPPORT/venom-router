import * as React from "react";
export type ThemeName = "venom-dark" | "venom-light";
export interface ThemeSwitcherProps {
    /** The theme currently applied to the document root. */
    value: ThemeName;
    /** Called with the newly chosen theme. The component never applies or persists it itself — the app sets `data-theme` and persists the choice (server-side, per SKILL.md), then feeds the resolved value back in as `value`. */
    onChange: (theme: ThemeName) => void;
    label?: string;
    className?: string;
}
/**
 * ThemeSwitcher — a controlled picker for the two shipped themes
 * (`venom-dark` / `venom-light`). No hidden storage: this is a pure
 * controlled component, so the host application owns persistence.
 */
export declare function ThemeSwitcher(props: ThemeSwitcherProps): React.JSX.Element;
