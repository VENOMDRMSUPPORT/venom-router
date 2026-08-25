import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { marked } from 'marked';

const here = dirname(fileURLToPath(import.meta.url));
const catalogRoot = join(here, '..');
const docsRoot = join(catalogRoot, 'docs');
const outputRoot = join(catalogRoot, 'dist-docs');
const manifest = JSON.parse(await readFile(join(catalogRoot, 'docs-site', 'content.manifest.json'), 'utf8'));
const contractSource = await readFile(join(catalogRoot, 'config', 'api-contract.ts'), 'utf8');
const contractHeader = contractSource.match(/CATALOG_API_CONTRACT_HEADER = '([^']+)'/)?.[1];
const contractVersion = contractSource.match(/CATALOG_API_CONTRACT_VERSION = '([^']+)'/)?.[1];
if (!contractHeader || !contractVersion) throw new Error('Unable to read Catalog API contract constants');
const template = await readFile(join(outputRoot, 'index.html'), 'utf8');
const basePath = normalizeBase(process.env.DOCS_BASE_PATH || '/');
const origin = (process.env.DOCS_PUBLIC_ORIGIN || '').replace(/\/$/, '');
const linkPrefix = basePath === '/' ? '' : basePath;

function normalizeBase(value) {
  const withSlash = `/${value.replace(/^\/+|\/+$/g, '')}`;
  return withSlash === '//' ? '/' : withSlash === '/undefined' ? '/' : withSlash;
}

function publicPath(slug) {
  if (slug === '/') return `${basePath}/`.replace(/\/+/g, '/');
  return `${basePath}${slug}/`.replace(/\/+/g, '/');
}

function escapeHtml(value) {
  return value.replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]));
}

function staticArticle(page, html) {
  return `<div class="docs-app"><main class="docs-main" style="margin-left:0"><div class="page-frame"><div class="breadcrumbs"><a href="${linkPrefix}/">Docs</a><span>/</span><span>${escapeHtml(page.section)}</span></div><div class="page-eyebrow">${escapeHtml(page.section)}</div><div class="reading-layout"><article class="doc-content"><div class="doc-title-row"><div><h1>${escapeHtml(page.title)}</h1><p class="lead">${escapeHtml(page.description)}</p></div></div><div class="prose">${html}</div></article></div></div></main></div>`;
}

for (const page of manifest.pages) {
  const markdown = (await readFile(join(docsRoot, 'content', page.file), 'utf8'))
    .replaceAll('{{CATALOG_API_CONTRACT_HEADER}}', contractHeader)
    .replaceAll('{{CATALOG_API_CONTRACT_VERSION}}', contractVersion);
  const html = marked.parse(markdown, { gfm: true, breaks: false })
    .replace(/^<h1>[\s\S]*?<\/h1>\s*/, '')
    .replace(/href="\/(?!\/)/g, `href="${linkPrefix}/`);
  const canonical = origin ? `${origin}${publicPath(page.slug)}` : '';
  let pageHtml = template
    .replace(/<title>[^<]*<\/title>/, `<title>${escapeHtml(page.title)} — Venom Catalog Docs</title>`)
    .replace(/<meta name="description" content="[^"]*" \/>/, `<meta name="description" content="${escapeHtml(page.description)}" />`)
    .replace('<div id="root"></div>', `<div id="root">${staticArticle(page, html)}</div>`);
  if (canonical) pageHtml = pageHtml.replace('</head>', `<link rel="canonical" href="${escapeHtml(canonical)}" /><meta property="og:title" content="${escapeHtml(page.title)}" /><meta property="og:description" content="${escapeHtml(page.description)}" /><meta property="og:url" content="${escapeHtml(canonical)}" /></head>`);
  const target = page.slug === '/' ? join(outputRoot, 'index.html') : join(outputRoot, page.slug.slice(1), 'index.html');
  await mkdir(dirname(target), { recursive: true });
  await writeFile(target, pageHtml);
}

await writeFile(join(outputRoot, 'robots.txt'), 'User-agent: *\nAllow: /\n');
if (origin) {
  const urls = manifest.pages.map((page) => `  <url><loc>${escapeHtml(`${origin}${publicPath(page.slug)}`)}</loc></url>`).join('\n');
  await writeFile(join(outputRoot, 'sitemap.xml'), `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`);
}

console.log(`prerendered ${manifest.pages.length} docs pages to ${outputRoot} (base ${basePath}, origin ${origin || 'relative'})`);
