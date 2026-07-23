import * as React from "react";
import { IconButton } from "../actions/IconButton";

export interface PaginationProps {
  page: number;
  pageCount: number;
  onPage?: (page: number) => void;
  rangeLabel?: React.ReactNode;
  className?: string;
}

export function Pagination(props: PaginationProps) {
  const { page, pageCount, onPage, rangeLabel, className = "" } = props;
  return (
    <div className={("vn-pagination " + className).trim()}>
      {rangeLabel ? <span className="vn-range">{rangeLabel}</span> : null}
      <IconButton icon="chevron-left" label="Previous page" size="sm" disabled={page <= 1} onClick={() => onPage && onPage(page - 1)} />
      <span className="vn-range">{page} / {pageCount}</span>
      <IconButton icon="chevron-right" label="Next page" size="sm" disabled={page >= pageCount} onClick={() => onPage && onPage(page + 1)} />
    </div>
  );
}
