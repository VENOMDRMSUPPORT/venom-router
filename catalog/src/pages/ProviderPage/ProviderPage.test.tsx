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
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
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

  test('replaces separate score columns with a clear Rank and Score pair', async () => {
    stubStaleService([scoredModel()]);
    renderProviderPage();

    expect(await screen.findByText('Models (1)')).toBeInTheDocument();
    const headers = screen.getAllByRole('columnheader').map((header) => header.textContent?.trim());
    expect(headers.slice(0, 3)).toEqual(['Rank', 'Model', 'Score']);
    expect(headers).not.toContain('VQ');
    expect(headers).not.toContain('VO');
    expect(screen.getByText('65.8%')).toBeInTheDocument();
    // A complete score no longer carries a coverage badge: a column of identical
    // "100% coverage" pills states per row that nothing is missing. The badge
    // now marks the exception, which is the row a reader must not miss.
    expect(screen.queryByText(/coverage/i)).not.toBeInTheDocument();
  });

  test('numbers the row by its position here, and names a tie compactly', async () => {
    stubStaleService([scoredModel()]);
    renderProviderPage();

    await screen.findByText('65.8%');
    // Asserts the FACTS the note has to carry, not one phrasing of them: where
    // the catalog-wide rank lives, that only complete results are placed, and
    // that ties are evidence-aware. Pinning the sentence made an editorial trim look like a
    // regression.
    const scopeNote = screen.getByTestId('rank-scope-note');
    expect(scopeNote).toHaveTextContent(/catalog/i);
    expect(scopeNote).toHaveTextContent(/overall-score-v1/i);
    expect(scopeNote).toHaveTextContent(/tie/i);
    // One row on screen is position 1 whatever the catalog says, and the
    // catalog's own number stays reachable rather than being dropped.
    const cell = screen.getByTestId('model-rank-cline-pass/deepseek-v4-flash');
    expect(cell).toHaveTextContent('T-1');
    expect(cell).toHaveAccessibleName('Tied position 1');
    expect(cell.getAttribute('title')).toContain('Catalog-wide rank: 9');
  });

  test('keeps incomplete overall coverage visible beside the score', async () => {
    stubStaleService([scoredModel({
      overallScore: {
        ...((scoredModel().overallScore as object)),
        overallCoverage: { scored: 6, applicable: 7, percent: 85.7142857 },
      },
    })]);
    renderProviderPage();

    expect(await screen.findByText('6 of 7 dimensions')).toBeInTheDocument();
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
    expect(toggle).toHaveTextContent('Evidence');
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

  test('keeps the table compact and exposes additional capabilities through a count', async () => {
    stubStaleService([
      staleWireModel({
        modelId: 'cline-pass/multimodal',
        capabilities: { tools: true, reasoning: true, structured: true, attachment: true },
        inputModalities: ['text', 'image', 'audio', 'video'],
      }),
    ]);
    renderProviderPage();

    const group = await screen.findByRole('group', { name: 'Capabilities for cline-pass/multimodal: 7 reported' });
    expect(group).toHaveTextContent('+3');
    expect(screen.getByLabelText('3 additional capabilities')).toBeInTheDocument();
  });
});

describe('the capability legend is a complete and responsive map', () => {
  test('adds Image Gen as the eighth capability and exposes each item semantically', async () => {
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    const toggle = await screen.findByRole('button', { name: /Explore capabilities \(8\)/ });
    fireEvent.click(toggle);

    const legend = screen.getByTestId('capability-legend-grid');
    expect(legend).toHaveAccessibleName('8 model capability types');
    expect(screen.getAllByRole('listitem')).toHaveLength(8);
    expect(legend).toHaveTextContent('Image Gen');
    expect(legend).toHaveTextContent('Native image creation & editing');
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
    // Owner decision (2026-08-21): identity findings print nothing inline. The
    // row names the model; the finding and its refused candidates live one
    // click away on the evidence badge and panel.
    expect(screen.queryByText(/identity review/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/not in the reference index/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/candidate[s]? refused/i)).not.toBeInTheDocument();
  });
});

describe('the identity label does not overstate what is unknown', () => {
  test('no inline identity wording prints at all, so none can overstate', async () => {
    // The precision concern below was about a label the row no longer prints
    // (owner decision 2026-08-21): the finding moved to the evidence panel,
    // whose identity section names the state and each refused candidate. What
    // the row must now guarantee is silence — no "reference index", no
    // "upstream", no candidate count beside the model name.
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
    expect(screen.queryByText(/reference index/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/no upstream match/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/candidate[s]? refused/i)).not.toBeInTheDocument();
  });
});

