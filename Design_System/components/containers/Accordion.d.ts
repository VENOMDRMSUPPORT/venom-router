import * as React from "react";
export interface AccordionItem {
    title: React.ReactNode;
    content: React.ReactNode;
}
export interface AccordionProps {
    items?: AccordionItem[];
    /** Indexes (into `items`) open by default. */
    defaultOpen?: number[];
    className?: string;
}
export declare function Accordion(props: AccordionProps): React.JSX.Element;
export interface CollapsibleProps {
    title?: React.ReactNode;
    defaultOpen?: boolean;
    children?: React.ReactNode;
    className?: string;
}
export declare function Collapsible(props: CollapsibleProps): React.JSX.Element;
