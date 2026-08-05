import { describe, expect, it } from "vitest";
import type { EffectiveOffering, OfferingCapability } from "../api/controlClient";
import {
  deriveModelStatus,
  distinctModelStats,
  hasVerifiedChat,
  isOfferingEnabled,
  PROBEABLE_OPERATIONS,
  probeTarget,
} from "./modelStatus";

function capability(overrides: Partial<OfferingCapability> = {}): OfferingCapability {
  return {
    operation: "chat",
    effective: true,
    state: "discovered",
    truth: "unknown",
    routable: false,
    provenance: "",
    ...overrides,
  };
}

function offering(caps: OfferingCapability[], id = "prov/model-a", accountId = "acct-1"): EffectiveOffering {
  return {
    account_id: accountId,
    provider_id: "prov",
    provider_model_id: id,
    availability: "available",
    effective_context_tokens: null,
    context_provenance: "",
    capabilities: caps,
    quality_score: 0,
    quality_known: false,
    cost: { is_free: null, conflict: false, confidence: 0, exact_identity_match: false, stale: false },
    classification: "general",
    tiers: {},
  };
}

describe("deriveModelStatus — the supported/unsupported/unknown matrix", () => {
  it("derives UNTESTED for a model with no capabilities at all", () => {
    expect(deriveModelStatus(offering([]))).toBe("untested");
  });

  it("derives UNTESTED when every capability truth is unknown", () => {
    expect(deriveModelStatus(offering([capability(), capability({ operation: "tools" })]))).toBe("untested");
  });

  it("derives WORKING from a single supported capability", () => {
    expect(deriveModelStatus(offering([capability({ truth: "supported" })]))).toBe("working");
  });

  it("derives WORKING when supported and unsupported coexist — one proven pass wins", () => {
    expect(
      deriveModelStatus(
        offering([capability({ truth: "unsupported" }), capability({ operation: "tools", truth: "supported" })]),
      ),
    ).toBe("working");
  });

  it("derives FAILED when at least one capability is unsupported and none is supported", () => {
    expect(
      deriveModelStatus(
        offering([capability({ truth: "unsupported" }), capability({ operation: "tools", truth: "unknown" })]),
      ),
    ).toBe("failed");
  });

  it("never converts unknown into failed — untested is not a failure", () => {
    expect(deriveModelStatus(offering([capability({ truth: "unknown" })]))).toBe("untested");
  });
});

describe("isOfferingEnabled — reports the server's routable flag, never recomputes it", () => {
  it("is enabled when any capability carries routable: true", () => {
    expect(isOfferingEnabled(offering([capability({ routable: true })]))).toBe(true);
  });

  it("is not enabled when no capability is routable, even if supported", () => {
    // truth: supported alone is NOT routability — the server's conjunction
    // (certified AND supported) decides, and here it said no.
    expect(isOfferingEnabled(offering([capability({ truth: "supported", routable: false })]))).toBe(false);
  });
});

describe("hasVerifiedChat — proven CHAT, the Live Models gate's own population", () => {
  it("is true for a certified chat capability whose truth is supported", () => {
    expect(hasVerifiedChat(offering([capability({ state: "certified", truth: "supported" })]))).toBe(
      true,
    );
  });

  // The Task 9 gate is a CONJUNCTION (status='certified' AND
  // capability_truth='supported'), and models.Certification.Transition
  // PRESERVES Truth across certified -> expired (edge 9, RecertifyTick's TTL
  // sweep) and certified -> suspended (edge 6). So "supported but no longer
  // certified" is a real, reachable row that the gate excludes — counting it
  // would re-open the very D1 hole this fix closes, one TTL later.
  it("is false for a supported chat capability whose certification EXPIRED", () => {
    expect(hasVerifiedChat(offering([capability({ state: "expired", truth: "supported" })]))).toBe(
      false,
    );
  });

  it("is false for a supported chat capability that was SUSPENDED", () => {
    expect(hasVerifiedChat(offering([capability({ state: "suspended", truth: "supported" })]))).toBe(
      false,
    );
  });

  it("is false for a supported NON-chat capability — declared caps are certified without a probe", () => {
    expect(
      hasVerifiedChat(
        offering([capability({ operation: "tools", state: "certified", truth: "supported" })]),
      ),
    ).toBe(false);
  });

  it("is false when chat is unknown even alongside a supported non-chat capability", () => {
    expect(
      hasVerifiedChat(
        offering([
          capability({ truth: "unknown" }),
          capability({ operation: "tools", state: "certified", truth: "supported" }),
        ]),
      ),
    ).toBe(false);
  });

  it("is false for an unsupported chat capability", () => {
    expect(hasVerifiedChat(offering([capability({ truth: "unsupported" })]))).toBe(false);
  });
});

