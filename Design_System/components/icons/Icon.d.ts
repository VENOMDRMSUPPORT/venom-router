import * as React from "react";
/** Canonical domain-concept -> glyph map (see icons/icon-map.md). */
export declare const DOMAIN_ICON_MAP: Record<string, string>;
export interface IconProps {
    /** A domain concept name (see DOMAIN_ICON_MAP) or a literal Lucide glyph name from icons/icons.css. */
    name: string;
    size?: number;
    /** Accessible name. Omit for decorative icons paired with visible text (renders `aria-hidden`). */
    label?: string;
    className?: string;
    style?: React.CSSProperties;
}
export declare function Icon(props: IconProps): React.JSX.Element;
