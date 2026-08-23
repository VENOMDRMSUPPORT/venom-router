import { describe, test, expect, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { DashboardPage } from './DashboardPage';
import type { CatalogData, HealthResponse } from '../../api/client';

/**
 * `DashboardPage` reads `data`/`error`/`loading` from `useCatalog()`. Mocking the
 * hook module — rather than standing up a real `CatalogProvider` + fetch — lets
 * each test hand the page an exact payload, including a payload that violates
 * the `CatalogMeta` type contract at runtime, which is exactly the case this
 * file exists to pin.
 */
type CatalogMockState = {
  data: CatalogData | null;
  error: string | null;
  loading: boolean;
  health?: HealthResponse | null;
  healthError?: string | null;
  healthLoading?: boolean;
  reload?: () => void;
};

const catalogMock = vi.hoisted(() => ({ current: {
  data: null as CatalogData | null,
  error: null as string | null,
  loading: false,
  health: null as HealthResponse | null,
  healthError: null as string | null,
  healthLoading: false,
  reload: vi.fn(),
} as CatalogMockState }));
vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => catalogMock.current,
}));

function meta(over: Partial<CatalogData['meta']> = {}): CatalogData['meta'] {
  return {
    methodologyVersion: 'venom-score-v2',
    profileId: 'balanced',
    scoringPolicy: {
      methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7,
      operationalWeight: 0.3,
      operationalPrecision: 0,
    },
    liveModels: 116,
    catalogReady: 108,
    needsVerification: 8,
    qualityScored: 102,
    modelScoreScored: 102,
    overallScoreScored: 0,
    operationalScored: 115,
    unrated: 14,
    identity: { resolved: 108, identityReview: 6, unresolved: 2 },
    identityDetail: { ambiguousOpen: 0, withRejectedCandidates: 6, rejectedCandidates: 9 },
    conflictedModels: 102,
    conflictsByField: {},
    identityRules: {},
    calibration: null,
    sortContracts: {},
    ...over,
  };
}

function baseData(over: Partial<CatalogData> = {}): CatalogData {
  return {
    models: [],
    providers: [],
    meta: meta(),
    origin: 'live',
    ...over,
  };
}

function renderDashboard() {
  return render(
    <MemoryRouter>
      <DashboardPage />
    </MemoryRouter>,
  );
}

async function settleDashboard() {
  await waitFor(() => expect(screen.queryByText('Loading alert lifecycle…')).not.toBeInTheDocument());
}

describe('the monitoring panel interaction experience', () => {
  test('collapses details and exposes retry for an unreachable health endpoint', async () => {
    catalogMock.current = {
      data: baseData(), error: null, loading: false, health: null,
      healthError: 'health endpoint unavailable', healthLoading: false, reload: vi.fn(),
    };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByText('Catalog API is unreachable')).toBeInTheDocument();
    const monitoringHide = screen.getAllByRole('button', { name: /hide details/i }).find((button) => button.getAttribute('aria-controls') === 'monitoring-signals');
    expect(monitoringHide).toBeDefined();
    fireEvent.click(monitoringHide!);
    expect(screen.queryByText('Catalog API is unreachable')).not.toBeInTheDocument();
    const monitoringShow = screen.getAllByRole('button', { name: /show details/i }).find((button) => button.getAttribute('aria-controls') === 'monitoring-signals');
    expect(monitoringShow).toBeDefined();
    fireEvent.click(monitoringShow!);
    expect(screen.getByText('Catalog API is unreachable')).toBeInTheDocument();
    const healthPanel = screen.getByRole('region', { name: 'Catalog health' });
    fireEvent.click(within(healthPanel).getByRole('button', { name: 'Retry' }));
    await settleDashboard();
    expect(catalogMock.current.reload).toHaveBeenCalled();
  });

  test('shows a nominal status after the health endpoint answers cleanly', async () => {
    const cleanHealth: HealthResponse = {
      service: { status: 'up', databaseReadable: true, startedAt: null, syncInFlight: false, currentRunStartedAt: null, schedulerEnabled: true, nextScheduledRunAt: null },
      catalog: { status: 'current', liveModels: 116, methodologyVersion: 'v1', staleAfterHours: 24, staleProviders: [], providers: [] },
      lastSync: null,
    };
    catalogMock.current = { data: baseData({ meta: meta({ needsVerification: 0 }) }), error: null, loading: false, health: cleanHealth, healthError: null, healthLoading: false, reload: vi.fn() };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByText('Catalog monitoring is clear')).toBeInTheDocument();
    expect(screen.getByText('All systems nominal')).toBeInTheDocument();
  });
});

