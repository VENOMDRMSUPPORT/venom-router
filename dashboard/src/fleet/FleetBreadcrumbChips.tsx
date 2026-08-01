import { Icon } from "@venom/design-system/icons";

/** Which fleet view the breadcrumb-row chips select. */
export type FleetView = "active" | "all";

export interface FleetBreadcrumbChipsProps {
  /** Providers with at least one connected account. */
  activeCount: number;
  /** Every integration in the catalog. */
  totalCount: number;
  /** The currently selected view. */
  view: FleetView;
  /** Called with the newly selected view when a chip is clicked. */
  onViewChange: (view: FleetView) => void;
}

/**
 * The Provider Fleet's breadcrumb-row chips (legacy console parity): two
 * TOGGLE buttons that switch the grid between "Active Providers <n>" (only
 * providers with ≥1 connected account) and "All Integrations <n>" (the full
 * catalog). Exactly one is selected at a time; the selected chip takes the
 * emphasized (accent-tinted) styling, the unselected one is subdued. Counts
 * stay live from GET /providers + GET /accounts. Rendered by the shell on
 * the right side of the global breadcrumb row, on the Providers page only.
 */
export default function FleetBreadcrumbChips(props: FleetBreadcrumbChipsProps) {
  const { activeCount, totalCount, view, onViewChange } = props;
  return (
    <div className="flex items-center gap-2" role="group" aria-label="Filter provider fleet by connection state">
      <ChipButton
        selected={view === "active"}
        onClick={() => onViewChange("active")}
        icon="circle-check"
        label="Active Providers"
        count={activeCount}
      />
      <ChipButton
        selected={view === "all"}
        onClick={() => onViewChange("all")}
        icon="layout-dashboard"
        label="All Integrations"
        count={totalCount}
      />
    </div>
  );
}

interface ChipButtonProps {
  selected: boolean;
  onClick: () => void;
  icon: string;
  label: string;
  count: number;
}

/** A single toggle chip. Selected = emphasized (strong border, primary
 * surface, accent-tinted count); unselected = subdued (default border,
 * secondary surface, neutral count). aria-pressed exposes the toggle state
 * to assistive tech. */
function ChipButton(props: ChipButtonProps) {
  const { selected, onClick, icon, label, count } = props;
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onClick}
      className={
        selected
          ? "inline-flex items-center gap-1.5 rounded-lg border border-border-strong bg-surface-primary px-3 py-1.5 text-xs font-medium text-text-primary shadow-sm transition-colors"
          : "inline-flex items-center gap-1.5 rounded-lg border border-border-default bg-surface-secondary px-3 py-1.5 text-xs font-medium text-text-muted shadow-sm transition-colors hover:text-text-secondary"
      }
    >
      <Icon name={icon} size={13} />
      {label}
      <span
        className={
          selected
            ? "rounded-md bg-accent-subtle-bg px-1.5 py-0.5 text-2xs font-semibold tabular-nums text-accent-text"
            : "rounded-md bg-surface-raised px-1.5 py-0.5 text-2xs font-semibold tabular-nums text-text-secondary"
        }
      >
        {count}
      </span>
    </button>
  );
}
