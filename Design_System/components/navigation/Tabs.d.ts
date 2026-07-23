import * as React from "react";
export type TabOption = string | {
    value: string;
    label: React.ReactNode;
    count?: number;
};
export interface TabsProps {
    tabs?: TabOption[];
    value?: string;
    onChange?: (value: string) => void;
    label?: string;
    className?: string;
}
export declare function Tabs(props: TabsProps): React.JSX.Element;
