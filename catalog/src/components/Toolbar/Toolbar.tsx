import { LuSearch, LuLayoutGrid, LuTable2 } from 'react-icons/lu';
import styles from './Toolbar.module.css';

interface ToolbarProps {
  query: string;
  onQueryChange: (value: string) => void;
  filter: string;
  onFilterChange: (value: string) => void;
  view: 'grid' | 'table';
  onViewChange: (view: 'grid' | 'table') => void;
}

const FILTERS = ['all', 'free', 'paid', '1m', 'multimodal'];

export function Toolbar({
  query,
  onQueryChange,
  filter,
  onFilterChange,
  view,
  onViewChange,
}: ToolbarProps) {
  return (
    <div className={styles.toolbar}>
      <div className={styles.search}>
        <LuSearch size={14} className={styles.searchIcon} />
        <input
          type="text"
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          placeholder="Search providers or models..."
          aria-label="Search providers"
          className={styles.input}
        />
      </div>

      <div className={styles.filters}>
        {FILTERS.map((f) => (
          <button
            key={f}
            className={`${styles.filterBtn} ${
              filter === f ? styles.filterActive : ''
            }`}
            onClick={() => onFilterChange(f)}
          >
            {f === '1m' ? '1M+ Context' : f.charAt(0).toUpperCase() + f.slice(1)}
          </button>
        ))}
      </div>

      <div className={styles.viewSwitcher}>
        <button
          className={`${styles.viewBtn} ${view === 'grid' ? styles.viewActive : ''}`}
          onClick={() => onViewChange('grid')}
          aria-label="Grid view"
        >
          <LuLayoutGrid size={14} />
          Grid
        </button>
        <button
          className={`${styles.viewBtn} ${view === 'table' ? styles.viewActive : ''}`}
          onClick={() => onViewChange('table')}
          aria-label="Table view"
        >
          <LuTable2 size={14} />
          Table
        </button>
      </div>
    </div>
  );
}
