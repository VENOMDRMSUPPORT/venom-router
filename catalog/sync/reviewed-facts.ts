/**
 * Reviewed facts that cannot be represented by a provider roster or its detail
 * vocabulary. These are source-backed corrections, not defaults: every value
 * carries a URL and explicit evidence so the enrichment pass can expose it with
 * provenance and reproduce it in both CLI and scheduled syncs.
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

export interface ReviewedFact<T> {
  value: T;
  ref: string;
  sourceUrl: string;
  evidence: string[];
  reviewedAt: string;
}

export interface ReviewedFactSet {
  context?: ReviewedFact<number>;
  maxOutput?: ReviewedFact<number>;
  inputModalities?: ReviewedFact<string[]>;
  tools?: ReviewedFact<boolean>;
  reasoning?: ReviewedFact<boolean>;
  structured?: ReviewedFact<boolean>;
  attachment?: ReviewedFact<boolean>;
}

export type ReviewedFactField = keyof ReviewedFactSet;
export type ReviewedFacts = Record<string, ReviewedFactSet>;

const VALUE_IS_VALID: Record<ReviewedFactField, (value: unknown) => boolean> = {
  context: (value) => typeof value === 'number' && Number.isFinite(value) && value > 0,
  maxOutput: (value) => typeof value === 'number' && Number.isFinite(value) && value > 0,
  inputModalities: (value) => Array.isArray(value) && value.length > 0 && value.every((item) => typeof item === 'string' && item.length > 0),
  tools: (value) => typeof value === 'boolean',
  reasoning: (value) => typeof value === 'boolean',
  structured: (value) => typeof value === 'boolean',
  attachment: (value) => typeof value === 'boolean',
};

const EXPECTED_TYPE: Record<ReviewedFactField, string> = {
  context: 'a positive number',
  maxOutput: 'a positive number',
  inputModalities: 'a non-empty string array',
  tools: 'a boolean',
  reasoning: 'a boolean',
  structured: 'a boolean',
  attachment: 'a boolean',
};

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

/** Validate the reviewed overlay at its trust boundary. */
export function parseReviewedFacts(raw: unknown): ReviewedFacts {
  if (!isObject(raw) || !isObject(raw.facts)) throw new Error('reviewed facts must contain a facts object');
  const parsed: ReviewedFacts = {};
  for (const [modelKey, fields] of Object.entries(raw.facts)) {
    if (!modelKey.includes('/') || !isObject(fields)) throw new Error(`invalid reviewed fact model key: ${modelKey}`);
    const set: ReviewedFactSet = {};
    for (const [fieldName, candidate] of Object.entries(fields)) {
      if (!(fieldName in VALUE_IS_VALID)) throw new Error(`unsupported reviewed fact field: ${fieldName}`);
      const field = fieldName as ReviewedFactField;
      if (!isObject(candidate)) throw new Error(`${modelKey}.${field} must be an object`);
      if (!VALUE_IS_VALID[field](candidate.value)) {
        throw new Error(`${modelKey}.${field} value must be ${EXPECTED_TYPE[field]}`);
      }
      if (typeof candidate.ref !== 'string' || !candidate.ref.trim()) throw new Error(`${modelKey}.${field}.ref is required`);
      if (typeof candidate.sourceUrl !== 'string' || !/^https?:\/\//.test(candidate.sourceUrl)) {
        throw new Error(`${modelKey}.${field}.sourceUrl must be an HTTP URL`);
      }
      if (!Array.isArray(candidate.evidence) || candidate.evidence.length === 0 || candidate.evidence.some((line) => typeof line !== 'string' || !line.trim())) {
        throw new Error(`${modelKey}.${field}.evidence must contain at least one line`);
      }
      if (typeof candidate.reviewedAt !== 'string' || !candidate.reviewedAt.trim()) {
        throw new Error(`${modelKey}.${field}.reviewedAt is required`);
      }
      (set as Record<string, unknown>)[field] = candidate;
    }
    parsed[modelKey] = set;
  }
  return parsed;
}

export function loadReviewedFacts(): ReviewedFacts {
  return parseReviewedFacts(JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'reviewed-facts.json'), 'utf8')));
}
