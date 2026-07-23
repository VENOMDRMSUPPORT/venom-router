import * as React from "react";
export interface LinkProps extends React.AnchorHTMLAttributes<HTMLAnchorElement> {
    /** Opens in a new tab with `rel="noreferrer"` and shows the external-link glyph. */
    external?: boolean;
}
export declare function Link(props: LinkProps): React.JSX.Element;
