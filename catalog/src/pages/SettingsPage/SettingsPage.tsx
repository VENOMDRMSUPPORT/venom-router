import { useEffect, useMemo, useState } from 'react';
import {
  LuArrowUpRight,
  LuBellRing,
  LuCheck,
  LuCloudDownload,
  LuDatabase,
  LuInfo,
  LuLayoutGrid,
  LuLoaderCircle,
  LuMoon,
  LuPalette,
  LuRefreshCw,
  LuRotateCcw,
  LuSun,
  LuTable2,
  LuUserRound,
  LuWandSparkles,
  LuSlidersHorizontal,
} from 'react-icons/lu';
import { Link } from 'react-router-dom';
import { useCatalog } from '../../hooks/useCatalog';
import styles from './SettingsPage.module.css';

type CatalogSettings = {
  theme: 'dark' | 'light';
  defaultView: 'grid' | 'table';
  reduceMotion: boolean;
};

const SETTINGS_KEY = 'venom-catalog-settings';

const DEFAULT_SETTINGS: CatalogSettings = {
  theme: 'dark',
  defaultView: 'table',
  reduceMotion: false,
};

const SECTIONS = [
  { id: 'workspace', label: 'Workspace', icon: LuUserRound },
  { id: 'experience', label: 'Experience', icon: LuPalette },
  { id: 'catalog', label: 'Catalog defaults', icon: LuLayoutGrid },
  { id: 'updates', label: 'Updates', icon: LuBellRing },
  { id: 'data', label: 'Data & control', icon: LuDatabase },
];

function readSettings(): CatalogSettings {
  if (typeof window === 'undefined') return DEFAULT_SETTINGS;

  try {
    const parsed = JSON.parse(window.localStorage.getItem(SETTINGS_KEY) ?? '{}') as Partial<CatalogSettings>;
    return {
      theme: parsed.theme === 'light' ? 'light' : 'dark',
      defaultView: parsed.defaultView === 'grid' ? 'grid' : 'table',
      reduceMotion: Boolean(parsed.reduceMotion),
    };
  } catch {
    return DEFAULT_SETTINGS;
  }
}

function formatSyncTime(value: string | null): string {
  if (!value) return 'No completed run recorded';

  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value));
}

