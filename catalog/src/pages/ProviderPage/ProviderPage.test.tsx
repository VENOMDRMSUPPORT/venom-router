/**
 * The provider page against a REAL stale service response.
 *
 * This is the regression for a blank `/provider/clinepass`: the live service was
 * answering with the pre-M5.1 shape, `VOCell` read `.length` off an absent
 * `vo.notApplicableDimensions`, and React unmounted the whole tree. Thirty-seven
 * SPA tests stayed green throughout, because every one of them handed the
 * renderers a complete `ApiModel` they had built themselves.
 *
 * So this file mounts the real `CatalogProvider`, which calls the real
 * `fetchCatalog`, over raw stale bytes — nothing between the wire and the page is
 * mocked. The assertions are positive: real content must APPEAR. A crashed tree
 * renders nothing, so "renders the model id" is the check that a thrown render
 * cannot fake.
 */

import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { CatalogProvider } from '../../hooks/useCatalog';
import { ProviderPage } from './ProviderPage';

/** The pre-M5.1 wire shape. Not an `ApiModel` — that is the point. */
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
      referenceInPerMTokens: null,
      referenceOutPerMTokens: null,
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

/** Override the provider row the stub serves, so tile status can be exercised. */
let providerOver: Record<string, unknown> = {};

function stubStaleService(models: unknown[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      const u = String(url);
      if (u.includes('/v1/models')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            models,
            meta: {
              methodologyVersion: 'venom-score-v1',
              profileId: 'balanced',
              liveModels: models.length,
              catalogReady: models.length,
              needsVerification: 0,
              qualityScored: models.length,
              operationalScored: models.length,
              unrated: 0,
              identity: { resolvedWithEvidence: 1, resolvedWithoutEvidence: 0, unresolved: 0, ambiguousOpen: 0 },
              identityRules: { exact: 1 },
              calibration: null,
              sortContracts: {},
            },
          }),
        } as unknown as Response;
      }
      if (u.includes('/v1/providers')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            providers: [
              {
                id: 'clinepass',
                name: 'ClinePass',
                rosterUrl: 'https://api.cline.bot/api/v1/ai/cline/recommended-models',
                liveModels: models.length,
                lastSuccessfulSyncAt: '2026-08-13T08:33:50.879Z',
                lastAttemptedSyncAt: '2026-08-13T08:33:50.879Z',
                lastOutcome: 'ok',
                freshness: 'fresh',
                hoursSinceSuccess: 2.5,
                qualityScored: models.length,
                modelScoreScored: models.length,
                overallScoreScored: 0,
                unrated: 0,
                ...providerOver,
              },
            ],
          }),
        } as unknown as Response;
      }
      throw new Error(`unexpected fetch: ${u}`);
    }),
  );
}

function renderProviderPage() {
  return render(
    <CatalogProvider>
      <MemoryRouter initialEntries={['/provider/clinepass']}>
        <Routes>
          <Route path="/provider/:id" element={<ProviderPage />} />
        </Routes>
      </MemoryRouter>
    </CatalogProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  providerOver = {};
});

