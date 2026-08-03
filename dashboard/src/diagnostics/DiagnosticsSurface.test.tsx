import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type {
  ReconciliationItem,
  RouteDecisionEntry,
  RouteExplanation,
} from "../api/controlClient";
import DiagnosticsSurface from "./DiagnosticsSurface";

const CSRF_TOKEN = "diag-csrf-token";
const ROUTES_URL = "GET /api/control/v1/diagnostics/routes?limit=50";
const RECON_URL = "GET /api/control/v1/diagnostics/reconciliation?limit=50";

/** Markers that must never appear on this surface. 09 §3.9 makes the payload
 * secret-free by construction; these prove the RENDERING adds nothing. */
const PROMPT_MARKER = "ZZQQ-prompt-text-marker";
const RAW_ERROR_MARKER = "ZZQQ-upstream-raw-error";

function decision(overrides: Partial<RouteDecisionEntry> = {}): RouteDecisionEntry {
  return {
    request_id: "req-1",
    decision_id: "dec-1",
    tier: "pro",
    workload_profile_bucket: "standard",
    created_at: "2026-08-01T09:00:00Z",
    candidates: { total: 5, eligible_groups: 2, group_keys: ["opencode-zen/zen-1"] },
    exclusion_reasons: { funding_ineligible: 2, capability_uncertified: 1 },
    chosen: { provider_id: "opencode-zen", provider_model_id: "zen-1", funding: "free" },
    scores: { chosen_quality: 0.91 },
    thinking: { requested: "ultra", applied: "extended", tier_clamped: true, certified_clamped: false },
    outcome: { terminal_status: "success", total_latency_ms: 412 },
    ...overrides,
  };
}

function explanation(overrides: Partial<RouteExplanation> = {}): RouteExplanation {
  // The explanation payload carries every decision field EXCEPT the outcome rollup
  // — the attempts array is the answer at that level (see RouteExplanation).
  const base: Record<string, unknown> = { ...decision() };
  delete base.outcome;
  return {
    ...(base as Omit<RouteDecisionEntry, "outcome">),
    attempts: [
      {
        attempt: 1,
        provider_id: "opencode-zen",
        account_id: "acct-1",
        offering_operation_id: "op-1",
        status: "failure",
        latency_ms: 120,
        thinking_clamped: false,
        reservation_id: "resv-1",
        started_at: "2026-08-01T09:00:00Z",
        finished_at: "2026-08-01T09:00:01Z",
      },
      {
        attempt: 2,
        provider_id: "opencode-zen",
        account_id: "acct-2",
        offering_operation_id: "op-2",
        status: "success",
        latency_ms: null,
        thinking_clamped: true,
        reservation_id: null,
        started_at: "2026-08-01T09:00:02Z",
        finished_at: null,
      },
    ],
    ...overrides,
  };
}

function reconciliationItem(overrides: Partial<ReconciliationItem> = {}): ReconciliationItem {
  return {
    reservation_id: "res-1",
    account_id: "acct-1",
    request_id: "req-1",
    attempt_id: "att-1",
    state: "reconciliation_pending",
    attempts: 2,
    leased: false,
    dispatched_at: 1_800_000_000,
    expires_at: 1_800_000_300,
    rebaseline_flagged: false,
    allocations: [
      {
        window_id: "win-1",
        unit: "requests",
        estimated_cost: 5,
        actual_cost: null,
        actual_confidence: null,
        state: "reserved",
      },
    ],
    ...overrides,
  };
}

function handlers(overrides: Record<string, () => Response> = {}) {
  return {
    [ROUTES_URL]: () => jsonResponse(200, { data: [decision()] }),
    [RECON_URL]: () => jsonResponse(200, { data: [] }),
    ...overrides,
  };
}

function mockAll(overrides: Record<string, () => Response> = {}): ReturnType<typeof createFetchMock> {
  const mock = createFetchMock(handlers(overrides));
  vi.stubGlobal("fetch", mock);
  return mock;
}