describe('the ranking says what it ranks against', () => {
  test('a filtered table explains its numbering and why rows show a tie', async () => {
    // Thirteen rows numbered #1, #2, #5, #5, #8, #9 ... with an "=" on almost
    // every one read as a broken table. The gaps are gone now that the column
    // numbers the rows on screen, but the ties remain and are still honest: they
    // are usually the SAME model sold by another provider, filtered out of this
    // view, so the reader sees a tie with nobody. Unexplained, a correct number
    // still looks like an error.
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
    expect(note).toMatch(/catalog/i);
    expect(note).toMatch(/another provider/i);
    expect(note).toMatch(/position in this list/i);
    expect(note).toMatch(/uncertainty intervals overlap/i);
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

  test('a row with neither id prints no identity finding inline', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([
      staleWireModel({ modelId: 'cline-pass/mystery', canonicalId: null, vendorModelId: null, identityState: 'unresolved', rejectedCandidates: [] }),
    ]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/mystery')).toBeInTheDocument();
    // Owner decision (2026-08-21): the finding moved entirely to the evidence
    // panel; the row itself stays quiet.
    expect(screen.queryByText(/not in the reference index/i)).not.toBeInTheDocument();
  });
});

describe('the reference marker sits on the column, not on every price', () => {
  const included = (over: Record<string, unknown> = {}) =>
    staleWireModel({
      pricing: { kind: 'included', inputPerMTokens: null, outputPerMTokens: null, referenceInPerMTokens: 3, referenceOutPerMTokens: 15, isFree: false },
      vo: { value: 84, dimensions: {}, missingDimensions: [], notApplicableDimensions: ['cost'], profileId: 'balanced' },
      ...over,
    });

  test('prices read as plain figures under unified In/Out column headers', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([included()]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.getAllByText('$3').length).toBeGreaterThan(0);
    expect(screen.getByTestId('cost-column-in').textContent?.trim()).toBe('InputUSD / 1M');
  });

  test('a row with no published rate shows a plain dash or included state', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([included({ pricing: { kind: 'included', inputPerMTokens: null, outputPerMTokens: null, referenceInPerMTokens: null, referenceOutPerMTokens: null, isFree: false } })]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.queryByText('ref —')).not.toBeInTheDocument();
  });

  test('a per-token provider keeps a unified price column', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([staleWireModel()]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.getByTestId('cost-column-in').textContent?.trim()).toBe('InputUSD / 1M');
  });

  test('labels a comparison rate as a market reference, never as the provider price', async () => {
    providerOver = { liveModels: 1, qualityScored: 1, unrated: 0 };
    stubStaleService([staleWireModel({
      pricing: { kind: 'free', inputPerMTokens: null, outputPerMTokens: null, referenceInPerMTokens: 0.13, referenceOutPerMTokens: 0.53, isFree: true },
    })]);
    renderProviderPage();

    expect(await screen.findByText('cline-pass/deepseek-v4-flash')).toBeInTheDocument();
    expect(screen.getAllByText('Market ref').length).toBeGreaterThan(0);
    expect(screen.getAllByText('/M').length).toBeGreaterThan(0);
  });
});

/**
 * Four live models carry the vendor's own `deprecated` marker while still being
 * served, ranked, and indistinguishable from the rest at a glance. They belong in
 * the roster — a model still answering requests is a fact — but "show me what I
 * can build on" had no way to ask.
 */
describe('a deprecated model can be filtered out of the roster', () => {
  const roster = () => [
    staleWireModel({ modelId: 'kept', displayName: 'Kept', lifecycle: null }),
    staleWireModel({ modelId: 'sunsetting', displayName: 'Sunsetting', lifecycle: 'deprecated' }),
  ];

  test('both are listed by default, because both are still served', () => {
    stubStaleService(roster());
    renderProviderPage();

    return waitFor(() => {
      expect(screen.getByText('Kept')).toBeInTheDocument();
      expect(screen.getByText('Sunsetting')).toBeInTheDocument();
    });
  });

  test('the Not Deprecated filter hides the one the vendor is retiring', async () => {
    stubStaleService(roster());
    renderProviderPage();
    await waitFor(() => expect(screen.getByText('Sunsetting')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /all models/i }));
    fireEvent.click(screen.getByRole('option', { name: 'Not Deprecated' }));

    expect(screen.getByText('Kept')).toBeInTheDocument();
    expect(screen.queryByText('Sunsetting')).toBeNull();
  });
});