describe('the provider table presents one server-derived model score', () => {
  const scoredModel = (over: Record<string, unknown> = {}) => staleWireModel({
    modelScore: {
      value: 65.79,
      display: '65.8%',
      methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7,
      operationalWeight: 0.3,
      operationalPrecision: 0,
      uncertainty: 0.035,
      bound: null,
      reason: null,
      qualityEvidenceLevel: 'measured',
      operationalCoverage: 'complete',
    },
    modelRank: 9,
    tiedAtModelRank: true,
    overallScore: {
      value: 65.79, display: '65.8%', status: 'complete', qualityScore: 67.5, operationalScore: 61.8,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 }, overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding', 'reasoning', 'longContext', 'toolCalling', 'structuredOutput', 'speed', 'costEfficiency'],
      excludedDimensions: ['vision'], uncertainty: 1, reasons: [], methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
    },
    overallRank: 9,
    tiedAtOverallRank: true,
    resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    ...over,
  });

  test('replaces Rank, VQ, and VO columns with # and Score', async () => {
    stubStaleService([scoredModel()]);
    renderProviderPage();

    expect(await screen.findByText('Models (1)')).toBeInTheDocument();
    const headers = screen.getAllByRole('columnheader').map((header) => header.textContent?.trim());
    expect(headers.slice(0, 3)).toEqual(['#', 'Model', 'Score']);
    expect(headers).not.toContain('Rank');
    expect(headers).not.toContain('VQ');
    expect(headers).not.toContain('VO');
    expect(screen.getByText('65.8%')).toBeInTheDocument();
    expect(screen.getByText('100% coverage')).toBeInTheDocument();
  });

  test('states the global scope and renders a dense-rank tie marker', async () => {
    stubStaleService([scoredModel()]);
    renderProviderPage();

    await screen.findByText('65.8%');
    expect(screen.getByTestId('rank-scope-note')).toHaveTextContent(/global rank across all catalog offers/i);
    expect(screen.getByTestId('model-rank-cline-pass/deepseek-v4-flash')).toHaveTextContent('#9=');
  });

  test('keeps incomplete overall coverage visible beside the score', async () => {
    stubStaleService([scoredModel({
      overallScore: {
        ...((scoredModel().overallScore as object)),
        overallCoverage: { scored: 6, applicable: 7, percent: 85.7142857 },
      },
    })]);
    renderProviderPage();

    expect(await screen.findByText('86% coverage')).toBeInTheDocument();
  });

  test('shows one Score block in grid view instead of separate VQ and VO blocks', async () => {
    stubStaleService([scoredModel()]);
    renderProviderPage();

    await screen.findByText('65.8%');
    fireEvent.click(screen.getByRole('button', { name: 'Grid view' }));

    expect(screen.getByText('Score')).toBeInTheDocument();
    expect(screen.queryByText('VQ')).not.toBeInTheDocument();
    expect(screen.queryByText('VO')).not.toBeInTheDocument();
  });
});

describe('one public model surface', () => {
  test('renders scored and unresolved rows once in one globally ordered table', async () => {
    const score = {
      value: 60, display: '60.0%', methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7, operationalWeight: 0.3, operationalPrecision: 0,
      uncertainty: 1, bound: null, reason: null, qualityEvidenceLevel: 'measured',
      operationalCoverage: 'complete',
    };
    const unresolvedScore = {
      ...score, value: null, display: '—', reason: 'missing_vq', qualityEvidenceLevel: 'unrated',
    };
    const overall = (value: number, rank: number) => ({
      overallScore: {
        value, display: `${value.toFixed(1)}%`, status: 'complete', qualityScore: value, operationalScore: value,
        qualityCoverage: { scored: 5, applicable: 5, percent: 100 }, overallCoverage: { scored: 7, applicable: 7, percent: 100 },
        includedDimensions: ['coding'], excludedDimensions: ['vision'], uncertainty: 1, reasons: [], methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
      }, overallRank: rank, tiedAtOverallRank: false,
    });
    const unresolvedOverall = (status: 'evaluating' | 'insufficient_evidence') => ({
      overallScore: { value: null, display: '—', status, qualityScore: null, operationalScore: null,
        qualityCoverage: { scored: 0, applicable: 5, percent: 0 }, overallCoverage: { scored: 0, applicable: 7, percent: 0 },
        includedDimensions: [], excludedDimensions: [], uncertainty: null, reasons: ['missing_coding_evaluation'], methodologyVersion: 'overall-score-v1', computedAt: null },
      overallRank: null, tiedAtOverallRank: false,
    });
    stubStaleService([
      staleWireModel({ modelId: 'rank-two', modelScore: score, modelRank: 2, tiedAtModelRank: false,
        ...overall(60, 2),
        resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null } }),
      staleWireModel({ modelId: 'z-processing', modelScore: unresolvedScore, modelRank: null, tiedAtModelRank: false,
        ...unresolvedOverall('evaluating'),
        catalogReady: false, missingFacts: ['context'], contextTokens: null,
        resolution: { state: 'processing', reasons: ['missing_context', 'missing_vq'], firstDetectedAt: '2026-08-19T10:00:00.000Z', lastAttemptAt: '2026-08-19T10:00:00.000Z', nextAttemptAt: '2026-08-19T10:01:00.000Z' } }),
      staleWireModel({ modelId: 'rank-one', modelScore: { ...score, value: 70, display: '70.0%' }, modelRank: 1, tiedAtModelRank: false,
        ...overall(70, 1),
        resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null } }),
      staleWireModel({ modelId: 'a-awaiting', modelScore: unresolvedScore, modelRank: null, tiedAtModelRank: false,
        ...unresolvedOverall('insufficient_evidence'),
        resolution: { state: 'awaiting_external_benchmark', reasons: ['missing_vq'], firstDetectedAt: '2026-08-19T10:00:00.000Z', lastAttemptAt: '2026-08-19T10:05:00.000Z', nextAttemptAt: null } }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('Models (4)')).toBeInTheDocument();
    expect(screen.getAllByRole('table')).toHaveLength(1);
    expect(screen.queryByText(/No model score/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Needs verification/)).not.toBeInTheDocument();
    const rows = screen.getAllByRole('row').slice(1).map((row) => row.textContent ?? '');
    expect(rows[0]).toContain('rank-one');
    expect(rows[1]).toContain('rank-two');
    expect(rows[2]).toContain('a-awaiting');
    expect(rows[3]).toContain('z-processing');
    expect(screen.getByText('Evaluating')).toBeInTheDocument();
    expect(screen.getByText('Insufficient evidence')).toBeInTheDocument();
  });
});