function renderSurface(deepLinkRequestID?: string) {
  return render(
    <DiagnosticsSurface
      csrfToken={CSRF_TOKEN}
      onSessionExpired={vi.fn()}
      deepLinkRequestID={deepLinkRequestID}
    />,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("DiagnosticsSurface — route list", () => {
  it("lists decisions with the rolled-up outcome", async () => {
    mockAll();
    renderSurface();

    const row = await screen.findByTestId("route-row-req-1");
    const text = row.textContent ?? "";
    expect(text).toMatch(/pro/i);
    expect(text).toContain("zen-1");
    expect(text).toMatch(/success/i);
    expect(text).toContain("412");
  });

  // The same rules Overview follows, because the same rollup is being rendered.
  it("renders a null terminal status as no-attempt-recorded, never as success", async () => {
    mockAll({
      [ROUTES_URL]: () =>
        jsonResponse(200, {
          data: [decision({ request_id: "req-null", outcome: { terminal_status: null, total_latency_ms: null } })],
        }),
    });
    renderSurface();

    const status = await screen.findByTestId("route-status-req-null");
    expect(status.textContent ?? "").toMatch(/no attempt recorded/i);
    expect(status.textContent ?? "").not.toMatch(/success/i);
  });

  it("renders a null total latency as unknown, never as 0 ms", async () => {
    mockAll({
      [ROUTES_URL]: () =>
        jsonResponse(200, {
          data: [decision({ request_id: "req-nolat", outcome: { terminal_status: "failure", total_latency_ms: null } })],
        }),
    });
    renderSurface();

    const latency = await screen.findByTestId("route-latency-req-nolat");
    expect(latency.textContent ?? "").toMatch(/unknown/i);
    expect(latency.textContent ?? "").not.toMatch(/\b0\b/);
  });
});

describe("DiagnosticsSurface — the route explanation", () => {
  it("renders the candidate set, scores, and exclusion codes VERBATIM", async () => {
    mockAll({
      "GET /api/control/v1/diagnostics/routes/req-1": () => jsonResponse(200, { data: explanation() }),
    });
    renderSurface();

    fireEvent.click(await screen.findByTestId("route-explain-req-1"));

    const detail = await screen.findByTestId("route-explanation");
    // Candidate counts.
    expect(detail.textContent ?? "").toContain("5");
    expect(detail.textContent ?? "").toContain("opencode-zen/zen-1");
    // The typed exclusion codes, EXACTLY as the API stated them. Rewording one
    // breaks matching it against the routing docs and the audit log.
    expect(within(detail).getByText("funding_ineligible")).toBeTruthy();
    expect(within(detail).getByText("capability_uncertified")).toBeTruthy();
    // Scores.
    expect(within(detail).getByText(/chosen_quality/)).toBeTruthy();
  });

  it("emphasizes the chosen route", async () => {
    mockAll({
      "GET /api/control/v1/diagnostics/routes/req-1": () => jsonResponse(200, { data: explanation() }),
    });
    renderSurface();
    fireEvent.click(await screen.findByTestId("route-explain-req-1"));

    const chosen = await screen.findByTestId("explanation-chosen");
    expect(chosen.textContent ?? "").toContain("zen-1");
    expect(chosen.textContent ?? "").toMatch(/chosen/i);
  });

  it("renders the thinking-clamp notes: requested, applied, and which clamp fired", async () => {
    mockAll({
      "GET /api/control/v1/diagnostics/routes/req-1": () => jsonResponse(200, { data: explanation() }),
    });
    renderSurface();
    fireEvent.click(await screen.findByTestId("route-explain-req-1"));

    const clamp = await screen.findByTestId("explanation-thinking");
    const text = clamp.textContent ?? "";
    expect(text).toMatch(/ultra/);
    expect(text).toMatch(/extended/);
    expect(text).toMatch(/tier/i);
  });

  it("renders each attempt, with a null latency as unknown rather than 0", async () => {
    mockAll({
      "GET /api/control/v1/diagnostics/routes/req-1": () => jsonResponse(200, { data: explanation() }),
    });
    renderSurface();
    fireEvent.click(await screen.findByTestId("route-explain-req-1"));

    await screen.findByTestId("attempt-1");
    const second = screen.getByTestId("attempt-2");
    expect(second.textContent ?? "").toMatch(/unknown/i);
    expect(second.textContent ?? "").not.toMatch(/\b0\s*ms/i);
    // A null reservation id is stated, not blanked.
    expect(second.textContent ?? "").toMatch(/no reservation/i);
  });

  it("renders an unrecognized exclusion code rather than dropping it", async () => {
    mockAll({
      "GET /api/control/v1/diagnostics/routes/req-1": () =>
        jsonResponse(200, {
          data: explanation({ exclusion_reasons: { brand_new_exclusion: 3 } }),
        }),
    });
    renderSurface();
    fireEvent.click(await screen.findByTestId("route-explain-req-1"));

    const detail = await screen.findByTestId("route-explanation");
    // A console older than its router must surface a code it cannot gloss —
    // dropping it would hide a real exclusion.
    expect(within(detail).getByText("brand_new_exclusion")).toBeTruthy();
    expect(detail.textContent ?? "").toContain("3");
  });

  it("renders a 404 as no-record-for-this-request-id, not an empty explanation", async () => {
    mockAll({
      "GET /api/control/v1/diagnostics/routes/req-1": () =>
        jsonResponse(404, {
          error: { code: "not_found", message: "route decision not found", request_id: "r1", retryable: false },
        }),
    });
    renderSurface();
    fireEvent.click(await screen.findByTestId("route-explain-req-1"));

    await waitFor(() =>
      expect(screen.getByTestId("route-explanation").textContent ?? "").toMatch(/no record/i),
    );
    // An empty candidate table would read as "nothing was considered", which is a
    // different and false claim.
    expect(screen.queryByTestId("explanation-chosen")).toBeNull();
    expect(screen.getByTestId("route-explanation").textContent ?? "").not.toMatch(/0 candidates/i);
  });
});

describe("DiagnosticsSurface — deep link", () => {
  it("opens the explanation for the request id it was given, without a click", async () => {
    // Overview links here as /diagnostics/routes/{request_id}; landing on the bare
    // list would silently discard the operator's intent.
    mockAll({
      "GET /api/control/v1/diagnostics/routes/req-deep": () =>
        jsonResponse(200, { data: explanation({ request_id: "req-deep" }) }),
    });
    renderSurface("req-deep");

    const detail = await screen.findByTestId("route-explanation");
    expect(detail.textContent ?? "").toContain("req-deep");
  });
});

describe("DiagnosticsSurface — reconciliation", () => {
  it("lists pending and unknown_consumption reservations", async () => {
    mockAll({
      [RECON_URL]: () =>
        jsonResponse(200, {
          data: [
            reconciliationItem(),
            reconciliationItem({ reservation_id: "res-2", state: "unknown_consumption", attempts: 5 }),
          ],
        }),
    });
    renderSurface();

    const pending = await screen.findByTestId("reconciliation-res-1");
    expect(pending.textContent ?? "").toMatch(/reconciliation_pending/);
    const terminal = screen.getByTestId("reconciliation-res-2");
    expect(terminal.textContent ?? "").toMatch(/unknown_consumption/);
  });

  it("renders an unsettled allocation cost as unknown, never as 0", async () => {
    mockAll({ [RECON_URL]: () => jsonResponse(200, { data: [reconciliationItem()] }) });
    renderSurface();

    const alloc = await screen.findByTestId("allocation-res-1-win-1");
    expect(alloc.textContent ?? "").toMatch(/unknown/i);
    // The ESTIMATE is known (5) and shown; the ACTUAL is not.
    expect(alloc.textContent ?? "").toContain("5");
  });

  it("sends the CSRF token when re-syncing", async () => {
    const mock = mockAll({
      [RECON_URL]: () => jsonResponse(200, { data: [reconciliationItem()] }),
      "POST /api/control/v1/diagnostics/reconciliation/res-1": () =>
        jsonResponse(200, { data: { reservation_id: "res-1", account_id: "acct-1", action: "resync" } }),
    });
    renderSurface();

    fireEvent.click(await screen.findByRole("button", { name: /re-?sync/i }));

    await waitFor(() => {
      const call = mock.mock.calls.find(
        ([input, init]) =>
          String(input) === "/api/control/v1/diagnostics/reconciliation/res-1" && init?.method === "POST",
      );
      expect(call).toBeTruthy();
    });
    const call = mock.mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/control/v1/diagnostics/reconciliation/res-1" && init?.method === "POST",
    ) as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(call[1].headers["X-CSRF-Token"]).toBe(CSRF_TOKEN);
    expect(JSON.parse(call[1].body as string)).toEqual({ action: "resync" });
  });

  it("offers no re-sync on a terminal unknown_consumption reservation", async () => {
    // 05 §4: no manual action can un-terminalize it, and the API answers 409. An
    // enabled button would promise a recovery that cannot happen.
    mockAll({
      [RECON_URL]: () =>
        jsonResponse(200, { data: [reconciliationItem({ reservation_id: "res-term", state: "unknown_consumption" })] }),
    });
    renderSurface();

    const row = await screen.findByTestId("reconciliation-res-term");
    expect(within(row).queryByRole("button", { name: /re-?sync/i })).toBeNull();
    expect(row.textContent ?? "").toMatch(/terminal/i);
  });

  it("surfaces a typed conflict from the action rather than a generic failure", async () => {
    mockAll({
      [RECON_URL]: () => jsonResponse(200, { data: [reconciliationItem()] }),
      "POST /api/control/v1/diagnostics/reconciliation/res-1": () =>
        jsonResponse(409, {
          error: {
            code: "reservation_terminal",
            message: "this reservation is not reconciliation_pending",
            request_id: "r1",
            retryable: false,
          },
        }),
    });
    const { container } = renderSurface();

    fireEvent.click(await screen.findByRole("button", { name: /re-?sync/i }));
    await waitFor(() => expect(container.textContent ?? "").toMatch(/reservation_terminal/));
  });
});

describe("DiagnosticsSurface — secrets", () => {
  it("renders no prompt, response, or raw provider error even when the payload carries one", async () => {
    // The API cannot return these fields (09 §3.9 makes the payload secret-free by
    // construction), so a stray one could only come from a client bug. This proves
    // the rendering never reaches for an unknown field, and that a poisoned status
    // is displayed as the closed-vocabulary value it normalizes to.
    mockAll({
      [ROUTES_URL]: () =>
        jsonResponse(200, {
          data: [
            {
              ...decision(),
              prompt: PROMPT_MARKER,
              raw_error: RAW_ERROR_MARKER,
            },
          ],
        }),
      "GET /api/control/v1/diagnostics/routes/req-1": () =>
        jsonResponse(200, {
          data: {
            ...explanation(),
            prompt: PROMPT_MARKER,
            attempts: [{ ...explanation().attempts[0], raw_error: RAW_ERROR_MARKER }],
          },
        }),
    });
    const { container } = renderSurface();

    await screen.findByTestId("route-row-req-1");
    fireEvent.click(screen.getByTestId("route-explain-req-1"));
    await screen.findByTestId("route-explanation");

    expect(container.innerHTML).not.toContain(PROMPT_MARKER);
    expect(container.innerHTML).not.toContain(RAW_ERROR_MARKER);
    expect(container.innerHTML).not.toMatch(/vk_live_|sk-|Bearer /);
  });
});

describe("DiagnosticsSurface — loading, empty, error, a11y", () => {
  it("renders a loading state before the route list arrives", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    renderSurface();
    expect(screen.getAllByRole("status").length).toBeGreaterThan(0);
  });

  it("renders an empty state when nothing has been routed", async () => {
    mockAll({ [ROUTES_URL]: () => jsonResponse(200, { data: [] }) });
    const { container } = renderSurface();
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no requests|nothing has been routed/i));
  });

  it("renders an error state per card, leaving the other card intact", async () => {
    mockAll({
      [ROUTES_URL]: () =>
        jsonResponse(500, {
          error: { code: "internal", message: "internal error", request_id: "r1", retryable: true },
        }),
      [RECON_URL]: () => jsonResponse(200, { data: [reconciliationItem()] }),
    });
    renderSurface();

    await waitFor(() =>
      expect(screen.getByTestId("diagnostics-card-routes").textContent ?? "").toMatch(/could not load/i),
    );
    // Reconciliation still rendered.
    await screen.findByTestId("reconciliation-res-1");
  });

  it("propagates a session expiry", async () => {
    const onSessionExpired = vi.fn();
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [ROUTES_URL]: () =>
          jsonResponse(401, {
            error: { code: "session_expired", message: "session expired", request_id: "r2", retryable: false },
          }),
        [RECON_URL]: () =>
          jsonResponse(401, {
            error: { code: "session_expired", message: "session expired", request_id: "r2", retryable: false },
          }),
      }),
    );
    render(
      <DiagnosticsSurface csrfToken={CSRF_TOKEN} onSessionExpired={onSessionExpired} />,
    );
    await waitFor(() => expect(onSessionExpired).toHaveBeenCalled());
  });

  it("has no axe violations with the list, the explanation, and reconciliation open", async () => {
    mockAll({
      [RECON_URL]: () => jsonResponse(200, { data: [reconciliationItem()] }),
      "GET /api/control/v1/diagnostics/routes/req-1": () => jsonResponse(200, { data: explanation() }),
    });
    const { container } = renderSurface();
    fireEvent.click(await screen.findByTestId("route-explain-req-1"));
    await screen.findByTestId("route-explanation");
    await assertNoAxeViolations(container);
  });

  it("has no axe violations when empty", async () => {
    mockAll({ [ROUTES_URL]: () => jsonResponse(200, { data: [] }) });
    const { container } = renderSurface();
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no requests|nothing has been routed/i));
    await assertNoAxeViolations(container);
  });
});
