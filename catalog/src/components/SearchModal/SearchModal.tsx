import { useState, useEffect, useRef, useMemo, type KeyboardEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  LuSearch,
  LuX,
  LuCpu,
  LuLayoutGrid,
  LuHistory,
  LuSettings,
  LuArrowRight,
  LuLayers,
} from 'react-icons/lu';
import { useCatalog } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { formatTokens } from '../../api/client';
import styles from './SearchModal.module.css';

interface SearchModalProps {
  isOpen: boolean;
  onClose: () => void;
}

type SearchResultItem =
  | {
      type: 'page';
      id: string;
      title: string;
      subtitle: string;
      path: string;
      icon: React.ReactNode;
    }
  | {
      type: 'provider';
      id: string;
      title: string;
      subtitle: string;
      path: string;
      modelCount: number;
      logo?: string | null;
      invertInDark?: boolean;
    }
  | {
      type: 'model';
      id: string;
      title: string;
      subtitle: string;
      path: string;
      providerName: string;
      providerId: string;
      contextTokens: number | null;
      vqDisplay?: string | null;
      isFree?: boolean | null;
      priceIn?: number | null;
    };

const PAGES = [
  {
    type: 'page' as const,
    id: 'page-dashboard',
    title: 'Dashboard',
    subtitle: 'All AI model providers & benchmark matrix overview',
    path: '/',
    icon: <LuLayoutGrid size={16} />,
  },
  {
    type: 'page' as const,
    id: 'page-changes',
    title: "What's New",
    subtitle: 'Audit log of model additions, retirements & price changes',
    path: '/changes',
    icon: <LuHistory size={16} />,
  },
  {
    type: 'page' as const,
    id: 'page-settings',
    title: 'Workspace Settings',
    subtitle: 'Preferences, themes, and workspace controls',
    path: '/settings',
    icon: <LuSettings size={16} />,
  },
];

