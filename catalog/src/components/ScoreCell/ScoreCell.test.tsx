import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { CostCell, ModelScoreCell } from './ScoreCell';
import type { ApiModel } from '../../api/client';

/** A complete, resolved row. Each test bends one thing. */
function model(over: Partial<ApiModel> = {}): ApiModel {
  return {
    providerId: 'p',
    modelId: 'm',
    lifecycle: null,
    canonicalId: 'up/m',
    vendorModelId: null,
    identityState: 'resolved',
    rejectedCandidates: [],
    displayName: 'm',
    state: 'active',
    contextTokens: 128_000,
    maxOutputTokens: 32_000,
    inputModalities: ['text'],
    capabilities: { tools: true, reasoning: true, structured: true, attachment: true },
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
      bound: null, reason: null, qualityEvidenceLevel: 'measured',
      operationalCoverage: 'complete',
    },
    overallScore: {
      value: 56, display: '56.0%', status: 'complete', qualityScore: 50, operationalScore: 70,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding', 'reasoning', 'longContext', 'toolCalling', 'structuredOutput', 'speed', 'costEfficiency'],
      excludedDimensions: ['vision'], uncertainty: 1, reasons: [], methodologyVersion: 'overall-score-v1', computedAt: '2026-08-13T00:00:00.000Z',
    },
    resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    modelRank: 1,
    tiedAtModelRank: false,
    overallRank: 1,
    tiedAtOverallRank: false,
    qualityRank: 1,
    tiedAtRank: false,
    firstSeenAt: '2026-08-01T00:00:00.000Z',
    lastSeenAt: '2026-08-13T00:00:00.000Z',
    ...over,
  };
}

describe('ModelScoreCell', () => {
  test('renders the server overall score and independent coverage', () => {
    render(
      <ModelScoreCell
        model={model({
          overallScore: {
            ...model().overallScore,
            value: 56.3,
            display: '56.3%',
            overallCoverage: { scored: 6, applicable: 7, percent: 85.7142857 },
          },
        })}
      />,
    );

    expect(screen.getByText('56.3%')).toHaveAttribute('title', expect.stringContaining('overall-score-v1'));
    // Counts, not a rounded percentage: "86% coverage" does not say what is
    // missing, and "6 of 7 dimensions" does — from the same two numbers.
    expect(screen.getByText('6 of 7 dimensions')).toBeInTheDocument();
  });

  test.each([
    ['evaluating', 'Evaluating'],
    ['insufficient_evidence', 'Insufficient evidence'],
    ['unknown', 'Unrated'],
  ] as const)('maps overall status %s to %s when no score exists', (status, label) => {
    render(
      <ModelScoreCell model={model({
        overallScore: { ...model().overallScore, status, value: null, display: '—', reasons: ['missing_coding_evaluation'] },
        overallRank: null,
      })} />,
    );
    expect(screen.getByText(label)).toBeInTheDocument();
  });

  test('does not fall back to the legacy model score', () => {
    render(<ModelScoreCell model={model({
      modelScore: { ...model().modelScore, value: 99, display: '99.0%' },
      overallScore: { ...model().overallScore, value: null, display: '—', status: 'insufficient_evidence' },
    })} />);
    expect(screen.queryByText('99.0%')).not.toBeInTheDocument();
    expect(screen.getByText('Insufficient evidence')).toBeInTheDocument();
  });
});

