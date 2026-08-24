/**
 * The wire boundary.
 *
 * Every other SPA test builds a complete `ApiModel` by hand, which is why none of
 * them could catch the defect this file exists for: the fixture asserts the very
 * shape the bug is about. A hand-built `ApiModel` can only ever prove that the
 * renderers agree with data the renderers already agree with.
 *
 * So these tests start from RAW BYTES — the JSON a real catalog service actually
 * put on the wire — and go through `fetchCatalog`, the one place where wire data
 * becomes an `ApiModel`. The fixtures below are deliberately NOT typed as
 * `ApiModel`: if they were, TypeScript would force them to carry the fields whose
 * absence is the whole point.
 *
 * The contract being pinned, in one line: an absent field becomes a *renderable*
 * absence, never a fabricated value. `[]`, `{}` and `unknown` say "this response
 * did not carry it". `0` would say "we looked and found none", which is a
 * different claim about the world.
 */

import { describe, test, expect, vi, afterEach } from 'vitest';
import { fetchCatalog, fetchEvaluationDetail, fetchEvaluationState, startEvaluation, regradeEvaluation, ServiceError, fetchHealth } from './client';

/**
 * One model exactly as the pre-M5.1 service shape puts it on the wire.
 *
 * Copied from a live `GET /v1/models` response: `identityState`,
 * `rejectedCandidates`, `conflicts`, `provenanceByField`,
 * `vo.notApplicableDimensions` and `vq.unratedReason` are simply not there.
 */
function staleWireModel(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    providerId: 'clinepass',
    modelId: 'cline-pass/deepseek-v4-flash',
    canonicalId: 'deepseek/deepseek-v4-flash',
    displayName: 'DeepSeek V4 Flash',
    state: 'active',
    contextTokens: 1_000_000,
    maxOutputTokens: 384_000,
    inputModalities: ['text'],
    capabilities: { tools: true, reasoning: true, structured: true, attachment: false },
    pricing: {
      kind: 'per_token',
      inputPerMTokens: 0.14,
      outputPerMTokens: 0.28,
      referenceInPerMTokens: 0.14,
      referenceOutPerMTokens: 0.28,
      isFree: false,
    },
    vq: {
      value: 39.2177824942824,
      uncertainty: 6.159902295761639,
      bound: null,
      evidenceLevel: 'calibrated',
      precision: 0,
      display: '39',
      provenance: null,
    },
    vo: {
      value: 74.39178108314263,
      dimensions: { context: 66.95, output: 92.98, capabilities: 60, cost: 88.54 },
      missingDimensions: [],
      profileId: 'balanced',
    },
    catalogReady: true,
    missingFacts: [],
    qualityRank: 31,
    tiedAtRank: true,
    firstSeenAt: '2026-08-12T20:30:48.525Z',
    lastSeenAt: '2026-08-13T08:33:50.879Z',
    ...over,
  };
}

/** The pre-M5.1 `meta`: the old `identity` sub-shape, and no truth counters. */
function staleWireMeta(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    methodologyVersion: 'venom-score-v1',
    profileId: 'balanced',
    liveModels: 116,
    catalogReady: 109,
    needsVerification: 7,
    qualityScored: 99,
    modelScoreScored: 99,
    operationalScored: 115,
    unrated: 17,
    // The superseded shape. Nothing here answers the question the current
    // contract asks, so nothing here may be presented as if it did.
    identity: { resolvedWithEvidence: 99, resolvedWithoutEvidence: 6, unresolved: 11, ambiguousOpen: 0 },
    identityRules: { exact: 94 },
    calibration: null,
    sortContracts: {},
    ...over,
  };
}

function wireProvider(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'clinepass',
    name: 'ClinePass',
    rosterUrl: 'https://api.cline.bot/api/v1/ai/cline/recommended-models',
    liveModels: 12,
    lastSuccessfulSyncAt: '2026-08-13T08:33:50.879Z',
    lastAttemptedSyncAt: '2026-08-13T08:33:50.879Z',
    lastOutcome: 'ok',
    freshness: 'fresh',
    hoursSinceSuccess: 2.5,
    qualityScored: 12,
    modelScoreScored: 12,
    unrated: 0,
    ...over,
  };
}

