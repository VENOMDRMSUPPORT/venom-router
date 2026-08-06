import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
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

// model-a: WORKING + routable (enabled) via chat, with TWO probeable-and-
// untested candidates (tools, context_window — chat is NOT probeable, the
// server accepts only the four probeable operations) to prove Test All (and
// per-chip testing) reaches every candidate on a model, not just the first.
// model-b: UNTESTED, NO probeable operation (chat-only — a chat op id would
// still not be probeable).
// model-c: FAILED, probeable via its tools op — a failed capability remains
// individually re-testable.
const OFFERINGS: EffectiveOffering[] = [
  offering({
    provider_model_id: "zen/model-a",
    display_name: "Model A",
    capabilities: [
      capability({ truth: "supported", state: "certified", routable: true }),
      // provenance on these two (both within the first-6 "shown" slice) is
      // fix-round 1 fixture: one declared, one probed, so the row exercises
      // the restored declared/probed distinction without touching the
      // WORKING/FAILED/UNTESTED tile counts (which key off truth only).
      capability({ operation: "tools", offering_operation_id: "op-a", provenance: "declared" }),
      capability({ operation: "context_window", offering_operation_id: "op-a2", provenance: "probed" }),
      capability({ operation: "vision" }),
      capability({ operation: "reasoning" }),
      capability({ operation: "coding" }),
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

function renderReport(overrides: Record<string, () => Response> = {}, onRefetch = vi.fn()) {
  const fetchMock = createFetchMock({
    "POST /api/control/v1/accounts/acct-1/discover": () => jsonResponse(202, { data: { job_id: "job-d" } }),
    "POST /api/control/v1/offerings/op-a/probe": () =>
      jsonResponse(202, { data: { job_id: "job-p", status_url: "/api/control/v1/jobs/job-p" } }),
    "POST /api/control/v1/offerings/op-a2/probe": () =>
      jsonResponse(202, { data: { job_id: "job-p", status_url: "/api/control/v1/jobs/job-p" } }),
    "POST /api/control/v1/offerings/op-c/probe": () =>
      jsonResponse(202, { data: { job_id: "job-p", status_url: "/api/control/v1/jobs/job-p" } }),
    "GET /api/control/v1/jobs/job-d": () =>
      jsonResponse(200, { data: { job_id: "job-d", kind: "discovery", status: "completed" } }),
    "GET /api/control/v1/jobs/job-p": () =>
      jsonResponse(200, { data: { job_id: "job-p", kind: "probe", status: "completed" } }),
    ...overrides,
  });
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
    const declaredChip = within(facts).getByTitle(/declared/i);
    const probedChip = within(facts).getByTitle(/probed|proven/i);

    // The DOM marker the design system's CSS keys off.
    expect(declaredChip.getAttribute("data-provenance")).toBe("declared");
    expect(probedChip.getAttribute("data-provenance")).toBe("probed");
    // The distinction is ALSO in the tooltip text, not the border alone.
    expect(declaredChip.getAttribute("title")).not.toBe(probedChip.getAttribute("title"));
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
