import * as React from "react";
export interface BreadcrumbItem {
    label: React.ReactNode;
    /** Omit on the last (current-page) item. */
    href?: string;
}
export interface BreadcrumbsProps {
    items?: BreadcrumbItem[];
    className?: string;
}
export declare function Breadcrumbs(props: BreadcrumbsProps): React.JSX.Element;