describe('a vendor qualifier is printed once, not in the name and again in a pill', () => {
  const scored = (over: Record<string, unknown> = {}) => staleWireModel({
    modelScore: {
      value: 65.79, display: '65.8%', methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7, operationalWeight: 0.3, operationalPrecision: 0,
      uncertainty: 0.035, bound: null, reason: null,
      qualityEvidenceLevel: 'measured', operationalCoverage: 'complete',
    },
    modelRank: 9,
    overallScore: {
      value: 65.79, display: '65.8%', status: 'complete', qualityScore: 67.5, operationalScore: 61.8,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding'], excludedDimensions: [], uncertainty: 1, reasons: [],
      methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
    },
    overallRank: 9,
    resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    ...over,
  });

  test('the name drops the parenthetical the badge lifts', async () => {
    // The reported defect: the row read "DeepSeek V4 Pro (New)" with a "New"
    // pill beside it. Both halves are asserted, because deleting the pill and
    // deleting the parenthetical are different fixes and only one is right —
    // the qualifier has to survive, once, as the badge.
    stubStaleService([scored({ modelId: 'deepseek-v4-pro', displayName: 'DeepSeek V4 Pro (New)' })]);
    renderProviderPage();

    expect(await screen.findByText('DeepSeek V4 Pro')).toBeInTheDocument();
    expect(screen.queryByText(/DeepSeek V4 Pro \(New\)/)).not.toBeInTheDocument();
    expect(screen.getByText('New')).toBeInTheDocument();
  });

  test('a name with nothing liftable is left alone', async () => {
    // No badge renders here, so stripping anything would delete the only copy
    // of a name the provider published.
    stubStaleService([scored({ modelId: 'mimo-v2.5-free', displayName: 'MiMo-V2.5 (free)' })]);
    renderProviderPage();

    expect(await screen.findByText('MiMo-V2.5 (free)')).toBeInTheDocument();
  });
});

describe('the bare api id is not printed when the row already shows it', () => {
  const scored = (over: Record<string, unknown> = {}) => staleWireModel({
    modelScore: {
      value: 65.79, display: '65.8%', methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7, operationalWeight: 0.3, operationalPrecision: 0,
      uncertainty: 0.035, bound: null, reason: null,
      qualityEvidenceLevel: 'measured', operationalCoverage: 'complete',
    },
    modelRank: 9,
    overallScore: {
      value: 65.79, display: '65.8%', status: 'complete', qualityScore: 67.5, operationalScore: 61.8,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding'], excludedDimensions: [], uncertainty: 1, reasons: [],
      methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
    },
    overallRank: 9,
    resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    ...over,
  });

  test('drops the bare id when the name restates it and a namespaced id ends in it', async () => {
    // The row used to read "DeepSeek V4 Pro" / "deepseek-v4-pro" /
    // "deepseek/deepseek-v4-pro" — the id twice, once bare and once prefixed.
    // The prefixed one stays, because it is the only one that also says whose
    // model this is.
    stubStaleService([scored({
      modelId: 'deepseek-v4-pro',
      displayName: 'DeepSeek V4 Pro',
      canonicalId: null,
      vendorModelId: 'deepseek/deepseek-v4-pro',
    })]);
    renderProviderPage();

    expect(await screen.findByText('DeepSeek V4 Pro')).toBeInTheDocument();
    expect(screen.getByText('deepseek/deepseek-v4-pro')).toBeInTheDocument();
    expect(screen.queryByText('deepseek-v4-pro')).not.toBeInTheDocument();
  });

  test('keeps the bare id when no other id line carries it', async () => {
    // `gpt-5.6-luna` resolves to nothing upstream. Trimming here on the
    // strength of the name alone would leave the row with no callable id at
    // all, and a pretty name is not an api argument.
    stubStaleService([scored({
      modelId: 'gpt-5.6-luna',
      displayName: 'GPT-5.6 Luna',
      canonicalId: null,
      vendorModelId: null,
    })]);
    renderProviderPage();

    expect(await screen.findByText('GPT-5.6 Luna')).toBeInTheDocument();
    expect(screen.getByText('gpt-5.6-luna')).toBeInTheDocument();
  });

  test('keeps the bare id when the other id line names a different offer', async () => {
    // This is the case the rule exists to survive. `hy3-free` resolves to
    // `tencent/hy3` — the upstream model, without the free tier. Hiding the id
    // would leave the page telling a reader to call `hy3`, which is a different
    // offer with different billing.
    stubStaleService([scored({
      modelId: 'hy3-free',
      displayName: 'Hy3 Free',
      canonicalId: null,
      vendorModelId: 'tencent/hy3',
    })]);
    renderProviderPage();

    expect(await screen.findByText('Hy3 Free')).toBeInTheDocument();
    expect(screen.getByText('hy3-free')).toBeInTheDocument();
    expect(screen.getByText('tencent/hy3')).toBeInTheDocument();
  });
});

