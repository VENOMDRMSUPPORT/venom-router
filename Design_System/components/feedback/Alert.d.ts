import * as React from "react";
export type AlertTone = "info" | "warning" | "critical" | "healthy" | "unknown";
export interface AlertProps {
    tone?: AlertTone;
    title?: React.ReactNode;
    code?: React.ReactNode;
    children?: React.ReactNode;
    actions?: React.ReactNode;
    className?: string;
}
export declare function Alert(props: AlertProps): React.JSX.Element;
