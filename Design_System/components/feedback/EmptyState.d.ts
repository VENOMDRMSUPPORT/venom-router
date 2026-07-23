import * as React from "react";
export interface EmptyStateProps {
    icon?: string;
    title: React.ReactNode;
    description?: React.ReactNode;
    action?: React.ReactNode;
    className?: string;
}
export declare function EmptyState(props: EmptyStateProps): React.JSX.Element;