/** Answer `/v1/models` and `/v1/providers` with the given raw bodies. */
function stubService(models: unknown[], meta: unknown, providers: unknown[] = [wireProvider()]) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (String(url).includes('/v1/models')) {
        return { ok: true, status: 200, json: async () => ({ models, meta }) } as unknown as Response;
      }
      if (String(url).includes('/v1/providers')) {
        return { ok: true, status: 200, json: async () => ({ providers }) } as unknown as Response;
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('a pre-M5.1 service response is normalised into renderable absences', () => {
  test('the six fields the current contract adds are always present, and never fabricated', async () => {
    stubService([staleWireModel()], staleWireMeta());
    const data = await fetchCatalog();
    const [m] = data.models;

    // Arrays and records: empty rather than undefined, so no consumer can throw
    // reading `.length` — the exact failure that blanked /provider/clinepass.
    expect(m.rejectedCandidates).toEqual([]);
    expect(m.conflicts).toEqual([]);
    expect(m.openConflicts).toEqual([]);
    expect(m.provenanceByField).toEqual({});
    expect(m.vo.notApplicableDimensions).toEqual([]);

    // A reason nobody sent is `null` — "not recorded" — not an invented token.
    expect(m.vq.unratedReason).toBeNull();
    expect(m.resolution).toEqual({
      state: 'unknown', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null,
    });
  });

  test('a current resolution object passes through unchanged', async () => {
    const resolution = {
      state: 'processing', reasons: ['missing_vq'], firstDetectedAt: '2026-08-19T10:00:00.000Z',
      lastAttemptAt: '2026-08-19T10:00:00.000Z', nextAttemptAt: '2026-08-19T10:01:00.000Z',
    };
    stubService([staleWireModel({ resolution })], staleWireMeta());
    const data = await fetchCatalog();
    expect(data.models[0].resolution).toEqual(resolution);
  });

  test('an absent composite score stays unreported rather than being recomputed in the client', async () => {
    stubService([staleWireModel()], staleWireMeta());
    const data = await fetchCatalog();
    const [m] = data.models;

    expect(m.modelScore).toEqual({
      value: null,
      display: '—',
      methodologyVersion: null,
      qualityWeight: null,
      operationalWeight: null,
      operationalPrecision: null,
      uncertainty: null,
      bound: null,
      reason: 'not_reported',
      qualityEvidenceLevel: 'calibrated',
      operationalCoverage: 'unknown',
    });
    expect(m.modelRank).toBeNull();
    expect(m.tiedAtModelRank).toBe(false);
    expect(m.overallScore).toEqual({
      value: null,
      display: '—',
      status: 'unknown',
      qualityScore: null,
      operationalScore: null,
      qualityCoverage: { scored: 0, applicable: 0, percent: 0 },
      overallCoverage: { scored: 0, applicable: 0, percent: 0 },
      includedDimensions: [],
      excludedDimensions: [],
      uncertainty: null,
      reasons: ['not_reported'],
      methodologyVersion: 'overall-score-v1',
      computedAt: null,
    });
    expect(m.overallRank).toBeNull();
    expect(m.tiedAtOverallRank).toBe(false);
    expect(data.meta.scoringPolicy).toEqual({
      methodologyVersion: null,
      qualityWeight: null,
      operationalWeight: null,
      operationalPrecision: null,
    });
  });

  test('an identity the response never stated reads as unknown, not as resolved and not as unresolved', async () => {
    // Both wrong answers are worse than no answer. `resolved` invents a proof
    // that this row is a specific upstream model; `unresolved` invents the
    // finding that nothing upstream matched. The response simply did not say.
    stubService([staleWireModel()], staleWireMeta());
    const data = await fetchCatalog();

    expect(data.models[0].identityState).toBe('unknown');
  });

  test('identity is not back-derived from canonicalId, in either direction', async () => {
    // `canonicalId` is set here and still must not promote the row to `resolved`:
    // the contract states the two are independent axes, and a client that infers
    // one from the other cannot tell "investigated and parked" from "never
    // looked at" — the distinction the field was added to carry.
    stubService(
      [staleWireModel({ canonicalId: 'deepseek/deepseek-v4-flash' }), staleWireModel({ modelId: 'x', canonicalId: null })],
      staleWireMeta(),
    );
    const data = await fetchCatalog();

    expect(data.models.map((m) => m.identityState)).toEqual(['unknown', 'unknown']);
  });

  test('an unknown value stays unknown — normalisation never lowers it to zero', async () => {
    stubService(
      [
        staleWireModel({
          contextTokens: null,
          maxOutputTokens: null,
          qualityRank: null,
          vq: { value: null, uncertainty: null, bound: null, evidenceLevel: 'unrated', precision: 0, display: '—', provenance: null },
          vo: { value: null, dimensions: {}, missingDimensions: ['context', 'output'], profileId: 'balanced' },
        }),
      ],
      staleWireMeta(),
    );
    const data = await fetchCatalog();
    const [m] = data.models;

    // The oldest rule in this catalog: unrated is not low-rated.
    expect(m.vq.value).toBeNull();
    expect(m.vo.value).toBeNull();
    expect(m.contextTokens).toBeNull();
    expect(m.maxOutputTokens).toBeNull();
    expect(m.qualityRank).toBeNull();
  });

  test('a real gap is not relabelled as not-applicable', async () => {
    // `missingDimensions` is open work; `notApplicableDimensions` is an answer.
    // Filling the second from the first would retire a gap by renaming it.
    stubService(
      [staleWireModel({ vo: { value: 40, dimensions: {}, missingDimensions: ['cost'], profileId: 'balanced' } })],
      staleWireMeta(),
    );
    const data = await fetchCatalog();

    expect(data.models[0].vo.missingDimensions).toEqual(['cost']);
    expect(data.models[0].vo.notApplicableDimensions).toEqual([]);
  });

  test('meta counters the response omitted read as unknown, not as zero', async () => {
    stubService([staleWireModel()], staleWireMeta());
    const { meta } = await fetchCatalog();

    expect(meta.identityDetail).toBeNull();
    expect(meta.conflictedModels).toBeNull();
    expect(meta.conflictsByField).toEqual({});

    // The superseded `identity` sub-shape answers a different question, so the
    // current one stays unanswered rather than being back-filled from it.
    expect(meta.identity).toEqual({ resolved: null, identityReview: null, unresolved: null });
  });

  test('facts the stale response DID carry are passed through untouched', async () => {
    stubService([staleWireModel()], staleWireMeta());
    const data = await fetchCatalog();
    const [m] = data.models;

    expect(m.contextTokens).toBe(1_000_000);
    expect(m.vq.display).toBe('39');
    expect(m.vq.evidenceLevel).toBe('calibrated');
    expect(m.vo.value).toBeCloseTo(74.39178108314263);
    expect(m.tiedAtRank).toBe(true);
    expect(m.pricing.inputPerMTokens).toBe(0.14);
    expect(data.origin).toBe('live');
  });
});

describe('a current-contract response is not clobbered by the normaliser', () => {
  test('preserves the server-derived score, rank, and policy verbatim', async () => {
    const modelScore = {
      value: 48.769,
      display: '48.8%',
      methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7,
      operationalWeight: 0.3,
      operationalPrecision: 0,
      uncertainty: 4.311931606,
      bound: null,
      reason: null,
      qualityEvidenceLevel: 'calibrated',
      operationalCoverage: 'complete',
    };
    const scoringPolicy = {
      methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7,
      operationalWeight: 0.3,
      operationalPrecision: 0,
    };
    const overallScore = {
      value: 72.456,
      display: '72.5%',
      status: 'complete',
      qualityScore: 75.2,
      operationalScore: 66.053333333,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding', 'reasoning', 'speed', 'costEfficiency'],
      excludedDimensions: ['vision'],
      uncertainty: 1.4,
      reasons: [],
      methodologyVersion: 'overall-score-v1',
      computedAt: '2026-08-19T10:00:00.000Z',
    };
    stubService(
      [staleWireModel({ modelScore, modelRank: 9, tiedAtModelRank: true, overallScore, overallRank: 4, tiedAtOverallRank: true })],
      staleWireMeta({ scoringPolicy }),
    );

    const data = await fetchCatalog();

    expect(data.models[0].modelScore).toEqual(modelScore);
    expect(data.models[0].modelRank).toBe(9);
    expect(data.models[0].tiedAtModelRank).toBe(true);
    expect(data.models[0].overallScore).toEqual(overallScore);
    expect(data.models[0].overallRank).toBe(4);
    expect(data.models[0].tiedAtOverallRank).toBe(true);
    expect(data.meta.scoringPolicy).toEqual(scoringPolicy);
  });

  test('every M5.1 field the service did send survives verbatim', async () => {
    const conflict = {
      field: 'context',
      sides: [{ value: 128000, by: 'models.dev' }, { value: 200000, by: 'provider' }],
      conflictType: 'value_mismatch',
      status: 'open',
      resolvedTo: null,
      detectedAt: '2026-08-13T00:00:00.000Z',
    };
    const rejection = {
      candidate: 'deepseek/deepseek-v3',
      verdict: 'candidate_rejected',
      why: 'parameter count differs',
      evidence: ['models.dev lists 671B'],
      source: 'models.dev',
      sourceRef: null,
      sourceUrl: null,
      evidenceState: 'first_party',
      resolverVersion: 'r1',
      candidateMeta: null,
      reviewedAt: null,
      recordedAt: '2026-08-13T00:00:00.000Z',
    };
    const provenance = {
      value: 128000,
      source: 'models.dev',
      sourceRef: null,
      sourceUrl: null,
      evidenceState: 'first_party',
      rawValue: 128000,
      resolverVersion: 'r1',
      probeVersion: null,
      resolvedAt: '2026-08-13T00:00:00.000Z',
    };

    stubService(
      [
        staleWireModel({
          identityState: 'identity_review',
          rejectedCandidates: [rejection],
          conflicts: [conflict],
          provenanceByField: { context: provenance },
          vq: { ...(staleWireModel().vq as object), value: null, display: '—', evidenceLevel: 'unrated', unratedReason: 'identity_ambiguous' },
          vo: { value: 70, dimensions: {}, missingDimensions: [], notApplicableDimensions: ['cost'], profileId: 'balanced' },
        }),
      ],
      staleWireMeta({
        identity: { resolved: 100, identityReview: 5, unresolved: 11 },
        identityDetail: { ambiguousOpen: 1, withRejectedCandidates: 5, rejectedCandidates: 9 },
        conflictedModels: 102,
        conflictsByField: { context: 102 },
      }),
    );

    const data = await fetchCatalog();
    const [m] = data.models;

    expect(m.identityState).toBe('identity_review');
    expect(m.rejectedCandidates).toEqual([rejection]);
    expect(m.conflicts).toEqual([conflict]);
    expect(m.openConflicts).toEqual([conflict]);
    expect(m.provenanceByField).toEqual({ context: provenance });
    expect(m.vq.unratedReason).toBe('identity_ambiguous');
    expect(m.vo.notApplicableDimensions).toEqual(['cost']);

    expect(data.meta.identity).toEqual({ resolved: 100, identityReview: 5, unresolved: 11 });
    expect(data.meta.identityDetail).toEqual({ ambiguousOpen: 1, withRejectedCandidates: 5, rejectedCandidates: 9 });
    expect(data.meta.conflictedModels).toBe(102);
    expect(data.meta.conflictsByField).toEqual({ context: 102 });
  });

  test('an explicitly resolved identity is preserved, not downgraded to unknown', async () => {
    stubService([staleWireModel({ identityState: 'resolved' })], staleWireMeta());
    const data = await fetchCatalog();

    expect(data.models[0].identityState).toBe('resolved');
  });

  test('a value outside the contract is refused rather than passed on as a state', async () => {
    // Fail closed: an unrecognised token is not a fourth identity state, and
    // rendering it raw would put a string nobody defined in front of a reader.
    stubService([staleWireModel({ identityState: 'probably_fine' })], staleWireMeta());
    const data = await fetchCatalog();

    expect(data.models[0].identityState).toBe('unknown');
  });
});

describe('the offline snapshot is the last live answer, or it is refused', () => {
  /** A snapshot file in the shape the service actually serves. */
  function stubSnapshot(body: unknown) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).includes('/v1/')) throw new Error('service down');
        return { ok: true, status: 200, json: async () => body } as unknown as Response;
      }),
    );
  }

  test('every meta figure the live page states, the offline page states too', async () => {
    // The three tiles that read MISSING in the browser. They were missing
    // because the snapshot was a dump of database ROWS and the client rebuilt
    // `meta` from them by hand — a reconstruction that has no identity column,
    // no conflict rows and no completeness verdict to read.
    stubSnapshot({
      generatedAt: '2026-08-18T09:00:00.000Z',
      models: [staleWireModel()],
      providers: [wireProvider()],
      meta: staleWireMeta({
        liveModels: 65,
        catalogReady: 56,
        needsVerification: 9,
        conflictedModels: 49,
        identity: { resolved: 56, identityReview: 5, unresolved: 4 },
        identityDetail: { ambiguousOpen: 0, withRejectedCandidates: 5, rejectedCandidates: 7 },
      }),
    });

    const data = await fetchCatalog();

    expect(data.origin).toBe('snapshot');
    expect(data.snapshotGeneratedAt).toBe('2026-08-18T09:00:00.000Z');
    expect(data.meta.conflictedModels).toBe(49);
    expect(data.meta.identity).toEqual({ resolved: 56, identityReview: 5, unresolved: 4 });
    expect(data.meta.identityDetail).toEqual({ ambiguousOpen: 0, withRejectedCandidates: 5, rejectedCandidates: 7 });
  });

  test('completeness is read from the snapshot, never assumed from the row count', async () => {
    // The fabrication this replaces: `catalogReady = models.length` and
    // `needsVerification = 0`, published no matter what the catalog found. The
    // fixture sends ONE model with counters that disagree with the row count,
    // so passing by coincidence is not available.
    stubSnapshot({
      generatedAt: '2026-08-18T09:00:00.000Z',
      models: [staleWireModel()],
      providers: [wireProvider()],
      meta: staleWireMeta({ liveModels: 65, catalogReady: 56, needsVerification: 9 }),
    });

    const data = await fetchCatalog();

    expect(data.meta.catalogReady).toBe(56);
    expect(data.meta.needsVerification).toBe(9);
  });

  test('snapshot rows go through the same normaliser as live rows', async () => {
    // One place decides what an absent field becomes. If the snapshot path had
    // its own answer, the fallback view could crash where the live view does
    // not — a second copy of a rule is the defect, not the variation.
    stubSnapshot({
      generatedAt: '2026-08-18T09:00:00.000Z',
      models: [staleWireModel()],
      providers: [wireProvider()],
      meta: staleWireMeta(),
    });

    const [m] = (await fetchCatalog()).models;

    expect(m.rejectedCandidates).toEqual([]);
    expect(m.conflicts).toEqual([]);
    expect(m.openConflicts).toEqual([]);
    expect(m.provenanceByField).toEqual({});
    expect(m.vo.notApplicableDimensions).toEqual([]);
    expect(m.vq.unratedReason).toBeNull();
    expect(m.identityState).toBe('unknown');
  });

  test('a snapshot from the superseded row format is refused, not reconstructed', async () => {
    // The old file carried `provider_id`/`vq_value` database columns and no
    // meta at all. Rendering it meant inventing every counter it could not
    // answer. Unknown is ineligible: the page says it has nothing to show and
    // names the command that fixes it, which is recoverable — a page quietly
    // reporting invented totals is not.
    stubSnapshot({
      generatedAt: '2026-08-12T20:30:49.046Z',
      providers: [{ id: 'clinepass', name: 'ClinePass', roster_url: 'https://example.invalid' }],
      models: [{ provider_id: 'clinepass', model_id: 'cline-pass/deepseek-v4-flash', vq_value: 39 }],
    });

    await expect(fetchCatalog()).rejects.toThrow(/superseded/i);
  });
});