describe('the change-history time window and deep links', () => {
  test('shows recent events by default and reveals older events in all-time view', async () => {
    const recent = new Date().toISOString();
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ changes: [
        { class: 'added', providerId: 'openrouter', modelId: 'gpt-5', field: null, from: null, to: null, note: null, observedAt: recent },
        { class: 'retired', providerId: 'openrouter', modelId: 'old-model', field: null, from: null, to: null, note: null, observedAt: '2020-01-01T00:00:00.000Z' },
      ] }),
    });
    vi.stubGlobal('fetch', fetchMock);
    try {
      catalogMock.current = { data: baseData({ meta: meta({ needsVerification: 0 }) }), error: null, loading: false, health: null, healthError: null, healthLoading: false, reload: vi.fn() };
      renderDashboard();
      await settleDashboard();

      await waitFor(() => expect(screen.getByRole('link', { name: 'gpt-5' })).toBeInTheDocument());
      expect(screen.queryByRole('link', { name: 'old-model' })).not.toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'gpt-5' })).toHaveAttribute('href', '/provider/openrouter?model=gpt-5');

      fireEvent.change(screen.getByLabelText('Window'), { target: { value: 'all' } });
      expect(await screen.findByRole('link', { name: 'old-model' })).toBeInTheDocument();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe('the runtime settings panel', () => {
  test('renders server-reported freshness, scheduler, and database settings', async () => {
    const runtimeHealth: HealthResponse = {
      service: { status: 'up', databaseReadable: true, startedAt: null, syncInFlight: false, currentRunStartedAt: null, schedulerEnabled: true, nextScheduledRunAt: null },
      catalog: { status: 'current', liveModels: 116, methodologyVersion: 'catalog-v3', staleAfterHours: 24, staleProviders: [], providers: [] },
      lastSync: null,
    };
    catalogMock.current = { data: baseData(), error: null, loading: false, health: runtimeHealth, healthError: null, healthLoading: false, reload: vi.fn() };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByRole('heading', { name: 'Catalog runtime settings' })).toBeInTheDocument();
    expect(screen.getByText('24 hours')).toBeInTheDocument();
    expect(screen.getByText('Enabled')).toBeInTheDocument();
    expect(screen.getByText('Source: /v1/health')).toBeInTheDocument();
  });
});

