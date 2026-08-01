import * as React from "react";
export interface PageContextBarProps {
    leading: React.ReactNode;
    actions?: React.ReactNode;
    secondary?: React.ReactNode;
    className?: string;
}
export declare function PageContextBar(props: PageContextBarProps): React.JSX.Element;
