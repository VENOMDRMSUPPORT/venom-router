import { describe, test, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
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

describe('the monitoring panel interaction experience', () => {
  test('collapses details and exposes retry for an unreachable health endpoint', () => {
    catalogMock.current = {
      data: baseData(), error: null, loading: false, health: null,
      healthError: 'health endpoint unavailable', healthLoading: false, reload: vi.fn(),
    };
    renderDashboard();

    expect(screen.getByText('Catalog API is unreachable')).toBeInTheDocument();
    const monitoringHide = screen.getAllByRole('button', { name: /hide details/i }).find((button) => button.getAttribute('aria-controls') === 'monitoring-signals');
    expect(monitoringHide).toBeDefined();
    fireEvent.click(monitoringHide!);
    expect(screen.queryByText('Catalog API is unreachable')).not.toBeInTheDocument();
    const monitoringShow = screen.getAllByRole('button', { name: /show details/i }).find((button) => button.getAttribute('aria-controls') === 'monitoring-signals');
    expect(monitoringShow).toBeDefined();
    fireEvent.click(monitoringShow!);
    expect(screen.getByText('Catalog API is unreachable')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(catalogMock.current.reload).toHaveBeenCalled();
  });

  test('shows a nominal status after the health endpoint answers cleanly', () => {
    const cleanHealth: HealthResponse = {
      service: { status: 'up', databaseReadable: true, startedAt: null, syncInFlight: false, currentRunStartedAt: null, schedulerEnabled: true, nextScheduledRunAt: null },
      catalog: { status: 'current', liveModels: 116, methodologyVersion: 'v1', staleAfterHours: 24, staleProviders: [], providers: [] },
      lastSync: null,
    };
    catalogMock.current = { data: baseData({ meta: meta({ needsVerification: 0 }) }), error: null, loading: false, health: cleanHealth, healthError: null, healthLoading: false, reload: vi.fn() };
    renderDashboard();

    expect(screen.getByText('Catalog monitoring is clear')).toBeInTheDocument();
    expect(screen.getByText('All systems nominal')).toBeInTheDocument();
  });
});

describe('the Dashboard status and empty-state experience', () => {
  test('shows operational catalog status and offers a clear action after a search', () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();

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
  test('a full meta renders the real counts', () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();

    expect(screen.getByText('9')).toBeInTheDocument();
    expect(screen.getByText('Identity candidates refused')).toBeInTheDocument();
  });

  test('a meta payload missing identityDetail does not crash the page, and does not render 0', () => {
    // Simulates a stale server answering with a pre-M5.1 shape: `identityDetail`
    // is simply absent from the JSON, so it is `undefined` at runtime even though
    // `CatalogMeta` declares it required. The component must degrade honestly.
    const staleMeta = meta();
    delete (staleMeta as Partial<typeof staleMeta>).identityDetail;
    catalogMock.current = { data: baseData({ meta: staleMeta }), error: null, loading: false };

    expect(() => renderDashboard()).not.toThrow();

    expect(screen.getByText('Identity candidates refused')).toBeInTheDocument();
    // The count must not read as a claimed zero — that says "we looked, there
    // are none", which is a different fact from "this response didn't say".
    const tile = screen.getByText('Identity candidates refused').closest('div');
    expect(tile).not.toHaveTextContent('0');
    expect(screen.queryByText('9')).not.toBeInTheDocument();
  });
});

describe('final evaluation coverage is distinct from operational readiness', () => {
  test('explains why a working provider can still have no complete overall score', () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();

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
  test('the filter menu holds provider filters, and nothing it cannot act on', () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();

    fireEvent.click(screen.getByRole('button', { name: /all providers/i }));
    const offered = screen.getAllByRole('option').map((option) => option.textContent);

    expect(offered.slice(0, 5)).toEqual(['All Providers', 'Free Models', 'Paid Models', '1M+ Context', 'Multimodal']);
    expect(offered).not.toContain('Not Deprecated');
    expect(screen.getByLabelText('Sort by')).toHaveValue('score');
  });

  test('the search box says what it actually searches', () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();

    const searchInput = screen.getByPlaceholderText('Search providers, models, or IDs...');
    expect(searchInput).toBeInTheDocument();
    expect(searchInput).toHaveAttribute('aria-describedby', 'dashboard-search-hint');
    expect(screen.getByText(/Advanced search:/i)).toBeInTheDocument();
  });

  test('sort direction toggles and freshness chooses the newest-first default', () => {
    catalogMock.current = { data: baseData(), error: null, loading: false };
    renderDashboard();

    const sort = screen.getByLabelText('Sort by');
    fireEvent.change(sort, { target: { value: 'freshness' } });
    expect(sort).toHaveValue('freshness');
    expect(screen.getByRole('button', { name: /sort descending/i })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /sort descending/i }));
    expect(screen.getByRole('button', { name: /sort ascending/i })).toBeInTheDocument();
  });
});
