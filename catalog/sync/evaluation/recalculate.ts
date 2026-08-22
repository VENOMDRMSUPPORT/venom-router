import type { EvaluationRepository, IdentityDimensionRow, OfferDimensionRow, OverallScoreRow } from './repository.ts';
import type { Db } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import {
  OPERATIONAL_DIMENSIONS,
  QUALITY_DIMENSIONS,
  aggregateOverallScore,
  type DimensionEvaluation,
  type OperationalDimension,
  type OverallScoreResult,
  type QualityDimension,
} from './score.ts';
import { scoreCostEfficiency } from './score.ts';
import { parseEvaluationIdentityFact, resolveOfferIdentityId } from './identity.ts';

export interface RecalculateOfferInput {
  providerId: string;
  modelId: string;
  identityId: string | null;
  /**
   * Set when a reviewed declaration forbids publishing a score for this offer.
   *
   * The string is recorded verbatim as the leading reason, so a withheld score
   * says WHY it is withheld rather than looking like missing evidence.
   */
  withheldReason?: string;
  computedAt: string;
}

export interface OverallRecalculationSummary {
  complete: number;
  incomplete: number;
  total: number;
}

interface OperationalFactRow {
  provider_id: string;
  model_id: string;
  identity_id: string | null;
  vendor_identity_json: string | null;
  tools: number | null;
  reasoning: number | null;
  structured: number | null;
  attachment: number | null;
  input_modalities: string | null;
  context_tokens: number | null;
  cost_kind: string | null;
  cost_in_per_m: number | null;
  cost_out_per_m: number | null;
}

const factApplicability = (value: number | null): 'supported' | 'unsupported' | 'unknown' =>
  value === 1 ? 'supported' : value === 0 ? 'unsupported' : 'unknown';

function parseModalities(value: string | null): string[] {
  if (!value) return [];
  try {
    const parsed = JSON.parse(value) as unknown;
    return Array.isArray(parsed) && parsed.every((item) => typeof item === 'string') ? parsed : [];
  } catch {
    return [];
  }
}

function qualityOfferFact(
  row: OperationalFactRow,
  dimension: QualityDimension,
  status: IdentityDimensionRow['status'],
  computedAt: string,
): OfferDimensionRow {
  return {
    providerId: row.provider_id,
    modelId: row.model_id,
    dimension,
    score: null,
    rawRate: null,
    uncertainty: null,
    confidence: null,
    sampleCount: 0,
    status,
    evidence: ['catalog-operational-facts'],
    evaluatedAt: computedAt,
    methodologyVersion: 'overall-score-v1',
  };
}

function offerFact(
  row: OperationalFactRow,
  dimension: 'speed' | 'costEfficiency',
  status: OfferDimensionRow['status'],
  computedAt: string,
  score: number | null = null,
  uncertainty: number | null = null,
): OfferDimensionRow {
  return {
    providerId: row.provider_id,
    modelId: row.model_id,
    dimension,
    score,
    rawRate: score === null ? null : score / 100,
    uncertainty,
    confidence: uncertainty === null ? null : Math.max(0, Math.min(1, 1 - uncertainty / 100)),
    sampleCount: score === null ? 0 : 100,
    status,
    evidence: ['catalog-operational-facts'],
    evaluatedAt: computedAt,
    methodologyVersion: 'overall-score-v1',
  };
}

/**
 * Projects only applicability and billing facts. It deliberately does not
 * create task-quality scores; runtime or accepted external evidence owns those.
 */
