import * as React from "react";
export interface PaginationProps {
    page: number;
    pageCount: number;
    onPage?: (page: number) => void;
    rangeLabel?: React.ReactNode;
    className?: string;
}
export declare function Pagination(props: PaginationProps): React.JSX.Element;
