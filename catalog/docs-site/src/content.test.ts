import { describe, expect, test } from 'vitest';
import { CATALOG_API_CONTRACT_HEADER, CATALOG_API_CONTRACT_VERSION } from '../../config/api-contract';
import { pages, renderMarkdown, searchPages, tocFor } from './content';

describe('documentation content contract', () => {
  test('has one ordered page manifest with unique slugs', () => {
    expect(pages.length).toBe(12);
    expect(new Set(pages.map((page) => page.slug)).size).toBe(pages.length);
    expect(pages.map((page) => page.order)).toEqual([...pages].sort((a, b) => a.order - b.order).map((page) => page.order));
  });

  test('resolves API contract tokens from shared configuration', () => {
    const html = renderMarkdown(pages.find((page) => page.slug === '/api/overview')!.markdown);
    expect(html).toContain(`${CATALOG_API_CONTRACT_HEADER}: ${CATALOG_API_CONTRACT_VERSION}`);
    expect(html).not.toContain('{{CATALOG_API_CONTRACT_VERSION}}');
    expect(html).not.toContain('{{CATALOG_API_CONTRACT_HEADER}}');
  });

  test('search returns API pages for contract queries', () => {
    const results = searchPages('contract header');
    expect(results.some((result) => result.page.slug === '/api/overview')).toBe(true);
  });

  test('TOC headings have stable ids', () => {
    const items = tocFor('## First Section\n\nText\n\n## Second Section');
    expect(items).toEqual([
      { id: 'first-section', label: 'First Section', level: 2 },
      { id: 'second-section', label: 'Second Section', level: 2 },
    ]);
  });

  test('local database diagnostics are not stable API documentation', () => {
    expect(pages.find((page) => page.slug === '/api/catalog-endpoints')!.markdown).toContain('not a stable integration contract');
  });
});
