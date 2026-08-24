import { useEffect, useState, type ReactNode } from 'react';
import { useLocation } from 'react-router-dom';
import { LuLayoutGrid, LuHistory, LuSearch, LuCpu, LuInfo, LuDatabase } from 'react-icons/lu';
import { useCatalog } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { ThemeToggle } from '../ThemeToggle/ThemeToggle';
import { SearchModal } from '../SearchModal/SearchModal';
import { NotificationCenter } from '../NotificationCenter/NotificationCenter';
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
  const activeProviderId = path.startsWith('/provider/') ? path.split('/provider/')[1] : undefined;
  let title = 'AI Model Catalogs';
  let subtitle = 'Live model inventories & benchmark matrix';
  let iconNode: ReactNode = <LuLayoutGrid size={16} className={styles.pageIcon} />;
  let breadcrumbs: Array<{ label: string; to?: string }> | null = null;

  if (path === '/') {
    title = 'AI Model Catalogs';
    subtitle = 'Live model inventories & benchmark matrix';
    iconNode = <LuLayoutGrid size={16} className={styles.pageIcon} />;
  } else if (activeProviderId) {
    const provider = data?.providers.find((p) => p.id === activeProviderId);
    const pres = present(activeProviderId);
    title = provider?.name ?? activeProviderId;
    subtitle = pres.blurb || 'Provider model roster & quality evidence';
    breadcrumbs = [
      { label: 'Overview', to: '/' },
      { label: 'Providers', to: '/' },
      { label: title },
    ];
    if (pres.logo) {
      iconNode = (
        <img
          src={pres.logo}
          alt=""
          className={`${styles.providerLogo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`}
        />
      );
    } else {
      iconNode = <LuCpu size={16} className={styles.pageIcon} />;
    }
  } else if (path === '/database') {
    title = 'Database Browser';
    subtitle = 'Safe read-only view of Catalog SQLite data';
    iconNode = <LuDatabase size={16} className={styles.pageIcon} />;
    breadcrumbs = [
      { label: 'Overview', to: '/' },
      { label: 'Database Browser' },
    ];
  } else if (path === '/changes') {
    title = "What's New";
    subtitle = 'Audit log of model additions, retirements & price changes';
    iconNode = <LuHistory size={16} className={styles.pageIcon} />;
    breadcrumbs = [
      { label: 'Overview', to: '/' },
      { label: "What's New" },
    ];
  } else {
    title = 'Venom Catalog';
    subtitle = 'AI Model Matrix';
    iconNode = <LuInfo size={16} className={styles.pageIcon} />;
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
            {breadcrumbs ? (
              <nav className={styles.breadcrumbs} aria-label="Breadcrumb">
                {breadcrumbs.map((b, idx) => (
                  <span key={b.label} className={styles.breadcrumbItem}>
                    {idx > 0 && <span className={styles.breadcrumbDivider}>/</span>}
                    {idx === breadcrumbs.length - 1 ? (
                      <span className={styles.breadcrumbActive}>{b.label}</span>
                    ) : (
                      <span className={styles.breadcrumbLink}>{b.label}</span>
                    )}
                  </span>
                ))}
              </nav>
            ) : (
              <h1 className={styles.pageTitle}>{title}</h1>
            )}
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
            <NotificationCenter providerId={activeProviderId} />
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
