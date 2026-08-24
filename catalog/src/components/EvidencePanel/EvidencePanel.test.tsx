import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { EvidencePanel } from './EvidencePanel';
import { factStateOf } from '../FactState/FactState';
import type { ApiModel } from '../../api/client';

/** A complete, resolved, benchmarked row. Each test bends one thing. */
function model(over: Partial<ApiModel> = {}): ApiModel {
  const openConflicts = over.openConflicts ?? (over.conflicts ?? []).filter((conflict) => conflict.status === 'open');
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
    openConflicts,
    provenanceByField: {},
    modelScore: {
      value: 56,
      display: '56.0%',
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

describe('overall score explanation', () => {
  test('shows the server formula, coverage, result, and methodology version', () => {
    render(<EvidencePanel model={model()} />);

    const breakdown = screen.getByTestId('overall-score-breakdown');
    expect(breakdown).toHaveTextContent('Quality 50.0');
    expect(breakdown).toHaveTextContent('70%');
    expect(breakdown).toHaveTextContent('operations 70.0');
    expect(breakdown).toHaveTextContent('30%');
    expect(breakdown).toHaveTextContent('56.0%');
    expect(breakdown).toHaveTextContent('overall-score-v1');
    expect(breakdown).toHaveTextContent('100% coverage');
  });
});

describe('resolution explanation', () => {
  test('shows lifecycle reasons, timestamps, and the last checked source', () => {
    render(<EvidencePanel model={model({
      resolution: {
        state: 'processing', reasons: ['missing_vq'],
        firstDetectedAt: '2026-08-19T10:00:00.000Z',
        lastAttemptAt: '2026-08-19T10:00:00.000Z',
        nextAttemptAt: '2026-08-19T10:01:00.000Z',
      },
      provenanceByField: {
        context: {
          value: 128_000, source: 'provider_api', sourceRef: 'show(model)',
          sourceUrl: 'https://provider.test/model', evidenceState: 'first_party', rawValue: 128_000,
          resolverVersion: 'resolver-v2', probeVersion: null, resolvedAt: '2026-08-19T10:00:00.000Z',
        },
      },
    })} />);

    const section = screen.getByTestId('resolution-section');
    expect(section).toHaveTextContent('Processing');
    expect(section).toHaveTextContent('missing_vq');
    expect(section).toHaveTextContent('2026-08-19T10:01:00.000Z');
    expect(section).toHaveTextContent('https://provider.test/model');
  });
});

describe('surface 1 — readiness is separate from quality', () => {
  test('an operationally complete row with no VQ says so, and is not treated as broken', () => {
    // The rule that keeps working models visible: an unrated VQ is a statement
    // about published benchmarks, never a reason to hide usable data.
    render(
      <EvidencePanel
        model={model({
          vq: { ...model().vq, value: null, display: '—', evidenceLevel: 'unrated', unratedReason: 'no_published_benchmark' },
        })}
      />,
    );

    expect(screen.getByTestId('readiness-state')).toHaveTextContent('Operationally complete');
    expect(screen.getByTestId('quality-state')).toHaveTextContent('Quality unrated');
    expect(screen.getByTestId('ready-but-unrated')).toBeInTheDocument();
  });

  test('the unrated reason is shown in words, not only as a token', () => {
    render(
      <EvidencePanel
        model={model({ vq: { ...model().vq, value: null, unratedReason: 'calibration_group_excluded' } })}
      />,
    );

    const reason = screen.getByTestId('unrated-reason');
    expect(reason).toHaveTextContent('calibration_group_excluded');
    expect(reason).toHaveTextContent(/no predictive power for this vendor/i);
  });
});

describe('surface 2 — a missing fact is named, never rendered as a value', () => {
  test('each missing field appears with a reason', () => {
    render(<EvidencePanel model={model({ catalogReady: false, missingFacts: ['structured', 'maxOutput'] })} />);

    expect(screen.getByTestId('missing-structured')).toHaveTextContent(/not published by any source/i);
    expect(screen.getByTestId('missing-maxOutput')).toBeInTheDocument();
    expect(screen.getByTestId('readiness-state')).toHaveTextContent('Incomplete');
  });

  test('a field missing BECAUSE of a conflict says that, not "nobody published it"', () => {
    render(
      <EvidencePanel
        model={model({
          catalogReady: false,
          missingFacts: ['structured'],
          conflicts: [{
            field: 'structured',
            sides: [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }],
            conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
            detectedAt: '2026-08-13T00:00:00.000Z',
          }],
        })}
      />,
    );

    expect(screen.getByTestId('missing-structured')).toHaveTextContent(/sources disagreed/i);
  });
});

describe('surface 3 — every side of a conflict, with who said it', () => {
  test('both values and both sources are rendered, and no winner is', () => {
    render(
      <EvidencePanel
        model={model({
          conflicts: [{
            field: 'structured',
            sides: [
              { value: true, by: 'aihubmix/hy3-preview' },
              { value: false, by: 'kilo/hy3-preview' },
            ],
            conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
            detectedAt: '2026-08-13T00:00:00.000Z',
          }],
        })}
      />,
    );

    const block = screen.getByTestId('conflict-structured');
    expect(block).toHaveTextContent('true');
    expect(block).toHaveTextContent('false');
    expect(block).toHaveTextContent('aihubmix/hy3-preview');
    expect(block).toHaveTextContent('kilo/hy3-preview');
    expect(block).toHaveTextContent('open');
  });
});

describe('surface 4 — provenance', () => {
  test('source, url, evidence state and resolver version all reach the screen', () => {
    render(
      <EvidencePanel
        model={model({
          provenanceByField: {
            context: {
              value: 128_000, source: 'models.dev', sourceRef: 'limit.context',
              sourceUrl: 'https://models.dev/api.json', evidenceState: 'first_party',
              rawValue: 128_000, resolverVersion: 'resolver-v2', probeVersion: null,
              resolvedAt: '2026-08-13T00:00:00.000Z',
            },
          },
        })}
      />,
    );

    const row = screen.getByTestId('fact-context');
    expect(row).toHaveTextContent('models.dev');
    expect(row).toHaveTextContent('first_party');
    expect(row).toHaveTextContent('resolver-v2');
    expect(screen.getByRole('link', { name: 'models.dev' })).toHaveAttribute('href', 'https://models.dev/api.json');
  });

  test('a value no probe produced says "not probed" rather than going blank', () => {
    // Blank would read as "unknown whether it was probed". It is known: it wasn't.
    render(
      <EvidencePanel
        model={model({
          provenanceByField: {
            tools: {
              value: true, source: 'models.dev', sourceRef: 'tools', sourceUrl: null,
              evidenceState: 'pooled_third_party', rawValue: true,
              resolverVersion: 'resolver-v2', probeVersion: null, resolvedAt: '2026-08-13T00:00:00.000Z',
            },
          },
        })}
      />,
    );

    expect(screen.getByTestId('fact-tools')).toHaveTextContent('not probed');
  });
});

describe('surface 5 — identity review and refused candidates', () => {
  test('the state, each candidate, its reason, evidence and metadata are shown', () => {
    render(
      <EvidencePanel
        model={model({
          canonicalId: null,
          identityState: 'identity_review',
          vq: { ...model().vq, value: null, unratedReason: 'identity_unresolved' },
          rejectedCandidates: [{
            candidate: 'qwen/qwen3.5-plus-02-15',
            verdict: 'candidate_rejected',
            why: 'only one of two dated snapshots is benchmarked',
            evidence: ['design_arena Elo 1171.875', 'the roster cannot distinguish snapshots'],
            source: 'identity_overlay', sourceRef: 'qwen3.5-plus',
            sourceUrl: 'https://openrouter.ai/api/v1/models',
            evidenceState: 'declared_policy', resolverVersion: 'identity-rejections-v1',
            candidateMeta: { designElo: 1171.875 }, reviewedAt: '2026-08-13',
            recordedAt: '2026-08-13T00:00:00.000Z',
          }],
        })}
      />,
    );

    expect(screen.getByTestId('identity-state')).toHaveTextContent('identity review');
    const block = screen.getByTestId('rejection-qwen/qwen3.5-plus-02-15');
    expect(block).toHaveTextContent('candidate rejected');
    expect(block).toHaveTextContent('only one of two dated snapshots is benchmarked');
    expect(block).toHaveTextContent('design_arena Elo 1171.875');
    expect(block).toHaveTextContent('declared_policy');
    expect(block).toHaveTextContent('identity-rejections-v1');
  });

  test('a refused candidate is never presented as the resolved identity', () => {
    const m = model({
      canonicalId: null,
      identityState: 'identity_review',
      rejectedCandidates: [{
        candidate: 'up/refused', verdict: 'candidate_rejected', why: 'no', evidence: [],
        source: 'identity_overlay', sourceRef: 'm', sourceUrl: null,
        evidenceState: 'declared_policy', resolverVersion: 'identity-rejections-v1',
        candidateMeta: null, reviewedAt: null, recordedAt: '2026-08-13T00:00:00.000Z',
      }],
    });
    render(<EvidencePanel model={m} />);

    expect(screen.getByTestId('identity-state')).not.toHaveTextContent('resolved');
    expect(m.canonicalId).toBeNull();
  });

  test('"no candidate exists" is a distinct verdict from a refusal', () => {
    render(
      <EvidencePanel
        model={model({
          canonicalId: null,
          identityState: 'identity_review',
          rejectedCandidates: [{
            candidate: null, verdict: 'no_candidate_exists',
            why: 'nothing upstream carries this name', evidence: ['token search: no match'],
            source: 'identity_overlay', sourceRef: 'm', sourceUrl: null,
            evidenceState: 'declared_policy', resolverVersion: 'identity-rejections-v1',
            candidateMeta: null, reviewedAt: null, recordedAt: '2026-08-13T00:00:00.000Z',
          }],
        })}
      />,
    );

    const block = screen.getByTestId('rejection-none');
    expect(block).toHaveTextContent('no candidate exists');
    expect(block).toHaveTextContent('nothing upstream carries this name');
  });
});

describe('surface 6 — notApplicable is not a gap', () => {
  test('an included subscription cost is explained as inapplicable, and explicitly not $0', () => {
    render(
      <EvidencePanel
        model={model({
          pricing: { ...model().pricing, kind: 'included', inputPerMTokens: null, outputPerMTokens: null },
          vo: { ...model().vo, notApplicableDimensions: ['cost'] },
        })}
      />,
    );

    const block = screen.getByTestId('na-cost');
    expect(block).toHaveTextContent(/subscription/i);
    expect(block).toHaveTextContent(/not \$0/i);
    // And it must NOT be listed among the gaps.
    expect(screen.queryByTestId('missing-cost')).not.toBeInTheDocument();
  });

  test('a notApplicable dimension and a missing one render in different sections', () => {
    render(
      <EvidencePanel
        model={model({
          catalogReady: false,
          missingFacts: ['maxOutput'],
          pricing: { ...model().pricing, kind: 'included' },
          vo: { ...model().vo, missingDimensions: ['output'], notApplicableDimensions: ['cost'] },
        })}
      />,
    );

    expect(screen.getByTestId('missing-section')).toBeInTheDocument();
    expect(screen.getByTestId('notapplicable-section')).toBeInTheDocument();
    expect(screen.getByTestId('missing-maxOutput')).toBeInTheDocument();
    expect(screen.getByTestId('na-cost')).toBeInTheDocument();
  });
});

describe('the four value states are distinguishable', () => {
  test('a conflict is reported ahead of a plain absence, because it is more specific', () => {
    const m = model({
      conflicts: [{
        field: 'structured', sides: [{ value: true, by: 'a' }, { value: false, by: 'b' }],
        conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
        detectedAt: '2026-08-13T00:00:00.000Z',
      }],
    });

    expect(factStateOf(m, 'structured', null)).toBe('conflicted');
    expect(factStateOf(m, 'maxOutput', null)).toBe('missing');
    expect(factStateOf(m, 'context', 128_000)).toBe('known');
    expect(factStateOf(m, 'cost', null, { notApplicable: true })).toBe('notApplicable');
  });

  test('a known value is never overridden by a conflict on a DIFFERENT field', () => {
    const m = model({
      conflicts: [{
        field: 'structured', sides: [{ value: true, by: 'a' }, { value: false, by: 'b' }],
        conflictType: 'source_disagreement', status: 'open', resolvedTo: null,
        detectedAt: '2026-08-13T00:00:00.000Z',
      }],
    });
    expect(factStateOf(m, 'context', 128_000)).toBe('known');
  });
});

describe('a missing fact is never shown with provenance', () => {
  test('a field named as a gap contributes no row to the provenance table', () => {
    // The rendering half of the qwen3.8-max contradiction. The backend now
    // prunes a fact whose resolver stopped proving it, so this payload is what
    // arrives; this pins that the panel presents it as one answer rather than
    // two. Deliberately NOT a client-side guard against a contradictory
    // payload — masking that upstream would hide the defect instead of ending
    // it, and the API-level invariant is asserted where it belongs, in
    // `server/app.test.ts`.
    render(
      <EvidencePanel
        model={model({
          contextTokens: null,
          catalogReady: false,
          missingFacts: ['context'],
          provenanceByField: {
            maxOutput: {
              value: 32_000, source: 'models.dev', sourceRef: 'limit.output', sourceUrl: null,
              evidenceState: 'first_party', rawValue: 32_000, resolverVersion: 'resolver-v2',
              probeVersion: null, resolvedAt: '2026-08-13T00:00:00.000Z',
            },
          },
        })}
      />,
    );

    expect(screen.getByTestId('missing-context')).toHaveTextContent('not published by any source we consult');
    expect(screen.queryByTestId('fact-context')).not.toBeInTheDocument();
    // The fields that DO have evidence still show it — the panel is not simply
    // suppressing the whole table.
    expect(screen.getByTestId('fact-maxOutput')).toBeInTheDocument();
  });
});

describe('the unrated reason for an unresolved identity does not misdescribe the cause', () => {
  test('it does not tell the reader that a benchmark would not help', async () => {
    // Identity is resolved AGAINST the benchmark index, so "no upstream model
    // matched this id" and "the index does not carry this model" are the same
    // event. `cline-pass/glm-5.3` is the case that exposed it: the index stops
    // at z-ai/glm-5.2, and the panel told the reader that more benchmarks would
    // not help — when the index listing the model is precisely what settles it.
    // A reason that misstates its own cause sends a reader to fix the wrong
    // thing, which is worse than no reason.
    render(
      <EvidencePanel
        model={{
          ...model(),
          identityState: 'identity_review',
          vq: { ...model().vq, value: null, unratedReason: 'identity_unresolved' },
        }}
      />,
    );

    const text = document.body.textContent ?? '';
    expect(text).not.toMatch(/more benchmarks would not help/i);
    expect(text).toMatch(/no upstream (model|index)/i);
  });
});

describe('a bounded VQ shows what the bound rests on', () => {
  test('the relation behind the figure is rendered, not just the figure', async () => {
    // Every other number on this page can be traced to a source by following a
    // field's provenance. A bound is the one figure that comes from a person's
    // judgement, which makes it the one that most needs its basis on screen —
    // and it was being shown as a bare "VQ ≥ 53" with nothing behind it.
    render(
      <EvidencePanel
        model={{
          ...model(),
          vq: {
            ...model().vq,
            value: 52.6,
            bound: 'lower',
            evidenceLevel: 'bounded',
            display: '≥ 53',
            unratedReason: null,
            provenance: {
              ...model().vq.provenance!,
              evidenceLevel: 'bounded',
              source: 'relation: Z.ai documents GLM-5.3 as using the same base model as GLM-5.2',
              sourceModelId: null,
            },
          },
        }}
      />,
    );

    const text = document.body.textContent ?? '';
    expect(text).toMatch(/same base model as GLM-5\.2/);
    expect(text).toMatch(/not a measurement/i);
  });

  test('a measured figure is not given a bound explanation it does not have', async () => {
    render(<EvidencePanel model={model()} />);
    expect(document.body.textContent ?? '').not.toMatch(/not a measurement/i);
  });
});

describe('every evidence state the service can send is explained', () => {
  test('vendor_default says whose figure it is, and what it is not', async () => {
    // The state that most needs its own words. Cline's own extension shows
    // `cline-pass/glm-5.3` at "Context: 128K" — a hardcoded fallback it applies
    // to any model missing from OpenRouter — while this catalog shows 1M, from
    // Z.ai's published figure for the model. A reader looking at both needs the
    // panel to say which question ours answers: what the MODEL supports, not
    // what this host serves.
    render(
      <EvidencePanel
        model={{
          ...model(),
          provenanceByField: {
            context: {
              value: 1_000_000,
              source: 'models.dev',
              sourceRef: 'zhipuai-coding-plan/glm-5.3.limit.context',
              sourceUrl: 'https://docs.bigmodel.cn/cn/coding-plan/overview',
              evidenceState: 'vendor_default',
              rawValue: 1_000_000,
              resolverVersion: 'v1',
              probeVersion: null,
              resolvedAt: '2026-08-18T00:00:00.000Z',
            },
          },
        }}
      />,
    );

    const token = screen.getByText('vendor_default');
    expect(token.getAttribute('title') ?? '').toMatch(/vendor|model supports/i);
    expect(token.getAttribute('title') ?? '').not.toBe('');
  });

  test('no state the resolvers can emit is left without words', () => {
    // A state added to the backend and not here renders as a bare token with an
    // empty tooltip — which is how `vendor_default` shipped.
    const STATES = ['first_party', 'vendor_default', 'pooled_third_party', 'index_confirmation', 'declared_policy', 'measured'];
    for (const state of STATES) {
      const { unmount } = render(
        <EvidencePanel
          model={{
            ...model(),
            provenanceByField: {
              context: {
                value: 1, source: 'models.dev', sourceRef: 'x', sourceUrl: null,
                evidenceState: state, rawValue: 1, resolverVersion: 'v1',
                probeVersion: null, resolvedAt: '2026-08-18T00:00:00.000Z',
              },
            },
          }}
        />,
      );
      expect(screen.getByText(state).getAttribute('title') ?? '').not.toBe('');
      unmount();
    }
  });
});

describe('a settled dispute reads as settled', () => {
  const settled = (over: Partial<ApiModel['conflicts'][number]> = {}): ApiModel['conflicts'][number] => ({
    field: 'structured',
    sides: [
      { value: false, by: 'qiniu-ai/gpt-oss-20b' },
      { value: true, by: 'nvidia/openai/gpt-oss-20b' },
    ],
    conflictType: 'source_disagreement',
    status: 'resolved',
    resolvedTo: 'true',
    detectedAt: '2026-08-23T16:28:53.315Z',
    ...over,
  });

  test('the section does not claim no value was taken when one was', () => {
    // Fifteen disputes were answered — `gpt-oss:20b` publishes
    // `structured: true`, cited to OpenRouter's supported_parameters listing
    // `structured_outputs` — and the panel still told the reader "sources
    // contradicted each other, so no value was taken". A value WAS taken.
    render(<EvidencePanel model={model({ conflicts: [settled()] })} />);

    const section = screen.getByTestId('conflict-section');
    expect(section.textContent).not.toMatch(/no value was taken/i);
    expect(section.textContent).toMatch(/resolved/i);
    // The verdict is shown, and both sides are still kept for audit.
    expect(screen.getByTestId('conflict-structured')).toHaveTextContent('true');
    expect(screen.getByTestId('conflict-structured')).toHaveTextContent('qiniu-ai/gpt-oss-20b');
  });

  test('an open dispute keeps the withholding wording', () => {
    // The other half: where nothing has answered, the original sentence is
    // exactly right and must survive.
    render(<EvidencePanel model={model({ conflicts: [settled({ status: 'open', resolvedTo: null })] })} />);

    expect(screen.getByTestId('conflict-section').textContent).toMatch(/no value was taken/i);
  });

  test('the mixed case counts what the SERVICE still calls open, not the panel', () => {
    // The sentence is the only place a number is narrated, so it reads the
    // server-owned view. `openConflicts` is deliberately narrower than the
    // history here: a panel that recounted `conflicts` itself would be a second
    // copy of a judgement the badge and the summary count already read once.
    const open = settled({ field: 'attachment', status: 'open', resolvedTo: null });
    render(<EvidencePanel model={model({ conflicts: [settled(), open], openConflicts: [open] })} />);

    const section = screen.getByTestId('conflict-section');
    expect(section.textContent).toMatch(/1 of 2 is still open/i);
  });

  test('a missing fact is not blamed on a dispute that was settled', () => {
    // `withheld: sources disagreed` was shown for any conflict on the field,
    // resolved or not. A field can only be missing BECAUSE of a dispute that is
    // still open.
    render(<EvidencePanel model={model({ missingFacts: ['structured'], conflicts: [settled()] })} />);

    expect(screen.getByTestId('missing-structured').textContent).not.toMatch(/sources disagreed/i);
  });
});
