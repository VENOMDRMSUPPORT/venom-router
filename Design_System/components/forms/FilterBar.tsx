import * as React from "react";
import { SearchField } from "./SearchField";
import { Tag } from "../display/Tag";
import { Button } from "../actions/Button";
import { Spinner } from "../feedback/Spinner";

export interface FilterBarActiveFilter {
  /** Stable identity for the chip — also used as the React key. */
  key: string;
  label: string;
  value: React.ReactNode;
  /** Omit to render a non-clearable summary chip. */
  onClear?: () => void;
}

export interface FilterBarProps {
  searchValue?: string;
  onSearchChange?: (value: string) => void;
  searchPlaceholder?: string;
  /** Accessible name for the search input; defaults to `searchPlaceholder` or "Search". */
  searchLabel?: string;
  /** Structured filter controls (Select, SegmentedControl, DateRange, …) rendered between search and the active-filter summary. Tab order follows DOM order: search -> structured controls -> active-filter chips -> clear all. */
  children?: React.ReactNode;
  activeFilters?: FilterBarActiveFilter[];
  onClearAll?: () => void;
  loading?: boolean;
  disabled?: boolean;
  /** Accessible name for the whole toolbar group. */
  label?: string;
  className?: string;
}

/**
 * FilterBar — the one search + structured-filter + active-filter-summary toolbar.
 * Wraps (never clips) on narrow widths; every control keeps a real accessible name
 * even when disabled or loading.
 */
export function FilterBar(props: FilterBarProps) {
  const {
    searchValue,
    onSearchChange,
    searchPlaceholder = "Search",
    searchLabel,
    children,
    activeFilters = [],
    onClearAll,
    loading = false,
    disabled = false,
    label = "Filters",
    className = "",
  } = props;

  const isBlocked = disabled || loading;

  return (
    <div className={("vn-toolbar vn-filter-bar " + className).trim()} role="group" aria-label={label} aria-busy={loading || undefined}>
      {onSearchChange || searchValue !== undefined ? (
        <SearchField
          value={searchValue}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => onSearchChange && onSearchChange(e.target.value)}
          placeholder={searchPlaceholder}
          label={searchLabel || searchPlaceholder}
          disabled={isBlocked}
        />
      ) : null}
      {children ? <div className="vn-filter-bar-controls">{children}</div> : null}
      {loading ? <Spinner label="Filtering" /> : null}
      <span className="vn-toolbar-spacer"></span>
      {activeFilters.length ? (
        <div className="vn-filter-bar-active" aria-label="Active filters">
          {activeFilters.map((f) => (
            <Tag key={f.key} onRemove={f.onClear && !isBlocked ? f.onClear : undefined} removeLabel={"Clear " + f.label + " filter"}>
              <span className="vn-caption" style={{ color: "inherit" }}>
                {f.label}:
              </span>{" "}
              {f.value}
            </Tag>
          ))}
          {onClearAll ? (
            <Button variant="ghost" size="sm" icon="x" onClick={onClearAll} disabled={isBlocked}>
              Clear all
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