describe('the alert center lifecycle experience', () => {
  test('filters alerts and persists acknowledge, resolve, and reopen actions', async () => {
    const now = '2026-08-23T10:00:00.000Z';
    let serviceStatus: 'open' | 'acknowledged' | 'resolved' = 'open';
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/alerts/service-degraded')) {
        const next = JSON.parse(String(init?.body ?? '{}')).status as typeof serviceStatus;
        serviceStatus = next;
        return Promise.resolve(new Response(JSON.stringify({ id: 'service-degraded', kind: 'service_degraded', severity: 'critical', title: 'Catalog service is degraded', detail: 'Database is unavailable.', providerId: null, modelId: null, status: serviceStatus, firstSeenAt: now, lastSeenAt: now, acknowledgedAt: serviceStatus === 'acknowledged' ? now : null, resolvedAt: serviceStatus === 'resolved' ? now : null, occurrenceCount: 1 }), { status: 200 }));
      }
      if (url.includes('/alerts')) {
        return Promise.resolve(new Response(JSON.stringify({
          generatedAt: now,
          summary: { total: 2, active: serviceStatus === 'resolved' ? 1 : 2, open: serviceStatus === 'open' ? 1 : 0, acknowledged: serviceStatus === 'acknowledged' ? 1 : 0, resolved: serviceStatus === 'resolved' ? 1 : 0, critical: serviceStatus === 'resolved' ? 0 : 1, warning: 1, info: 0 },
          alerts: [
            { id: 'service-degraded', kind: 'service_degraded', severity: 'critical', title: 'Catalog service is degraded', detail: 'Database is unavailable.', providerId: null, modelId: null, status: serviceStatus, firstSeenAt: now, lastSeenAt: now, acknowledgedAt: serviceStatus === 'acknowledged' ? now : null, resolvedAt: serviceStatus === 'resolved' ? now : null, occurrenceCount: 1 },
            { id: 'stale-provider:acme', kind: 'stale_provider', severity: 'warning', title: 'Acme needs a fresh sync', detail: 'Provider data is stale.', providerId: 'acme', modelId: null, status: 'resolved', firstSeenAt: now, lastSeenAt: now, acknowledgedAt: null, resolvedAt: now, occurrenceCount: 2 },
          ],
        }), { status: 200 }));
      }
      if (url.includes('/changes')) return Promise.resolve(new Response(JSON.stringify({ changes: [], byClass: {}, cursor: null }), { status: 200 }));
      return Promise.reject(new Error('unexpected request'));
    });
    vi.stubGlobal('fetch', fetchMock);
    try {
      catalogMock.current = { data: baseData(), error: null, loading: false, health: null, healthError: null, healthLoading: false, reload: vi.fn() };
      renderDashboard();
      await settleDashboard();

      expect(await screen.findByText('Catalog service is degraded')).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'acme' })).toHaveAttribute('href', '/provider/acme');
      fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'resolved' } });
      expect(screen.getByText('Acme needs a fresh sync')).toBeInTheDocument();

      fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'open' } });
      fireEvent.click(screen.getByRole('button', { name: 'Acknowledge' }));
      await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => String(init?.body).includes('acknowledged'))).toBe(true));
      expect(screen.getByText('Acknowledged', { selector: 'span' })).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: 'Resolve' }));
      await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => String(init?.body).includes('resolved'))).toBe(true));
      expect(screen.getAllByText('Resolved', { selector: 'span' }).length).toBeGreaterThan(0);
      fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));
      await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => String(init?.body).includes('open'))).toBe(true));
      expect(screen.getByText('Open', { selector: 'span' })).toBeInTheDocument();
      expect(fetchMock.mock.calls.filter(([, init]) => String(init?.body).includes('acknowledged')).length).toBeGreaterThan(0);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe('the performance monitoring panel', () => {
  test('shows an honest no-measurement state when no speed probe is complete', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByRole('heading', { name: 'Latency & model performance' })).toBeInTheDocument();
    expect(screen.getByText('No measured performance data')).toBeInTheDocument();
    expect(screen.getByText('0/0 measured')).toBeInTheDocument();
  });
});

describe('the Dashboard status and empty-state experience', () => {
  test('shows operational catalog status and offers a clear action after a search', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByText('Live catalog')).toBeInTheDocument();
    const searchInput = screen.getByPlaceholderText('Search providers, models, or IDs...');
    fireEvent.change(searchInput, { target: { value: 'missing-provider' } });

    expect(screen.getByText('No providers match this view')).toBeInTheDocument();
    const clear = screen.getByRole('button', { name: 'Clear search and filters' });
    fireEvent.click(clear);
    expect(searchInput).toHaveValue('');
  });
});

describe('the "Identity candidates refused" tile survives a partial meta payload', () => {
  test('a full meta renders the real counts', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByText('9')).toBeInTheDocument();
    expect(screen.getByText('Identity candidates refused')).toBeInTheDocument();
  });

  test('a meta payload missing identityDetail does not crash the page, and does not render 0', async () => {
    // Simulates a stale server answering with a pre-M5.1 shape: `identityDetail`
    // is simply absent from the JSON, so it is `undefined` at runtime even though
    // `CatalogMeta` declares it required. The component must degrade honestly.
    const staleMeta = meta();
    delete (staleMeta as Partial<typeof staleMeta>).identityDetail;
    catalogMock.current = { data: baseData({ meta: staleMeta }), error: null, loading: false };

    renderDashboard();
    await settleDashboard();

    expect(screen.getByText('Identity candidates refused')).toBeInTheDocument();
    // The count must not read as a claimed zero — that says "we looked, there
    // are none", which is a different fact from "this response didn't say".
    const tile = screen.getByText('Identity candidates refused').closest('div');
    expect(tile).not.toHaveTextContent('0');
    expect(screen.queryByText('9')).not.toBeInTheDocument();
  });
});

