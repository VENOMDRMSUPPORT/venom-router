import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Db } from '../../db/index.ts';

const HERE = dirname(fileURLToPath(import.meta.url));

export type EvaluationIdentityKind = 'benchmark' | 'provider_scoped';
export type EvaluationConsent = 'not_required' | 'required' | 'granted';

export interface EvaluationIdentityDeclaration {
  kind: EvaluationIdentityKind;
  identityId: string;
  consent: EvaluationConsent;
  sourceUrl: string;
  evidence: string[];
  reviewedAt: string;
}

export type EvaluationIdentityOverlay = Record<string, EvaluationIdentityDeclaration>;

export interface EvaluationIdentityFact {
  id: string;
  kind: EvaluationIdentityKind;
  consent: EvaluationConsent;
}

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

export function parseEvaluationIdentities(raw: unknown): EvaluationIdentityOverlay {
  if (!isObject(raw) || raw.version !== 1 || !isObject(raw.entries)) {
    throw new Error('evaluation identities must contain version 1 and an entries object');
  }
  const result: EvaluationIdentityOverlay = {};
  for (const [key, value] of Object.entries(raw.entries)) {
    if (!key.includes('/') || !isObject(value)) throw new Error(`invalid evaluation identity key: ${key}`);
    const kind = value.kind;
    if (kind !== 'benchmark' && kind !== 'provider_scoped') throw new Error(`${key}.kind is invalid`);
    if (typeof value.identityId !== 'string' || !value.identityId.trim()) throw new Error(`${key}.identityId is required`);
    const consent = value.consent;
    if (consent !== 'not_required' && consent !== 'required' && consent !== 'granted') throw new Error(`${key}.consent is invalid`);
    if (typeof value.sourceUrl !== 'string' || !/^https?:\/\//.test(value.sourceUrl)) throw new Error(`${key}.sourceUrl is invalid`);
    if (!Array.isArray(value.evidence) || value.evidence.length === 0 || value.evidence.some((line) => typeof line !== 'string' || !line.trim())) {
      throw new Error(`${key}.evidence must contain at least one line`);
    }
    if (typeof value.reviewedAt !== 'string' || !value.reviewedAt.trim()) throw new Error(`${key}.reviewedAt is required`);
    result[key] = {
      kind,
      identityId: value.identityId,
      consent,
      sourceUrl: value.sourceUrl,
      evidence: value.evidence,
      reviewedAt: value.reviewedAt,
    };
  }
  return result;
}

export function loadEvaluationIdentities(): EvaluationIdentityOverlay {
  return parseEvaluationIdentities(JSON.parse(readFileSync(join(HERE, '..', '..', 'overlays', 'evaluation-identities.json'), 'utf8')));
}

/**
 * Validate a stored `evaluationIdentity` fact value.
 *
 * Split from the query so a caller that ALREADY selected the column does not
 * have to ask the database a second time for an answer it is holding. Any
 * unreadable or partial shape is null: a declaration we cannot parse is not a
 * declaration.
 */
export function parseEvaluationIdentityFact(value: string | null): EvaluationIdentityFact | null {
  if (!value) return null;
  try {
    const parsed = JSON.parse(value) as Partial<EvaluationIdentityFact>;
    if ((parsed.kind !== 'benchmark' && parsed.kind !== 'provider_scoped')
      || (parsed.consent !== 'not_required' && parsed.consent !== 'required' && parsed.consent !== 'granted')
      || typeof parsed.id !== 'string' || !parsed.id) return null;
    return { id: parsed.id, kind: parsed.kind, consent: parsed.consent };
  } catch {
    return null;
  }
}

export function readEvaluationIdentity(db: Db, providerId: string, modelId: string): EvaluationIdentityFact | null {
  const row = db.prepare(`SELECT value FROM model_facts
    WHERE provider_id=? AND model_id=? AND field='evaluationIdentity'`).get(providerId, modelId) as { value: string | null } | undefined;
  return parseEvaluationIdentityFact(row?.value ?? null);
}

/**
 * The offer-identity precedence, stated once.
 *
 * The VQ source id first (a settled match in the reference index), then the
 * vendor identity recorded on the row, then a reviewed evaluation identity.
 * This order decides which offers share a single body of evidence, so a second
 * copy of it drifting would silently split or merge evidence between offers —
 * which is why the planner and the recalculation both call this instead of
 * spelling the order out themselves.
 */
export function resolveOfferIdentityId(input: {
  canonicalId: string | null;
  vendorIdentityJson: string | null;
  evaluationIdentity: EvaluationIdentityFact | null;
}): string | null {
  if (input.canonicalId) return input.canonicalId;
  if (input.vendorIdentityJson) {
    try {
      const parsed = JSON.parse(input.vendorIdentityJson) as unknown;
      if (typeof parsed === 'string') return parsed;
    } catch {
      // An unreadable vendor fact is not an identity. Fall through to the review.
    }
  }
  return input.evaluationIdentity?.id ?? null;
}
