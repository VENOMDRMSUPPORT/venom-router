import { marked } from 'marked';
import manifest from '../content.manifest.json';
import { CATALOG_API_CONTRACT_HEADER, CATALOG_API_CONTRACT_VERSION } from '../../config/api-contract.ts';

export type ApiEndpoint = { method: string; path: string };

export type PageMeta = {
  slug: string;
  file: string;
  title: string;
  description: string;
  section: string;
  order: number;
  apiEndpoints?: ApiEndpoint[];
};

type Manifest = { pages: PageMeta[] };

const docsBasePath = (import.meta.env.BASE_URL || '/').replace(/\/$/, '');
const docsLinkPrefix = docsBasePath || '';

const markdownFiles = import.meta.glob('../../docs/content/*.md', {
  eager: true,
  query: '?raw',
  import: 'default',
}) as Record<string, string>;

export const pages = [...(manifest as Manifest).pages]
  .sort((a, b) => a.order - b.order)
  .map((page) => ({ ...page, markdown: findMarkdown(page.file) }));

function findMarkdown(file: string): string {
  const key = Object.keys(markdownFiles).find((candidate) => candidate.endsWith(`/docs/content/${file}`));
  if (!key) throw new Error(`Missing documentation content: ${file}`);
  return markdownFiles[key];
}

export function pageBySlug(slug: string): (PageMeta & { markdown: string }) | undefined {
  return pages.find((page) => page.slug === slug);
}

export function pageGroups() {
  return pages.reduce<Record<string, typeof pages>>((groups, page) => {
    (groups[page.section] ??= []).push(page);
    return groups;
  }, {});
}

export function slugify(value: string): string {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9\s-]/g, '')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-');
}

export type TocItem = { id: string; label: string; level: number };

export function tocFor(markdown: string): TocItem[] {
  const items: TocItem[] = [];
  const seen = new Map<string, number>();
  for (const match of markdown.matchAll(/^##\s+(.+)$/gm)) {
    const label = match[1].trim();
    const base = slugify(label);
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    items.push({ id: count ? `${base}-${count + 1}` : base, label, level: 2 });
  }
  return items;
}

export function plainText(markdown: string): string {
  return markdown
    .replaceAll('{{CATALOG_API_CONTRACT_HEADER}}', CATALOG_API_CONTRACT_HEADER)
    .replaceAll('{{CATALOG_API_CONTRACT_VERSION}}', CATALOG_API_CONTRACT_VERSION)
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/[#>*_~-]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

export function searchPages(query: string) {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return [];
  return pages
    .map((page) => {
      const haystack = `${page.title} ${page.description} ${plainText(page.markdown)}`.toLowerCase();
      const index = haystack.indexOf(normalized);
      return index < 0 ? null : {
        page,
        snippet: plainText(page.markdown).slice(Math.max(0, index - 45), index + 125),
        score: (page.title.toLowerCase().includes(normalized) ? 10 : 0) + (index === 0 ? 5 : 0),
      };
    })
    .filter((result): result is NonNullable<typeof result> => result !== null)
    .sort((a, b) => b.score - a.score || a.page.order - b.page.order)
    .slice(0, 8);
}

export function renderMarkdown(markdown: string, linkPrefix = docsLinkPrefix): string {
  const resolvedMarkdown = markdown
    .replaceAll('{{CATALOG_API_CONTRACT_HEADER}}', CATALOG_API_CONTRACT_HEADER)
    .replaceAll('{{CATALOG_API_CONTRACT_VERSION}}', CATALOG_API_CONTRACT_VERSION);
  const html = (marked.parse(resolvedMarkdown, { gfm: true, breaks: false }) as string)
    .replace(/^<h1>[\s\S]*?<\/h1>\s*/, '')
    .replace(/href="\/(?!\/)/g, `href="${linkPrefix}/`);
  const seen = new Map<string, number>();
  return html.replace(/<h2>(.*?)<\/h2>/g, (_full, inner: string) => {
    const text = inner.replace(/<[^>]+>/g, '');
    const base = slugify(text);
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    const id = count ? `${base}-${count + 1}` : base;
    return `<h2 id="${id}"><a class="heading-anchor" href="#${id}" aria-label="Link to ${text}">#</a>${inner}</h2>`;
  });
}
