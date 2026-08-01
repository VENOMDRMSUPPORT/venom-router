import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type {
  AccountProjection,
  ModelGroup,
  Provider,
  RouteDecisionEntry,
  RouteOutcome,
} from "../api/controlClient";
import OverviewSurface from "./OverviewSurface";

const PROVIDERS_URL = "GET /api/control/v1/providers";
const ACCOUNTS_URL = "GET /api/control/v1/accounts?limit=200";
const MODELS_URL = "GET /api/control/v1/models?limit=200";
const POLICY_URL = "GET /api/control/v1/routing/policy";
const CENSUS_URL = "GET /api/control/v1/certifications/review";
const ROUTES_URL = "GET /api/control/v1/diagnostics/routes?limit=10";

function provider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: "opencode-zen",
    display_name: "OpenCode Zen",
    description: "API-key provider.",
    auth_mode: "api_key",
    funding: { mode: "owner_policy", locked: false, non_expiring: false, fixed: null },
    capabilities: [],
    configured: true,
    ...overrides,
  };
}

function account(overrides: Partial<AccountProjection> = {}): AccountProjection {
  return {
    id: "acct-1",
    provider: "opencode-zen",
    external_id: "ext-secret-1",
    auth_type: "api_key",
    connection_state: "connected",
    health_state: "healthy",
    reauth_in_progress: false,
    identity: { plan: "Free" },
    funding: { funding: "free", source: "owner_policy", locked: false },
    display_status: "healthy",
    eligibility: { eligible: true },
    quota: [
      {
        source: "provider_evidence",
        unit: "requests",
        window_type: "rolling_5h",
        window_key: "5h",
        state: "available",
        freshness: "fresh",
        used: 10,
        remaining: 90,
        total: 100,
        limit_value: 100,
        reserved: 0,
        reset_at: 1_800_000_000,
        observed_at: "2026-08-01T09:00:00Z",
      },
    ],
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

function modelGroup(overrides: Partial<ModelGroup> = {}): ModelGroup {
  return {
    model_id: "model-zen-chat",
    display_name: "Zen Chat",
    native_context_tokens: 262144,
    quality_rating: 0.8,
    offerings: [],
    ...overrides,
  };
}

const TIERS = [
  {
    tier: "lite",
    funding: "free_only",
    context_ceiling_tokens: 262144,
    thinking_ceiling: "none",
    attempt_budget: 3,
    scored: false,
    weights: null,
    competitive_band: null,
    latency_tie_break_only: true,
  },
  {
    tier: "pro",
    funding: "free_and_paid",
    context_ceiling_tokens: 524288,
    thinking_ceiling: "extended",
    attempt_budget: 4,
    scored: true,
    weights: {
      quality: 0.4,
      reliability: 0.25,
      quota_headroom: 0.15,
      evidence_confidence: 0,
      cost_class: 0.15,
      latency: 0.05,
    },
    competitive_band: 0.08,
    latency_tie_break_only: false,
  },
];

function routeEntry(overrides: Partial<RouteDecisionEntry> = {}): RouteDecisionEntry {
  return {
    request_id: "req-1",
    decision_id: "dec-1",
    tier: "pro",
    workload_profile_bucket: "standard",
    created_at: "2026-08-01T09:00:00Z",
    candidates: { total: 5, eligible_groups: 2, group_keys: ["opencode-zen/zen-chat-1"] },
    exclusion_reasons: { funding_ineligible: 2 },
    chosen: { provider_id: "opencode-zen", provider_model_id: "zen-chat-1", funding: "free" },
    scores: { chosen_quality: 0.91 },
    thinking: { requested: "ultra", applied: "extended", tier_clamped: true, certified_clamped: false },
    outcome: { terminal_status: "success", total_latency_ms: 412 },
    ...overrides,
  };
}

function censusBody(count = 0) {
  return {
    data: {
      scanned: 1,
      limit: 50,
      truncated: false,
      evaluated_reasons: ["capability_not_certified"],
      not_evaluated_reasons: [
        "identity_unresolved",
        "context_unverified",
        "funding_unknown",
        "no_healthy_account",
        "quota_exhausted",
        "quota_insufficient",
        "cooling_down",
      ],
      by_reason: [{ reason: "capability_not_certified", count }],
    },
  };
}

/** Every card's fetch, all succeeding. Individual tests override one route to
 * prove per-card isolation. */
function handlers(overrides: Record<string, () => Response> = {}) {
  return {
    [PROVIDERS_URL]: () => jsonResponse(200, { data: { providers: [provider()] } }),
    [ACCOUNTS_URL]: () => jsonResponse(200, { data: { accounts: [account()] } }),
    [MODELS_URL]: () => jsonResponse(200, { data: [modelGroup()] }),
    [POLICY_URL]: () => jsonResponse(200, { data: { tiers: TIERS } }),
    [CENSUS_URL]: () => jsonResponse(200, censusBody()),
    [ROUTES_URL]: () => jsonResponse(200, { data: [routeEntry()] }),
    ...overrides,
  };
}

function mockAll(overrides: Record<string, () => Response> = {}): void {
  vi.stubGlobal("fetch", createFetchMock(handlers(overrides)));
}

function renderSurface() {
  return render(<OverviewSurface csrfToken="overview-csrf" onSessionExpired={vi.fn()} />);
}

/** A 500 envelope for a card whose failure is under test. */
function failure(): Response {
  return jsonResponse(500, {
    error: { code: "internal", message: "internal error", request_id: "r1", retryable: true },
  });
}

/** A never-settling fetch, for loading-state assertions. */
function pending(): Response {
  return new Promise<Response>(() => {}) as unknown as Response;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("OverviewSurface — summary cards", () => {
  it("renders the fleet, health, quota and tier cards from their read models", async () => {
    mockAll();
    renderSurface();

    const fleet = await screen.findByTestId("overview-card-fleet");
    expect(fleet.textContent ?? "").toMatch(/1/);

    const health = await screen.findByTestId("overview-card-health");
    // display_status is rendered verbatim, never re-derived client-side.
    expect(health.textContent ?? "").toMatch(/healthy/i);

    const quota = await screen.findByTestId("overview-card-quota");
    expect(quota.textContent ?? "").toMatch(/\d/);

    const tiers = await screen.findByTestId("overview-card-tiers");
    // The tier names come from the policy read, not from a literal list.
    expect(tiers.textContent ?? "").toMatch(/lite/i);
    expect(tiers.textContent ?? "").toMatch(/pro/i);
    // `max` was NOT in the served payload, so it must not appear.
    expect(tiers.textContent ?? "").not.toMatch(/\bmax\b/i);

    const models = await screen.findByTestId("overview-card-models");
    expect(models.textContent ?? "").toMatch(/1/);
  });
});

describe("OverviewSurface — recent activity", () => {
  it("renders a decision's tier, chosen route, clamps, and rolled-up outcome", async () => {
    mockAll();
    renderSurface();

    const row = await screen.findByTestId("activity-req-1");
    const text = row.textContent ?? "";
    expect(text).toMatch(/pro/i);
    expect(text).toContain("zen-chat-1");
    expect(text).toContain("opencode-zen");
    expect(text).toMatch(/free/i);
    expect(text).toMatch(/success/i);
    expect(text).toContain("412");
    // The thinking clamp is reported, not silently dropped.
    expect(text).toMatch(/clamp/i);
  });

  // THE null-outcome assertion. A decision with no attempt rows made NO attempt;
  // rendering that as success would invent a result the router never produced.
  it("renders a null outcome as no-attempt-recorded, never as success", async () => {
    const outcome: RouteOutcome = { terminal_status: null, total_latency_ms: null };
    mockAll({
      [ROUTES_URL]: () =>
        jsonResponse(200, { data: [routeEntry({ request_id: "req-null", outcome })] }),
    });
    renderSurface();

    const status = await screen.findByTestId("activity-status-req-null");
    expect(status.textContent ?? "").toMatch(/no attempt recorded/i);
    expect(status.textContent ?? "").not.toMatch(/success/i);
  });

  it("renders a null latency as unknown, and never as 0 ms", async () => {
    mockAll({
      [ROUTES_URL]: () =>
        jsonResponse(200, {
          data: [
            routeEntry({
              request_id: "req-nolat",
              outcome: { terminal_status: "failure", total_latency_ms: null },
            }),
          ],
        }),
    });
    renderSurface();

    const latency = await screen.findByTestId("activity-latency-req-nolat");
    expect(latency.textContent ?? "").toMatch(/unknown/i);
    // The exact failure this guards: "0 ms" reads as an instant response.
    expect(latency.textContent ?? "").not.toMatch(/\b0\b/);
    expect(latency.textContent ?? "").not.toMatch(/0\s*ms/i);
  });

  it("renders a decision that chose nothing without inventing a provider", async () => {
    mockAll({
      [ROUTES_URL]: () =>
        jsonResponse(200, {
          data: [
            routeEntry({
              request_id: "req-noroute",
              chosen: { provider_id: null, provider_model_id: null, funding: null },
              outcome: { terminal_status: null, total_latency_ms: null },
            }),
          ],
        }),
    });
    renderSurface();

    const row = await screen.findByTestId("activity-req-noroute");
    expect(row.textContent ?? "").toMatch(/no route chosen/i);
  });

  it("links each row to the per-request diagnostics view by request id", async () => {
    mockAll();
    renderSurface();

    const link = await screen.findByTestId("activity-link-req-1");
    // P6-UI-008 owns the detail view; this only points at it by request id.
    expect(link.getAttribute("href") ?? "").toContain("req-1");
  });

  it("renders an empty activity state when no request has been routed yet", async () => {
    mockAll({ [ROUTES_URL]: () => jsonResponse(200, { data: [] }) });
    renderSurface();

    const card = await screen.findByTestId("overview-card-activity");
    await waitFor(() => expect(card.textContent ?? "").toMatch(/no requests|nothing has been routed/i));
  });
});

describe("OverviewSurface — per-card isolation", () => {
  // One failing fetch must degrade ONLY its own card. A page that threw would
  // take the whole operator home down over one read model.
  it("keeps every other card rendered when the activity read fails", async () => {
    mockAll({ [ROUTES_URL]: () => failure() });
    renderSurface();

    const activity = await screen.findByTestId("overview-card-activity");
    await waitFor(() => expect(activity.textContent ?? "").toMatch(/could not load/i));

    // The rest of the page is intact.
    expect((await screen.findByTestId("overview-card-fleet")).textContent ?? "").toMatch(/\d/);
    screen.getByTestId("overview-card-health");
    screen.getByTestId("overview-card-quota");
    screen.getByTestId("overview-card-tiers");
  });

  it("keeps every other card rendered when the fleet read fails", async () => {
    mockAll({ [PROVIDERS_URL]: () => failure(), [ACCOUNTS_URL]: () => failure() });
    renderSurface();

    const fleet = await screen.findByTestId("overview-card-fleet");
    await waitFor(() => expect(fleet.textContent ?? "").toMatch(/could not load/i));

    const activity = await screen.findByTestId("overview-card-activity");
    await waitFor(() => expect(activity.textContent ?? "").toMatch(/pro/i));
    screen.getByTestId("overview-card-tiers");
  });

  it("keeps every other card rendered when the tier-policy read fails", async () => {
    mockAll({ [POLICY_URL]: () => failure() });
    renderSurface();

    const tiers = await screen.findByTestId("overview-card-tiers");
    await waitFor(() => expect(tiers.textContent ?? "").toMatch(/could not load/i));
    expect((await screen.findByTestId("overview-card-fleet")).textContent ?? "").toMatch(/\d/);
  });

  it("renders each card's own loading state independently", async () => {
    mockAll({ [ROUTES_URL]: () => pending() });
    renderSurface();

    // The fleet card resolves while activity is still in flight.
    await screen.findByTestId("overview-card-fleet");
    const activity = screen.getByTestId("overview-card-activity");
    expect(activity.querySelector('[role="status"]')).toBeTruthy();
  });
});

describe("OverviewSurface — review banner", () => {
  it("shows the review banner when the backlog is non-empty", async () => {
    mockAll({ [CENSUS_URL]: () => jsonResponse(200, censusBody(3)) });
    renderSurface();

    const banner = await screen.findByTestId("review-queue-banner");
    expect(banner.textContent ?? "").toMatch(/3/);
    expect(within(banner).getByText("capability_not_certified")).toBeTruthy();
  });

  it("does not show the backlog call to action when the backlog is empty", async () => {
    mockAll({ [CENSUS_URL]: () => jsonResponse(200, censusBody(0)) });
    renderSurface();

    await screen.findByTestId("overview-card-fleet");
    expect(screen.queryByRole("button", { name: /review the backlog/i })).toBeNull();
  });
});

describe("OverviewSurface — secrets and accessibility", () => {
  it("renders no account external id and no credential material", async () => {
    mockAll();
    const { container } = renderSurface();
    await screen.findByTestId("activity-req-1");

    // external_id is on the accounts projection but must never reach the DOM
    // here (07 §5a / 01 §6c).
    expect(container.innerHTML).not.toContain("ext-secret-1");
    expect(container.innerHTML).not.toMatch(/vk_live_|sk-|Bearer /);
  });

  it("has no axe violations with every card populated", async () => {
    mockAll({ [CENSUS_URL]: () => jsonResponse(200, censusBody(2)) });
    const { container } = renderSurface();
    await screen.findByTestId("activity-req-1");
    await assertNoAxeViolations(container);
  });

  it("has no axe violations when every card is empty", async () => {
    mockAll({
      [PROVIDERS_URL]: () => jsonResponse(200, { data: { providers: [] } }),
      [ACCOUNTS_URL]: () => jsonResponse(200, { data: { accounts: [] } }),
      [MODELS_URL]: () => jsonResponse(200, { data: [] }),
      [ROUTES_URL]: () => jsonResponse(200, { data: [] }),
    });
    const { container } = renderSurface();
    await screen.findByTestId("overview-card-activity");
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no requests|nothing has been routed/i));
    await assertNoAxeViolations(container);
  });

  it("has no axe violations when a card has failed", async () => {
    mockAll({ [ROUTES_URL]: () => failure() });
    const { container } = renderSurface();
    await waitFor(() =>
      expect(screen.getByTestId("overview-card-activity").textContent ?? "").toMatch(/could not load/i),
    );
    await assertNoAxeViolations(container);
  });
});
