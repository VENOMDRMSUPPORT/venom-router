import { describe, test, expect } from 'vitest';
import { modelMatchesFilter, providerMatchesFilter } from './filters';
import type { ApiModel } from './client';

function fakeModel(over: Partial<ApiModel> = {}): ApiModel {
  return {
    lifecycle: null,
    providerId: 'test-provider',
    modelId: 'test-model',
    canonicalId: null,
    vendorModelId: null,
    identityState: 'resolved',
    rejectedCandidates: [],
    displayName: 'Test Model',
    state: 'active',
    contextTokens: 128_000,
    maxOutputTokens: 4_096,
    inputModalities: ['text'],
    capabilities: { tools: true, reasoning: true, structured: true, attachment: false },
    pricing: {
      kind: 'per_token',
      inputPerMTokens: 1.0,
      outputPerMTokens: 2.0,
      referenceInPerMTokens: null,
      referenceOutPerMTokens: null,
      isFree: false,
    },
    vq: {
      value: 60,
      uncertainty: null,
      bound: null,
      evidenceLevel: 'measured',
      precision: 1,
      display: '60.0',
      unratedReason: null,
      provenance: null,
    },
    vo: {
      value: 70,
      dimensions: {},
      missingDimensions: [],
      notApplicableDimensions: [],
      profileId: 'balanced',
    },
    modelScore: {
      value: 65,
      display: '65.0%',
      methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7,
      operationalWeight: 0.3,
      operationalPrecision: 0,
      uncertainty: null,
      bound: null,
      reason: null,
      qualityEvidenceLevel: 'measured',
      operationalCoverage: 'complete',
    },
    overallScore: {
      value: 65,
      display: '65.0%',
      status: 'complete',
      qualityScore: 60,
      operationalScore: 70,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: [],
      excludedDimensions: [],
      uncertainty: null,
      reasons: [],
      methodologyVersion: 'overall-score-v1',
      computedAt: null,
    },
    resolution: {
      state: 'complete',
      reasons: [],
      firstDetectedAt: null,
      lastAttemptAt: null,
      nextAttemptAt: null,
    },
    catalogReady: true,
    missingFacts: [],
    conflicts: [],
    provenanceByField: {},
    qualityRank: 1,
    tiedAtRank: false,
    modelRank: 1,
    tiedAtModelRank: false,
    overallRank: 1,
    tiedAtOverallRank: false,
    firstSeenAt: '2026-08-01T00:00:00Z',
    lastSeenAt: '2026-08-01T00:00:00Z',
    ...over,
  };
}

describe('modelMatchesFilter', () => {
  test('all filter always matches', () => {
    expect(modelMatchesFilter(fakeModel(), 'all')).toBe(true);
  });

  test('free filter matches only when isFree is true', () => {
    const free = fakeModel({ pricing: { kind: 'free', inputPerMTokens: 0, outputPerMTokens: 0, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: true } });
    const paid = fakeModel({ pricing: { kind: 'per_token', inputPerMTokens: 1, outputPerMTokens: 2, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false } });
    const included = fakeModel({ pricing: { kind: 'included', inputPerMTokens: null, outputPerMTokens: null, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false } });

    expect(modelMatchesFilter(free, 'free')).toBe(true);
    expect(modelMatchesFilter(paid, 'free')).toBe(false);
    expect(modelMatchesFilter(included, 'free')).toBe(false);
  });

  test('paid filter matches subscription included and per_token paid models', () => {
    const free = fakeModel({ pricing: { kind: 'free', inputPerMTokens: 0, outputPerMTokens: 0, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: true } });
    const paid = fakeModel({ pricing: { kind: 'per_token', inputPerMTokens: 1, outputPerMTokens: 2, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false } });
    const included = fakeModel({ pricing: { kind: 'included', inputPerMTokens: null, outputPerMTokens: null, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false } });

    expect(modelMatchesFilter(free, 'paid')).toBe(false);
    expect(modelMatchesFilter(paid, 'paid')).toBe(true);
    expect(modelMatchesFilter(included, 'paid')).toBe(true);
  });

  test('1m filter matches context tokens >= 1,000,000', () => {
    const small = fakeModel({ contextTokens: 128_000 });
    const exact1m = fakeModel({ contextTokens: 1_000_000 });
    const large = fakeModel({ contextTokens: 2_000_000 });

    expect(modelMatchesFilter(small, '1m')).toBe(false);
    expect(modelMatchesFilter(exact1m, '1m')).toBe(true);
    expect(modelMatchesFilter(large, '1m')).toBe(true);
  });

  test('multimodal filter matches input modalities with non-text modalities', () => {
    const textOnly = fakeModel({ inputModalities: ['text'] });
    const vision = fakeModel({ inputModalities: ['text', 'image'] });
    const audio = fakeModel({ inputModalities: ['audio'] });

    expect(modelMatchesFilter(textOnly, 'multimodal')).toBe(false);
    expect(modelMatchesFilter(vision, 'multimodal')).toBe(true);
    expect(modelMatchesFilter(audio, 'multimodal')).toBe(true);
  });

  test('current filter filters out deprecated models', () => {
    const active = fakeModel({ lifecycle: null });
    const deprecated = fakeModel({ lifecycle: 'deprecated' });

    expect(modelMatchesFilter(active, 'current')).toBe(true);
    expect(modelMatchesFilter(deprecated, 'current')).toBe(false);
  });
});

describe('providerMatchesFilter', () => {
  test('matches provider if at least one model matches the filter', () => {
    const models = [
      fakeModel({ providerId: 'p1', pricing: { kind: 'free', inputPerMTokens: 0, outputPerMTokens: 0, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: true } }),
      fakeModel({ providerId: 'p2', pricing: { kind: 'per_token', inputPerMTokens: 1, outputPerMTokens: 2, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false } }),
    ];

    expect(providerMatchesFilter('p1', models, 'free')).toBe(true);
    expect(providerMatchesFilter('p2', models, 'free')).toBe(false);
    expect(providerMatchesFilter('p1', models, 'paid')).toBe(false);
    expect(providerMatchesFilter('p2', models, 'paid')).toBe(true);
    expect(providerMatchesFilter('p1', models, 'all')).toBe(true);
  });
});