export function SettingsPage() {
  const { data, loading, error, reload } = useCatalog();
  const [settings, setSettings] = useState<CatalogSettings>(readSettings);
  const [saved, setSaved] = useState(false);
  const [syncState, setSyncState] = useState<'idle' | 'running' | 'success' | 'error'>('idle');
  const [syncMessage, setSyncMessage] = useState('');
  const [activeSection, setActiveSection] = useState('workspace');

  useEffect(() => {
    window.localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings));
    window.localStorage.setItem('catalog-theme', settings.theme);
    document.documentElement.setAttribute('data-theme', settings.theme);
    document.documentElement.dataset.reduceMotion = settings.reduceMotion ? 'true' : 'false';
    window.dispatchEvent(new CustomEvent('catalog-theme-change', { detail: settings.theme }));

    setSaved(true);
    const timeout = window.setTimeout(() => setSaved(false), 1200);
    return () => window.clearTimeout(timeout);
  }, [settings]);

  useEffect(() => {
    if (typeof window === 'undefined' || !('IntersectionObserver' in window)) return;

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setActiveSection(entry.target.id);
          }
        }
      },
      {
        rootMargin: '-80px 0px -55% 0px',
        threshold: 0.1,
      }
    );

    const sectionElements = document.querySelectorAll('section[id]');
    sectionElements.forEach((el) => observer.observe(el));

    return () => observer.disconnect();
  }, []);

  const updateSetting = <K extends keyof CatalogSettings>(key: K, value: CatalogSettings[K]) => {
    setSettings((current) => ({ ...current, [key]: value }));
  };

  const { modelsCount, providerCount, staleProviders, lastSuccessfulSync } = useMemo(() => {
    const providers = data?.providers ?? [];
    const times = providers
      .map((provider) => provider.lastSuccessfulSyncAt)
      .filter((value): value is string => Boolean(value))
      .sort();

    return {
      modelsCount: data?.meta?.liveModels ?? providers.reduce((total, provider) => total + provider.liveModels, 0),
      providerCount: providers.length,
      staleProviders: providers.filter((provider) => provider.freshness !== 'fresh').length,
      lastSuccessfulSync: times.at(-1) ?? null,
    };
  }, [data]);

  const exportPreferences = () => {
    const blob = new Blob([JSON.stringify(settings, null, 2)], { type: 'application/json' });
    const url = window.URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'venom-catalog-preferences.json';
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.URL.revokeObjectURL(url);
  };

  const resetPreferences = () => {
    if (!window.confirm('Reset your saved catalog preferences to the defaults?')) return;
    setSettings(DEFAULT_SETTINGS);
    window.localStorage.removeItem('venom-catalog-provider-filters');
  };

  const requestSync = async () => {
    if (!window.confirm('Start a catalog synchronization now? The catalog will retain its last successful data if this run fails.')) {
      return;
    }

    setSyncState('running');
    setSyncMessage('');

    try {
      const response = await fetch('/v1/sync', { method: 'POST' });
      const body = (await response.json().catch(() => ({}))) as { error?: string };
      if (!response.ok) throw new Error(body.error ?? 'The synchronization request was not accepted.');

      setSyncState('success');
      setSyncMessage('Synchronization started. The catalog will refresh when the run completes.');
      window.setTimeout(() => void reload(), 1500);
    } catch (syncError) {
      setSyncState('error');
      setSyncMessage(syncError instanceof Error ? syncError.message : 'Unable to start synchronization.');
    }
  };

  const scrollToSection = (id: string) => {
    setActiveSection(id);
    const element = document.getElementById(id);
    if (element) {
      element.scrollIntoView({ behavior: 'smooth' });
    }
  };

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div className={styles.headerTop}>
          <h1 className={styles.title}>Workspace settings</h1>
          <div className={styles.saveState} aria-live="polite">
            {saved ? <><LuCheck size={14} /> Saved</> : 'Preferences are current'}
          </div>
        </div>
        <p className={styles.subtitle}>
          Control the parts of Venom Catalog that are available in this workspace. Changes are saved in this browser and apply immediately.
        </p>
      </header>

      <div className={styles.layout}>
        <aside className={styles.sectionNav} aria-label="Settings sections">
          <div className={styles.navHeader}>
            <div className={styles.navHeaderTitleRow}>
              <span className={styles.navHeaderIcon}>
                <LuSlidersHorizontal size={14} />
              </span>
              <span className={styles.navHeaderTitle}>Preferences</span>
            </div>
            <span className={styles.navHeaderBadge}>{SECTIONS.length} sections</span>
          </div>

          <nav className={styles.navItemsList}>
            {SECTIONS.map((s) => {
              const Icon = s.icon;
              const isActive = activeSection === s.id;
              return (
                <a
                  key={s.id}
                  href={`#${s.id}`}
                  className={`${styles.navLink} ${isActive ? styles.navLinkActive : ''}`}
                  onClick={(e) => {
                    e.preventDefault();
                    scrollToSection(s.id);
                  }}
                >
                  <Icon size={15} className={styles.navLinkIcon} />
                  <span>{s.label}</span>
                  {isActive && <span className={styles.activeDot} />}
                </a>
              );
            })}
          </nav>
        </aside>

        <div className={styles.content}>
          <SettingsSection id="workspace" icon={<LuUserRound size={18} />} title="Workspace" description="Identity and access information supplied by the current catalog workspace.">
            <div className={styles.identityCard}>
              <div className={styles.avatar}>EN</div>
              <div>
                <strong>Engineering workspace</strong>
                <span>Internal catalog · local workspace profile</span>
              </div>
              <span className={styles.readonly}>Read-only</span>
            </div>
            <div className={styles.inlineNotice}>
              <LuInfo size={16} />
              <span>This catalog does not manage passwords or account profiles. Workspace identity is controlled outside the catalog.</span>
            </div>
          </SettingsSection>

          <SettingsSection id="experience" icon={<LuPalette size={18} />} title="Experience" description="Choose a display mode that is comfortable for focused catalog work.">
            <div className={styles.choiceGrid}>
              <ChoiceCard
                active={settings.theme === 'dark'}
                icon={<LuMoon size={18} />}
                title="Dark"
                description="High-focus, low-light workspace."
                onClick={() => updateSetting('theme', 'dark')}
              />
              <ChoiceCard
                active={settings.theme === 'light'}
                icon={<LuSun size={18} />}
                title="Light"
                description="Brighter canvas for daytime review."
                onClick={() => updateSetting('theme', 'light')}
              />
            </div>
            <ToggleRow
              title="Reduce interface motion"
              description="Keeps state changes calm and removes non-essential motion."
              checked={settings.reduceMotion}
              onChange={(checked) => updateSetting('reduceMotion', checked)}
            />
          </SettingsSection>

          <SettingsSection id="catalog" icon={<LuLayoutGrid size={18} />} title="Catalog defaults" description="These preferences shape the first view you see on provider pages without changing source data or eligibility rules.">
            <div className={styles.choiceGrid}>
              <ChoiceCard
                active={settings.defaultView === 'table'}
                icon={<LuTable2 size={18} />}
                title="Table by default"
                description="Compare context, pricing, and evidence at a glance."
                onClick={() => updateSetting('defaultView', 'table')}
              />
              <ChoiceCard
                active={settings.defaultView === 'grid'}
                icon={<LuLayoutGrid size={18} />}
                title="Grid by default"
                description="Use a roomier visual scan for model discovery."
                onClick={() => updateSetting('defaultView', 'grid')}
              />
            </div>
          </SettingsSection>

          <SettingsSection id="updates" icon={<LuBellRing size={18} />} title="Updates" description="Changes are published in the catalog’s activity feed when source-backed model data changes.">
            <div className={styles.updateCard}>
              <div className={styles.updateIcon}><LuWandSparkles size={18} /></div>
              <div>
                <strong>Change feed is active</strong>
                <p>Review additions, removals, and verified field changes from the same audit trail used by the catalog.</p>
              </div>
              <Link to="/changes" className={styles.textLink}>Open What’s New <LuArrowUpRight size={14} /></Link>
            </div>
            <div className={styles.inlineNotice}>
              <LuInfo size={16} />
              <span>Email and push controls are intentionally hidden because this workspace does not have an active delivery channel.</span>
            </div>
          </SettingsSection>

          <SettingsSection id="data" icon={<LuDatabase size={18} />} title="Data & control" description="Review the current catalog health, manage this browser’s preferences, and request a fresh source synchronization.">
            <div className={styles.healthGrid}>
              <HealthMetric label="Live models" value={loading ? '…' : String(modelsCount)} />
              <HealthMetric label="Providers" value={loading ? '…' : String(providerCount)} />
              <HealthMetric label="Needs attention" value={loading ? '…' : String(staleProviders)} tone={staleProviders > 0 ? 'warning' : 'good'} />
              <HealthMetric label="Last successful sync" value={loading ? '…' : formatSyncTime(lastSuccessfulSync)} compact />
            </div>
            {error && <div className={`${styles.statusMessage} ${styles.error}`}><LuInfo size={16} /> Live catalog data could not be refreshed: {error}</div>}
            {syncMessage && <div className={`${styles.statusMessage} ${syncState === 'error' ? styles.error : styles.success}`}><LuInfo size={16} /> {syncMessage}</div>}
            <div className={styles.actionRows}>
              <div>
                <strong>Refresh the catalog</strong>
                <span>Runs the server-side source workflow. Failed runs keep the last successful catalog intact.</span>
              </div>
              <button type="button" className={styles.primaryButton} onClick={requestSync} disabled={syncState === 'running'}>
                {syncState === 'running' ? <LuLoaderCircle className={styles.spin} size={16} /> : <LuRefreshCw size={16} />}
                {syncState === 'running' ? 'Starting sync…' : 'Run sync'}
              </button>
            </div>
            <div className={styles.actionRows}>
              <div>
                <strong>Export preferences</strong>
                <span>Downloads this browser’s current Venom Catalog settings as JSON.</span>
              </div>
              <button type="button" className={styles.secondaryButton} onClick={exportPreferences}><LuCloudDownload size={16} /> Export</button>
            </div>
            <div className={styles.actionRows}>
              <div>
                <strong>Reset browser preferences</strong>
                <span>Restores the theme, motion, and provider-page defaults used in this browser.</span>
              </div>
              <button type="button" className={styles.dangerButton} onClick={resetPreferences}><LuRotateCcw size={16} /> Reset</button>
            </div>
          </SettingsSection>
        </div>
      </div>
    </div>
  );
}

