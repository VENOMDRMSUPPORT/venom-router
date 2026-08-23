/**
 * Re-score a dimension from evidence already bought.
 *
 * When a grader is repaired, every dimension it has already scored is carrying a
 * verdict the current grader would not give. The usual answer is to run the
 * whole corpus again, which pays a second time for responses the provider
 * already sent. This replays the retained responses instead: same evidence, same
 * scenarios, corrected reading, no provider requests.
 *
 * It refuses anything it cannot replay in full. A dimension needs a response for
 * EVERY sample it was scored on — a partial replay would silently score a model
 * on a subset and present it as the same measurement. Those runs are reported so
 * they can be re-run deliberately, which is the only honest way to fix them.
 */
import type { Db } from '../../db/index.ts';
import { transaction } from '../../db/index.ts';
import { answerWasCutOff, buildEvaluationFixtures, fixtureDigest } from './fixtures.ts';
import { createEvaluationRepository } from './repository.ts';
import { OVERALL_SCORE_POLICY, smoothCriterionScore, type QualityDimension } from './score.ts';

export interface RegradeSummary {
  /** Dimensions replayed and re-scored. */
  rescored: Array<{ identityId: string; dimension: string; before: number | null; after: number }>;
  /** Scored dimensions that could not be re-derived, and the reason each could not. */
  unreplayable: Array<{
    identityId: string;
    dimension: string;
    retained: number;
    samples: number;
    reason: UnreplayableReason;
    /**
     * Whether the published score was withdrawn as a result.
     *
     * Only ever true for `answer_truncated`. The other two reasons mean the
     * evidence cannot be RE-READ, which is not the same as it never having
     * existed — those scores were produced from real answers and stay put.
     */
    demoted: boolean;
  }>;
}

interface SampleRow {
  run_id: number;
  identity_id: string;
  dimension: string;
  scenario_id: string;
  response_json: string | null;
}

/**
 * Why a scored dimension could not be re-derived from what is already stored.
 *
 * `answer_truncated` is the one that costs money: the provider never finished
 * an answer for at least one sample, so there is nothing to re-read and the
 * dimension needs a real re-run. Naming it separately is the difference between
 * "replay this for free" and "this must be bought again".
 */
export type UnreplayableReason = 'responses_not_retained' | 'answer_truncated' | 'unreadable_response';

export interface RegradeInput {
  db: Db;
  now: () => string;
  /** Limit to one dimension; omit to replay every scored quality dimension. */
  dimension?: QualityDimension;
  /** Compute and report the outcome without writing any score. */
  dryRun?: boolean;
}

export function regradeFromRetainedResponses(input: RegradeInput): RegradeSummary {
  const { db, now } = input;
  const fixtures = buildEvaluationFixtures();
  const testSetHash = fixtureDigest(fixtures);
  const summary: RegradeSummary = { rescored: [], unreplayable: [] };

  const rows = db.prepare(`
    SELECT es.run_id, er.identity_id, er.dimension, es.scenario_id, es.response_json
    FROM evaluation_samples es
    JOIN evaluation_runs er ON er.id = es.run_id
    WHERE er.run_kind = 'runtime'
      AND er.status = 'complete'
      AND er.test_set_hash = ?
      AND er.identity_id IS NOT NULL
      ${input.dimension ? 'AND er.dimension = ?' : ''}
    ORDER BY es.run_id, es.scenario_id, es.repetition
  `).all(...(input.dimension ? [testSetHash, input.dimension] : [testSetHash])) as unknown as SampleRow[];

  const byRun = new Map<number, SampleRow[]>();
  for (const row of rows) {
    const existing = byRun.get(row.run_id);
    if (existing) existing.push(row);
    else byRun.set(row.run_id, [row]);
  }

  const repository = createEvaluationRepository(db);
  const expected = OVERALL_SCORE_POLICY.scenarioCount * OVERALL_SCORE_POLICY.repetitions;

  transaction(db, () => {
    for (const samples of byRun.values()) {
      const { identity_id: identityId, dimension } = samples[0];
      const retained = samples.filter((sample) => sample.response_json !== null);

      // Full retention or nothing. Re-scoring on a subset would publish a
      // different measurement under the same name.
      if (retained.length !== samples.length || samples.length !== expected) {
        summary.unreplayable.push({
          identityId, dimension, retained: retained.length, samples: samples.length,
          reason: 'responses_not_retained', demoted: false,
        });
        continue;
      }

      const scenarios = new Map(fixtures[dimension as QualityDimension].map((f) => [f.id, f]));
      let successes = 0;
      let criteria = 0;
      let refusal: UnreplayableReason | null = null;
      for (const sample of retained) {
        const fixture = scenarios.get(sample.scenario_id);
        if (!fixture) { refusal = 'unreadable_response'; break; }
        let body: unknown;
        try { body = JSON.parse(sample.response_json!) as unknown; } catch { refusal = 'unreadable_response'; break; }
        // A sample the provider never finished answering is absence of evidence,
        // and its trace is not a substitute. Re-reading it would republish a
        // truncation as a measurement — the exact defect this replay corrects.
        if (answerWasCutOff(body)) { refusal = 'answer_truncated'; break; }
        const graded = fixture.grade(body);
        successes += graded.weightedSuccesses;
        criteria += graded.weightedCriteria;
      }
      const storedScore = (): number | null => (db.prepare(
        `SELECT score FROM model_identity_scores WHERE identity_id=? AND dimension=? AND methodology_ver=?`,
      ).get(identityId, dimension, OVERALL_SCORE_POLICY.methodologyVersion) as unknown as { score: number | null } | undefined)?.score ?? null;

      if (refusal || criteria === 0) {
        const reason = refusal ?? 'unreadable_response';
        // A published number derived from a response the provider never finished
        // is not a measurement, and leaving it in place would keep it inside
        // every overall score and ranking that reads it. It is withdrawn rather
        // than corrected: there is nothing here to correct it FROM.
        //
        // Withdrawn, not deleted. The row keeps its provenance and says what
        // happened; `score: null` is what makes `recalculate` drop the dimension
        // out of coverage instead of aggregating a zero. Unknown is an honest
        // result and must remain unknown.
        const demote = reason === 'answer_truncated' && storedScore() !== null;
        if (demote && !input.dryRun) {
          repository.saveIdentityDimension({
            identityId,
            dimension,
            score: null,
            rawRate: null,
            uncertainty: null,
            confidence: null,
            sampleCount: null,
            status: 'unknown',
            rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
            testSetHash,
            evidence: ['withdrawn:answer-truncated', `run:${samples[0].run_id}`, `fixture:${testSetHash}`],
            evaluatedAt: now(),
            methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
          });
        }
        summary.unreplayable.push({
          identityId, dimension, retained: retained.length, samples: samples.length,
          reason, demoted: demote,
        });
        continue;
      }

      const before = storedScore();

      const score = smoothCriterionScore(successes, criteria);
      summary.rescored.push({ identityId, dimension, before, after: score.score });
      if (input.dryRun) continue;
      repository.saveIdentityDimension({
        identityId,
        dimension,
        score: score.score,
        rawRate: score.rawRate,
        uncertainty: score.uncertainty,
        confidence: score.confidence,
        sampleCount: score.sampleCount,
        status: 'scored',
        rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
        testSetHash,
        // The trail says the number was re-derived rather than re-measured, so
        // nobody later reads it as a fresh run against the provider.
        evidence: [`regraded:retained-responses`, `run:${samples[0].run_id}`, `fixture:${testSetHash}`],
        evaluatedAt: now(),
        methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
      });
    }
  });

  return summary;
}
