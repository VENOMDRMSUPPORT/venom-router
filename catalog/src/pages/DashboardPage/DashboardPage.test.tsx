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

function provider(over: Partial<CatalogData['providers'][number]> = {}): CatalogData['providers'][number] {
  return {
    id: 'acme',
    name: 'Acme AI',
    rosterUrl: 'https://acme.test/models',
    liveModels: 1,
    lastSuccessfulSyncAt: '2026-08-25T00:00:00.000Z',
    lastAttemptedSyncAt: '2026-08-25T00:00:00.000Z',
    lastOutcome: 'ok',
    freshness: 'fresh',
    hoursSinceSuccess: 1,
    qualityScored: 0,
    modelScoreScored: 0,
    overallScoreScored: 0,
    unrated: 0,
    ...over,
  };
}

/**
 * Only the fields this page reads. The cast is deliberate: building a full
 * ApiModel here would restate half the client module to test a statistics
 * paragraph, and the page must not read fields this factory does not declare.
 */
function model(over: Record<string, unknown> = {}): CatalogData['models'][number] {
  return {
    providerId: 'acme',
    modelId: 'acme-model',
    displayName: 'Acme Model',
    contextTokens: 128_000,
    inputModalities: ['text'],
    capabilities: { tools: true, reasoning: true, structured: true, attachment: false },
    pricing: { isFree: false },
    overallScore: { value: null, display: '\u2014', status: 'unknown' },
    performance: { status: 'unmeasured', sampleCount: 0, successfulSamples: 0, ttftMedianSeconds: null, outputTokensPerSecondMedian: null, endToEndP95Seconds: null, successRate: null },
    resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    vo: { value: null },
    ...over,
  } as unknown as CatalogData['models'][number];
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

describe('the notification history', () => {
  test('renders recorded model and fetch events without operational status filters or lifecycle actions', async () => {
    const now = '2026-08-23T10:00:00.000Z';
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/notifications')) {
        return Promise.resolve(new Response(JSON.stringify({
          generatedAt: now,
          summary: { total: 2, unread: 1, read: 1 },
          notifications: [
            { id: 'model-event:1', kind: 'model_added', category: 'success', title: 'acme added model acme-code', detail: 'The provider added this model to its published roster.', providerId: 'acme', modelId: 'acme-code', observedAt: now, readAt: null, createdAt: now },
            { id: 'sync-run:2', kind: 'fetch_problem', category: 'warning', title: 'beta data refresh needs attention', detail: 'The catalog could not refresh this provider’s model data.', providerId: 'beta', modelId: null, observedAt: now, readAt: now, createdAt: now },
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

      expect(await screen.findByText('acme added model acme-code')).toBeInTheDocument();
      expect(screen.getByText('beta data refresh needs attention')).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'acme' })).toHaveAttribute('href', '/provider/acme');
      expect(screen.queryByLabelText('Alert filters')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Acknowledge' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Resolve' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Reopen' })).not.toBeInTheDocument();
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

    expect(screen.getByRole('heading', { name: 'Model performance' })).toBeInTheDocument();
    expect(screen.getByText('No measured performance data')).toBeInTheDocument();
    expect(screen.getByText('0/0 measured')).toBeInTheDocument();
  });
});

describe('the Dashboard masthead', () => {
  test('states the operational status and offers no browsing chrome', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeInTheDocument();
    expect(screen.getByText('Live catalog')).toBeInTheDocument();
    // Browsing lives on the provider pages and in the command palette; this
    // page is a statistics console and offers no search of its own.
    expect(screen.queryByPlaceholderText(/Search providers/)).not.toBeInTheDocument();
  });
});

describe('the provider fleet table', () => {
  test('lists every provider as a status row linking to its page, most covered first', async () => {
    catalogMock.current = {
      data: baseData({
        providers: [
          provider({ id: 'beta', name: 'Beta Cloud', liveModels: 2, overallScoreScored: 0, freshness: 'stale' }),
          provider({ id: 'acme', name: 'Acme AI', liveModels: 3, overallScoreScored: 3 }),
        ],
      }),
      error: null, loading: false,
    };
    renderDashboard();
    await settleDashboard();

    const fleet = screen.getByRole('region', { name: 'Provider fleet' });
    const cards = within(fleet).getAllByRole('link');
    expect(cards).toHaveLength(2);
    // Fixed order, most complete scoring coverage first: the card that needs
    // work is the one that stands out by falling to the end.
    expect(cards[0]).toHaveAccessibleName('Acme AI');
    expect(cards[0]).toHaveAttribute('href', '/provider/acme');
    expect(within(cards[0]).getByText(/100%/)).toBeInTheDocument();
    expect(cards[1]).toHaveAccessibleName('Beta Cloud');
    expect(cards[1]).toHaveAttribute('href', '/provider/beta');
    expect(within(cards[1]).getByText(/0%/)).toBeInTheDocument();
  });
});

describe('the provider fleet pager', () => {
  test('appears only past four providers and pages without losing anyone', async () => {
    catalogMock.current = {
      data: baseData({
        providers: Array.from({ length: 6 }, (_, index) => provider({
          id: `p${index}`, name: `Provider ${index}`, overallScoreScored: 1, liveModels: 1,
        })),
      }),
      error: null, loading: false,
    };
    renderDashboard();
    await settleDashboard();

    const fleet = screen.getByRole('region', { name: 'Provider fleet' });
    expect(within(fleet).getAllByRole('link')).toHaveLength(4);
    expect(within(fleet).getByText('1 / 2')).toBeInTheDocument();
    expect(within(fleet).getByRole('button', { name: 'Previous providers' })).toBeDisabled();

    fireEvent.click(within(fleet).getByRole('button', { name: 'Next providers' }));
    expect(within(fleet).getAllByRole('link')).toHaveLength(2);
    expect(within(fleet).getByText('2 / 2')).toBeInTheDocument();
    expect(within(fleet).getByRole('button', { name: 'Next providers' })).toBeDisabled();
    expect(within(fleet).getByRole('link', { name: 'Provider 5' })).toBeInTheDocument();
  });

  test('four or fewer providers get no pager at all', async () => {
    catalogMock.current = { data: baseData({ providers: [provider()] }), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    expect(screen.queryByRole('button', { name: 'Next providers' })).not.toBeInTheDocument();
  });
});

describe('the performance charts draw measurements, not inventions', () => {
  test('two measured models produce both latency profiles with honest bounds', async () => {
    const measured = (id: string, ttft: number, p95: number) => model({
      modelId: id,
      performance: {
        status: 'measured', sampleCount: 10, successfulSamples: 10,
        ttftMedianSeconds: ttft, outputTokensPerSecondMedian: 50,
        endToEndP95Seconds: p95, successRate: 1,
      },
    });
    catalogMock.current = {
      data: baseData({ models: [measured('fast', 0.4, 3.1), measured('slow', 2.2, 9.8), model({ modelId: 'unmeasured' })] }),
      error: null, loading: false,
    };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByText('2/3 measured')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Median time to first token across 2 measured models, from 0.40 to 2.20 seconds' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '95th percentile end-to-end response across 2 measured models, from 3.10 to 9.80 seconds' })).toBeInTheDocument();
    expect(screen.queryByText('No measured performance data')).not.toBeInTheDocument();
  });
});

describe('the composition section keeps unknown apart from no', () => {
  test('a capability nobody answered for is counted as unknown, not folded into no', async () => {
    catalogMock.current = {
      data: baseData({
        models: [
          model({ modelId: 'm-yes', capabilities: { tools: true, reasoning: true, structured: true, attachment: false } }),
          model({ modelId: 'm-no', capabilities: { tools: false, reasoning: false, structured: false, attachment: false } }),
          model({ modelId: 'm-unknown', capabilities: { tools: null, reasoning: null, structured: null, attachment: null } }),
        ],
      }),
      error: null, loading: false,
    };
    renderDashboard();
    await settleDashboard();

    const coverage = screen.getByTestId('capability-coverage');
    expect(within(coverage).getByRole('img', { name: 'Tool calling: 1 yes, 1 no, 1 unknown' })).toBeInTheDocument();
  });

  test('unscored models are a separate row, never a zero-score bucket', async () => {
    catalogMock.current = {
      data: baseData({
        models: [
          model({ modelId: 'scored', overallScore: { value: 84, display: '84.0%', status: 'complete' } }),
          model({ modelId: 'unscored' }),
        ],
      }),
      error: null, loading: false,
    };
    renderDashboard();
    await settleDashboard();

    const scores = screen.getByRole('heading', { name: 'Overall score distribution' }).closest('article')!;
    const row = (label: string) => within(scores).getByText(label).closest('div')!;
    expect(within(row('80\u201389')).getByText('1')).toBeInTheDocument();
    expect(within(row('Not yet scored')).getByText('1')).toBeInTheDocument();
    expect(within(row('Below 60')).getByText('0')).toBeInTheDocument();
  });
});

describe('the "Identity candidates refused" tile survives a partial meta payload', () => {
  test('a full meta renders the real counts', async () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();
    await settleDashboard();

    expect(screen.getByText('9')).toBeInTheDocument();
    expect(screen.getByText('Identity candidates refused')).toBeInTheDocument();
    expect(screen.getByText(/across 6 models/)).toBeInTheDocument();
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
    const row = screen.getByText('Identity candidates refused').closest('div');
    expect(row).not.toHaveTextContent(/[0-9]/);
    expect(row).toHaveTextContent('unknown, not zero');
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
    expect(await screen.findByRole('heading', { level: 1, name: 'Dashboard' })).toBeInTheDocument();
  });

  test('an empty catalog still renders instead of tearing down', async () => {
    // The other order: data present but carrying no models, which is what the
    // performance helpers are handed before anything is measured.
    catalogMock.current = {
      data: baseData({ models: [], providers: [] }), error: null, loading: false,
      health: null, healthError: null, healthLoading: false, reload: vi.fn(),
    };
    renderDashboard();

    expect(await screen.findByRole('heading', { level: 1, name: 'Dashboard' })).toBeInTheDocument();
  });
});