describe("distinctModelStats — distinct provider_model_id, working = proven CHAT (the D1 seam)", () => {
  it("deduplicates the same model across two accounts and unions the working flag", () => {
    const stats = distinctModelStats([
      offering([capability({ truth: "unknown" })], "prov/model-a", "acct-1"),
      offering([capability({ state: "certified", truth: "supported" })], "prov/model-a", "acct-2"),
      offering([capability({ truth: "unsupported" })], "prov/model-b", "acct-1"),
    ]);
    expect(stats).toEqual({ total: 2, working: 1 });
  });

  it("returns zeros for no offerings (a REAL zero — the unknown case is the caller's null)", () => {
    expect(distinctModelStats([])).toEqual({ total: 0, working: 0 });
  });

  // D1: the advertised headline must equal the Live Models gate's population.
  // certifyDeclaredCapabilities certifies a DECLARED non-chat capability with
  // no runtime probe at all, so "tools: supported" proves nothing was ever
  // successfully asked of the model. An account whose chat probes all
  // rate-limited would otherwise read "N working / M discovered" on the card
  // while Live Models (certified+supported CHAT) shows zero.
  it("counts a model with supported TOOLS but unknown chat in total, NOT in working", () => {
    const stats = distinctModelStats([
      offering(
        [
          capability({ truth: "unknown" }),
          capability({ operation: "tools", state: "certified", truth: "supported" }),
        ],
        "prov/declared-only",
      ),
    ]);
    expect(stats).toEqual({ total: 1, working: 0 });
  });

  it("counts a model with supported CHAT in working", () => {
    const stats = distinctModelStats([
      offering(
        [capability({ state: "certified", truth: "supported" }), capability({ operation: "tools" })],
        "prov/proven-chat",
      ),
    ]);
    expect(stats).toEqual({ total: 1, working: 1 });
  });

  it("counts the proven-chat model but not its declared-only sibling", () => {
    const stats = distinctModelStats([
      offering([capability({ state: "certified", truth: "supported" })], "prov/proven-chat"),
      offering(
        [capability({ operation: "vision", state: "certified", truth: "supported" })],
        "prov/declared-only",
      ),
    ]);
    expect(stats).toEqual({ total: 2, working: 1 });
  });

  // deriveModelStatus is deliberately NOT changed: the Model Test Report's
  // per-model badge legitimately reports ANY proven capability. Only the fleet
  // headline is gated on chat.
  it("leaves deriveModelStatus reporting WORKING for the same declared-only model", () => {
    const declaredOnly = offering(
      [
        capability({ truth: "unknown" }),
        capability({ operation: "tools", state: "certified", truth: "supported" }),
      ],
      "prov/declared-only",
    );
    expect(deriveModelStatus(declaredOnly)).toBe("working");
    expect(distinctModelStats([declaredOnly]).working).toBe(0);
  });
});

describe("probeTarget — only the server's four probeable operations, never chat", () => {
  it("mirrors the server's probeableOperations contract exactly (internal/httpapi/probe.go)", () => {
    expect([...PROBEABLE_OPERATIONS].sort()).toEqual(["context_window", "structured_output", "tools", "vision"]);
  });

  it("returns undefined for a chat-only model — chat is certified by use, the probe endpoint rejects it 422", () => {
    expect(probeTarget(offering([capability({ offering_operation_id: "op-chat" })]))).toBeUndefined();
  });

  it("targets the tools op id for a chat+tools model, ignoring chat's own id", () => {
    expect(
      probeTarget(
        offering([
          capability({ offering_operation_id: "op-chat" }),
          capability({ operation: "tools", offering_operation_id: "op-tools" }),
        ]),
      ),
    ).toBe("op-tools");
  });

  it("targets the vision op id for a chat+vision model with no tools", () => {
    expect(
      probeTarget(
        offering([
          capability({ offering_operation_id: "op-chat" }),
          capability({ operation: "vision", offering_operation_id: "op-vision" }),
        ]),
      ),
    ).toBe("op-vision");
  });

  it("prefers tools over the other probeable operations regardless of listing order", () => {
    expect(
      probeTarget(
        offering([
          capability({ operation: "vision", offering_operation_id: "op-vision" }),
          capability({ operation: "context_window", offering_operation_id: "op-ctx" }),
          capability({ operation: "tools", offering_operation_id: "op-tools" }),
        ]),
      ),
    ).toBe("op-tools");
  });

  it("skips a probeable operation without an offering_operation_id — never a composed id", () => {
    expect(
      probeTarget(
        offering([
          capability({ operation: "tools" }),
          capability({ operation: "structured_output", offering_operation_id: "op-so" }),
        ]),
      ),
    ).toBe("op-so");
  });

  it("returns undefined when the only ids belong to operations outside the set", () => {
    expect(
      probeTarget(
        offering([
          capability({ offering_operation_id: "op-chat" }),
          capability({ operation: "streaming", offering_operation_id: "op-stream" }),
          capability({ operation: "tools" }),
        ]),
      ),
    ).toBeUndefined();
  });
});
