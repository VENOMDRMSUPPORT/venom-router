import { describe, expect, it } from "vitest";
import type { EffectiveOffering, OfferingCapability } from "../api/controlClient";
import { deriveModelStatus, distinctModelStats, isOfferingEnabled, PROBEABLE_OPERATIONS, probeTarget } from "./modelStatus";

function capability(overrides: Partial<OfferingCapability> = {}): OfferingCapability {
  return {
    operation: "chat",
    effective: true,
    state: "discovered",
    truth: "unknown",
    routable: false,
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

describe("distinctModelStats — distinct provider_model_id, working = any WORKING offering", () => {
  it("deduplicates the same model across two accounts and unions the working flag", () => {
    const stats = distinctModelStats([
      offering([capability({ truth: "unknown" })], "prov/model-a", "acct-1"),
      offering([capability({ truth: "supported" })], "prov/model-a", "acct-2"),
      offering([capability({ truth: "unsupported" })], "prov/model-b", "acct-1"),
    ]);
    expect(stats).toEqual({ total: 2, working: 1 });
  });

  it("returns zeros for no offerings (a REAL zero — the unknown case is the caller's null)", () => {
    expect(distinctModelStats([])).toEqual({ total: 0, working: 0 });
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
