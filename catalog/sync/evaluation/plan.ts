/**
 * What to evaluate for one offer, and what it will cost.
 *
 * This rule has exactly one home. It used to live inline in the terminal
 * script; the service needs the same decision, and a second copy of a core
 * mechanism is a defect in this repository rather than a variation. Both
 * callers import this.
 *
 * Pure: it reads the database and returns a decision. It never contacts a
 * provider and never writes.
 */
import type { Db } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { resolveEvaluationCredential } from './provider-transport.ts';
import { OVERALL_SCORE_POLICY, QUALITY_DIMENSIONS, type QualityDimension } from './score.ts';

export interface EvaluationPlan {
  providerId: string;
  modelId: string;
  identityId: string | null;
  dimensions: QualityDimension[];
  skipped: Array<{ dimension: QualityDimension; reason: 'already_scored' | 'unsupported' }>;
  speed: 'missing' | 'scored';
  blocked: null | 'model_not_found' | 'identity_unresolved' | 'missing_credentials';
  estimatedRequests: number;
}

export interface PlanEvaluationInput {
  providerId: string;
  modelId: string;
  testSetHash: string;
  force?: boolean;
  /** Injected so tests never depend on the ambient environment. */
  hasCredential?: (providerId: string) => boolean;
}

const REQUESTS_PER_DIMENSION =
  OVERALL_SCORE_POLICY.scenarioCount * OVERALL_SCORE_POLICY.repetitions + OVERALL_SCORE_POLICY.warmupRequests;
const REQUESTS_PER_SPEED_RUN = OVERALL_SCORE_POLICY.scenarioCount + OVERALL_SCORE_POLICY.warmupRequests;

function blockedPlan(
  input: PlanEvaluationInput,
  reason: NonNullable<EvaluationPlan['blocked']>,
  identityId: string | null,
): EvaluationPlan {
  return {
    providerId: input.providerId,
    modelId: input.modelId,
    identityId,
    dimensions: [],
    skipped: [],
    speed: 'missing',
    blocked: reason,
    estimatedRequests: 0,
  };
}

/**
 * The offer's identity: the VQ source id, else the recorded vendor identity.
 *
 * Quality belongs to an identity, not to an offer, so this is what decides
 * whether two provider listings share a single body of evidence.
 */
export function resolveIdentity(db: Db, providerId: string, modelId: string): string | null {
  const row = db.prepare(`
    SELECT (SELECT source_model_id FROM model_scores s
             WHERE s.provider_id=? AND s.model_id=? AND s.kind='VQ') canonical_id,
           (SELECT value FROM model_facts f
             WHERE f.provider_id=? AND f.model_id=? AND f.field='vendorIdentity') vendor_identity_json
  `).get(providerId, modelId, providerId, modelId) as unknown as
    { canonical_id: string | null; vendor_identity_json: string | null } | undefined;
  if (!row) return null;
  if (row.canonical_id) return row.canonical_id;
  if (!row.vendor_identity_json) return null;
  try {
    const parsed = JSON.parse(row.vendor_identity_json) as unknown;
    return typeof parsed === 'string' ? parsed : null;
  } catch {
    return null;
  }
}

export function planEvaluation(db: Db, input: PlanEvaluationInput): EvaluationPlan {
  const exists = db.prepare(
    `SELECT 1 FROM models WHERE provider_id=? AND model_id=? AND status IN ('active','missing')`,
  ).get(input.providerId, input.modelId);
  if (!exists) return blockedPlan(input, 'model_not_found', null);

  const identityId = resolveIdentity(db, input.providerId, input.modelId);
  if (!identityId) return blockedPlan(input, 'identity_unresolved', null);

  const hasCredential = input.hasCredential ?? ((id: string) => resolveEvaluationCredential(id) !== null);
  if (!hasCredential(input.providerId)) return blockedPlan(input, 'missing_credentials', identityId);

  const repository = createEvaluationRepository(db);
  const scored = new Set(repository.identityDimensions(identityId)
    .filter((row) => row.status === 'scored' && row.testSetHash === input.testSetHash)
    .map((row) => row.dimension));
  const applicability = new Map(repository.offerDimensions(input.providerId, input.modelId)
    .map((row) => [row.dimension, row.status]));

  const dimensions: QualityDimension[] = [];
  const skipped: EvaluationPlan['skipped'] = [];
  for (const dimension of QUALITY_DIMENSIONS) {
    // Unsupported wins over already-scored: a capability the offer does not have
    // is excluded from coverage entirely, not merely satisfied.
    if (applicability.get(dimension) === 'unsupported') {
      skipped.push({ dimension, reason: 'unsupported' });
      continue;
    }
    if (!input.force && scored.has(dimension)) {
      skipped.push({ dimension, reason: 'already_scored' });
      continue;
    }
    dimensions.push(dimension);
  }

  const speed: EvaluationPlan['speed'] =
    !input.force && applicability.get('speed') === 'scored' ? 'scored' : 'missing';

  return {
    providerId: input.providerId,
    modelId: input.modelId,
    identityId,
    dimensions,
    skipped,
    speed,
    blocked: null,
    estimatedRequests: dimensions.length * REQUESTS_PER_DIMENSION
      + (speed === 'missing' ? REQUESTS_PER_SPEED_RUN : 0),
  };
}
