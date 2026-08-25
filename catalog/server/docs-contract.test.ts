import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { openDb } from '../db/index.ts';
import { CATALOG_API_CONTRACT_HEADER, CATALOG_API_CONTRACT_VERSION } from '../config/api-contract.ts';
import { route, type AppDeps } from './app.ts';
import type { SyncRunner } from './sync-runner.ts';
import type { EvaluationRunner } from './evaluation-runner.ts';

type EndpointDeclaration = { method: string; path: string };
type PageDeclaration = { file: string; apiEndpoints?: EndpointDeclaration[] };

type Manifest = { pages: PageDeclaration[] };

const here = dirname(fileURLToPath(import.meta.url));
const catalogRoot = join(here, '..');
const manifest = JSON.parse(readFileSync(join(catalogRoot, 'docs-site', 'content.manifest.json'), 'utf8')) as Manifest;

function endpointDeclarations() {
  return manifest.pages.flatMap((page) => page.apiEndpoints ?? []);
}

function concretePath(path: string) {
  return path
    .replace('{providerId}', 'missing-provider')
    .replace('{modelId}', 'missing-model');
}

function dependencies(db: ReturnType<typeof openDb>): AppDeps {
  const runner = {
    isRunning: false,
    currentRunStartedAt: null,
    lastOutcome: null,
    run: async () => ({ status: 'succeeded' }),
  } as unknown as SyncRunner;
  const evaluations = {
    state: { state: 'idle' },
    enqueue: () => ({ accepted: false, reason: 'model_not_found', plan: { blocked: 'model_not_found' } }),
    stop: () => ({ state: 'idle' }),
  } as unknown as EvaluationRunner;
  return { db, runner, evaluations };
}

function bodyFor(method: string, path: string): unknown {
  if (method === 'PATCH' && path === '/v1/notifications/read') return {};
  if (method === 'POST' && path === '/v1/evaluations') return { providerId: 'missing-provider', modelId: 'missing-model' };
  if (method === 'POST' && path === '/v1/evaluations/regrade') return { providerId: 'missing-provider', modelId: 'missing-model', dryRun: true };
  return undefined;
}

test('every documented endpoint resolves through the real route boundary', async () => {
  const db = openDb(':memory:');
  try {
    for (const declaration of endpointDeclarations()) {
      const path = concretePath(declaration.path);
      const result = await route(
        dependencies(db),
        new URL(`http://127.0.0.1${path}`),
        declaration.method,
        bodyFor(declaration.method, declaration.path),
      ) as { status: number; body: any };
      const unknownRoute = result.status === 404 && result.body?.path === path;
      assert.equal(unknownRoute, false, `${declaration.method} ${declaration.path} was not recognized`);
    }
  } finally {
    db.close();
  }
});

test('the public docs reference the shared API contract identifiers', () => {
  assert.match(CATALOG_API_CONTRACT_HEADER, /^x-[a-z-]+$/);
  assert.match(CATALOG_API_CONTRACT_VERSION, /^catalog-api-v\d+$/);
  const apiOverview = readFileSync(join(catalogRoot, 'docs', 'content', 'api-overview.md'), 'utf8');
  assert.ok(apiOverview.includes('{{CATALOG_API_CONTRACT_HEADER}}'));
  assert.ok(apiOverview.includes('{{CATALOG_API_CONTRACT_VERSION}}'));
  assert.equal(apiOverview.includes('catalog-api-v2'), false, 'API version must not be duplicated in Markdown');
});

test('the legacy alerts route remains an explicit v2 migration response', async () => {
  const db = openDb(':memory:');
  try {
    const result = await route(dependencies(db), new URL('http://127.0.0.1/v1/alerts'), 'GET') as { status: number; body: any };
    assert.equal(result.status, 410);
    assert.equal(result.body.replacement, '/v1/notifications');
    assert.equal(result.body.contractVersion, CATALOG_API_CONTRACT_VERSION);
  } finally {
    db.close();
  }
});