describe('the evidence badge counts outstanding work, not settled verdicts', () => {
  const scored = (over: Record<string, unknown> = {}) => staleWireModel({
    modelScore: {
      value: 65.79, display: '65.8%', methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7, operationalWeight: 0.3, operationalPrecision: 0,
      uncertainty: 0.035, bound: null, reason: null,
      qualityEvidenceLevel: 'measured', operationalCoverage: 'complete',
    },
    modelRank: 9,
    overallScore: {
      value: 65.79, display: '65.8%', status: 'complete', qualityScore: 67.5, operationalScore: 61.8,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding'], excludedDimensions: [], uncertainty: 1, reasons: [],
      methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
    },
    overallRank: 9,
    resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    missingFacts: [],
    rejectedCandidates: [],
    ...over,
  });

  const structuredConflict = (status: 'open' | 'resolved') => ([{
    field: 'structured',
    sides: [{ value: false, by: 'qiniu-ai/gpt-oss-20b' }, { value: true, by: 'nvidia/openai/gpt-oss-20b' }],
    conflictType: 'source_disagreement',
    status,
    resolvedTo: status === 'resolved' ? 'true' : null,
    detectedAt: '2026-08-23T16:28:53.315Z',
  }]);

  test('a resolved conflict raises no flag', async () => {
    // `ollama-cloud/gpt-oss:20b` kept an amber "1" after its dispute was
    // answered — `structured: true`, cited to OpenRouter's supported_parameters.
    // The count read `conflicts.length` regardless of status, so a recorded
    // verdict looked exactly like an outstanding problem.
    stubStaleService([scored({ modelId: 'gpt-oss:20b', conflicts: structuredConflict('resolved') })]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-gpt-oss:20b');
    expect(toggle.textContent).not.toMatch(/\d/);
    // The tooltip names the verdict rather than falling back to the generic
    // line: there IS something to read here, it just needs nobody's attention.
    expect(toggle.getAttribute('title')).toMatch(/1 settled — nothing outstanding/);
  });

  test('an open conflict still raises one', async () => {
    // The other half: a real dispute must keep its flag, or the badge stops
    // being worth looking at.
    stubStaleService([scored({ modelId: 'qwen3.5-plus', conflicts: structuredConflict('open') })]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-qwen3.5-plus');
    expect(toggle.textContent).toMatch(/1/);
    expect(toggle.getAttribute('title')).toMatch(/1 conflicted/);
  });
});

describe('waiting on the world is not the same as work to do', () => {
  const scored = (over: Record<string, unknown> = {}) => staleWireModel({
    modelScore: {
      value: 65.79, display: '65.8%', methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7, operationalWeight: 0.3, operationalPrecision: 0,
      uncertainty: 0.035, bound: null, reason: null,
      qualityEvidenceLevel: 'measured', operationalCoverage: 'complete',
    },
    modelRank: 9,
    overallScore: {
      value: 65.79, display: '65.8%', status: 'complete', qualityScore: 67.5, operationalScore: 61.8,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding'], excludedDimensions: [], uncertainty: 1, reasons: [],
      methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
    },
    overallRank: 9,
    conflicts: [], missingFacts: [], rejectedCandidates: [],
    ...over,
  });

  test('awaiting an external benchmark raises no flag', async () => {
    // Four offers wore an amber flag for this — `big-pickle`, both `Ox Alpha
    // Free` rows and `deepseek-v4-flash-vision-exp` — and were read as having
    // conflicts. This state is reached only when the sole outstanding reasons are
    // `missing_vq`/`missing_vo`: nobody has published a benchmark for the model
    // yet. There is no work here for the owner, and the offer is fully scored.
    stubStaleService([scored({
      modelId: 'big-pickle',
      resolution: { state: 'awaiting_external_benchmark', reasons: ['missing_vq'], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    })]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-big-pickle');
    expect(toggle.textContent).not.toMatch(/\d/);
  });

  test('an incomplete source still raises one', async () => {
    // The other half. `source_incomplete` means an operational fact this catalog
    // is supposed to have is missing, which IS work to do.
    stubStaleService([scored({
      modelId: 'qwen3.5-plus',
      resolution: { state: 'source_incomplete', reasons: ['missing_structured'], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    })]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-qwen3.5-plus');
    expect(toggle.textContent).toMatch(/1/);
    expect(toggle.getAttribute('title')).toMatch(/source_incomplete/);
  });
});

describe('the evidence flag means a human still has to do something', () => {
  const base = (over: Record<string, unknown> = {}) => staleWireModel({
    modelScore: {
      value: 65.79, display: '65.8%', methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7, operationalWeight: 0.3, operationalPrecision: 0,
      uncertainty: 0.035, bound: null, reason: null,
      qualityEvidenceLevel: 'measured', operationalCoverage: 'complete',
    },
    modelRank: 9,
    overallScore: {
      value: 65.79, display: '65.8%', status: 'complete', qualityScore: 67.5, operationalScore: 61.8,
      qualityCoverage: { scored: 5, applicable: 5, percent: 100 },
      overallCoverage: { scored: 7, applicable: 7, percent: 100 },
      includedDimensions: ['coding'], excludedDimensions: [], uncertainty: 1, reasons: [],
      methodologyVersion: 'overall-score-v1', computedAt: '2026-08-19T10:00:00.000Z',
    },
    overallRank: 9,
    resolution: { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null },
    conflicts: [], missingFacts: [], rejectedCandidates: [],
    ...over,
  });

  const refusal = (candidate: string) => ({
    candidate, verdict: 'candidate_rejected', why: 'context mismatch of roughly 4x',
    evidence: ['reference index context_length: 1000000'], source: 'identity_overlay',
    sourceRef: 'x', sourceUrl: null, evidenceState: 'declared_policy',
    resolverVersion: 'identity-rejections-v1', candidateMeta: {},
    reviewedAt: '2026-08-18', recordedAt: '2026-08-23T16:36:54.979Z',
  });

  test('a refused identity candidate is a decision, not a task', async () => {
    // The fifth report of "models with conflicts" on rows that had none. Both
    // survivors were flagged purely by refusals: `cline-pass/glm-5.3` had one and
    // `qwen3.5-plus` two, with zero open conflicts, nothing missing and a
    // complete score. A refusal is finished work, recorded with its evidence —
    // the reader may want to read it, but nobody has to act on it.
    stubStaleService([base({ modelId: 'cline-pass/glm-5.3', rejectedCandidates: [refusal('z-ai/glm-5.3-0722')] })]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-cline-pass/glm-5.3');
    expect(toggle.textContent).not.toMatch(/\d/);
    // Still reachable, and the tooltip says what is there to read.
    expect(toggle.getAttribute('title')).toMatch(/1 refused candidate/);
  });

  test('a resolved conflict is likewise readable but not flagged', async () => {
    stubStaleService([base({
      modelId: 'gpt-oss:20b',
      conflicts: [{
        field: 'structured', sides: [{ value: false, by: 'a/m' }, { value: true, by: 'b/m' }],
        conflictType: 'source_disagreement', status: 'resolved', resolvedTo: 'true',
        detectedAt: '2026-08-23T16:28:53.315Z',
      }],
    })]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-gpt-oss:20b');
    expect(toggle.textContent).not.toMatch(/\d/);
    expect(toggle.getAttribute('title')).toMatch(/1 settled/);
  });

  test('a missing fact IS a task and keeps its flag', async () => {
    stubStaleService([base({ modelId: 'needs-work', missingFacts: ['structured'] })]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-needs-work');
    expect(toggle.textContent).toMatch(/1/);
    expect(toggle.getAttribute('title')).toMatch(/1 missing/);
  });

  test('a decision alongside a real task does not hide the task', async () => {
    stubStaleService([base({
      modelId: 'both',
      missingFacts: ['structured'],
      rejectedCandidates: [refusal('a'), refusal('b')],
    })]);
    renderProviderPage();

    const toggle = await screen.findByTestId('evidence-toggle-both');
    expect(toggle.textContent).toMatch(/1/);
    expect(toggle.textContent).not.toMatch(/3/);
    expect(toggle.getAttribute('title')).toMatch(/2 refused candidate/);
  });
});
