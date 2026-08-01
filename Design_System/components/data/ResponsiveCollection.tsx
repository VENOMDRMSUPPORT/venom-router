import * as React from "react";
import { DataTable, type DataTableColumn } from "./Table";

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

function useCompactCollection(): boolean {
  const query = "(max-width: 899px)";
  const [compact, setCompact] = React.useState(() =>
    typeof window !== "undefined" && typeof window.matchMedia === "function"
      ? window.matchMedia(query).matches
      : false,
  );

  React.useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia(query);
    const update = () => setCompact(media.matches);
    update();
    media.addEventListener?.("change", update);
    return () => media.removeEventListener?.("change", update);
  }, []);

  return compact;
}

export function ResponsiveCollection<T extends object>(props: ResponsiveCollectionProps<T>) {
  const { rows, columns, rowKey, renderCard, label, empty, loading = false, className = "", getRowProps } = props;
  const compact = useCompactCollection();

  if (!compact) {
    return (
      <DataTable
        rows={rows}
        columns={columns}
        rowKey={rowKey}
        label={label}
        empty={empty}
        loading={loading}
        className={("vn-responsive-collection-table " + className).trim()}
        getRowProps={getRowProps}
      />
    );
  }

  if (loading) {
    return (
      <div className={("vn-responsive-collection-cards " + className).trim()} aria-label={label} aria-busy="true">
        {[0, 1, 2].map((index) => <div key={index} className="vn-card vn-card--pad"><span className="vn-skeleton vn-responsive-collection-skeleton" /></div>)}
      </div>
    );
  }

  if (rows.length === 0) return <>{empty}</>;

  return (
    <div className={("vn-responsive-collection-cards " + className).trim()} aria-label={label}>
      {rows.map((row, index) => <React.Fragment key={String(row[rowKey])}>{renderCard(row, index)}</React.Fragment>)}
    </div>
  );
}
