import * as React from "react";
export interface TooltipProps {
    content: React.ReactNode;
    /** Exactly one element — receives `aria-describedby` while the tooltip is open. */
    children: React.ReactElement;
    className?: string;
}
/** Hover/focus tooltip. Content must be supplementary — never the only path to critical info. */
export declare function Tooltip(props: TooltipProps): React.JSX.Element;
