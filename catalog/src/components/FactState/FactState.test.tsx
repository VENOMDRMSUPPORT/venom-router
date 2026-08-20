import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { FactState, factStateOf } from './FactState';
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
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 }, overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding'], excludedDimensions: ['vision'], uncertainty: 1, reasons: [], methodologyVersion: 'overall-score-v1', computedAt: '2026-08-13T00:00:00.000Z',
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

describe('factStateOf — the four states', () => {
  test('a value present, of any kind, is known', () => {
    expect(factStateOf(model(), 'context', 128_000)).toBe('known');
    expect(factStateOf(model(), 'structured', false)).toBe('known');
  });

  test('a field forced not-applicable is notApplicable regardless of any other signal', () => {
    expect(factStateOf(model(), 'cost', null, { notApplicable: true })).toBe('notApplicable');
  });

  test('a null value with no conflict on that field is missing', () => {
    expect(factStateOf(model(), 'maxOutput', null)).toBe('missing');
  });

  test('a null value WITH a conflict on that exact field is conflicted, not missing', () => {
    const m = model({
      conflicts: [{
        field: 'structured', sides: [{ value: true, by: 'a' }, { value: false, by: 'b' }],
        conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
        detectedAt: '2026-08-13T00:00:00.000Z',
      }],
    });
    expect(factStateOf(m, 'structured', null)).toBe('conflicted');
  });

  test('a conflict on a DIFFERENT field does not leak into this field\'s state', () => {
    const m = model({
      conflicts: [{
        field: 'structured', sides: [{ value: true, by: 'a' }, { value: false, by: 'b' }],
        conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
        detectedAt: '2026-08-13T00:00:00.000Z',
      }],
    });
    expect(factStateOf(m, 'maxOutput', null)).toBe('missing');
  });

  test('notApplicable outranks a conflict on the same field — it is the more settled answer', () => {
    const m = model({
      conflicts: [{
        field: 'cost', sides: [{ value: 1, by: 'a' }, { value: 2, by: 'b' }],
        conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
        detectedAt: '2026-08-13T00:00:00.000Z',
      }],
    });
    expect(factStateOf(m, 'cost', null, { notApplicable: true })).toBe('notApplicable');
  });

  test('a known value is never demoted by a conflict on its own field', () => {
    const m = model({
      conflicts: [{
        field: 'context', sides: [{ value: 128_000, by: 'a' }, { value: 64_000, by: 'b' }],
        conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
        detectedAt: '2026-08-13T00:00:00.000Z',
      }],
    });
    // A resolved conflict can still leave a known value on the model; `known`
    // must win once a value is actually present, ordering notwithstanding.
    expect(factStateOf(m, 'context', 128_000)).toBe('known');
  });
});

describe('<FactState> — known renders as plain children, the other three render as a chip', () => {
  test('known renders exactly its children, with no chip wrapper', () => {
    render(<FactState state="known">128K</FactState>);
    expect(screen.getByText('128K')).toBeInTheDocument();
    expect(screen.queryByText('n/a')).not.toBeInTheDocument();
    expect(screen.queryByText('missing')).not.toBeInTheDocument();
  });

  test('notApplicable renders a chip labelled "n/a"', () => {
    render(<FactState state="notApplicable" />);
    const chip = screen.getByText('n/a');
    expect(chip).toHaveAttribute('data-state', 'notApplicable');
  });

  test('missing renders a chip labelled "missing"', () => {
    render(<FactState state="missing" />);
    const chip = screen.getByText('missing');
    expect(chip).toHaveAttribute('data-state', 'missing');
  });

  test('conflicted renders a chip labelled "conflict"', () => {
    render(<FactState state="conflicted" />);
    const chip = screen.getByText('conflict');
    expect(chip).toHaveAttribute('data-state', 'conflicted');
  });
});

describe('the essential distinction — notApplicable must never render like missing', () => {
  test('their labels differ', () => {
    render(
      <>
        <FactState state="notApplicable" />
        <FactState state="missing" />
      </>,
    );
    const na = screen.getByText('n/a');
    const missing = screen.getByText('missing');
    expect(na.textContent).not.toBe(missing.textContent);
  });

  test('their data-state markers differ, so nothing downstream can confuse them', () => {
    render(
      <>
        <FactState state="notApplicable" />
        <FactState state="missing" />
      </>,
    );
    expect(screen.getByText('n/a')).toHaveAttribute('data-state', 'notApplicable');
    expect(screen.getByText('missing')).toHaveAttribute('data-state', 'missing');
  });

  test('their CSS classes differ, so they cannot be styled identically by accident', () => {
    render(
      <>
        <FactState state="notApplicable" />
        <FactState state="missing" />
      </>,
    );
    const na = screen.getByText('n/a');
    const missing = screen.getByText('missing');
    // Each chip carries the shared `chip` class plus a state-specific class;
    // the state-specific class must not coincide between the two states.
    const naClasses = na.className.split(' ');
    const missingClasses = missing.className.split(' ');
    const naOnly = naClasses.filter((c) => !missingClasses.includes(c));
    expect(naOnly.length).toBeGreaterThan(0);
  });

  test('their titles explain different facts about the world', () => {
    render(
      <>
        <FactState state="notApplicable" />
        <FactState state="missing" />
      </>,
    );
    const na = screen.getByText('n/a');
    const missing = screen.getByText('missing');
    expect(na.getAttribute('title')).not.toBe(missing.getAttribute('title'));
    expect(na.getAttribute('title')).toMatch(/not applicable/i);
    expect(missing.getAttribute('title')).toMatch(/no source published/i);
  });
});

describe('a value withheld by conflict is distinguishable from a value nobody published', () => {
  test('conflicted and missing carry different labels, states and titles', () => {
    render(
      <>
        <FactState state="conflicted" />
        <FactState state="missing" />
      </>,
    );
    const conflicted = screen.getByText('conflict');
    const missing = screen.getByText('missing');
    expect(conflicted).toHaveAttribute('data-state', 'conflicted');
    expect(missing).toHaveAttribute('data-state', 'missing');
    expect(conflicted.getAttribute('title')).toMatch(/sources disagreed/i);
    expect(missing.getAttribute('title')).toMatch(/no source published/i);
  });

  test('end to end: factStateOf feeding <FactState> renders "conflict" for a conflicted field and "missing" for a plain gap on the same model', () => {
    const m = model({
      conflicts: [{
        field: 'structured', sides: [{ value: true, by: 'a' }, { value: false, by: 'b' }],
        conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
        detectedAt: '2026-08-13T00:00:00.000Z',
      }],
    });
    render(
      <>
        <FactState state={factStateOf(m, 'structured', null)} />
        <FactState state={factStateOf(m, 'maxOutput', null)} />
      </>,
    );
    expect(screen.getByText('conflict')).toBeInTheDocument();
    expect(screen.getByText('missing')).toBeInTheDocument();
  });
});