describe('CostCell — the billingKind FactState branch', () => {
  test('per_token with no published price is named as a gap, not rendered as a blank', () => {
    // No own price and no `free`/`included` kind: this falls through to the
    // FactState('billingKind') branch, which must say "missing", not "—".
    render(
      <CostCell
        model={model({
          pricing: {
            kind: 'unknown', inputPerMTokens: null, outputPerMTokens: null,
            referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: null,
          },
        })}
        side="in"
      />,
    );
    expect(screen.getByText('missing')).toBeInTheDocument();
    expect(screen.queryByText('Free')).not.toBeInTheDocument();
    expect(screen.queryByText('Included · n/a')).not.toBeInTheDocument();
  });

  test('a billingKind gap withheld by a conflict on that field says "conflict", not "missing"', () => {
    render(
      <CostCell
        model={model({
          pricing: {
            kind: 'unknown', inputPerMTokens: null, outputPerMTokens: null,
            referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: null,
          },
          conflicts: [{
            field: 'billingKind',
            sides: [{ value: 'per_token', by: 'a/m' }, { value: 'free', by: 'b/m' }],
            conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
            detectedAt: '2026-08-13T00:00:00.000Z',
          }],
        })}
        side="in"
      />,
    );
    expect(screen.getByText('conflict')).toBeInTheDocument();
    expect(screen.queryByText('missing')).not.toBeInTheDocument();
  });

  test('the billingKind gap chip still coexists with a reference price note', () => {
    render(
      <CostCell
        model={model({
          pricing: {
            kind: 'unknown', inputPerMTokens: null, outputPerMTokens: null,
            referenceInPerMTokens: 3, referenceOutPerMTokens: null, isFree: null,
          },
        })}
        side="in"
      />,
    );
    expect(screen.getByText('missing')).toBeInTheDocument();
    expect(screen.getByText(/ref \$3/)).toBeInTheDocument();
  });

  test('free and included still take their own, earlier branches — the gap branch does not shadow them', () => {
    const { unmount } = render(
      <CostCell
        model={model({ pricing: { kind: 'free', inputPerMTokens: 0, outputPerMTokens: 0, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: true } })}
        side="in"
      />,
    );
    expect(screen.getByText('Free')).toBeInTheDocument();
    expect(screen.queryByText('missing')).not.toBeInTheDocument();
    unmount();

    render(
      <CostCell
        model={model({ pricing: { kind: 'included', inputPerMTokens: null, outputPerMTokens: null, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false } })}
        side="in"
      />,
    );
    expect(screen.getByText('Included · n/a')).toBeInTheDocument();
    expect(screen.queryByText('missing')).not.toBeInTheDocument();
  });
});

const scored = (coverage: { percent: number; scored: number; applicable: number }) => model({
  overallScore: { ...model().overallScore, value: 91.3, display: '91.3%', status: 'complete', overallCoverage: coverage },
});
const unscored = () => model({
  overallScore: {
    ...model().overallScore, value: null, display: '—', status: 'insufficient_evidence',
    reasons: ['unknown_speed'],
  },
});

describe('what the score cell says, and what it stops repeating', () => {
  test('a complete score shows the number and no coverage badge', () => {
    // Thirteen rows each carrying "100% coverage" is thirteen statements that
    // nothing is missing. A badge should mark the exception, not the norm.
    render(<ModelScoreCell model={scored({ percent: 100, scored: 6, applicable: 6 })} />);
    expect(screen.getByText('91.3%')).toBeInTheDocument();
    expect(screen.queryByText(/coverage/i)).not.toBeInTheDocument();
  });

  test('partial coverage is still called out, because that one matters', () => {
    render(<ModelScoreCell model={scored({ percent: 67, scored: 4, applicable: 6 })} />);
    expect(screen.getByText('4 of 6 dimensions')).toBeInTheDocument();
  });

  test('an unscored row states its state once, not twice', () => {
    // It used to render an em dash AND an "Insufficient evidence" badge: two
    // glyphs for one fact, stacked, on every unplaced row.
    render(<ModelScoreCell model={unscored()} />);
    expect(screen.getByText('Insufficient evidence')).toBeInTheDocument();
    expect(screen.queryByText('—')).not.toBeInTheDocument();
  });
});

/**
 * Two records of the same canonical model scored 85.9% and 74.6%, both labelled
 * complete at 100% coverage, because a disputed `attachment` fact made vision
 * applicable to one and not the other. The percentage is computed against the
 * APPLICABLE dimensions, so a narrower test set certifies itself as 100% — and
 * 24 rows graded on 7 dimensions sat in one ranking beside 25 graded on 8, with
 * nothing on screen saying so.
 */
describe('a score says how much of the test set produced it', () => {
  test('an excluded dimension is named, with the count out of the full set', () => {
    render(<ModelScoreCell model={model()} />);

    const badge = screen.getByTestId('overall-graded-on');
    expect(badge).toHaveTextContent('7 of 8');
    expect(badge.getAttribute('title')).toContain('vision');
  });

  test('a model graded on everything carries no such badge', () => {
    render(<ModelScoreCell model={model({
      overallScore: {
        ...model().overallScore,
        includedDimensions: ['coding', 'reasoning', 'longContext', 'toolCalling', 'structuredOutput', 'vision', 'speed', 'costEfficiency'],
        excludedDimensions: [],
        overallCoverage: { scored: 8, applicable: 8, percent: 100 },
      },
    })} />);

    expect(screen.queryByTestId('overall-graded-on')).toBeNull();
  });

  test('an unscored model shows its state, not a dimension count', () => {
    render(<ModelScoreCell model={model({
      overallScore: { ...model().overallScore, value: null, display: '—', status: 'insufficient_evidence' },
    })} />);

    expect(screen.queryByTestId('overall-graded-on')).toBeNull();
    expect(screen.getByText('Insufficient evidence')).toBeInTheDocument();
  });
});
