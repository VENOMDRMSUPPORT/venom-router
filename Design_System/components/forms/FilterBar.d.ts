import * as React from "react";
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
export declare function FilterBar(props: FilterBarProps): React.JSX.Element;