describe('the evaluation control client', () => {
  const withFetch = async (impl: typeof fetch, run: () => Promise<void>) => {
    const original = globalThis.fetch;
    globalThis.fetch = impl;
    try {
      await run();
    } finally {
      globalThis.fetch = original;
    }
  };
  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });

  test('a refusal comes back as a value carrying the typed reason', async () => {
    await withFetch(
      (async () => json({ error: 'cannot evaluate', reason: 'missing_credentials' }, 422)) as typeof fetch,
      async () => {
        const outcome = await startEvaluation('p', 'm');
        expect(outcome.ok).toBe(false);
        if (outcome.ok) throw new Error('unreachable');
        expect(outcome.reason).toBe('missing_credentials');
        expect(outcome.status).toBe(422);
      },
    );
  });

  test('a refusal with no body still names something usable', async () => {
    await withFetch(
      (async () => new Response('', { status: 409 })) as typeof fetch,
      async () => {
        const outcome = await startEvaluation('p', 'm');
        expect(outcome.ok).toBe(false);
        if (outcome.ok) throw new Error('unreachable');
        expect(outcome.reason).toBe('http_409');
      },
    );
  });

  test('an accepted start reports success', async () => {
    await withFetch(
      (async () => json({ position: 1, plan: { estimatedRequests: 63 } }, 202)) as typeof fetch,
      async () => {
        expect((await startEvaluation('p', 'm')).ok).toBe(true);
      },
    );
  });

  test('the state snapshot carries the queue and the job in flight', async () => {
    await withFetch(
      (async () => json({
        state: 'running',
        current: {
          providerId: 'p', modelId: 'm', dimension: 'coding',
          samplesCompleted: 24, samplesTotal: 63,
          dimensionsCompleted: [], dimensionsRemaining: ['coding'],
        },
        queue: [{ providerId: 'p', modelId: 'm2' }],
        recent: [],
      })) as typeof fetch,
      async () => {
        const state = await fetchEvaluationState();
        expect(state.state).toBe('running');
        expect(state.current?.samplesCompleted).toBe(24);
        expect(state.queue).toHaveLength(1);
      },
    );
  });

  test('the plan and its evidence are read from one route, not two', async () => {
    // The evidence used to be discarded here, which is what made a fully-scored
    // model a dead end in the modal. Both halves come from the same response.
    const seen: string[] = [];
    await withFetch(
      (async (input: RequestInfo | URL) => {
        seen.push(String(input));
        return json({
          identityId: 'vendor/model',
          plan: { dimensions: ['coding'], skipped: [], speed: 'missing', blocked: null, estimatedRequests: 86 },
          identityDimensions: [{ dimension: 'reasoning', score: 91.4, status: 'scored', evidence: ['run:1'] }],
          offerDimensions: [],
        });
      }) as typeof fetch,
      async () => {
        const detail = await fetchEvaluationDetail('p', 'm');
        expect(detail.plan.estimatedRequests).toBe(86);
        expect(detail.identityDimensions[0].score).toBe(91.4);
        expect(seen).toHaveLength(1);
        expect(seen[0]).toContain('/v1/models/p/m/evaluation');
      },
    );
  });
});

