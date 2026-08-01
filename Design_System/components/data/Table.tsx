import * as React from "react";
import { Icon } from "../icons/Icon";

export interface TableProps {
  children?: React.ReactNode;
  label?: string;
  className?: string;
  maxHeight?: string | number;
}

export function Table(props: TableProps) {
  const { children, label, className = "", maxHeight } = props;
  return (
    <div className={("vn-table-wrap vn-scroll " + className).trim()} style={maxHeight ? { maxHeight } : undefined}>
      <table className="vn-table" aria-label={label}>{children}</table>
    </div>
  );
}

/** A row is any record — DataTable is generic in the row shape so `render`/`rowKey` stay typed per caller. */
export type DataTableRow = object;

export interface DataTableColumn<T extends DataTableRow = DataTableRow> {
  key: string;
  label?: React.ReactNode;
  numeric?: boolean;
  mono?: boolean;
  sortable?: boolean;
  width?: string | number;
  /** Render a cell from the row and its zero-based position in the current collection. */
  render?: (row: T, index: number) => React.ReactNode;
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
  getRowProps?: (row: T) => React.HTMLAttributes<HTMLTableRowElement>;
}

export function DataTable<T extends DataTableRow = DataTableRow>(props: DataTableProps<T>) {
  const { columns = [], rows = [], rowKey, label, sort, onSort, onRowClick, selectedKey, empty, loading = false, className = "", getRowProps } = props;
  const key = (r: T, i: number): React.Key => (rowKey ? (r[rowKey] as React.Key) : i);
  return (
    <div className={("vn-table-wrap vn-scroll " + className).trim()}>
      <table className="vn-table" aria-label={label}>
        <thead><tr>
          {columns.map((c) => (
            <th key={c.key} className={c.numeric ? "vn-numeric" : undefined} aria-sort={sort && sort.key === c.key ? (sort.dir === "asc" ? "ascending" : "descending") : undefined} style={c.width ? { width: c.width } : undefined}>
              {c.sortable ? (
                <button type="button" onClick={() => onSort && onSort({ key: c.key, dir: sort && sort.key === c.key && sort.dir === "asc" ? "desc" : "asc" })}>
                  {c.label}<Icon name={sort && sort.key === c.key ? (sort.dir === "asc" ? "chevron-down" : "chevron-right") : "chevrons-up-down"} size={11} />
                </button>
              ) : c.label}
            </th>
          ))}
        </tr></thead>
        <tbody>
          {loading ? (
            [0, 1, 2].map((i) => (
              <tr key={"sk" + i}>{columns.map((c) => <td key={c.key}><span className="vn-skeleton" style={{ display: "inline-block", width: c.numeric ? 48 : "70%", height: 12 }}></span></td>)}</tr>
            ))
          ) : rows.length === 0 ? (
            <tr><td colSpan={columns.length} style={{ height: "auto", padding: 0 }}>{empty}</td></tr>
          ) : rows.map((r, i) => {
            const extra = getRowProps ? getRowProps(r) : {};
            return (
            <tr {...extra} key={key(r, i)} className={[extra.className ?? "", onRowClick ? "is-clickable" : ""].filter(Boolean).join(" ") || undefined} tabIndex={onRowClick ? 0 : extra.tabIndex}
              aria-selected={selectedKey != null && key(r, i) === selectedKey ? true : undefined}
              onClick={() => onRowClick && onRowClick(r)}
              onKeyDown={(e: React.KeyboardEvent) => { if (onRowClick && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); onRowClick(r); } }}>
              {columns.map((c) => (
                <td key={c.key} className={[c.numeric ? "vn-numeric" : "", c.mono ? "vn-mono" : ""].filter(Boolean).join(" ") || undefined}>
                  {c.render ? c.render(r, i) : ((r as Record<string, unknown>)[c.key] as React.ReactNode)}
                </td>
              ))}
            </tr>
          )})}
        </tbody>
      </table>
    </div>
  );
}