export function projectOfferOperationalEvidence(
  db: Db,
  repository: EvaluationRepository = createEvaluationRepository(db),
  computedAt: string,
): void {
  const rows = db.prepare(`
    SELECT m.provider_id, m.model_id,
      (SELECT source_model_id FROM model_scores s
        WHERE s.provider_id=m.provider_id AND s.model_id=m.model_id AND s.kind='VQ') identity_id,
      (SELECT value FROM model_facts f
        WHERE f.provider_id=m.provider_id AND f.model_id=m.model_id AND f.field='vendorIdentity') vendor_identity_json,
      m.tools, m.reasoning, m.structured, m.attachment, m.input_modalities,
      m.context_tokens, m.cost_kind, m.cost_in_per_m, m.cost_out_per_m
    FROM models m
    WHERE m.status IN ('active','missing')
    ORDER BY m.provider_id, m.model_id
  `).all() as unknown as OperationalFactRow[];

  for (const row of rows) {
    const existingOffer = new Map(repository.offerDimensions(row.provider_id, row.model_id).map((item) => [item.dimension, item]));
    const modalities = parseModalities(row.input_modalities);
    const vision = row.attachment === null
      ? 'unknown'
      : row.attachment === 1 || modalities.includes('image') ? 'supported' : 'unsupported';
    const applicability: Array<[QualityDimension, IdentityDimensionRow['status']]> = [
      ['toolCalling', factApplicability(row.tools)],
      ['reasoning', factApplicability(row.reasoning)],
      ['structuredOutput', factApplicability(row.structured)],
      ['longContext', row.context_tokens === null ? 'unknown' : 'supported'],
      ['vision', vision],
    ];
    for (const [dimension, status] of applicability) {
      if (!existingOffer.has(dimension)) repository.saveOfferDimension(qualityOfferFact(row, dimension, status, computedAt));
    }
    if (!existingOffer.has('speed')) repository.saveOfferDimension(offerFact(row, 'speed', 'unknown', computedAt));
    if (!existingOffer.has('costEfficiency')) {
      const hasPrices = row.cost_in_per_m !== null && row.cost_out_per_m !== null;
      const freeOrIncluded = row.cost_kind === 'free' || row.cost_kind === 'included';
      if (freeOrIncluded || (row.cost_kind === 'per_token' && hasPrices)) {
        const scored = scoreCostEfficiency({
          inputPricePerM: freeOrIncluded ? 0 : row.cost_in_per_m!,
          outputPricePerM: freeOrIncluded ? 0 : row.cost_out_per_m!,
        });
        repository.saveOfferDimension(offerFact(row, 'costEfficiency', 'scored', computedAt, scored.score, scored.uncertainty));
      } else {
        repository.saveOfferDimension(offerFact(row, 'costEfficiency', 'unknown', computedAt));
      }
    }
  }
}

const unknownDimension = (): DimensionEvaluation => ({
  applicability: 'unknown', score: null, uncertainty: null, confidence: null, sampleCount: 0,
});

function fromIdentity(row: IdentityDimensionRow | undefined): DimensionEvaluation {
  if (!row) return unknownDimension();
  return {
    applicability: row.status,
    score: row.score,
    uncertainty: row.uncertainty,
    confidence: row.confidence,
    sampleCount: row.sampleCount ?? 0,
  };
}

function fromOffer(row: OfferDimensionRow | undefined): DimensionEvaluation {
  if (!row) return unknownDimension();
  return {
    applicability: row.status,
    score: row.score,
    uncertainty: row.uncertainty,
    confidence: row.confidence,
    sampleCount: row.sampleCount ?? 0,
  };
}

function fromIdentityWithOfferApplicability(
  identity: IdentityDimensionRow | undefined,
  offer: OfferDimensionRow,
): DimensionEvaluation {
  if (offer.status === 'unsupported') return fromOffer(offer);
  if (!identity) return fromOffer(offer);
  return {
    applicability: identity.score === null ? offer.status : 'scored',
    score: identity.score,
    uncertainty: identity.uncertainty,
    confidence: identity.confidence,
    sampleCount: identity.sampleCount ?? 0,
  };
}

