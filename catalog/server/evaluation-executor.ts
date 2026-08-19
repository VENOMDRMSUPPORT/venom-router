/**
 * The executor the queue drives.
 *
 * It is the same persistence path the terminal batch uses — nothing about how
 * evidence is produced or stored changes because the caller is now a service.
 * The only addition is a progress callback, wrapped around the transport so the
 * count comes from requests actually issued rather than from a guess.
 *
 * The credential lookup and the transport are injectable, which is what lets
 * this be tested without contacting a provider or reading the environment.
 */
import type { Db } from '../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { createEvaluationTransport, resolveEvaluationCredential } from '../sync/evaluation/provider-transport.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';
import { persistDimensionEvaluation } from '../sync/evaluation/runner.ts';
import { OVERALL_SCORE_POLICY } from '../sync/evaluation/score.ts';
import { createStreamingSpeedProbe } from '../sync/evaluation/speed-probe.ts';
import { persistSpeedEvaluation } from '../sync/evaluation/speed-runner.ts';
import type { EvaluationTransport } from '../sync/evaluation/transport.ts';
import type { EvaluationJobExecutor } from './evaluation-runner.ts';

/**
 * Everything a fresh dimension sends: the warmups plus every graded sample.
 *
 * Progress is counted in REQUESTS, not in graded samples, because the warmups
 * go over the same wire and a progress bar that ignores them appears frozen
 * while they run. A resumed dimension inherits completed samples and therefore
 * finishes below this total, which is correct — it had less to do.
 */
const REQUESTS_PER_DIMENSION =
  OVERALL_SCORE_POLICY.warmupRequests + OVERALL_SCORE_POLICY.scenarioCount * OVERALL_SCORE_POLICY.repetitions;

export interface ExecutorOverrides {
  credential?: (providerId: string) => string | null;
  transport?: (input: { providerId: string; modelId: string; credential: string }) => EvaluationTransport;
  now?: () => string;
}

export function createEvaluationExecutor(db: Db, overrides: ExecutorOverrides = {}): EvaluationJobExecutor {
  const fixtures = buildEvaluationFixtures();
  const testSetHash = fixtureDigest(fixtures);
  const now = overrides.now ?? (() => new Date().toISOString());
  const credentialFor = overrides.credential ?? resolveEvaluationCredential;
  const transportFor = overrides.transport ?? createEvaluationTransport;

  return {
    async runDimension({ providerId, modelId, identityId, dimension, onSample }) {
      const credential = credentialFor(providerId);
      // Should not happen — the plan refuses a provider with no credential
      // before a job is queued — but returning the typed incomplete state is
      // cheaper than trusting that and never worth a thrown error.
      if (!credential) return { status: 'insufficient_evidence', score: null };

      const transport = transportFor({ providerId, modelId, credential });
      let issued = 0;
      const counting: EvaluationTransport = async (payload, secret) => {
        const outcome = await transport(payload, secret);
        onSample(Math.min(++issued, REQUESTS_PER_DIMENSION), REQUESTS_PER_DIMENSION);
        return outcome;
      };

      const result = await persistDimensionEvaluation({
        db,
        providerId,
        modelId,
        identityId,
        dimension,
        scenarios: fixtures[dimension],
        transport: counting,
        credential,
        testSetHash,
        now,
      });
      return {
        status: result.status,
        score: result.status === 'complete' ? result.score.score : null,
      };
    },

    async runSpeed({ providerId, modelId }) {
      const credential = credentialFor(providerId);
      if (!credential) return { status: 'insufficient_evidence' };
      const result = await persistSpeedEvaluation({
        db,
        providerId,
        modelId,
        probe: createStreamingSpeedProbe({ providerId, modelId, credential }),
        now,
      });
      return { status: result.status };
    },

    recalculate() {
      recalculatePublishedOffers(db, now());
    },
  };
}
