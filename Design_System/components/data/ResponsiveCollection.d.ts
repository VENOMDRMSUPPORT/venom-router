import * as React from "react";
import { type DataTableColumn } from "./Table";
export interface ResponsiveCollectionProps<T extends object> {
    rows: T[];
    columns: DataTableColumn<T>[];
    rowKey: keyof T;
    renderCard: (row: T, index: number) => React.ReactNode;
    label: string;
    empty?: React.ReactNode;
    loading?: boolean;
    className?: string;
    getRowProps?: (row: T) => React.HTMLAttributes<HTMLTableRowElement>;
}
export declare function ResponsiveCollection<T extends object>(props: ResponsiveCollectionProps<T>): React.JSX.Element;
