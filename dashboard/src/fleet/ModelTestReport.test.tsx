import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { AccountProjection, EffectiveOffering, OfferingCapability } from "../api/controlClient";
import ModelTestReport from "./ModelTestReport";
import { probeTarget } from "./modelStatus";

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

// model-a: WORKING + routable (enabled) + probeable via its chat op.
// model-b: UNTESTED, NO probeable operation (no offering_operation_id).
// model-c: FAILED, probeable.
const OFFERINGS: EffectiveOffering[] = [
  offering({
    provider_model_id: "zen/model-a",
    display_name: "Model A",
    capabilities: [
      capability({ truth: "supported", state: "certified", routable: true, offering_operation_id: "op-a" }),
      capability({ operation: "tools" }),
      capability({ operation: "vision" }),
      capability({ operation: "reasoning" }),
      capability({ operation: "coding" }),
      capability({ operation: "structured_output" }),
      capability({ operation: "streaming" }),
      capability({ operation: "context_window" }),
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
    capabilities: [capability({ truth: "unsupported", offering_operation_id: "op-c" })],
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

    const dialog = await screen.findByRole("dialog", { name: /model test report: opencode zen/i });
    within(dialog).getByText(/test models for opencode zen and review what this account can route/i);

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
});

describe("ModelTestReport — actions", () => {
  it("Refresh Models runs discovery for the account, polls the job, and refetches", async () => {
    const { fetchMock, onRefetch } = renderReport();
    await screen.findByRole("dialog");

    fireEvent.click(screen.getByRole("button", { name: /refresh models/i }));

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/discover"))).toBe(true);
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/jobs/job-d"))).toBe(true);
      expect(onRefetch).toHaveBeenCalled();
    });
  });

  it("Test All probes ONLY the models with a probeable operation, then refetches", async () => {
    const { fetchMock, onRefetch } = renderReport();
    await screen.findByRole("dialog");

    fireEvent.click(screen.getByRole("button", { name: /test all/i }));

    await waitFor(() => expect(onRefetch).toHaveBeenCalled());
    const probeCalls = fetchMock.mock.calls.filter(([input]) => String(input).includes("/probe"));
    expect(probeCalls.map(([input]) => String(input)).sort()).toEqual([
      "/api/control/v1/offerings/op-a/probe",
      "/api/control/v1/offerings/op-c/probe",
    ]);
  });

  it("disables the per-model Test action when there is no probeable operation, with the reason in the title", async () => {
    renderReport();
    await screen.findByRole("dialog");

    const testB = within(screen.getByTestId("report-row-zen/model-b")).getByRole("button", { name: /test model b/i });
    expect((testB as HTMLButtonElement).disabled).toBe(true);
    expect(testB.getAttribute("title")).toBe("This model has no probeable operation");

    const testA = within(screen.getByTestId("report-row-zen/model-a")).getByRole("button", { name: /test model a/i });
    expect((testA as HTMLButtonElement).disabled).toBe(false);
  });

  it("probes a single model from its row action", async () => {
    const { fetchMock, onRefetch } = renderReport();
    await screen.findByRole("dialog");

    fireEvent.click(within(screen.getByTestId("report-row-zen/model-c")).getByRole("button", { name: /test model c/i }));

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/offerings/op-c/probe"))).toBe(true);
      expect(onRefetch).toHaveBeenCalled();
    });
  });
});

describe("probeTarget — chat first, then any probeable capability, else nothing", () => {
  it("prefers the chat operation's offering_operation_id", () => {
    expect(
      probeTarget({
        capabilities: [
          capability({ operation: "tools", offering_operation_id: "op-tools" }),
          capability({ operation: "chat", offering_operation_id: "op-chat" }),
        ],
      } as never),
    ).toBe("op-chat");
  });

  it("falls back to the first capability carrying an id", () => {
    expect(
      probeTarget({
        capabilities: [capability({ operation: "chat" }), capability({ operation: "tools", offering_operation_id: "op-tools" })],
      } as never),
    ).toBe("op-tools");
  });

  it("returns undefined when nothing is probeable — never a composed id", () => {
    expect(probeTarget({ capabilities: [capability()] } as never)).toBeUndefined();
  });
});
