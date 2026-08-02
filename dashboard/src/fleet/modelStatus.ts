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

/** The offering-operation id a probe should target for this model: the
 * chat operation's when it carries one, else the first capability with an
 * id. ABSENT means NOT PROBEABLE (native/transport-only operations have no
 * offering_operations row) — the control stays disabled with the reason
 * stated, never a composed id. */
export function probeTarget(offering: Pick<EffectiveOffering, "capabilities">): string | undefined {
  const chat = offering.capabilities.find((c) => c.operation === "chat" && c.offering_operation_id);
  if (chat) return chat.offering_operation_id;
  return offering.capabilities.find((c) => c.offering_operation_id)?.offering_operation_id;
}
