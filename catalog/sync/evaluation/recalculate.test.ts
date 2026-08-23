import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { projectOfferOperationalEvidence, recalculateOfferOverall, recalculatePublishedOffers } from './recalculate.ts';
import { QUALITY_DIMENSIONS } from './score.ts';

function seeded() {
  const db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p1','P1','https://p1.test')`).run();
  db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at) VALUES ('p1','m1','active','2026-08-19','2026-08-19')`).run();
  db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at) VALUES ('p1','m1','vendorIdentity','"vendor/m1"','models.dev','2026-08-19')`).run();
  return { db, repo: createEvaluationRepository(db) };
}

describe('overall score recalculation', () => {
  test('projects applicability and free cost from exact facts without inventing task quality', () => {
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET tools=1, structured=0, attachment=0,
      input_modalities='["text"]', cost_kind='free' WHERE provider_id='p1' AND model_id='m1'`).run();

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const offer = new Map(repo.offerDimensions('p1', 'm1').map((row) => [row.dimension, row]));
    assert.equal(offer.get('toolCalling')?.status, 'supported');
    assert.equal(offer.get('structuredOutput')?.status, 'unsupported');
    assert.equal(offer.get('vision')?.status, 'unsupported');
    assert.equal(offer.get('costEfficiency')?.status, 'scored');
    assert.ok((offer.get('costEfficiency')?.score ?? 100) < 100);
    assert.equal(repo.identityDimensions('vendor/m1').length, 0);
  });

  test('offer applicability excludes an unsupported dimension but preserves an identity score when supported', () => {
    const { db, repo } = seeded();
    for (const modelId of ['supported', 'unsupported']) {
      db.prepare(`INSERT INTO models (provider_id,model_id,status,tools,first_seen_at,last_seen_at)
        VALUES ('p1',?, 'active', ?, '2026-08-19','2026-08-19')`).run(modelId, modelId === 'supported' ? 1 : 0);
      db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at)
        VALUES ('p1',?,'vendorIdentity','"vendor/shared"','models.dev','2026-08-19')`).run(modelId);
    }
    repo.saveIdentityDimension({
      identityId: 'vendor/shared', dimension: 'toolCalling', score: 75, rawRate: 0.75,
      uncertainty: 2, confidence: 0.98, sampleCount: 300, status: 'scored',
      rubricVersion: 'catalog-rubrics-v1', testSetHash: 'hash', evidence: ['run:1'],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const supported = recalculateOfferOverall(repo, {
      providerId: 'p1', modelId: 'supported', identityId: 'vendor/shared', computedAt: '2026-08-19',
    });
    const unsupported = recalculateOfferOverall(repo, {
      providerId: 'p1', modelId: 'unsupported', identityId: 'vendor/shared', computedAt: '2026-08-19',
    });
    assert.ok(supported.reasons.every((reason) => reason !== 'missing_toolCalling_evaluation'));
    assert.ok(unsupported.excludedDimensions.includes('toolCalling'));
  });

  test('unknown price remains unknown and never becomes a zero-cost score', () => {
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET cost_kind='unknown' WHERE provider_id='p1' AND model_id='m1'`).run();

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const cost = repo.offerDimensions('p1', 'm1').find((row) => row.dimension === 'costEfficiency');
    assert.equal(cost?.status, 'unknown');
    assert.equal(cost?.score, null);
  });

  test('persists a complete 70/30 projection from exact identity and offer dimensions', () => {
    const { repo } = seeded();
    for (const dimension of QUALITY_DIMENSIONS) repo.saveIdentityDimension({
      identityId: 'vendor/m1', dimension, score: 80, rawRate: 0.8, uncertainty: 1,
      confidence: 0.99, sampleCount: 60, status: dimension === 'vision' ? 'unsupported' : 'scored',
      rubricVersion: 'catalog-rubrics-v1', testSetHash: 'hash', evidence: [],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });
    for (const [dimension, score] of [['speed', 60], ['costEfficiency', 40]] as const) repo.saveOfferDimension({
      providerId: 'p1', modelId: 'm1', dimension, score, rawRate: score / 100,
      uncertainty: 1, confidence: 0.99, sampleCount: 60, status: 'scored', evidence: [],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });
    const result = recalculateOfferOverall(repo, { providerId: 'p1', modelId: 'm1', identityId: 'vendor/m1', computedAt: '2026-08-19' });
    assert.equal(result.status, 'complete');
    assert.equal(result.value, 71);
    assert.equal(repo.overall('p1', 'm1')?.value, 71);
  });

  test('missing identity evidence remains insufficient instead of inheriting a family score', () => {
    const { repo } = seeded();
    repo.saveOfferDimension({
      providerId: 'p1', modelId: 'm1', dimension: 'costEfficiency', score: 90,
      rawRate: 0.9, uncertainty: 1, confidence: 0.99, sampleCount: 100,
      status: 'scored', evidence: ['pricing'], evaluatedAt: '2026-08-19',
      methodologyVersion: 'overall-score-v1',
    });
    const result = recalculateOfferOverall(repo, { providerId: 'p1', modelId: 'm1', identityId: null, computedAt: '2026-08-19' });
    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.value, null);
    assert.ok(result.reasons.includes('identity_unresolved'));
    assert.ok(result.includedDimensions.includes('costEfficiency'));
    assert.equal(result.overallCoverage.scored, 1);
  });

  test('projects every published offer without inventing evidence', () => {
    const { db, repo } = seeded();
    const summary = recalculatePublishedOffers(db, '2026-08-19');
    assert.deepEqual(summary, { complete: 0, incomplete: 1, total: 1 });
    assert.equal(repo.overall('p1', 'm1')?.status, 'insufficient_evidence');
  });

  test('uses a reviewed provider-scoped identity for published-offer recalculation', () => {
    const { db, repo } = seeded();
    db.prepare(`DELETE FROM model_facts WHERE provider_id='p1' AND model_id='m1' AND field='vendorIdentity'`).run();
    db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at)
      VALUES ('p1','m1','evaluationIdentity',?,'reviewed_source','2026-08-19')`)
      .run(JSON.stringify({ id: 'p1/m1', kind: 'provider_scoped', consent: 'not_required' }));
    for (const dimension of QUALITY_DIMENSIONS) repo.saveIdentityDimension({
      identityId: 'p1/m1', dimension, score: 80, rawRate: 0.8, uncertainty: 1,
      confidence: 0.99, sampleCount: 60, status: dimension === 'vision' ? 'unsupported' : 'scored',
      rubricVersion: 'catalog-rubrics-v1', testSetHash: 'hash', evidence: [],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });
    db.prepare(`UPDATE models SET tools=1, reasoning=1, structured=1, attachment=0,
      input_modalities='["text"]', cost_kind='free' WHERE provider_id='p1' AND model_id='m1'`).run();
    repo.saveOfferDimension({ providerId: 'p1', modelId: 'm1', dimension: 'speed', score: 60,
      rawRate: 0.6, uncertainty: 1, confidence: 0.99, sampleCount: 60, status: 'scored', evidence: [],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1' });
    const summary = recalculatePublishedOffers(db, '2026-08-19');
    assert.equal(summary.complete, 1);
    assert.equal(repo.overall('p1', 'm1')?.status, 'complete');
  });
});

/**
 * Consent governs PUBLICATION, not only acquisition.
 *
 * `planEvaluation` already refuses to buy samples for an offering whose reviewed
 * declaration says consent is required. A score still standing on samples bought
 * before that review would publish the same claim anyway, so the recalculation
 * withholds it — and says which reason withheld it, so it cannot be misread as
 * missing evidence.
 */
describe('a reviewed consent requirement withholds the published score', () => {
  /**
   * A fully scored offer whose identity came from a review — the same shape as
   * "uses a reviewed provider-scoped identity" above, so the ONLY difference
   * between the two cases below is the declared consent.
   */
  function withEvaluationIdentity(consent: 'granted' | 'required') {
    const { db, repo } = seeded();
    db.prepare(`DELETE FROM model_facts WHERE provider_id='p1' AND model_id='m1' AND field='vendorIdentity'`).run();
    db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at)
      VALUES ('p1','m1','evaluationIdentity',?,'reviewed_source','2026-08-21')`)
      .run(JSON.stringify({ id: 'vendor/m1', kind: 'benchmark', consent }));
    for (const dimension of QUALITY_DIMENSIONS) {
      repo.saveIdentityDimension({
        identityId: 'vendor/m1', dimension, score: 80, rawRate: 0.8, uncertainty: 1,
        confidence: 0.99, sampleCount: 60, status: dimension === 'vision' ? 'unsupported' : 'scored',
        rubricVersion: 'catalog-rubrics-v1', testSetHash: 'hash', evidence: [],
        evaluatedAt: '2026-08-21', methodologyVersion: 'overall-score-v1',
      });
    }
    db.prepare(`UPDATE models SET tools=1, reasoning=1, structured=1, attachment=0,
      input_modalities='["text"]', cost_kind='free' WHERE provider_id='p1' AND model_id='m1'`).run();
    repo.saveOfferDimension({
      providerId: 'p1', modelId: 'm1', dimension: 'speed', score: 60, rawRate: 0.6,
      uncertainty: 1, confidence: 0.99, sampleCount: 60, status: 'scored', evidence: [],
      evaluatedAt: '2026-08-21', methodologyVersion: 'overall-score-v1',
    });
    return { db, repo };
  }

  // The control case. Without it, a fixture that never produced a score would
  // let the withholding test below pass for a reason that has nothing to do
  // with consent.
  test('a granted consent publishes a real score', () => {
    const { db, repo } = withEvaluationIdentity('granted');
    recalculatePublishedOffers(db, '2026-08-21');
    const overall = repo.overall('p1', 'm1');
    assert.ok(overall);
    assert.ok(overall.value !== null, 'the fixture must be able to produce a score at all');
    assert.ok(!overall.reasons.includes('consent_required'));
  });

  test('a required consent publishes no value, and names the reason', () => {
    const { db, repo } = withEvaluationIdentity('required');
    recalculatePublishedOffers(db, '2026-08-21');
    const overall = repo.overall('p1', 'm1');
    assert.ok(overall);
    assert.equal(overall.value, null, 'a withheld offer must not carry a score');
    assert.equal(overall.status, 'insufficient_evidence');
    assert.equal(overall.reasons[0], 'consent_required', 'the reason leads, so it is not read as missing evidence');
  });
});

describe('vision applicability is decided by the image modality, not by attachment', () => {
  /**
   * `attachment` says the endpoint accepts a file. It does not say the model can
   * SEE one, and treating it as evidence of sight produced a measured
   * contradiction: `opencode-go/mimo-v2.5-pro` publishes `["text"]` and no image
   * modality, was still marked vision-supported, and answered `400` on all three
   * samples of every vision scenario. Its sibling `deepseek-v4-flash`, with the
   * same `["text"]` and `attachment=0`, was correctly excluded and scored.
   */
  test('a text-only model with attachment support cannot do vision', () => {
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET attachment=1, input_modalities='["text"]'
      WHERE provider_id='p1' AND model_id='m1'`).run();

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const offer = new Map(repo.offerDimensions('p1', 'm1').map((row) => [row.dimension, row]));
    assert.equal(offer.get('vision')?.status, 'unsupported');
  });

  test('a declared image modality is vision support even with attachment unknown', () => {
    // `clinepass/cline-pass/qwen3.8-max` publishes `["text","image","video"]`
    // with no attachment flag. The old expression tested `attachment === null`
    // first and returned `unknown` without ever reading the modalities, so a
    // model that plainly states it takes images was left unmeasurable.
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET attachment=NULL, input_modalities='["text","image","video"]'
      WHERE provider_id='p1' AND model_id='m1'`).run();

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const offer = new Map(repo.offerDimensions('p1', 'm1').map((row) => [row.dimension, row]));
    assert.equal(offer.get('vision')?.status, 'supported');
  });

  test('an image modality is support regardless of the attachment flag', () => {
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET attachment=0, input_modalities='["text","image"]'
      WHERE provider_id='p1' AND model_id='m1'`).run();

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const offer = new Map(repo.offerDimensions('p1', 'm1').map((row) => [row.dimension, row]));
    assert.equal(offer.get('vision')?.status, 'supported');
  });

  test('no published modalities at all stays unknown rather than guessing', () => {
    // Unknown is an honest result. Absent modalities are not evidence of a
    // text-only model, so this must not become `unsupported`.
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET attachment=1, input_modalities=NULL
      WHERE provider_id='p1' AND model_id='m1'`).run();

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const offer = new Map(repo.offerDimensions('p1', 'm1').map((row) => [row.dimension, row]));
    assert.equal(offer.get('vision')?.status, 'unknown');
  });
});

describe('a projected applicability row refreshes, a measured one does not', () => {
  test('a corrected fact rewrites a stale pure projection', () => {
    // The reason the vision fix was invisible: applicability was write-once, so
    // `mimo-v2.5-pro` kept a `supported` row derived from the old attachment
    // rule forever.
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET attachment=1, input_modalities='["text","image"]'
      WHERE provider_id='p1' AND model_id='m1'`).run();
    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');
    assert.equal(
      new Map(repo.offerDimensions('p1', 'm1').map((r) => [r.dimension, r])).get('vision')?.status,
      'supported',
    );

    // The provider corrects its roster: this offering takes no images.
    db.prepare(`UPDATE models SET input_modalities='["text"]' WHERE provider_id='p1' AND model_id='m1'`).run();
    projectOfferOperationalEvidence(db, repo, '2026-08-20T00:00:00.000Z');

    assert.equal(
      new Map(repo.offerDimensions('p1', 'm1').map((r) => [r.dimension, r])).get('vision')?.status,
      'unsupported',
    );
  });

  test('a withdrawn measurement is not re-derived from a roster fact', () => {
    // This is the half that matters. A withdrawn row also has `score: null`, so
    // testing the score alone would let a projection erase the finding that the
    // dimension could not be measured — exactly what `withdrawn:*` records.
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET attachment=0, input_modalities='["text","image"]'
      WHERE provider_id='p1' AND model_id='m1'`).run();
    repo.saveOfferDimension({
      providerId: 'p1', modelId: 'm1', dimension: 'vision',
      score: null, rawRate: null, uncertainty: null, confidence: null, sampleCount: null,
      status: 'unknown', evidence: ['withdrawn:no-evidence-and-run-failed'],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });

    projectOfferOperationalEvidence(db, repo, '2026-08-20T00:00:00.000Z');

    const row = new Map(repo.offerDimensions('p1', 'm1').map((r) => [r.dimension, r])).get('vision');
    assert.equal(row?.status, 'unknown');
    assert.deepEqual(row?.evidence, ['withdrawn:no-evidence-and-run-failed']);
  });

  test('a scored row survives a projection pass untouched', () => {
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET attachment=0, input_modalities='["text"]'
      WHERE provider_id='p1' AND model_id='m1'`).run();
    repo.saveOfferDimension({
      providerId: 'p1', modelId: 'm1', dimension: 'vision',
      score: 87.5, rawRate: 0.875, uncertainty: 2, confidence: 0.98, sampleCount: 60,
      status: 'scored', evidence: ['runtime:p1/m1', 'run:9'],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });

    projectOfferOperationalEvidence(db, repo, '2026-08-20T00:00:00.000Z');

    const row = new Map(repo.offerDimensions('p1', 'm1').map((r) => [r.dimension, r])).get('vision');
    assert.equal(row?.score, 87.5);
    assert.equal(row?.status, 'scored');
  });
});