describe('final evaluation coverage is distinct from operational readiness', () => {
  test('explains why a working provider can still have no complete overall score', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByText('Complete overall scores')).toBeInTheDocument();
    expect(screen.getByTitle(/Operational data is available for 115\/116 models/i)).toBeInTheDocument();
  });
});

/**
 * This page lists PROVIDERS, and its filter predicate has branches for free,
 * paid, 1M+ context and multimodal — and for nothing else. It took the model
 * filter list only as the Toolbar's default, so "Not Deprecated" arrived here
 * with no branch to act on: an offered control whose only possible effect was
 * none at all. That is the same defect the change-class page was just fixed for.
 */
describe('the dashboard offers the filters it can actually apply', () => {
  test('the filter menu holds provider filters, and nothing it cannot act on', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    fireEvent.click(screen.getByRole('button', { name: /all providers/i }));
    const offered = within(screen.getByRole('listbox')).getAllByRole('option').map((option) => option.textContent);

    expect(offered.slice(0, 5)).toEqual(['All Providers', 'Free Models', 'Paid Models', '1M+ Context', 'Multimodal']);
    expect(offered).not.toContain('Not Deprecated');
    expect(screen.getByLabelText('Sort by')).toHaveValue('score');
  });

  test('the search box says what it actually searches', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    const searchInput = screen.getByPlaceholderText('Search providers, models, or IDs...');
    expect(searchInput).toBeInTheDocument();
    expect(searchInput).toHaveAttribute('aria-describedby', 'dashboard-search-hint');
    expect(screen.getByText(/Advanced search:/i)).toBeInTheDocument();
  });

  test('sort direction toggles and freshness chooses the newest-first default', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    const sort = screen.getByLabelText('Sort by');
    fireEvent.change(sort, { target: { value: 'freshness' } });
    expect(sort).toHaveValue('freshness');
    expect(screen.getByRole('button', { name: /sort descending/i })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /sort descending/i }));
    expect(screen.getByRole('button', { name: /sort ascending/i })).toBeInTheDocument();
  });
});

describe('the dashboard survives its own loading state', () => {
  /**
   * Every other test in this file hands `useCatalog` a finished payload, so the
   * first render already has data and the empty path never runs. That is exactly
   * why 227 green SPA tests sat alongside a dashboard that white-screened in the
   * browser: `performanceSummary` and `performanceList` were declared AFTER the
   * `if (loading && !data)` and `if (!data)` guards, so the loading render called
   * 42 hooks and the render that followed called 44.
   *
   *   Rendered more hooks than during the previous render.
   *
   * React tears the tree down on that, which is the blank page. The transition
   * has to be driven here or the violation cannot appear.
   */
  test('the hook order does not change when data arrives', async () => {
    catalogMock.current = { data: null, error: null, loading: true, health: null, healthError: null, healthLoading: false, reload: vi.fn() };
    const { rerender } = renderDashboard();
    expect(screen.queryByRole('heading', { name: /catalog service unavailable/i })).not.toBeInTheDocument();

    catalogMock.current = { ...catalogMock.current, data: baseData(), loading: false };
    rerender(<MemoryRouter><DashboardPage /></MemoryRouter>);

    // A crashed tree renders nothing, so real content is the check a thrown
    // render cannot fake.
    await settleDashboard();
    expect(await screen.findByText(/Enterprise Matrix 2026/i)).toBeInTheDocument();
  });

  test('an empty catalog still renders instead of tearing down', async () => {
    // The other order: data present but carrying no models, which is what the
    // performance helpers are handed before anything is measured.
    catalogMock.current = {
      data: baseData({ models: [], providers: [] }), error: null, loading: false,
      health: null, healthError: null, healthLoading: false, reload: vi.fn(),
    };
    renderDashboard();

    expect(await screen.findByText(/Enterprise Matrix 2026/i)).toBeInTheDocument();
  });
});