describe('/provider/:id survives a pre-M5.1 service response', () => {
  test('the page renders its real content instead of blanking', async () => {
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    // Nothing below this line can pass on a tree that threw during render.
    expect(await screen.findByText('ClinePass')).toBeInTheDocument();
    expect(screen.getByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    // The response predates model-score-v1, so it remains visible and honestly
    // unplaced instead of having a composite invented in the browser.
    expect(screen.getByText('Unrated')).toBeInTheDocument();
    expect(screen.queryByText(/No model score/)).not.toBeInTheDocument();
    expect(screen.getByText('Unrated')).toBeInTheDocument();
    // The context window reaches both the header stat and the row's own cell.
    expect(screen.getAllByText('1M')).toHaveLength(2);
  });

  test('a stale VO shape remains renderable after the columns are merged', async () => {
    // The exact crash site: `vo.notApplicableDimensions` absent from the wire.
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    expect(await screen.findByText('Unrated')).toBeInTheDocument();
    expect(screen.queryByText(/No model score/)).not.toBeInTheDocument();
    expect(screen.queryByTestId('vo-notapplicable')).not.toBeInTheDocument();
  });

  test('the evidence toggle counts what it can see, and shows no flag count', async () => {
    // `conflicts` + `rejectedCandidates` + `missingFacts` — two of the three
    // absent from the wire. Summing them was the second crash waiting behind
    // the first.
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-cline-pass/deepseek-v4-flash');
    expect(toggle).toHaveTextContent('why');
  });

  test('an identity the response never stated is not reported as unresolved', async () => {
    // The dishonest degradation this replaces: `identityState !== 'resolved'`
    // made every row of a stale payload claim "identity unresolved" — a finding
    // the service never published, printed beside a canonical id that
    // contradicts it.
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    await screen.findByText('cline-pass/deepseek-v4-flash');
    expect(screen.queryByText('identity unresolved')).not.toBeInTheDocument();
    expect(screen.getByText('identity not reported')).toBeInTheDocument();
  });

  test('opening the evidence panel on a stale row does not crash either', async () => {
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-cline-pass/deepseek-v4-flash');
    fireEvent.click(toggle);

    expect(await screen.findByTestId('evidence-panel')).toBeInTheDocument();
    // No provenance rows to show, and it says so by omitting the section rather
    // than rendering an empty table that looks like a complete answer.
    expect(screen.queryByTestId('provenance-section')).not.toBeInTheDocument();
    expect(screen.queryByTestId('conflict-section')).not.toBeInTheDocument();
  });
});

describe('capability states stay inside the icon language', () => {
  test('a conflicted attachment renders as an icon state, not a raw field label', async () => {
    stubStaleService([
      staleWireModel({
        modelId: 'cline-pass/qwen3.8-max',
        capabilities: { tools: true, reasoning: true, structured: true, attachment: null },
        conflicts: [
          {
            field: 'attachment',
            sides: [{ value: true, by: 'nano-gpt/qwen3.8-max:thinking' }, { value: false, by: 'digitalocean/qwen3.8-max' }],
            conflictType: 'source_disagreement',
            status: 'open',
            resolvedTo: null,
            detectedAt: '2026-08-13T08:33:50.879Z',
          },
        ],
      }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/qwen3.8-max')).toBeInTheDocument();
    expect(screen.getByLabelText('Attachments — sources disagree')).toHaveAttribute('data-state', 'conflicted');
    expect(screen.queryByText('attachment')).not.toBeInTheDocument();
    expect(screen.queryByText('missing')).not.toBeInTheDocument();
  });
});

describe('the overall-score tile reports reproducible evaluation coverage', () => {
  /** The tile's status icon, by its accessible label. */
  const tileStatus = () => screen.getByTestId('quality-tile-status').getAttribute('data-status');

  test('the tile counts overall scores, not the legacy composite', async () => {
    providerOver = { liveModels: 2, qualityScored: 2, modelScoreScored: 2, overallScoreScored: 1, unrated: 0 };
    stubStaleService([
      staleWireModel(),
      staleWireModel({
        modelId: 'cline-pass/vo-only',
        modelScore: { value: null, display: '—', methodologyVersion: 'model-score-v1', qualityWeight: 0.7, operationalWeight: 0.3, operationalPrecision: 0, uncertainty: null, bound: null, reason: 'missing_vq', qualityEvidenceLevel: 'unrated', operationalCoverage: 'complete' },
        modelRank: null,
        vq: { value: null, uncertainty: null, bound: null, evidenceLevel: 'unrated', precision: 0, display: '—', unratedReason: 'no_published_benchmark', provenance: null },
      }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/vo-only')).toBeInTheDocument();
    expect(screen.getByText('Complete evaluations')).toBeInTheDocument();
    expect(screen.getByText('1/2')).toBeInTheDocument();
    expect(screen.getByTestId('quality-tile-status').getAttribute('title')).toMatch(/1 model\(s\)/i);
  });

  test('a recorded VQ reason does not masquerade as a complete overall evaluation', async () => {
    // The page says, three sections below this tile, that unknown quality is not
    // low quality and the gap is in what the industry measured. The tile used to
    // answer that with the SAME warning triangle it shows for a provider serving
    // zero models — telling the owner their provider is broken when nothing is.
    // `cline-pass/glm-5.3` is the case: operationally complete, and unrated only
    // because no benchmark index lists the model yet.
    providerOver = { liveModels: 2, qualityScored: 1, modelScoreScored: 1, overallScoreScored: 1, unrated: 1 };
    stubStaleService([
      staleWireModel(),
      staleWireModel({
        modelId: 'cline-pass/glm-5.3',
        canonicalId: null,
        qualityRank: null,
        vq: { value: null, uncertainty: null, bound: null, evidenceLevel: 'unrated', precision: 0, display: '—', unratedReason: 'identity_unresolved', provenance: null },
      }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/glm-5.3')).toBeInTheDocument();
    expect(tileStatus()).toBe('warn');
  });

  test('a model unrated with no reason recorded still raises the flag', async () => {
    // The signal is not being deleted, it is being aimed. A row with no recorded
    // reason is a hole in OUR data — the one case here that is ours to act on —
    // and silencing that too would turn an honest tile into a decorative one.
    providerOver = { liveModels: 2, qualityScored: 1, modelScoreScored: 1, overallScoreScored: 1, unrated: 1 };
    stubStaleService([
      staleWireModel(),
      staleWireModel({
        modelId: 'cline-pass/mystery',
        canonicalId: null,
        qualityRank: null,
        vq: { value: null, uncertainty: null, bound: null, evidenceLevel: 'unrated', precision: 0, display: '—', unratedReason: null, provenance: null },
      }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/mystery')).toBeInTheDocument();
    expect(tileStatus()).toBe('warn');
  });

  test('a provider serving nothing is still an error, not a shrug', async () => {
    providerOver = { liveModels: 0, qualityScored: 0, modelScoreScored: 0, overallScoreScored: 0, unrated: 0 };
    stubStaleService([]);
    renderProviderPage();

    expect(await screen.findByText('Live models')).toBeInTheDocument();
    expect(tileStatus()).toBe('warn');
  });
});

describe('legacy quality evidence cannot complete the overall-score tile', () => {
  test('a reviewed VQ bound is not counted as a complete overall evaluation', async () => {
    // Reaching 13/13 by adding a reviewed bound made the tile assert "All
    // models benchmarked externally" — which is exactly the overstatement the
    // bound was carefully labelled to avoid making. A count that is right for
    // the wrong reason is how a catalog starts lying quietly.
    providerOver = { liveModels: 2, qualityScored: 2, modelScoreScored: 2, overallScoreScored: 0, unrated: 0 };
    stubStaleService([
      staleWireModel(),
      staleWireModel({
        modelId: 'cline-pass/glm-5.3',
        canonicalId: null,
        qualityRank: 5,
        vq: { value: 52.6, uncertainty: null, bound: 'lower', evidenceLevel: 'bounded', precision: 0, display: '≥ 53', unratedReason: null, provenance: null },
      }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/glm-5.3')).toBeInTheDocument();
    const title = screen.getByTestId('quality-tile-status').getAttribute('title') ?? '';
    expect(title).toMatch(/2 model\(s\) still lack sufficient reproducible evidence/i);
  });

  test('a complete overall evaluation is stated plainly', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, modelScoreScored: 1, overallScoreScored: 1, unrated: 0 };
    stubStaleService([staleWireModel({
      overallScore: {
        value: 60, display: '60.0%', status: 'complete', qualityScore: 60, operationalScore: 60,
        qualityCoverage: { scored: 5, applicable: 5, percent: 100 }, overallCoverage: { scored: 7, applicable: 7, percent: 100 },
        includedDimensions: ['coding'], excludedDimensions: ['vision'], uncertainty: 1, reasons: [], methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
      }, overallRank: 1, tiedAtOverallRank: false,
    })]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.getByTestId('quality-tile-status').getAttribute('title')).toMatch(/every published model has a complete overall-score-v1 evaluation/i);
  });
});

describe('a fact true of every row is stated once, not once per cell', () => {
  /** Two rows of a subscription provider, as the service now serves them. */
  const included = (over: Record<string, unknown> = {}) =>
    staleWireModel({
      pricing: {
        kind: 'included',
        inputPerMTokens: null,
        outputPerMTokens: null,
        referenceInPerMTokens: 3,
        referenceOutPerMTokens: 15,
        isFree: false,
      },
      vo: { value: 84, dimensions: { context: 1, output: 1, capabilities: 1 }, missingDimensions: [], notApplicableDimensions: ['cost'], profileId: 'balanced' },
      ...over,
    });

  test('the subscription is explained once for the provider', async () => {
    providerOver = { liveModels: 2, qualityScored: 2, unrated: 0 };
    stubStaleService([included(), included({ modelId: 'cline-pass/kimi-k2.6' })]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/kimi-k2.6')).toBeInTheDocument();
    expect(screen.getByTestId('billing-note')).toBeInTheDocument();
  });

  test('and not repeated in every price cell', async () => {
    // Thirteen rows x two price cells x one VO badge = the same provider-level
    // sentence printed thirty-nine times, in the vocabulary the catalog uses for
    // ABSENT values — so an answer read as a page full of holes.
    providerOver = { liveModels: 2, qualityScored: 2, unrated: 0 };
    stubStaleService([included(), included({ modelId: 'cline-pass/kimi-k2.6' })]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/kimi-k2.6')).toBeInTheDocument();
    expect(screen.queryByText('Included · n/a')).not.toBeInTheDocument();
    expect(screen.queryByText('cost n/a')).not.toBeInTheDocument();
    // The figure that actually varies per row survives. Its meaning now sits on
    // the column header rather than on every cell — see the marker test below.
    expect(screen.getAllByText('$3').length).toBeGreaterThan(0);
  });

  test('a provider that really does charge per token keeps its prices in the rows', async () => {
    // The guard: this must not turn into "hide the cost column".
    providerOver = { liveModels: 1, qualityScored: 1, modelScoreScored: 1, overallScoreScored: 1, unrated: 0 };
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.queryByTestId('billing-note')).not.toBeInTheDocument();
    expect(screen.getAllByText('$0.14').length).toBeGreaterThan(0);
  });
});

describe('the identity slot answers the question the column asks', () => {
  test('a row with no canonical id says so, rather than naming an internal state', async () => {
    // The column shows a canonical id for every other row — "moonshotai/kimi-k3"
    // — so the question it asks is "which upstream model is this?". Answering
    // "identity review (1 refused)" answers a different question: it reports the
    // workflow state this row is parked in. The reader is left to guess what was
    // reviewed and what was refused.
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([
      staleWireModel({
        modelId: 'cline-pass/glm-5.3',
        canonicalId: null,
        identityState: 'identity_review',
        rejectedCandidates: [
          { candidate: null, verdict: 'no_candidate_exists', why: 'no index lists it', evidence: [], source: 'identity_overlay', sourceRef: null, sourceUrl: null, evidenceState: 'declared_policy', resolverVersion: 'v1', candidateMeta: null, reviewedAt: '2026-08-18', recordedAt: '2026-08-18' },
        ],
      }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/glm-5.3')).toBeInTheDocument();
    // Phrased as an answer about the row rather than as a workflow state. The
    // exact wording is pinned by the precision test below; what this one holds
    // is that the label is not `identity review`.
    expect(screen.queryByText(/identity review/i)).not.toBeInTheDocument();
    expect(screen.getByText(/not in the reference index/i)).toBeInTheDocument();
    // The count survives: "investigated" and "untouched" must stay tellable
    // apart, which is the entire reason the review state exists.
    expect(screen.getByText(/1 candidate refused/i)).toBeInTheDocument();
  });
});

describe('the identity label does not overstate what is unknown', () => {
  test('it names the index that has no entry, not "upstream" in general', async () => {
    // "no upstream match" contradicts the row it sits on. Upstream DOES identify
    // cline-pass/glm-5.3: models.dev lists it under Z.ai’s own storefronts, and
    // that is where this very row’s 1M context and 128K max output came from.
    // What has no entry is the REFERENCE INDEX — the one a canonical id and a
    // benchmark figure are drawn from. Saying "upstream" for that asserts nothing
    // anywhere knows this model, beside facts that were read from somewhere.
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([
      staleWireModel({
        modelId: 'cline-pass/glm-5.3',
        canonicalId: null,
        identityState: 'identity_review',
        rejectedCandidates: [
          { candidate: null, verdict: 'no_candidate_exists', why: 'not listed', evidence: [], source: 'identity_overlay', sourceRef: null, sourceUrl: null, evidenceState: 'declared_policy', resolverVersion: 'v1', candidateMeta: null, reviewedAt: '2026-08-18', recordedAt: '2026-08-18' },
        ],
      }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/glm-5.3')).toBeInTheDocument();
    expect(screen.getByText(/reference index/i)).toBeInTheDocument();
    expect(screen.queryByText(/no upstream match/i)).not.toBeInTheDocument();
    expect(screen.getByText(/1 candidate refused/i)).toBeInTheDocument();
  });
});

describe('the ranking says what it ranks against', () => {
  test('a filtered table explains why its numbers skip and why rows show a tie', async () => {
    // Thirteen rows numbered #1, #2, #5, #5, #8, #9 ... with an "=" on almost
    // every one reads as a broken table. Both are honest: the rank is
    // catalog-wide, so other providers' models occupy the gaps, and the ties are
    // usually the SAME model sold by another provider — filtered out of this
    // view, so the reader sees a tie with nobody. Unstated scope is why a
    // correct number looks like an error.
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([staleWireModel({
      modelScore: {
        value: 48.8,
        display: '48.8%',
        methodologyVersion: 'model-score-v1',
        qualityWeight: 0.7,
        operationalWeight: 0.3,
        operationalPrecision: 0,
        uncertainty: 4.3,
        bound: null,
        reason: null,
        qualityEvidenceLevel: 'calibrated',
        operationalCoverage: 'complete',
      },
      modelRank: 31,
      tiedAtModelRank: true,
      overallScore: {
        value: 48.8, display: '48.8%', status: 'complete', qualityScore: 50, operationalScore: 46,
        qualityCoverage: { scored: 5, applicable: 5, percent: 100 }, overallCoverage: { scored: 7, applicable: 7, percent: 100 },
        includedDimensions: ['coding'], excludedDimensions: ['vision'], uncertainty: 4.3, reasons: [], methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
      },
      overallRank: 31,
      tiedAtOverallRank: true,
    })]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    const note = screen.getByTestId('rank-scope-note').textContent ?? '';
    expect(note).toMatch(/global rank across all catalog offers/i);
    expect(note).toMatch(/another provider/i);
  });
});

describe('the model column shows which model the row is', () => {
  test('a vendor-established identity is shown, not a sentence about its absence', async () => {
    // What the column is for. `cline-pass/glm-5.3` IS `z-ai/glm-5.3` — models.dev
    // lists it under Z.ai’s own storefronts, and that listing is where the same
    // row’s 1M context came from. Printing a sentence about the reference index
    // there answered a question the column does not ask.
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([
      staleWireModel({
        modelId: 'cline-pass/glm-5.3',
        canonicalId: null,
        vendorModelId: 'z-ai/glm-5.3',
        identityState: 'identity_review',
        rejectedCandidates: [
          { candidate: null, verdict: 'no_candidate_exists', why: 'not listed', evidence: [], source: 'identity_overlay', sourceRef: null, sourceUrl: null, evidenceState: 'declared_policy', resolverVersion: 'v1', candidateMeta: null, reviewedAt: '2026-08-18', recordedAt: '2026-08-18' },
        ],
      }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/glm-5.3')).toBeInTheDocument();
    expect(screen.getByText('z-ai/glm-5.3')).toBeInTheDocument();
    expect(screen.queryByText(/not in the reference index/i)).not.toBeInTheDocument();
  });

  test('and it is marked as the vendor’s id, not as a benchmark linkage', async () => {
    // The guard. Every other row’s id in this column IS the entry a score was
    // taken from; this one is not, and rendering them identically would claim a
    // measurement that does not exist.
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([
      staleWireModel({ modelId: 'cline-pass/glm-5.3', canonicalId: null, vendorModelId: 'z-ai/glm-5.3', identityState: 'identity_review', rejectedCandidates: [] }),
    ]);
    renderProviderPage();

    const id = await screen.findByText('z-ai/glm-5.3');
    expect(id.getAttribute('title') ?? '').toMatch(/vendor|no benchmark|not the entry/i);
  });

  test('a row with neither id still says so plainly', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([
      staleWireModel({ modelId: 'cline-pass/mystery', canonicalId: null, vendorModelId: null, identityState: 'unresolved', rejectedCandidates: [] }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/mystery')).toBeInTheDocument();
    expect(screen.getByText(/not in the reference index/i)).toBeInTheDocument();
  });
});

describe('the reference marker sits on the column, not on every price', () => {
  const included = (over: Record<string, unknown> = {}) =>
    staleWireModel({
      pricing: { kind: 'included', inputPerMTokens: null, outputPerMTokens: null, referenceInPerMTokens: 3, referenceOutPerMTokens: 15, isFree: false },
      vo: { value: 84, dimensions: {}, missingDimensions: [], notApplicableDimensions: ['cost'], profileId: 'balanced' },
      ...over,
    });

  test('prices read as plain figures once the column says what they are', async () => {
    // Twenty-six `ref` prefixes for one column-level fact, the same shape as the
    // `Included · n/a` repetition. The marker has to survive somewhere though: a
    // bare $3 under a plan reads as what you pay, which is the one thing
    // ClinePass's documentation says it is not.
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([included()]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.getAllByText('$3').length).toBeGreaterThan(0);
    expect(screen.queryByText(/^ref \$3$/)).not.toBeInTheDocument();
    // and the column still declares it
    expect(screen.getByTestId('cost-column-in').textContent ?? '').toMatch(/ref/i);
  });

  test('a row with no published rate shows a plain dash', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([included({ pricing: { kind: 'included', inputPerMTokens: null, outputPerMTokens: null, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false } })]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.queryByText('ref —')).not.toBeInTheDocument();
  });

  test('a per-token provider keeps a plain price column with no ref marker', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.getByTestId('cost-column-in').textContent ?? '').not.toMatch(/ref/i);
  });
});
