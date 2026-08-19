import { useState, useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import { LuLayoutGrid, LuHistory, LuSearch, LuCpu, LuInfo } from 'react-icons/lu';
import { useCatalog } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { ThemeToggle } from '../ThemeToggle/ThemeToggle';
import { SearchModal } from '../SearchModal/SearchModal';
import type { Theme } from '../../hooks/useTheme';
import styles from './Header.module.css';

interface HeaderProps {
  theme: Theme;
  onToggleTheme: () => void;
}

export function Header({ theme, onToggleTheme }: HeaderProps) {
  const location = useLocation();
  const { data } = useCatalog();
  const [isSearchOpen, setIsSearchOpen] = useState(false);

  const path = location.pathname;
  let title = 'AI Model Catalogs';
  let subtitle = 'Live model inventories & benchmark matrix';
  let iconNode: React.ReactNode = <LuLayoutGrid size={18} className={styles.pageIcon} />;

  if (path === '/') {
    title = 'AI Model Catalogs';
    subtitle = 'Live model inventories & benchmark matrix';
    iconNode = <LuLayoutGrid size={18} className={styles.pageIcon} />;
  } else if (path.startsWith('/provider/')) {
    const providerId = path.split('/provider/')[1];
    const provider = data?.providers.find((p) => p.id === providerId);
    const pres = present(providerId);
    title = provider?.name ?? providerId;
    subtitle = pres.blurb || 'Provider model roster & quality evidence';
    if (pres.logo) {
      iconNode = (
        <img
          src={pres.logo}
          alt=""
          className={`${styles.providerLogo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`}
        />
      );
    } else {
      iconNode = <LuCpu size={18} className={styles.pageIcon} />;
    }
  } else if (path === '/changes') {
    title = "What's New";
    subtitle = 'Audit log of model additions, retirements & price changes';
    iconNode = <LuHistory size={18} className={styles.pageIcon} />;
  } else {
    title = 'Venom Catalog';
    subtitle = 'AI Model Matrix';
    iconNode = <LuInfo size={18} className={styles.pageIcon} />;
  }

  // Global keyboard shortcut for Ctrl+K / Cmd+K
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setIsSearchOpen((prev) => !prev);
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);

  return (
    <>
      <header className={styles.header}>
        <div className={styles.left}>
          <div className={styles.iconWrapper}>{iconNode}</div>
          <div className={styles.pageMeta}>
            <h1 className={styles.pageTitle}>{title}</h1>
            <span className={styles.pageDesc}>{subtitle}</span>
          </div>
        </div>

        <div className={styles.right}>
          <button
            type="button"
            className={styles.searchBtn}
            onClick={() => setIsSearchOpen(true)}
            title="Search catalog (Ctrl+K)"
            aria-label="Search catalog"
          >
            <LuSearch size={15} />
            <span className={styles.searchLabel}>Search catalog...</span>
            <kbd className={styles.shortcut}>⌘K</kbd>
          </button>

          <div className={styles.toggleWrapper}>
            <ThemeToggle theme={theme} onToggle={onToggleTheme} />
          </div>

          <div className={styles.profile} title="Developer Profile">
            <img src="/assets/user-avatar.png" alt="Profile Avatar" className={styles.avatar} />
            <span className={styles.statusDot} />
          </div>
        </div>
      </header>

      <SearchModal isOpen={isSearchOpen} onClose={() => setIsSearchOpen(false)} />
    </>
  );
}