export function SearchModal({ isOpen, onClose }: SearchModalProps) {
  const navigate = useNavigate();
  const { data } = useCatalog();
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Auto focus input on open and reset state
  useEffect(() => {
    if (isOpen) {
      setQuery('');
      setSelectedIndex(0);
      const timer = setTimeout(() => {
        inputRef.current?.focus();
      }, 50);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  // Global Esc key listener
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  // Compute search results
  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) {
      // Default initial view: Pages and top providers
      const topProviders: SearchResultItem[] = (data?.providers ?? []).slice(0, 5).map((p) => {
        const pres = present(p.id);
        return {
          type: 'provider',
          id: `provider-${p.id}`,
          title: p.name,
          subtitle: pres.blurb || `${p.liveModels} models available`,
          path: `/provider/${p.id}`,
          modelCount: p.liveModels,
          logo: pres.logo,
          invertInDark: pres.invertInDark,
        };
      });

      return [...PAGES, ...topProviders];
    }

    const matchedPages: SearchResultItem[] = PAGES.filter(
      (p) => p.title.toLowerCase().includes(q) || p.subtitle.toLowerCase().includes(q)
    );

    const matchedProviders: SearchResultItem[] = (data?.providers ?? [])
      .filter(
        (p) =>
          p.name.toLowerCase().includes(q) ||
          p.id.toLowerCase().includes(q) ||
          (present(p.id).blurb?.toLowerCase().includes(q) ?? false)
      )
      .slice(0, 4)
      .map((p) => {
        const pres = present(p.id);
        return {
          type: 'provider',
          id: `provider-${p.id}`,
          title: p.name,
          subtitle: pres.blurb || `${p.liveModels} models available`,
          path: `/provider/${p.id}`,
          modelCount: p.liveModels,
          logo: pres.logo,
          invertInDark: pres.invertInDark,
        };
      });

    const matchedModels: SearchResultItem[] = (data?.models ?? [])
      .filter((m) => {
        // Users search the name the provider's app shows, not the raw id, so
        // both must match. "ox alpha" finds x-preview-f-free; "x-preview" still
        // does, because the id stays searchable.
        const idMatch = m.modelId.toLowerCase().includes(q);
        const nameMatch = m.displayName?.toLowerCase().includes(q) ?? false;
        const canonMatch = m.canonicalId?.toLowerCase().includes(q) ?? false;
        const provMatch = m.providerId.toLowerCase().includes(q);
        return idMatch || nameMatch || canonMatch || provMatch;
      })
      .slice(0, 10)
      .map((m) => {
        const prov = data?.providers.find((p) => p.id === m.providerId);
        return {
          type: 'model',
          id: `model-${m.providerId}-${m.modelId}`,
          title: m.displayName || m.modelId,
          subtitle: m.canonicalId && m.canonicalId !== m.modelId ? `Proven as ${m.canonicalId}` : '',
          path: `/provider/${m.providerId}?model=${encodeURIComponent(m.modelId)}`,
          providerName: prov?.name ?? m.providerId,
          providerId: m.providerId,
          contextTokens: m.contextTokens,
          vqDisplay: m.vq.value !== null ? m.vq.display : null,
          isFree: m.pricing.isFree,
          priceIn: m.pricing.inputPerMTokens,
        };
      });

    return [...matchedPages, ...matchedProviders, ...matchedModels];
  }, [query, data]);

  // Keep selected index in range
  useEffect(() => {
    setSelectedIndex(0);
  }, [results.length]);

  // Scroll active item into view
  useEffect(() => {
    if (!listRef.current) return;
    const activeEl = listRef.current.querySelector<HTMLElement>(`[data-selected="true"]`);
    if (activeEl && typeof activeEl.scrollIntoView === 'function') {
      activeEl.scrollIntoView({ block: 'nearest' });
    }
  }, [selectedIndex]);

  const handleSelect = (item: SearchResultItem) => {
    navigate(item.path);
    onClose();
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex((prev) => (prev + 1) % Math.max(1, results.length));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex((prev) => (prev - 1 + results.length) % Math.max(1, results.length));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (results[selectedIndex]) {
        handleSelect(results[selectedIndex]);
      }
    }
  };

  if (!isOpen) return null;

  return (
    <div className={styles.backdrop} onClick={onClose} role="dialog" aria-modal="true" aria-label="Search catalog">
      <div className={styles.dialog} onClick={(e) => e.stopPropagation()}>
        <div className={styles.searchBar}>
          <LuSearch size={18} className={styles.searchIcon} />
          <input
            ref={inputRef}
            type="text"
            className={styles.input}
            placeholder="Search models, providers, pages..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={handleKeyDown}
            aria-autocomplete="list"
          />
          {query && (
            <button
              type="button"
              className={styles.clearBtn}
              onClick={() => {
                setQuery('');
                inputRef.current?.focus();
              }}
              title="Clear search"
            >
              <LuX size={14} />
            </button>
          )}
          <kbd className={styles.escBadge} onClick={onClose}>ESC</kbd>
        </div>

        <div className={styles.resultsList} ref={listRef}>
          {results.length === 0 ? (
            <div className={styles.emptyState}>
              <LuCpu size={28} className={styles.emptyIcon} />
              <p className={styles.emptyTitle}>No matching catalog items found</p>
              <span className={styles.emptyDesc}>Try searching for a model name (e.g. "claude", "llama"), provider, or page</span>
            </div>
          ) : (
            results.map((item, idx) => {
              const isSelected = idx === selectedIndex;

              if (item.type === 'page') {
                return (
                  <div
                    key={item.id}
                    className={`${styles.resultItem} ${isSelected ? styles.selectedItem : ''}`}
                    data-selected={isSelected}
                    onClick={() => handleSelect(item)}
                    onMouseEnter={() => setSelectedIndex(idx)}
                  >
                    <div className={styles.itemIconBox}>{item.icon}</div>
                    <div className={styles.itemContent}>
                      <span className={styles.itemTitle}>{item.title}</span>
                      <span className={styles.itemSubtitle}>{item.subtitle}</span>
                    </div>
                    <span className={styles.itemTag}>Page</span>
                    <LuArrowRight size={14} className={styles.actionArrow} />
                  </div>
                );
              }

              if (item.type === 'provider') {
                return (
                  <div
                    key={item.id}
                    className={`${styles.resultItem} ${isSelected ? styles.selectedItem : ''}`}
                    data-selected={isSelected}
                    onClick={() => handleSelect(item)}
                    onMouseEnter={() => setSelectedIndex(idx)}
                  >
                    <div className={styles.itemIconBox}>
                      {item.logo ? (
                        <img
                          src={item.logo}
                          alt=""
                          className={`${styles.itemLogo} ${item.invertInDark ? 'logo-invert-dark' : ''}`}
                        />
                      ) : (
                        <LuCpu size={16} />
                      )}
                    </div>
                    <div className={styles.itemContent}>
                      <span className={styles.itemTitle}>{item.title}</span>
                      <span className={styles.itemSubtitle}>{item.subtitle}</span>
                    </div>
                    <span className={styles.modelCountBadge}>{item.modelCount} models</span>
                    <LuArrowRight size={14} className={styles.actionArrow} />
                  </div>
                );
              }

              if (item.type === 'model') {
                return (
                  <div
                    key={item.id}
                    className={`${styles.resultItem} ${isSelected ? styles.selectedItem : ''}`}
                    data-selected={isSelected}
                    onClick={() => handleSelect(item)}
                    onMouseEnter={() => setSelectedIndex(idx)}
                  >
                    <div className={styles.itemIconBox}>
                      <LuLayers size={16} />
                    </div>
                    <div className={styles.itemContent}>
                      <div className={styles.itemTitleRow}>
                        <span className={styles.itemTitle}>{item.title}</span>
                        <span className={styles.providerPill}>{item.providerName}</span>
                      </div>
                      {item.subtitle && <span className={styles.itemSubtitle}>{item.subtitle}</span>}
                    </div>
                    <div className={styles.modelMeta}>
                      {item.contextTokens && (
                        <span className={styles.metaBadge}>{formatTokens(item.contextTokens)}</span>
                      )}
                      {item.vqDisplay && (
                        <span className={styles.vqBadge}>VQ {item.vqDisplay}</span>
                      )}
                      {item.isFree ? (
                        <span className={styles.freeBadge}>Free</span>
                      ) : item.priceIn !== null && item.priceIn !== undefined ? (
                        <span className={styles.priceBadge}>${item.priceIn}/M</span>
                      ) : null}
                    </div>
                    <LuArrowRight size={14} className={styles.actionArrow} />
                  </div>
                );
              }

              return null;
            })
          )}
        </div>

        <div className={styles.footer}>
          <div className={styles.footerHints}>
            <span><kbd>↑</kbd> <kbd>↓</kbd> navigate</span>
            <span><kbd>↵</kbd> select</span>
            <span><kbd>esc</kbd> close</span>
          </div>
          <span className={styles.footerCount}>{results.length} results</span>
        </div>
      </div>
    </div>
  );
}
