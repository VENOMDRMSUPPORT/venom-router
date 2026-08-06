import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock } from "../test/fetchMock";
import type { AccountProjection, EffectiveOffering, OfferingCapability } from "../api/controlClient";
import ModelTestReport from "./ModelTestReport";

const CSRF_TOKEN = "report-csrf-token";

const ACCOUNT: AccountProjection = {
  id: "acct-1",
  provider: "opencode-zen",
  external_id: "key_9c41e8b0f2",
  auth_type: "api_key",
  connection_state: "connected",
  health_state: "healthy",
  reauth_in_progress: false,
  identity: { email: "owner@example.test" },
  funding: { funding: "free", source: "owner_policy", locked: false },
  display_status: "healthy",
  eligibility: { eligible: true },
  quota: [],
  created_at: "2026-07-01T00:00:00Z",
  updated_at: "2026-07-01T00:00:00Z",
};

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

function offering(overrides: Partial<EffectiveOffering>): EffectiveOffering {
  return {
    account_id: "acct-1",
    provider_id: "opencode-zen",
    provider_model_id: "zen/model",
    availability: "available",
    effective_context_tokens: null,
    context_provenance: "",
    capabilities: [],
    quality_score: 0,
    quality_known: false,
    cost: { is_free: null, conflict: false, confidence: 0, exact_identity_match: false, stale: false },
    classification: "general",
    tiers: {},
    ...overrides,
  } as EffectiveOffering;
}

// model-a: WORKING + routable (enabled) via chat, with a real context/
// provenance pair (whole-branch review — no fixture here previously carried
// one, so the modal's ≈/✓ mark and megatoken rounding went unexercised) and
// eight capabilities so the report's +N overflow and the declared/probed
// provenance distinction are both exercised on one row.
// model-b: UNTESTED.
// model-c: FAILED.
const OFFERINGS: EffectiveOffering[] = [
  offering({
    provider_model_id: "zen/model-a",
    display_name: "Model A",
    // provider_cap (declared, unverified) — ≈ mark; 1.5 megatokens exercises
    // the non-trivial decimal rounding path (trimTrailingZero only fires on
    // a WHOLE megatoken count, e.g. 1.0 -> "1").
    effective_context_tokens: 1_500_000,
    context_provenance: "provider_cap",
    capabilities: [
      capability({ truth: "supported", state: "certified", routable: true }),
      // provenance on these two (both within the first-6 "shown" slice) is
      // fix-round 1 fixture: one declared, one probed, so the row exercises
      // the restored declared/probed distinction without touching the
      // WORKING/FAILED/UNTESTED tile counts (which key off truth only).
      // truth/state (whole-branch review, 2026-08-06): provenance is ONLY
      // ever non-empty when the server's own Routable(state, truth) holds
      // (internal/intelligence/readmodel.go:226) — state certified AND truth
      // supported. The old fixture paired a "declared"/"probed" provenance
      // with the default truth "unknown", a (state, truth, provenance)
      // triple the server can never produce, and under which both chips
      // render identically (same truth, same colour, same border).
      capability({
        operation: "tools",
        offering_operation_id: "op-a",
        state: "certified",
        truth: "supported",
        provenance: "declared",
      }),
      capability({
        operation: "context_window",
        offering_operation_id: "op-a2",
        state: "certified",
        truth: "supported",
        provenance: "probed",
      }),
      capability({ operation: "vision" }),
      capability({ operation: "reasoning" }),
      // image_generation (whole-branch review): "coding" is not one of the
      // eight values internal/models.ParseOperation accepts — a value the
      // server can never send.
      capability({ operation: "image_generation" }),
      capability({ operation: "structured_output" }),
      capability({ operation: "streaming" }),
    ],
  }),
  offering({
    provider_model_id: "zen/model-b",
    display_name: "Model B",
    capabilities: [capability()],
  }),
  offering({
    provider_model_id: "zen/model-c",
    display_name: "Model C",
    capabilities: [capability({ operation: "tools", truth: "unsupported", offering_operation_id: "op-c" })],
  }),
];

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// The modal is display-only (ModelTestReport.tsx triggers no discovery, probe,
// or benchmark call of its own) and issues no fetch at all — `overrides` is
// kept only so a future test can stub one without changing this signature.
// The dead POST /offerings/*/probe, POST /accounts/*/discover, and
// GET /jobs/* stubs that used to live here (whole-branch review, 2026-08-06)
// had no client function calling them once Test All / per-chip testing were
// removed from this dialog.
function renderReport(overrides: Record<string, () => Response> = {}, onRefetch = vi.fn()) {
  const fetchMock = createFetchMock({ ...overrides });
  vi.stubGlobal("fetch", fetchMock);

  render(
    <ModelTestReport
      open
      account={ACCOUNT}
      providerName="OpenCode Zen"
      offerings={OFFERINGS}
      csrfToken={CSRF_TOKEN}
      onSessionExpired={vi.fn()}
      onClose={vi.fn()}
      onRefetch={onRefetch}
    />,
  );
  return { fetchMock, onRefetch };
}

