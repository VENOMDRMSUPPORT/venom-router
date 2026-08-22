import { describe, test, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ComparePage } from './ComparePage';
import type { ApiModel } from '../../api/client';

function m(over: Partial<ApiModel> & { providerId: string; canonicalId: string | null }): ApiModel {
  return {
    modelId: 'm',
    lifecycle: null,
    vendorModelId: null,
    identityState: 'resolved',
    rejectedCandidates: [],
    displayName: 'Model',
    state: 'active',
    contextTokens: 128_000,
    maxOutputTokens: 32_000,
    inputModalities: ['text'],
    capabilities: { tools: true, reasoning: true, structured: true, attachment: false },
    pricing: {
      kind: 'per_token', inputPerMTokens: 1, outputPerMTokens: 5,
      referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false,
    },
    vq: {
      value: 50, uncertainty: 0.05, bound: null, evidenceLevel: 'measured',
      precision: 1, display: '50.0', unratedReason: null, provenance: null,
    },
    vo: { value: 70, dimensions: {}, missingDimensions: [], notApplicableDimensions: [], profileId: 'balanced' },
    catalogReady: true,
    missingFacts: [],
    conflicts: [],
    provenanceByField: {},
    modelScore: {
      value: 56, display: '56.0%', methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7, operationalWeight: 0.3, operationalPrecision: 0, uncertainty: 0.035,
      bound: null, reason: null, qualityEvidenceLevel: 'measured', operationalCoverage: 'complete',
    },
    overallScore: {
      value: 80, display: '80.0%', status: 'complete', qualityScore: 85, operationalScore: 70,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding', 'reasoning', 'longContext', 'toolCalling', 'structuredOutput', 'speed', 'costEfficiency'],
      excludedDimensions: ['vision'], uncertainty: 1, reasons: [],
      methodologyVersion: 'overall-score-v1', computedAt: '2026-08-20T00:00:00.000Z',
    },
    resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    modelRank: 1, tiedAtModelRank: false,
    overallRank: 1, tiedAtOverallRank: false,
    qualityRank: 1, tiedAtRank: false,
    firstSeenAt: '2026-08-01T00:00:00.000Z',
    lastSeenAt: '2026-08-20T00:00:00.000Z',
    ...over,
  } as ApiModel;
}

const offer = (
  providerId: string,
  canonicalId: string,
  value: number | null,
  over: Partial<ApiModel> = {},
): ApiModel => {
  const base = m({ providerId, canonicalId });
  return m({
    providerId, canonicalId, modelId: `${providerId}/x`,
    overallScore: {
      ...base.overallScore,
      value,
      display: value === null ? '—' : `${value.toFixed(1)}%`,
      status: value === null ? 'unknown' : 'complete',
      operationalScore: value === null ? null : value - 10,
    },
    ...over,
  });
};

const models: ApiModel[] = [];

vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => ({
    data: { models, providers: [], meta: { liveModels: models.length } },
    loading: false,
    error: null,
    reload: vi.fn(),
  }),
}));

function renderPage(rows: ApiModel[]) {
  models.length = 0;
  models.push(...rows);
  return render(
    <MemoryRouter>
      <ComparePage />
    </MemoryRouter>,
  );
}

describe('ComparePage', () => {
  test('shows one group per model served by more than one provider', () => {
    renderPage([
      offer('clinepass', 'moonshotai/kimi-k3', 93.9),
      offer('opencode-go', 'moonshotai/kimi-k3', 91.0),
      offer('opencode-go', 'alone/only-here', 88.0),
    ]);

    expect(screen.getAllByTestId(/^compare-group-/)).toHaveLength(1);
    expect(screen.getByTestId('compare-group-moonshotai/kimi-k3')).toBeInTheDocument();
  });

  test('names the provider to route to, and by how much', () => {
    renderPage([
      offer('clinepass', 'moonshotai/kimi-k3', 93.9),
      offer('opencode-go', 'moonshotai/kimi-k3', 91.0),
    ]);

    const group = screen.getByTestId('compare-group-moonshotai/kimi-k3');
    expect(within(group).getByTestId('compare-best')).toHaveTextContent('clinepass');
    expect(within(group).getByTestId('compare-spread')).toHaveTextContent('2.9');
  });

  test('each offer links to the provider page that owns it', () => {
    renderPage([
      offer('clinepass', 'moonshotai/kimi-k3', 93.9),
      offer('opencode-go', 'moonshotai/kimi-k3', 91.0),
    ]);

    const group = screen.getByTestId('compare-group-moonshotai/kimi-k3');
    // Two links per row on purpose: the provider name and the open affordance.
    expect(within(group).getByRole('link', { name: 'clinepass' })).toHaveAttribute('href', '/provider/clinepass');
    expect(within(group).getByRole('link', { name: 'Open clinepass' })).toHaveAttribute('href', '/provider/clinepass');
  });

  /**
   * The mimo-v2.5-pro case. Two rows of one model, 11 points apart, both
   * labelled complete at 100% coverage — because a disputed capability put
   * vision on one side's exam and not the other's. Presenting that gap as a
   * provider difference would be the single most misleading thing this page
   * could do.
   */
  test('a group whose offers took different exams says so instead of implying a winner', () => {
    const wide = offer('opencode-go', 'xiaomi/mimo-v2.5-pro', 74.6);
    renderPage([
      offer('clinepass', 'xiaomi/mimo-v2.5-pro', 85.9),
      m({
        ...wide,
        overallScore: {
          ...wide.overallScore,
          includedDimensions: [...wide.overallScore.includedDimensions, 'vision'],
          excludedDimensions: [],
        },
      }),
    ]);

    const group = screen.getByTestId('compare-group-xiaomi/mimo-v2.5-pro');
    const warning = within(group).getByTestId('compare-not-comparable');
    expect(warning).toHaveTextContent(/vision/);
    expect(within(group).queryByTestId('compare-best')).toBeNull();
    expect(within(group).queryByTestId('compare-spread')).toBeNull();
  });

  test('an offer nobody has scored shows as unrated, not as zero', () => {
    renderPage([
      offer('opencode-go', 'tencent/hy3', 57.6),
      offer('opencode-zen', 'tencent/hy3', null),
    ]);

    const group = screen.getByTestId('compare-group-tencent/hy3');
    expect(within(group).getByTestId('compare-offer-opencode-zen')).toHaveTextContent('Unrated');
    expect(within(group).getByTestId('compare-offer-opencode-zen')).not.toHaveTextContent('0.0%');
  });

  test('a catalog where no model is sold twice says that, rather than rendering an empty page', () => {
    renderPage([offer('opencode-go', 'alone/only-here', 88.0)]);

    expect(screen.getByTestId('compare-empty')).toBeInTheDocument();
    expect(screen.queryAllByTestId(/^compare-group-/)).toHaveLength(0);
  });
});
