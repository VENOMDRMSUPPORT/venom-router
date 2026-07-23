import * as React from "react";
export interface PopoverProps {
    trigger: React.ReactElement;
    /** Either static content, or a render-prop that receives a `close` callback. */
    children?: React.ReactNode | ((close: () => void) => React.ReactNode);
    align?: "start" | "end";
    className?: string;
}
export declare function Popover(props: PopoverProps): React.JSX.Element;
