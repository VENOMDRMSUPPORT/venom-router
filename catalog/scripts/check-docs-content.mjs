import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..');
const contentRoot = join(root, 'docs', 'content');
const manifest = JSON.parse(await readFile(join(root, 'docs-site', 'content.manifest.json'), 'utf8'));
const pages = manifest.pages;
assert.ok(Array.isArray(pages) && pages.length > 0, 'docs manifest must contain pages');
assert.equal(new Set(pages.map((page) => page.slug)).size, pages.length, 'docs slugs must be unique');
assert.equal(new Set(pages.map((page) => page.file)).size, pages.length, 'docs files must be unique');
assert.equal(pages.some((page) => page.slug === '/'), true, 'docs must have a root page');

const referencedFiles = new Set();
for (const page of pages) {
  assert.match(page.slug, /^\/[a-z0-9/_-]*$/, `invalid slug: ${page.slug}`);
  assert.ok(page.title && page.description && page.section, `missing metadata for ${page.slug}`);
  const markdown = await readFile(join(contentRoot, page.file), 'utf8');
  referencedFiles.add(page.file);
  const h1 = markdown.match(/^#\s+.+$/gm) ?? [];
  assert.equal(h1.length, 1, `${page.file} must contain exactly one H1`);
  assert.ok(markdown.trim().length > 80, `${page.file} is too short to be a documentation page`);
  assert.equal(markdown.includes('catalog-api-v2'), false, `${page.file} must resolve API version from shared config`);
  const contractPage = ['api-overview.md', 'errors-and-pagination.md', 'glossary.md'].includes(page.file);
  assert.equal(markdown.includes('{{CATALOG_API_CONTRACT_HEADER}}') || markdown.includes('{{CATALOG_API_CONTRACT_VERSION}}'), contractPage, `${page.file} contains an unexpected unresolved contract token`);
  for (const match of markdown.matchAll(/\]\((\/[a-z0-9/_-]+)\)/g)) {
    assert.ok(pages.some((candidate) => candidate.slug === match[1]), `${page.file} links to missing page ${match[1]}`);
  }
  for (const endpoint of page.apiEndpoints ?? []) {
    assert.match(endpoint.method, /^(GET|POST|PATCH|DELETE)$/);
    assert.match(endpoint.path, /^\/v1\//);
    assert.equal(endpoint.path.startsWith('/v1/db/'), false, 'Database Browser cannot enter stable public API declarations');
  }
}

const files = (await readdir(contentRoot)).filter((file) => file.endsWith('.md'));
for (const file of files) assert.ok(referencedFiles.has(file), `${file} is not in the docs manifest`);
assert.ok(!pages.some((page) => (page.apiEndpoints ?? []).some((endpoint) => endpoint.path.startsWith('/v1/db/'))), 'local DB routes must remain excluded');
console.log(`docs content check passed (${pages.length} pages, ${files.length} Markdown files)`);
