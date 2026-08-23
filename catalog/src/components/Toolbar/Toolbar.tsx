import { useState, useRef, useEffect } from 'react';
import { LuSearch, LuLayoutGrid, LuTable2, LuFilter, LuChevronDown, LuCheck } from 'react-icons/lu';
import styles from './Toolbar.module.css';

export interface FilterOption {
  value: string;
  label: string;
}

interface ToolbarProps {
  query: string;
  onQueryChange: (value: string) => void;
  filter: string;
  onFilterChange: (value: string) => void;
  view: 'grid' | 'table';
  onViewChange: (view: 'grid' | 'table') => void;
  placeholder?: string;
  /**
   * The filters THIS page has. Defaults to the model filters.
   *
   * It used to be one hardcoded list for every caller, which meant "What's new"
   * — filtering by change class — offered "Free Models" and "1M+ Context".
   * Choosing either compared a change class against a model predicate, matched
   * nothing, and emptied the page: a control whose only possible effect was to
   * hide everything.
   */
  options?: FilterOption[];
}

/** The filters that apply to a list of models. */
export const MODEL_FILTERS: FilterOption[] = [
  { value: 'all', label: 'All Models' },
  { value: 'free', label: 'Free Models' },
  { value: 'paid', label: 'Paid Models' },
  { value: '1m', label: '1M+ Context' },
  { value: 'multimodal', label: 'Multimodal' },
  // Still served and still ranked, but the vendor has marked it for retirement.
  { value: 'current', label: 'Not Deprecated' },
];

/**
 * The filters that apply to a list of PROVIDERS.
 *
 * A provider matches when one of its models does, so most model predicates
 * carry over. `current` does not: "has at least one non-deprecated model" is
 * true of nearly every provider, so offering it there would be a control that
 * cannot narrow anything — the same defect as the change-class page offering
 * "1M+ Context". Declared as its own list rather than derived by subtracting
 * from MODEL_FILTERS, so the next model filter added above cannot silently
 * reach a page that lists sellers.
 */
export const PROVIDER_FILTERS: FilterOption[] = [
  { value: 'all', label: 'All Providers' },
  { value: 'free', label: 'Free Models' },
  { value: 'paid', label: 'Paid Models' },
  { value: '1m', label: '1M+ Context' },
  { value: 'multimodal', label: 'Multimodal' },
];

export function Toolbar({
  query,
  onQueryChange,
  filter,
  onFilterChange,
  view,
  onViewChange,
  placeholder = 'Search models by name or ID...',
  options = MODEL_FILTERS,
}: ToolbarProps) {
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  // Close dropdown on click outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setDropdownOpen(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  // Falls back to the caller's first option, not the module default: the filter
  // state is a plain string that survives a route change, so a value belonging
  // to another page must leave the trigger naming something this page has.
  const activeOption = options.find((opt) => opt.value === filter) ?? options[0];

  return (
    <div className={styles.toolbar}>
      {/* Search Input */}
      <div className={styles.search}>
        <LuSearch size={14} className={styles.searchIcon} />
        <input
          type="text"
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          placeholder={placeholder}
          aria-label="Search providers"
          className={styles.input}
        />
        {query && (
          <button
            type="button"
            className={styles.clearBtn}
            onClick={() => onQueryChange('')}
            aria-label="Clear search"
          >
            ×
          </button>
        )}
      </div>

      {/* Custom Sleek Filter Dropdown */}
      <div className={styles.dropdownContainer} ref={dropdownRef}>
        <button
          type="button"
          className={`${styles.filterDropdownBtn} ${dropdownOpen ? styles.dropdownOpen : ''}`}
          onClick={() => setDropdownOpen((v) => !v)}
          aria-haspopup="listbox"
          aria-expanded={dropdownOpen}
        >
          <LuFilter size={13} className={styles.filterIcon} />
          <span className={styles.filterLabel}>{activeOption.label}</span>
          <LuChevronDown size={13} className={`${styles.chevron} ${dropdownOpen ? styles.chevronRotated : ''}`} />
        </button>

        {dropdownOpen && (
          <div className={styles.menu} role="listbox">
            {options.map((opt) => {
              const isSelected = filter === opt.value;
              return (
                <button
                  key={opt.value}
                  type="button"
                  className={`${styles.menuItem} ${isSelected ? styles.menuItemSelected : ''}`}
                  onClick={() => {
                    onFilterChange(opt.value);
                    setDropdownOpen(false);
                  }}
                  role="option"
                  aria-selected={isSelected}
                >
                  <span className={styles.menuItemText}>{opt.label}</span>
                  {isSelected && <LuCheck size={14} className={styles.checkIcon} />}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {/* View Switcher */}
      <div className={styles.viewSwitcher}>
        <button
          type="button"
          className={`${styles.viewBtn} ${view === 'grid' ? styles.viewActive : ''}`}
          onClick={() => onViewChange('grid')}
          aria-label="Grid view"
          title="Grid view"
        >
          <LuLayoutGrid size={14} />
          <span className={styles.viewLabel}>Grid</span>
        </button>
        <button
          type="button"
          className={`${styles.viewBtn} ${view === 'table' ? styles.viewActive : ''}`}
          onClick={() => onViewChange('table')}
          aria-label="Table view"
          title="Table view"
        >
          <LuTable2 size={14} />
          <span className={styles.viewLabel}>Table</span>
        </button>
      </div>
    </div>
  );
}
