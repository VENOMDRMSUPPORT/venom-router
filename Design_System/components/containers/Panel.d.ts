import * as React from "react";
export interface PanelProps {
    title?: React.ReactNode;
    actions?: React.ReactNode;
    children?: React.ReactNode;
    className?: string;
}
export declare function Panel(props: PanelProps): React.JSX.Element;