describe("ModelTestReport — derived tiles and rows", () => {
  it("derives the four tiles from capability truths and the server's routable flag", async () => {
    renderReport();

    const dialog = await screen.findByRole("dialog", { name: /model report: opencode zen/i });
    within(dialog).getByText(/what opencode zen exposes on this account/i);

    const tiles = dialog.querySelectorAll(".vnd-report-tile");
    expect(tiles).toHaveLength(4);
    expect(tiles[0].textContent).toContain("Working");
    expect(tiles[0].textContent).toContain("1");
    expect(tiles[1].textContent).toContain("Failed");
    expect(tiles[1].textContent).toContain("1");
    expect(tiles[2].textContent).toContain("Untested");
    expect(tiles[2].textContent).toContain("1");
    expect(tiles[3].textContent).toContain("Enabled");
    expect(tiles[3].textContent).toContain("1");
    expect(tiles[3].getAttribute("title")).toBe("Models with at least one routable (certified + supported) capability");
  });

  it("renders per-row status badges, mono ids, and capability chips capped at 6 with +N overflow", async () => {
    renderReport();
    await screen.findByRole("dialog");

    expect(screen.getByTestId("report-status-zen/model-a").textContent).toBe("WORKING");
    expect(screen.getByTestId("report-status-zen/model-b").textContent).toBe("UNTESTED");
    expect(screen.getByTestId("report-status-zen/model-c").textContent).toBe("FAILED");

    const rowA = screen.getByTestId("report-row-zen/model-a");
    within(rowA).getByText("zen/model-a");
    // 8 capabilities -> 6 chips + "+2".
    within(rowA).getByText("+2");
  });

  // Whole-branch review (2026-08-06): `showLabels={false}` on
  // ModelCapabilitySet leaves every other assertion in this file green — it
  // only ever hides the TEXT label, never the chip itself — while silently
  // reverting to the icon-only chips this modal exists to eliminate. Pin the
  // label text directly, on the modal's own call site.
  it("renders each capability's text label on the modal, not just its icon", async () => {
    renderReport();
    await screen.findByRole("dialog");

    const facts = screen.getByTestId("report-facts-zen/model-a");
    // model-a's first shown capability is "chat" (capability() defaults to
    // operation: "chat"); its text label must be visible without a hover.
    within(facts).getByText("chat");
  });

  // Fix round 1 (2026-08-06): deleting CapabilityChips.tsx silently retired a
  // 2026-08-05 owner requirement — a "declared" capability (the provider's
  // own say-so) must read apart from a "probed" one (proven by a runtime
  // measurement) WITHOUT hovering. Restored in the design system itself
  // (CapabilityIcon's `provenance` prop -> `data-provenance` + a tooltip
  // clause) and wired through ModelCapabilitySet's new `provenances` map,
  // not as a local wrapper.
  it("marks model-a's declared capability distinctly from its probed one, via a DOM marker and the tooltip — never colour alone", async () => {
    renderReport();
    await screen.findByRole("dialog");

    const facts = screen.getByTestId("report-facts-zen/model-a");
    // Scoped to the capability chip LIST specifically: `facts` also carries
    // the context-provenance mark, whose own title text ("≈ declared by
    // provider...") otherwise collides with a plain /declared/i search once
    // model-a has a real context/provenance pair (see the context-mark test
    // below).
    const chipList = within(facts).getByRole("list", { name: /capability truth/i });
    const declaredChip = within(chipList).getByTitle(/declared/i);
    const probedChip = within(chipList).getByTitle(/probed|proven/i);

    // The DOM marker the design system's CSS keys off.
    expect(declaredChip.getAttribute("data-provenance")).toBe("declared");
    expect(probedChip.getAttribute("data-provenance")).toBe("probed");
    // The distinction is ALSO in the tooltip text, not the border alone.
    expect(declaredChip.getAttribute("title")).not.toBe(probedChip.getAttribute("title"));
  });

  // Whole-branch review (2026-08-06): every fixture in this file used to
  // inherit `effective_context_tokens: null` / `context_provenance: ""` from
  // `offering()`'s own defaults, with nothing overriding either — so the
  // modal's ≈/✓ provenance mark, the megatoken rounding, and the unverified
  // "?" suffix were all unexercised on this surface. model-a now carries a
  // real (1,500,000 tokens, "provider_cap") pair.
  it("renders the context-provenance mark and the formatted megatoken value on model-a", async () => {
    renderReport();
    await screen.findByRole("dialog");

    const facts = screen.getByTestId("report-facts-zen/model-a");
    // provider_cap = declared, unverified -> the "≈" mark, never "✓".
    within(facts).getByText("≈");
    expect(within(facts).queryByText("✓")).toBeNull();
    // 1,500,000 -> "1.5M"; unverified (not context_provenance "native")
    // appends " ?" — never rendered as an unqualified, seemingly-verified
    // number.
    within(facts).getByText(/1\.5M\s*\?/);
  });

  it("renders NO selection controls — no checkboxes, no Enable All / Disable All, no Save selection (no backend persistence)", async () => {
    renderReport();
    const dialog = await screen.findByRole("dialog");

    expect(dialog.querySelectorAll('input[type="checkbox"]')).toHaveLength(0);
    expect(within(dialog).queryByText(/enable all/i)).toBeNull();
    expect(within(dialog).queryByText(/disable all/i)).toBeNull();
    expect(within(dialog).queryByText(/save selection/i)).toBeNull();
    // Close is the ONLY footer action (no "Save selection" primary).
    const footer = dialog.querySelector(".vn-dialog-footer") as HTMLElement;
    const footerButtons = within(footer).getAllByRole("button");
    expect(footerButtons.map((b) => b.textContent)).toEqual(["Close"]);
  });

  it("filters by search and by status", async () => {
    renderReport();
    await screen.findByRole("dialog");

    fireEvent.change(screen.getByRole("searchbox", { name: /search models/i }), { target: { value: "model-b" } });
    expect(screen.queryByTestId("report-row-zen/model-a")).toBeNull();
    screen.getByTestId("report-row-zen/model-b");

    fireEvent.change(screen.getByRole("searchbox", { name: /search models/i }), { target: { value: "" } });
    fireEvent.change(screen.getByRole("combobox", { name: /filter models by status/i }), { target: { value: "working" } });
    screen.getByTestId("report-row-zen/model-a");
    expect(screen.queryByTestId("report-row-zen/model-c")).toBeNull();
  });

  it("uses the same unrated wording as the Live Models page", async () => {
    renderReport();
    expect(await screen.findAllByText("Not rated")).not.toHaveLength(0);
    expect(screen.queryByText(/not rated — unknown/i)).toBeNull();
  });

  // Whole-branch review (2026-08-06): an empty `capabilities` array used to
  // pass straight into ModelCapabilitySet, which has no empty-state message
  // of its own — it just rendered an empty `role="list"`. The deleted local
  // CapabilityChips component said "No capability observed for this model
  // yet."; this pins the modal's restored parity with the Live Models page,
  // which already carries that message for the same state.
  it("shows a message, not an empty list, when an offering has zero observed capabilities", async () => {
    vi.stubGlobal("fetch", createFetchMock({}));
    render(
      <ModelTestReport
        open
        account={ACCOUNT}
        providerName="OpenCode Zen"
        offerings={[offering({ provider_model_id: "zen/model-empty", capabilities: [] })]}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onClose={vi.fn()}
        onRefetch={vi.fn()}
      />,
    );
    await screen.findByRole("dialog");

    const facts = screen.getByTestId("report-facts-zen/model-empty");
    within(facts).getByText(/no capability has been observed/i);
    expect(facts.querySelector('[role="list"]')).toBeNull();
  });

  it("has zero axe violations", async () => {
    renderReport();
    await screen.findByRole("dialog");
    await assertNoAxeViolations(document.body);
  });

  it("renders no test or refresh control, and no cost chip — the modal is a report", async () => {
    renderReport();
    await screen.findByText("zen/model-a");

    for (const label of [/refresh models/i, /test all/i]) {
      expect(screen.queryByRole("button", { name: label })).toBeNull();
    }
    expect(screen.queryByText(/cost unknown/i)).toBeNull();
    expect(screen.queryByText(/\bfree\b/i)).toBeNull();
    expect(screen.queryByText(/click one to run a test/i)).toBeNull();
    // The display controls stay.
    screen.getByPlaceholderText(/search models/i);
  });
});
