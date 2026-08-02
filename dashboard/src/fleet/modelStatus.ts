// Pure model-status derivation over the effective-offering projection
// (04 §3), shared by the Providers page's stat cards, row counts, and the
// Model Test Report.
//
// The derivation reports CAPABILITY TRUTHS the server already stated —
// it never re-derives certification or routability:
//
//   truth "supported"   -> proven working
//   truth "unsupported" -> proven failed
//   truth "unknown"     -> untested
//
// Model status is the honest aggregate of its capabilities: WORKING needs
// at least one proven-supported capability; FAILED needs at least one
// proven-unsupported one AND no supported one; anything else is UNTESTED —
// never coerced to a fabricated pass or fail.

import type { EffectiveOffering } from "../api/controlClient";

export type ModelStatus = "working" | "failed" | "untested";

/** One offering's derived status from its capability truths. */
export function deriveModelStatus(offering: Pick<EffectiveOffering, "capabilities">): ModelStatus {
  let sawUnsupported = false;
  for (const capability of offering.capabilities) {
    if (capability.truth === "supported") return "working";
    if (capability.truth === "unsupported") sawUnsupported = true;
  }
  return sawUnsupported ? "failed" : "untested";
}

/** "Enabled/routable": at least one capability the SERVER flagged routable
 * (certified AND supported — intelligence.Project's conjunction). Rendered
 * verbatim from `routable`, never recomputed here. */
export function isOfferingEnabled(offering: Pick<EffectiveOffering, "capabilities">): boolean {
  return offering.capabilities.some((c) => c.routable === true);
}

export interface DistinctModelStats {
  /** Distinct provider_model_id count. */
  total: number;
  /** Distinct models with at least one WORKING offering. */
  working: number;
}

/** Distinct-model stats across a set of offerings (possibly spanning
 * several accounts): `total` counts distinct provider_model_id values;
 * `working` counts the distinct models where AT LEAST ONE offering derives
 * WORKING. */
export function distinctModelStats(offerings: readonly EffectiveOffering[]): DistinctModelStats {
  const seen = new Set<string>();
  const working = new Set<string>();
  for (const offering of offerings) {
    seen.add(offering.provider_model_id);
    if (deriveModelStatus(offering) === "working") working.add(offering.provider_model_id);
  }
  return { total: seen.size, working: working.size };
}

/** Distinct provider_model_id count for one account's offerings. */
export function distinctModelCount(offerings: readonly EffectiveOffering[]): number {
  return distinctModelStats(offerings).total;
}

/** The four operations the probe endpoint accepts — an exact mirror of the
 * server's `probeableOperations` (internal/httpapi/probe.go): anything else
 * is rejected 422 before a probe ever runs. chat and streaming are
 * DELIBERATELY excluded — they are certified by actual successful use, not
 * a deliberate probe — and image_generation is reserved future scope.
 * Declared in probe-preference order: tools first, then the remaining
 * three in the server set's declared order. */
export const PROBEABLE_OPERATIONS: ReadonlySet<string> = new Set([
  "tools",
  "context_window",
  "structured_output",
  "vision",
]);

/** The offering-operation id a probe should target for this model: the id
 * of the first capability whose operation is in PROBEABLE_OPERATIONS
 * (iterated in its declared preference order — tools first) AND that
 * carries an offering_operation_id. Never chat — the server rejects it 422.
 * `undefined` means NOT PROBEABLE (native/transport-only operations have no
 * offering_operations row, and chat-only models have nothing the probe
 * endpoint accepts) — the control stays disabled with the reason stated,
 * never a composed id. */
export function probeTarget(offering: Pick<EffectiveOffering, "capabilities">): string | undefined {
  for (const operation of PROBEABLE_OPERATIONS) {
    const match = offering.capabilities.find((c) => c.operation === operation && c.offering_operation_id);
    if (match) return match.offering_operation_id;
  }
  return undefined;
}
