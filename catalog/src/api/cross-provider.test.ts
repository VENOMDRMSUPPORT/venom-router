import { describe, test, expect } from 'vitest';
import { groupByCanonicalModel, type CrossProviderGroup } from './cross-provider';
import type { ApiModel } from './client';

/**
 * Fifteen of the catalog's thirty-three canonical models are served by more than
 * one provider, and the quality half of their score is identical by
 * construction — the difference is entirely operational. Kimi K3 scores 93.9% at
 * ClinePass and 91.0% at OpenCode Go; MiMo V2.5 Pro spans 85.9% to 74.6%. That
 * is the routing decision the catalog exists to inform, and no screen showed it.
 */
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

const scored = (providerId: string, canonicalId: string, value: number, over: Partial<ApiModel> = {}) =>
  m({
    providerId, canonicalId, modelId: `${providerId}/x`,
    overallScore: { ...m({ providerId, canonicalId }).overallScore, value, display: `${value.toFixed(1)}%` },
    ...over,
  });

const ids = (groups: CrossProviderGroup[]) => groups.map((g) => g.canonicalId);

describe('groupByCanonicalModel', () => {
  test('only models more than one provider serves are a comparison', () => {
    const groups = groupByCanonicalModel([
      scored('clinepass', 'moonshotai/kimi-k3', 93.9),
      scored('opencode-go', 'moonshotai/kimi-k3', 91.0),
      scored('opencode-go', 'alone/only-here', 88.0),
    ]);

    expect(ids(groups)).toEqual(['moonshotai/kimi-k3']);
  });

  test('offers are ordered best score first, so the answer is the top row', () => {
    const groups = groupByCanonicalModel([
      scored('opencode-zen', 'xiaomi/mimo-v2.5', 77.5),
      scored('clinepass', 'xiaomi/mimo-v2.5', 83.4),
      scored('opencode-go', 'xiaomi/mimo-v2.5', 80.9),
    ]);

    expect(groups[0].offers.map((o) => o.providerId)).toEqual(['clinepass', 'opencode-go', 'opencode-zen']);
  });

  test('the spread is the reason to look — widest first', () => {
    const groups = groupByCanonicalModel([
      scored('a', 'narrow/pair', 90), scored('b', 'narrow/pair', 89),
      scored('a', 'wide/pair', 90), scored('b', 'wide/pair', 75),
    ]);

    expect(ids(groups)).toEqual(['wide/pair', 'narrow/pair']);
    expect(groups[0].spread).toBeCloseTo(15);
    expect(groups[1].spread).toBeCloseTo(1);
  });

  /**
   * The quality half is shared per canonical model by construction, so a
   * difference in the graded dimension set means the two rows took different
   * exams — the mimo-v2.5-pro case, where a disputed `attachment` fact put
   * vision on one side's test and not the other's. That has to be stated, not
   * averaged away.
   */
  test('a group whose rows were graded differently is flagged as not comparable', () => {
    const wide = scored('opencode-go', 'xiaomi/mimo-v2.5-pro', 74.6);
    const groups = groupByCanonicalModel([
      scored('clinepass', 'xiaomi/mimo-v2.5-pro', 85.9),
      m({
        ...wide,
        overallScore: {
          ...wide.overallScore,
          includedDimensions: [...wide.overallScore.includedDimensions, 'vision'],
          excludedDimensions: [],
        },
      }),
    ]);

    expect(groups[0].comparable).toBe(false);
    expect(groups[0].gradedOnDifferentDimensions).toEqual(['vision']);
  });

  test('rows graded identically are comparable', () => {
    const groups = groupByCanonicalModel([
      scored('clinepass', 'moonshotai/kimi-k3', 93.9),
      scored('opencode-go', 'moonshotai/kimi-k3', 91.0),
    ]);

    expect(groups[0].comparable).toBe(true);
    expect(groups[0].gradedOnDifferentDimensions).toEqual([]);
  });

  /**
   * Live output caught this and the unit tests had not: two of fifteen groups
   * were titled `cline-pass/qwen3.8-max` and `cline-pass/glm-5.3`. A provider
   * that publishes no name for a row leaves `displayName` as the id, and the
   * title was taken from whichever offer scored highest — so the best offer
   * having no name cost the whole group its name, even though a sibling offer
   * published one.
   */
  test('the group is titled by an offer that actually has a name', () => {
    const groups = groupByCanonicalModel([
      scored('clinepass', 'qwen/qwen3.8-max', 93.5, { modelId: 'cline-pass/qwen3.8-max', displayName: 'cline-pass/qwen3.8-max' }),
      scored('opencode-go', 'qwen/qwen3.8-max', 93.0, { modelId: 'qwen3.8-max', displayName: 'Qwen3.8 Max' }),
    ]);

    expect(groups[0].displayName).toBe('Qwen3.8 Max');
  });

  test('the best offer still names the group when it has a real name', () => {
    const groups = groupByCanonicalModel([
      scored('clinepass', 'moonshotai/kimi-k3', 93.9, { modelId: 'cline-pass/kimi-k3', displayName: 'Kimi K3' }),
      scored('opencode-go', 'moonshotai/kimi-k3', 91.0, { modelId: 'kimi-k3', displayName: 'Kimi K3 Coder' }),
    ]);

    expect(groups[0].displayName).toBe('Kimi K3');
  });

  test('when no offer publishes a name the id stands rather than nothing', () => {
    const groups = groupByCanonicalModel([
      scored('a', 'v/unnamed', 90, { modelId: 'a/unnamed', displayName: 'a/unnamed' }),
      scored('b', 'v/unnamed', 80, { modelId: 'b/unnamed', displayName: 'b/unnamed' }),
    ]);

    expect(groups[0].displayName).toBe('a/unnamed');
  });

  test('a model with no settled identity is not pooled with anything', () => {
    // Grouping by a null canonical id would merge every unidentified row in the
    // catalog into one fictional "model" — the exact guess the identity rules
    // refuse to make.
    const groups = groupByCanonicalModel([
      m({ providerId: 'a', canonicalId: null, modelId: 'a/unknown-1', identityState: 'unresolved' }),
      m({ providerId: 'b', canonicalId: null, modelId: 'b/unknown-2', identityState: 'unresolved' }),
    ]);

    expect(groups).toEqual([]);
  });

  test('an unscored offer is listed but never presented as the best', () => {
    const unscored = scored('opencode-zen', 'tencent/hy3', 0);
    const groups = groupByCanonicalModel([
      scored('opencode-go', 'tencent/hy3', 57.6),
      m({ ...unscored, overallScore: { ...unscored.overallScore, value: null, display: '—', status: 'unknown' } }),
    ]);

    expect(groups[0].offers.map((o) => o.providerId)).toEqual(['opencode-go', 'opencode-zen']);
    expect(groups[0].best?.providerId).toBe('opencode-go');
    // One scored row cannot span anything, so there is no spread to rank on.
    expect(groups[0].spread).toBeNull();
  });

  test('two providers with no score at all yield no best and no spread', () => {
    const base = scored('a', 'nobody/scored', 0);
    const blank = (providerId: string) =>
      m({ ...base, providerId, overallScore: { ...base.overallScore, value: null, display: '—', status: 'unknown' } });

    const groups = groupByCanonicalModel([blank('a'), blank('b')]);

    expect(groups[0].best).toBeNull();
    expect(groups[0].spread).toBeNull();
  });
});
