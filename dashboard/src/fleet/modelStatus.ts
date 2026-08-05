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
//
// The fleet HEADLINE count is narrower and lives in distinctModelStats /
// hasVerifiedChat: it counts proven CHAT only, so the advertised
// "{working} working" can never exceed the Live Models gate's population.
// See distinctModelStats' own comment for why the two differ.

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

/** "Proven CHAT": this offering has a CHAT capability that is both
 * `certified` and `supported` — i.e. a real completion actually came back
 * from a real probe, and that evidence is still current.
 *
 * This is a verbatim mirror of the server's Live Models gate
 * (`CatalogRepo.ListOfferings` with LiveOnly: `oo.operation = 'chat' AND
 * c.status = 'certified' AND c.capability_truth = 'supported'`), which is the
 * whole point — see distinctModelStats.
 *
 * Both clauses carry weight:
 *   - `operation === "chat"`, because the server certifies DECLARED non-chat
 *     capabilities (tools, vision, …) straight from their models.dev
 *     declaration with no runtime probe at all, so a supported `tools` truth
 *     proves only that the provider ADVERTISED the capability;
 *   - `state === "certified"`, because models.Certification.Transition
 *     PRESERVES the truth across certified -> expired (the recertify TTL
 *     sweep) and certified -> suspended. "supported but no longer certified"
 *     is a real row, and the gate excludes it. */
export function hasVerifiedChat(offering: Pick<EffectiveOffering, "capabilities">): boolean {
  return offering.capabilities.some(
    (c) => c.operation === "chat" && c.state === "certified" && c.truth === "supported",
  );
}

export interface DistinctModelStats {
  /** Distinct provider_model_id count. */
  total: number;
  /** Distinct models with at least one PROVEN-CHAT offering. */
  working: number;
}

/** Distinct-model stats across a set of offerings (possibly spanning
 * several accounts): `total` counts distinct provider_model_id values;
 * `working` counts the distinct models where AT LEAST ONE offering has
 * PROVEN CHAT (hasVerifiedChat).
 *
 * Why chat and not deriveModelStatus's "any proven capability" (D1 — one
 * honest number, stated once): this stat IS the fleet headline, rendered as
 * "{working} working / {total} discovered", and the page's own Live Models
 * list is populated by the certified+supported CHAT gate. Counting a model
 * working off a DECLARED non-chat capability — which the server certifies
 * with no runtime probe — would advertise "N working" beside a Live Models
 * list showing zero, which is exactly what happens to an account whose chat
 * probes all rate-limit. The advertised count must equal the gate's
 * population.
 *
 * deriveModelStatus is deliberately NOT narrowed the same way: the Model Test
 * Report's per-model badge reports what was actually proven about THAT model,
 * where a proven `tools` capability is a legitimate, non-headline fact. */
export function distinctModelStats(offerings: readonly EffectiveOffering[]): DistinctModelStats {
  const seen = new Set<string>();
  const working = new Set<string>();
  for (const offering of offerings) {
    seen.add(offering.provider_model_id);
    if (hasVerifiedChat(offering)) working.add(offering.provider_model_id);
  }
  return { total: seen.size, working: working.size };
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

/** Every offering-operation id this model can be individually re-tested on:
 * one entry per capability whose operation is in PROBEABLE_OPERATIONS and
 * that carries an offering_operation_id, in PROBEABLE_OPERATIONS' declared
 * preference order (tools first). Never chat/streaming — the server rejects
 * them 422. Truth is NOT filtered on: an untested candidate, an already
 * failed capability, and an already-proven one are all legitimately
 * re-testable, matching each capability chip's own individual test action.
 * `[]` means nothing here is probeable (native/transport-only operations
 * have no offering_operations row, and a chat-only model has nothing the
 * probe endpoint accepts) — never a composed id. */
export function probeTargets(offering: Pick<EffectiveOffering, "capabilities">): string[] {
  const ids: string[] = [];
  for (const operation of PROBEABLE_OPERATIONS) {
    for (const c of offering.capabilities) {
      if (c.operation === operation && c.offering_operation_id) ids.push(c.offering_operation_id);
    }
  }
  return ids;
}
