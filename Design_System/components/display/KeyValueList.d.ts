import * as React from "react";
export interface KeyValueItem {
    key: React.ReactNode;
    value: React.ReactNode;
    mono?: boolean;
}
export interface KeyValueListProps {
    items?: KeyValueItem[];
    className?: string;
}
export declare function KeyValueList(props: KeyValueListProps): React.JSX.Element;
