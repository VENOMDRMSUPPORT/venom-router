import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { DashboardPage } from './DashboardPage';
import type { CatalogData } from '../../api/client';

/**
 * `DashboardPage` reads `data`/`error`/`loading` from `useCatalog()`. Mocking the
 * hook module — rather than standing up a real `CatalogProvider` + fetch — lets
 * each test hand the page an exact payload, including a payload that violates
 * the `CatalogMeta` type contract at runtime, which is exactly the case this
 * file exists to pin.
 */
const catalogMock = vi.hoisted(() => ({ current: { data: null as CatalogData | null, error: null as string | null, loading: false } }));
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
