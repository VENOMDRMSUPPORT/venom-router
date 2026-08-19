/**
 * Identity rejections — the record of candidates examined and refused.
 *
 * The identity resolver has three outcomes: resolved, ambiguous (a human must
 * choose) and unresolved (nothing matched). None of them can say what was
 * already tried. A row sitting in review therefore looks identical to a row
 * nobody investigated, and the same plausible-but-wrong bind keeps getting
 * proposed — which is exactly how a fuzzy matcher once bound `gpt-oss:20b` to
 * `gpt-oss-120b` with the correct target sitting in the same list.
 *
 * So the refusals are kept as evidence, and they are kept in the DATABASE rather
 * than only in `overlays/identity.json`, because a decision no consumer can read
 * is not part of the catalog. The catalog is the source of truth or it is not.
 *
 * The one invariant this module exists to protect: **a rejection may never
 * become a resolution.** Nothing here is ever consulted by `resolveIdentity`,
 * nothing here writes `models`, and an id that appears in both `mappings` and
 * `rejected` is a contradiction that fails the run rather than resolving to
 * whichever the code happened to read first.
 */

import type { Db } from '../db/index.ts';
import { transaction } from '../db/index.ts';

/**
 * Version of the rejection ingestion, stamped on every record.
 *
 * Separate from the resolver version: a change to how a refusal is recorded is
 * not a change to how an identity is decided, and conflating the two would make
 * "which logic produced this row" unanswerable for both.
 */
export const REJECTION_RESOLVER_VERSION = 'identity-rejections-v1';

/** What kind of finding a record holds. Two different claims, never merged. */
export type RejectionVerdict = 'candidate_rejected' | 'no_candidate_exists';

/** One refused candidate, as the overlay declares it. */
export interface RejectedCandidateEntry {
  /** The refused upstream id, or null when the finding is that none exists. */
  candidate: string | null;
  why: string;
  /** Re-verifiable lines a later reader can check against primary sources. */
  evidence?: string[];
  sourceUrl?: string;
  /** What was known about the candidate when it was refused. */
  candidateMeta?: Record<string, unknown>;
}

export interface RejectionOverlay {
  reviewedAt?: string;
  entries: Record<string, { verdict?: string; candidates: RejectedCandidateEntry[] }>;
}

/** A parsed record, one per candidate, ready to persist. */
export interface ParsedRejection {
  /** The overlay key — a catalog model id, not a provider-qualified one. */
  modelId: string;
  candidate: string | null;
  verdict: RejectionVerdict;
  why: string;
  evidence: string[];
  sourceUrl: string | null;
  candidateMeta: Record<string, unknown> | null;
  reviewedAt: string | null;
}

/**
 * Flatten the overlay into one record per candidate.
 *
 * Pure: no database, no clock. The verdict comes from the candidate itself
 * rather than from the entry's label, so an entry that says `identity_review`
 * while listing a real candidate still produces `candidate_rejected` — the data
 * decides, not the annotation.
 */
export function parseRejections(overlay: RejectionOverlay): ParsedRejection[] {
  const out: ParsedRejection[] = [];
  for (const [modelId, entry] of Object.entries(overlay.entries ?? {})) {
    for (const c of entry.candidates ?? []) {
      out.push({
        modelId,
        candidate: c.candidate ?? null,
        verdict: c.candidate ? 'candidate_rejected' : 'no_candidate_exists',
        why: c.why,
        evidence: c.evidence ?? [],
        sourceUrl: c.sourceUrl ?? null,
        candidateMeta: c.candidateMeta ?? null,
        reviewedAt: overlay.reviewedAt ?? null,
      });
    }
  }
  return out;
}

export interface IngestOptions {
  /**
   * The resolved mappings from the same overlay.
   *
   * Passed so the contradiction can be DETECTED rather than trusted not to
   * happen: an id that is both mapped and rejected must fail the run, because
   * silently preferring either one is precisely how a refused candidate would
   * end up as a resolved identity.
   */
  mappings?: Record<string, string>;
}

/**
 * Persist the rejections against every provider offering that serves the id.
 *
 * The overlay is keyed by catalog model id and several providers sell the same
 * one — `qwen3.5-plus` is sold by both OpenCode rosters. Attaching the record to
 * only one of them would leave the other looking un-investigated.
 *
 * A key nobody serves is skipped rather than stored: a rejection is a statement
 * about an offering in this catalog, and an orphan row would survive the model
 * it describes.
 */
export function ingestRejections(
  db: Db,
  overlay: RejectionOverlay,
  now: () => string,
  opts: IngestOptions = {},
): { records: number; offerings: number; skipped: string[] } {
  const parsed = parseRejections(overlay);

  for (const r of parsed) {
    if (opts.mappings && r.modelId in opts.mappings) {
      throw new Error(
        `identity overlay: "${r.modelId}" is both mapped and rejected. ` +
          'A refused candidate must never become a resolved identity, so this ' +
          'contradiction fails the run rather than resolving to whichever entry ' +
          'was read first.',
      );
    }
  }

  const at = now();
  const skipped: string[] = [];
  let records = 0;
  const offerings = new Set<string>();

  transaction(db, () => {
    const insert = db.prepare(
      `INSERT INTO identity_rejections (provider_id, model_id, rejected_candidate, verdict, reason,
                                        evidence_json, source, source_ref, source_url, evidence_state,
                                        resolver_version, candidate_meta_json, reviewed_at, recorded_at)
       VALUES (?,?,?,?,?,?,'identity_overlay',?,?,'declared_policy',?,?,?,?)
       ON CONFLICT(provider_id, model_id, rejected_candidate) DO UPDATE SET
         verdict = excluded.verdict, reason = excluded.reason,
         evidence_json = excluded.evidence_json, source_url = excluded.source_url,
         resolver_version = excluded.resolver_version,
         candidate_meta_json = excluded.candidate_meta_json,
         reviewed_at = excluded.reviewed_at, recorded_at = excluded.recorded_at`,
    );
    const offeringsOf = db.prepare(
      `SELECT provider_id FROM models WHERE model_id = ? AND status IN ('active','missing')`,
    );

    for (const r of parsed) {
      const rows = offeringsOf.all(r.modelId) as unknown as { provider_id: string }[];
      if (rows.length === 0) {
        skipped.push(r.modelId);
        continue;
      }
      for (const { provider_id } of rows) {
        insert.run(
          provider_id, r.modelId, r.candidate ?? '', r.verdict, r.why,
          JSON.stringify(r.evidence), r.modelId, r.sourceUrl,
          REJECTION_RESOLVER_VERSION,
          r.candidateMeta === null ? null : JSON.stringify(r.candidateMeta),
          r.reviewedAt, at,
        );
        records++;
        offerings.add(`${provider_id}/${r.modelId}`);
      }
    }
  });

  return { records, offerings: offerings.size, skipped: [...new Set(skipped)] };
}
