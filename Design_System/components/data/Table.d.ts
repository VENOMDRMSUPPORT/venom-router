import * as React from "react";
export interface TableProps {
    children?: React.ReactNode;
    label?: string;
    className?: string;
    maxHeight?: string | number;
}
export declare function Table(props: TableProps): React.JSX.Element;
/** A row is any record — DataTable is generic in the row shape so `render`/`rowKey` stay typed per caller. */
export type DataTableRow = Record<string, unknown>;
export interface DataTableColumn<T extends DataTableRow = DataTableRow> {
    key: string;
    label?: React.ReactNode;
    numeric?: boolean;
    mono?: boolean;
    sortable?: boolean;
    width?: string | number;
    render?: (row: T) => React.ReactNode;
}
export interface DataTableSort {
    key: string;
    dir: "asc" | "desc";
}
export interface DataTableProps<T extends DataTableRow = DataTableRow> {
    columns?: DataTableColumn<T>[];
    rows?: T[];
    /** Property used to derive each row's React key; falls back to array index when omitted. */
    rowKey?: keyof T;
    label?: string;
    sort?: DataTableSort;
    onSort?: (sort: DataTableSort) => void;
    onRowClick?: (row: T) => void;
    selectedKey?: React.Key;
    empty?: React.ReactNode;
    loading?: boolean;
    className?: string;
}
export declare function DataTable<T extends DataTableRow = DataTableRow>(props: DataTableProps<T>): React.JSX.Element;