export function recalculateOfferOverall(
  repository: EvaluationRepository,
  input: RecalculateOfferInput,
): OverallScoreResult {
  // A withheld offer reads NO evidence — neither identity-level nor
  // offer-level. Nulling the identity alone would still aggregate whatever
  // offer-scoped rows exist and publish a number, which is the outcome being
  // refused.
  const withheld = input.withheldReason;
  const identityRows: IdentityDimensionRow[] = input.identityId && !withheld
    ? repository.identityDimensions(input.identityId)
    : [];
  const offerRows: OfferDimensionRow[] = withheld
    ? []
    : repository.offerDimensions(input.providerId, input.modelId);
  const identity = new Map(identityRows.map((row) => [row.dimension, row]));
  const offer = new Map(offerRows.map((row) => [row.dimension, row]));
  const quality = Object.fromEntries(QUALITY_DIMENSIONS.map((dimension) => [
    dimension,
    offer.has(dimension)
      ? fromIdentityWithOfferApplicability(identity.get(dimension), offer.get(dimension)!)
      : fromIdentity(identity.get(dimension)),
  ])) as Record<QualityDimension, DimensionEvaluation>;
  // Offer applicability gates the identity-level task score. It never copies a
  // score between identities or providers.
  for (const dimension of QUALITY_DIMENSIONS) {
    const providerRow = offer.get(dimension);
    if (!providerRow) quality[dimension] = fromIdentity(identity.get(dimension));
  }
  const operational = Object.fromEntries(OPERATIONAL_DIMENSIONS.map((dimension) => [
    dimension,
    fromOffer(offer.get(dimension)),
  ])) as Record<OperationalDimension, DimensionEvaluation>;
  const aggregated = aggregateOverallScore({ quality, operational });
  const result = withheld
    ? { ...aggregated, reasons: [withheld, ...aggregated.reasons] }
    : input.identityId
      ? aggregated
      : { ...aggregated, reasons: ['identity_unresolved', ...aggregated.reasons] };
  repository.saveOverall(toStored(input, result));
  return result;
}

function toStored(input: RecalculateOfferInput, result: OverallScoreResult): OverallScoreRow {
  return {
    providerId: input.providerId,
    modelId: input.modelId,
    value: result.value,
    qualityScore: result.qualityScore,
    operationalScore: result.operationalScore,
    qualityCoverage: result.qualityCoverage,
    overallCoverage: result.overallCoverage,
    includedDimensions: result.includedDimensions,
    excludedDimensions: result.excludedDimensions,
    status: result.status,
    uncertainty: result.uncertainty,
    reasons: result.reasons,
    methodologyVersion: 'overall-score-v1',
    computedAt: input.computedAt,
  };
}

interface PublishedOfferRow {
  provider_id: string;
  model_id: string;
  canonical_id: string | null;
  vendor_identity_json: string | null;
  evaluation_identity_json: string | null;
}

export function recalculatePublishedOffers(db: Db, computedAt: string): OverallRecalculationSummary {
  const repository = createEvaluationRepository(db);
  projectOfferOperationalEvidence(db, repository, computedAt);
  const rows = db.prepare(`
    SELECT m.provider_id, m.model_id,
      (SELECT source_model_id FROM model_scores s
        WHERE s.provider_id=m.provider_id AND s.model_id=m.model_id AND s.kind='VQ') canonical_id,
      (SELECT value FROM model_facts f
        WHERE f.provider_id=m.provider_id AND f.model_id=m.model_id AND f.field='vendorIdentity') vendor_identity_json
      ,(SELECT value FROM model_facts f
        WHERE f.provider_id=m.provider_id AND f.model_id=m.model_id AND f.field='evaluationIdentity') evaluation_identity_json
    FROM models m
    WHERE m.status IN ('active','missing')
    ORDER BY m.provider_id, m.model_id
  `).all() as unknown as PublishedOfferRow[];
  let complete = 0;
  for (const row of rows) {
    // Parsed from the row the projection already selected, never re-queried per
    // offer: asking again was one extra statement per published offer for an
    // answer already in hand.
    const evaluationIdentity = parseEvaluationIdentityFact(row.evaluation_identity_json);
    const result = recalculateOfferOverall(repository, {
      providerId: row.provider_id,
      modelId: row.model_id,
      identityId: resolveOfferIdentityId({
        canonicalId: row.canonical_id,
        vendorIdentityJson: row.vendor_identity_json,
        evaluationIdentity,
      }),
      // Consent governs PUBLICATION, not only acquisition. `planEvaluation`
      // refuses to buy samples for an offering whose review demands consent;
      // a score still standing on samples bought before that review would make
      // the same claim anyway, so it is withheld with its reason attached.
      withheldReason: evaluationIdentity?.consent === 'required' ? 'consent_required' : undefined,
      computedAt,
    });
    if (result.status === 'complete') complete++;
  }
  return { complete, incomplete: rows.length - complete, total: rows.length };
}
