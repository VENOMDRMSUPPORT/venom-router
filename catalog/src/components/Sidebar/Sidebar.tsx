import { NavLink } from 'react-router-dom';
import { LuLayoutGrid, LuHistory } from "react-icons/lu";
import { useCatalog } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import styles from './Sidebar.module.css';

interface SidebarProps {
  open: boolean;
  onClose: () => void;
}

export function Sidebar({ open, onClose }: SidebarProps) {
  // The provider list comes from the API like everything else: a hardcoded nav
  // would go stale the first time a provider is added.
  const { data } = useCatalog();
  const providers = data?.providers ?? [];

  return (
    <>
      <aside
        className={`${styles.sidebar} ${open ? styles.open : ''}`}
        aria-label="Catalog navigation"
      >
        <div className={styles.header}>
          <img
            src="/assets/catalog-3d-logo.jpg"
            alt="Venom Catalog"
            className={styles.logo}
          />
          <div className={styles.brandText}>
            <span className={styles.brandName}>Venom Catalog</span>
            <span className={styles.brandDesc}>AI Model Matrix</span>
          </div>
        </div>

        <nav className={styles.nav}>
          <div className={styles.section}>
            <span className={styles.sectionTitle}>Overview</span>
            <NavLink
              to="/"
              end
              className={({ isActive }) =>
                `${styles.item} ${isActive ? styles.active : ''}`
              }
              onClick={onClose}
            >
              <LuLayoutGrid size={15} />
              <span>All Providers</span>
            </NavLink>
            <NavLink
              to="/changes"
              className={({ isActive }) =>
                `${styles.item} ${isActive ? styles.active : ''}`
              }
              onClick={onClose}
            >
              <LuHistory size={15} />
              <span>What's New</span>
            </NavLink>
          </div>

          <div className={styles.section}>
            <span className={styles.sectionTitle}>Providers</span>
            {providers.map((p) => (
              <NavLink
                key={p.id}
                to={`/provider/${p.id}`}
                className={({ isActive }) =>
                  `${styles.item} ${isActive ? styles.active : ''}`
                }
                onClick={onClose}
              >
                {present(p.id).logo && (
                  <img
                    src={present(p.id).logo}
                    alt=""
                    className={`${styles.navLogo} ${present(p.id).invertInDark ? 'logo-invert-dark' : ''}`}
                  />
                )}
                <span className={styles.itemLabel}>{p.name}</span>
                <span className={styles.pill}>{p.liveModels}</span>
              </NavLink>
            ))}
          </div>
        </nav>
      </aside>

      {open && (
        <div className={styles.overlay} onClick={onClose} aria-hidden="true" />
      )}
    </>
  );
}
