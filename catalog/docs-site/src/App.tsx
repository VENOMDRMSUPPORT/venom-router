import { useEffect, useMemo, useState } from 'react';
import { Link, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { CATALOG_API_CONTRACT_VERSION } from '../../config/api-contract';
import { pages, pageBySlug, pageGroups, renderMarkdown, searchPages, tocFor } from './content';
import './styles.css';

type DocsProps = { embedded?: boolean };
type DocsLink = (path: string) => string;

function Brand({ href }: { href: string }) {
  return (
    <Link className="brand" to={href} aria-label="Venom Catalog documentation home">
      <span className="brand-mark">V</span>
      <span><strong>Venom</strong><small>Catalog Docs</small></span>
    </Link>
  );
}

function SearchBox({ compact = false, href }: { compact?: boolean; href: DocsLink }) {
  const navigate = useNavigate();
  const [query, setQuery] = useState('');
  const results = useMemo(() => searchPages(query), [query]);
  return (
    <div className={`search ${compact ? 'search-compact' : ''}`}>
      <span className="search-icon" aria-hidden="true">⌕</span>
      <input value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => {
        if (event.key === 'Enter' && results[0]) { navigate(href(results[0].page.slug)); setQuery(''); }
        if (event.key === 'Escape') setQuery('');
      }} placeholder="Search docs" aria-label="Search documentation" />
      <kbd>⌘ K</kbd>
      {query && <div className="search-results" role="listbox" aria-label="Search results">
        {results.length ? results.map(({ page, snippet }) => <Link key={page.slug} to={href(page.slug)} onClick={() => setQuery('')} role="option">
          <span className="result-section">{page.section}</span><strong>{page.title}</strong><small>{snippet}…</small>
        </Link>) : <div className="search-empty">No matching pages.</div>}
      </div>}
    </div>
  );
}

function Sidebar({ current, open, onClose, href }: { current: string; open: boolean; onClose: () => void; href: DocsLink }) {
  const groups = pageGroups();
  return <>
    {open && <button className="scrim" aria-label="Close navigation" onClick={onClose} />}
    <aside className={`docs-sidebar ${open ? 'is-open' : ''}`}>
      <div className="sidebar-top"><Brand href={href('/')} /><button className="sidebar-close" onClick={onClose} aria-label="Close navigation">×</button></div>
      <nav aria-label="Documentation navigation">
        {Object.entries(groups).map(([section, entries]) => <div className="nav-group" key={section}>
          <div className="nav-label">{section}</div>
          {entries.map((page) => <Link key={page.slug} to={href(page.slug)} onClick={onClose} className={current === page.slug ? 'active' : ''} aria-current={current === page.slug ? 'page' : undefined}>
            <span className="nav-dot" />{page.title}
          </Link>)}
        </div>)}
      </nav>
      <div className="sidebar-footer"><div className="version-pill" title="Current API contract"><span className="status-dot" />{CATALOG_API_CONTRACT_VERSION}</div><p>Public documentation for the current local Catalog boundary.</p></div>
    </aside>
  </>;
}

function Topbar({ onMenu, href }: { onMenu: () => void; href: DocsLink }) {
  const catalogUrl = import.meta.env.VITE_CATALOG_URL;
  const repositoryUrl = import.meta.env.VITE_REPO_URL;
  return <header className="topbar">
    <button className="mobile-menu" onClick={onMenu} aria-label="Open documentation navigation">☰</button>
    <div className="topbar-brand"><Brand href={href('/')} /></div>
    <div className="topbar-search"><SearchBox compact href={href} /></div>
    <nav className="top-links" aria-label="External navigation">
      {catalogUrl && <a href={catalogUrl}>Open Catalog <span aria-hidden="true">↗</span></a>}
      {repositoryUrl && <a href={repositoryUrl} target="_blank" rel="noreferrer">GitHub <span aria-hidden="true">↗</span></a>}
    </nav>
  </header>;
}

function PageHeader({ page, href }: { page: ReturnType<typeof pageBySlug>; href: DocsLink }) {
  if (!page) return null;
  return <><div className="breadcrumbs"><Link to={href('/')}>Docs</Link><span>/</span><span>{page.section}</span></div><div className="page-eyebrow">{page.section}<span>·</span><span>Updated with the product contract</span></div></>;
}

function DocPage({ slug, href, linkPrefix }: { slug: string; href: DocsLink; linkPrefix: string }) {
  const page = pageBySlug(slug);
  const location = useLocation();
  const [copied, setCopied] = useState(false);
  useEffect(() => { if (location.hash) document.getElementById(location.hash.slice(1))?.scrollIntoView(); }, [location.hash]);
  if (!page) return <NotFound href={href} />;
  const toc = tocFor(page.markdown);
  const index = pages.findIndex((item) => item.slug === page.slug);
  const previous = pages[index - 1];
  const next = pages[index + 1];
  const html = renderMarkdown(page.markdown, linkPrefix);
  const copyPageLink = async () => { await navigator.clipboard?.writeText(window.location.href); setCopied(true); window.setTimeout(() => setCopied(false), 1400); };
  return <div className="page-frame">
    <PageHeader page={page} href={href} />
    <div className="reading-layout"><article className="doc-content">
      <div className="doc-title-row"><div><h1>{page.title}</h1><p className="lead">{page.description}</p></div><button className="copy-link" onClick={copyPageLink} title="Copy page link">{copied ? 'Copied' : 'Copy link'}</button></div>
      <div className="prose" dangerouslySetInnerHTML={{ __html: html }} />
      <div className="page-nav">
        {previous ? <Link to={href(previous.slug)} className="page-nav-item prev"><small>Previous</small><strong>← {previous.title}</strong></Link> : <span />}
        {next ? <Link to={href(next.slug)} className="page-nav-item next"><small>Next</small><strong>{next.title} →</strong></Link> : <span />}
      </div>
    </article>{toc.length > 0 && <aside className="toc" aria-label="On this page"><div className="toc-title">On this page</div>{toc.map((item) => <a key={item.id} href={`#${item.id}`}>{item.label}</a>)}</aside>}</div>
  </div>;
}

function NotFound({ href }: { href: DocsLink }) {
  return <div className="not-found"><div className="not-found-code">404</div><h1>Page not found</h1><p>The documentation page you requested does not exist.</p><Link className="primary-button" to={href('/')}>Return to docs home</Link></div>;
}

export default function DocsApp({ embedded = false }: DocsProps) {
  const location = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const prefix = embedded ? '/docs' : '';
  const href: DocsLink = (path) => `${prefix}${path === '/' ? '/' : path}`;
  const current = embedded && location.pathname.startsWith('/docs') ? location.pathname.slice('/docs'.length) || '/' : location.pathname;
  useEffect(() => setMenuOpen(false), [location.pathname]);
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => { if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); document.querySelector<HTMLInputElement>('.search input')?.focus(); } };
    window.addEventListener('keydown', onKeyDown); return () => window.removeEventListener('keydown', onKeyDown);
  }, []);
  return <div className="docs-app"><Topbar onMenu={() => setMenuOpen(true)} href={href} /><Sidebar current={current} open={menuOpen} onClose={() => setMenuOpen(false)} href={href} /><main className="docs-main"><Routes>{pages.map((page) => <Route key={page.slug} path={href(page.slug)} element={<DocPage slug={page.slug} href={href} linkPrefix={prefix} />} />)}<Route path={href('*')} element={<NotFound href={href} />} /></Routes></main></div>;
}