function SettingsSection({ id, icon, title, description, children }: { id: string; icon: React.ReactNode; title: string; description: string; children: React.ReactNode }) {
  return (
    <section className={styles.sectionCard} id={id}>
      <div className={styles.sectionHeader}>
        <span className={styles.sectionIcon}>{icon}</span>
        <div className={styles.sectionHeaderText}>
          <h2 className={styles.sectionTitle}>{title}</h2>
          <p className={styles.sectionDesc}>{description}</p>
        </div>
      </div>
      <div className={styles.sectionBody}>{children}</div>
    </section>
  );
}

function ChoiceCard({ active, icon, title, description, onClick }: { active: boolean; icon: React.ReactNode; title: string; description: string; onClick: () => void }) {
  return (
    <button type="button" className={`${styles.choiceCard} ${active ? styles.choiceActive : ''}`} onClick={onClick} aria-pressed={active}>
      <span className={styles.choiceIcon}>{icon}</span>
      <div className={styles.choiceText}>
        <strong className={styles.choiceTitle}>{title}</strong>
        <span className={styles.choiceDesc}>{description}</span>
      </div>
      {active && (
        <span className={styles.choiceCheckBadge} aria-hidden="true">
          <LuCheck size={13} strokeWidth={3} />
        </span>
      )}
    </button>
  );
}

function ToggleRow({ title, description, checked, onChange }: { title: string; description: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <div className={styles.toggleRow}>
      <div><strong>{title}</strong><span>{description}</span></div>
      <button type="button" className={`${styles.toggle} ${checked ? styles.toggleOn : ''}`} role="switch" aria-checked={checked} aria-label={title} onClick={() => onChange(!checked)}><span /></button>
    </div>
  );
}

function HealthMetric({ label, value, tone, compact }: { label: string; value: string; tone?: 'good' | 'warning'; compact?: boolean }) {
  return <div className={`${styles.healthMetric} ${compact ? styles.healthMetricWide : ''}`}><span>{label}</span><strong className={tone ? styles[tone] : ''}>{value}</strong></div>;
}