describe('fetchHealth', () => {
  test('returns typed health response from /v1/health', async () => {
    const mockHealth = {
      service: {
        status: 'up',
        databaseReadable: true,
        startedAt: '2026-08-22T00:00:00Z',
        syncInFlight: false,
        currentRunStartedAt: null,
        schedulerEnabled: true,
        nextScheduledRunAt: '2026-08-22T06:00:00Z',
      },
      catalog: {
        status: 'current',
        liveModels: 116,
        methodologyVersion: 'overall-score-v1',
        staleAfterHours: 24,
        staleProviders: [],
        providers: [],
      },
      lastSync: null,
    };

    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).includes('/v1/health')) {
          return {
            ok: true,
            status: 200,
            json: async () => mockHealth,
          } as unknown as Response;
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const res = await fetchHealth();
    expect(res.service.status).toBe('up');
    expect(res.catalog.status).toBe('current');
    expect(res.catalog.liveModels).toBe(116);
  });
});

describe('a dead service is told apart from a service that answered badly', () => {
  /**
   * The dev proxy's answer when nothing holds the api port.
   *
   * Verified against the real thing: vite turns ECONNREFUSED into a bare
   * `500 text/plain` with an empty body. Every /v1 read became "(500)", which
   * reads as a service bug and sent a whole debugging session down the wrong
   * path when the service simply was not running.
   */
  const deadUpstream = () => vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: false,
    status: 500,
    json: async () => { throw new SyntaxError('Unexpected end of JSON input'); },
  } as unknown as Response)));

  /** The real service always answers JSON, even on 404. */
  const serviceSays = (status: number, body: unknown) =>
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: status < 400, status, json: async () => body } as unknown as Response)));

  test('a non-JSON failure is unreachable, not a service error', async () => {
    deadUpstream();
    const err = await fetchEvaluationState().then(() => null, (e) => e);
    expect(err).toBeInstanceOf(ServiceError);
    expect(err.unreachable).toBe(true);
    // The message has to name the cause, because "(500)" named the wrong one.
    expect(err.message).toMatch(/not answering/i);
    expect(err.message).not.toMatch(/^evaluation state unavailable/);
  });

  test('a JSON failure is the service answering, and its reason is kept', async () => {
    serviceSays(404, { error: 'no such model' });
    const err = await fetchEvaluationDetail('p', 'm').then(() => null, (e) => e);
    expect(err).toBeInstanceOf(ServiceError);
    expect(err.unreachable).toBe(false);
    expect(err.status).toBe(404);
    expect(err.message).toMatch(/no such model/);
  });

  test('the service own 503 is an answer, not an unreachable service', async () => {
    // This is why the test is on body shape and not on status: a status rule
    // like ">= 500 means dead" would call a real degraded-health answer a dead
    // socket, and the caller reads that 503 body on purpose.
    serviceSays(503, { status: 'degraded', databaseReadable: false });
    const state = await fetchEvaluationState();
    expect(state).toMatchObject({ status: 'degraded' });
  });

  test('a thrown fetch is unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch'); }));
    const err = await fetchEvaluationState().then(() => null, (e) => e);
    expect(err).toBeInstanceOf(ServiceError);
    expect(err.unreachable).toBe(true);
    expect(err.status).toBe(null);
  });

  test('an abort stays an abort and is not reported as a dead service', async () => {
    // useCatalog cancels in-flight reads on unmount and keys off the signal.
    // Dressing an abort up as a service failure would put a red banner on a
    // page the owner just navigated away from.
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new DOMException('The operation was aborted.', 'AbortError');
    }));
    const err = await fetchEvaluationState().then(() => null, (e) => e);
    expect(err).not.toBeInstanceOf(ServiceError);
    expect((err as DOMException).name).toBe('AbortError');
  });

  test('a bodyless 4xx is still the service answering', async () => {
    // The status half of the rule earns its place here. A proxy with a dead
    // upstream answers 500, 502 or 504 — never a 409. Calling this unreachable
    // would send the reader hunting for a stopped process that is running fine.
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false,
      status: 409,
      json: async () => { throw new SyntaxError('Unexpected end of JSON input'); },
    } as unknown as Response)));
    const err = await fetchEvaluationState().then(() => null, (e) => e);
    expect(err).toBeInstanceOf(ServiceError);
    expect(err.unreachable).toBe(false);
    expect(err.status).toBe(409);
  });

  test('the refusal-as-a-value routes say unreachable instead of http_500', async () => {
    // startEvaluation and regradeEvaluation return a reason rather than throwing,
    // so they need the same distinction in their own vocabulary.
    deadUpstream();
    expect(await startEvaluation('p', 'm')).toMatchObject({ ok: false, reason: 'service_unreachable' });
    expect(await regradeEvaluation('p', 'm')).toMatchObject({ ok: false, reason: 'service_unreachable' });
  });

  test('a real refusal keeps the service own reason', async () => {
    serviceSays(409, { error: 'an evaluation is running' });
    expect(await startEvaluation('p', 'm')).toMatchObject({ ok: false, reason: 'an evaluation is running' });
  });
});
