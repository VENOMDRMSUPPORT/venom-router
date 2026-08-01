import { Icon } from "@venom/design-system/icons";

export interface FleetBreadcrumbChipsProps {
  /** Providers with at least one connected account. */
  activeCount: number;
  /** Every integration in the catalog. */
  totalCount: number;
}

/**
 * The Provider Fleet's breadcrumb-row chips (legacy console parity):
 * "Active Providers <n>" and the emphasized "All Integrations <n>", both
 * counted from the live GET /providers + GET /accounts data. Rendered by
 * the shell on the right side of the global breadcrumb row, on the
 * Providers page only. Purely presentational.
 */
export default function FleetBreadcrumbChips(props: FleetBreadcrumbChipsProps) {
  const { activeCount, totalCount } = props;
  return (
    <div className="flex items-center gap-2">
      <span className="inline-flex items-center gap-1.5 rounded-lg border border-border-default bg-surface-secondary px-3 py-1.5 text-xs font-medium text-text-muted shadow-sm">
        <Icon name="circle-check" size={13} />
        Active Providers
        <span className="rounded-md bg-surface-raised px-1.5 py-0.5 text-2xs font-semibold tabular-nums text-text-secondary">
          {activeCount}
        </span>
      </span>
      <span className="inline-flex items-center gap-1.5 rounded-lg border border-border-strong bg-surface-primary px-3 py-1.5 text-xs font-medium text-text-primary shadow-sm">
        <Icon name="layout-dashboard" size={13} />
        All Integrations
        <span className="rounded-md bg-accent-subtle-bg px-1.5 py-0.5 text-2xs font-semibold tabular-nums text-accent-text">
          {totalCount}
        </span>
      </span>
    </div>
  );
}
