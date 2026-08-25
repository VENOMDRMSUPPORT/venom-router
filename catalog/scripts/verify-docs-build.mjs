import assert from 'node:assert/strict';
import { access, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const output = join(root, 'dist-docs');
const manifest = JSON.parse(await readFile(join(root, 'docs-site', 'content.manifest.json'), 'utf8'));
const escapeHtml = (value) => value.replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]));

for (const page of manifest.pages) {
  const target = page.slug === '/' ? join(output, 'index.html') : join(output, page.slug.slice(1), 'index.html');
  await access(target);
  const html = await readFile(target, 'utf8');
  assert.ok(html.includes(`<h1>${escapeHtml(page.title)}</h1>`), `${page.slug} is missing its prerendered title`);
  assert.equal(html.includes('{{CATALOG_API_CONTRACT_VERSION}}'), false, `${page.slug} contains an unresolved contract token`);
  assert.equal(html.includes('{{CATALOG_API_CONTRACT_HEADER}}'), false, `${page.slug} contains an unresolved contract token`);
}

await access(join(output, 'robots.txt'));
if (process.env.DOCS_PUBLIC_ORIGIN) await access(join(output, 'sitemap.xml'));
console.log(`docs build verification passed (${manifest.pages.length} prerendered pages)`);
