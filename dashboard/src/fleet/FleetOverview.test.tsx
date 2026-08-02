import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ComponentProps } from "react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import FleetOverview from "./FleetOverview";

const CSRF_TOKEN = "fleet-csrf-token";

const PROVIDER_OCZ = {
  id: "opencode-zen",
  display_name: "OpenCode Zen",
  description: "An API-key provider.",
  auth_mode: "api_key" as const,
  funding: { mode: "owner_policy", locked: false, non_expiring: false, fixed: null },
  capabilities: [],
  configured: true,
  missing_env: [],
};

const PROVIDER_ANTIGRAVITY = {
  id: "antigravity",
  display_name: "Antigravity",
  description: "An OAuth provider.",
  auth_mode: "oauth2" as const,
  funding: { mode: "fixed", locked: true, non_expiring: false, fixed: "paid" },
  capabilities: [],
  configured: false,
  missing_env: ["VENOM_ANTIGRAVITY_CLIENT_SECRET", "VENOM_ANTIGRAVITY_CLIENT_ID"],
};

// A second, CONFIGURED OAuth provider — Antigravity is deliberately
// setup-required (used to prove the missing-env banner), so the OAuth
// connect flow test uses this one instead.
const PROVIDER_CLAUDE_CODE = {
  id: "claude-code",
  display_name: "Claude Code",
  description: "An OAuth provider.",
  auth_mode: "oauth2" as const,
  funding: { mode: "provider_evidence", locked: false, non_expiring: false, fixed: null },
  capabilities: [],
  configured: true,
  missing_env: [],
};

// Agnes ships its official icon in the embedded public provider assets.
const PROVIDER_AGNES = {
  id: "agnes-ai",
  display_name: "Agnes AI",
  description: "An API-key provider with an official logo.",
  auth_mode: "api_key" as const,
  funding: { mode: "evidence_required", locked: false, non_expiring: false, fixed: null },
  capabilities: [],
  configured: true,
  missing_env: [],
};

// The codex slug carries a displayName override + its own CTA in
// providerMeta ("Login with ChatGPT").
const PROVIDER_CODEX = {
  id: "codex",
  display_name: "Codex",
  description: "ChatGPT OAuth provider.",
  auth_mode: "oauth2" as const,
  funding: { mode: "provider_evidence", locked: false, non_expiring: false, fixed: null },
  capabilities: [],
  configured: true,
  missing_env: [],
};

// The custom OpenAI-compatible path template — the one catalog entry with
// no connect flow in this console, proving the "Integration unavailable"
// action state. Visible under the All tab ONLY.
const PROVIDER_CUSTOM = {
  id: "custom",
  display_name: "Custom (OpenAI-compatible)",
  description: "Generic OpenAI-compatible endpoint; configured per account.",
  auth_mode: "custom_openai" as const,
  funding: { mode: "evidence_required", locked: false, non_expiring: false, fixed: null },
  capabilities: [],
  configured: true,
  missing_env: [],
};

const ALL_PROVIDERS = [PROVIDER_OCZ, PROVIDER_ANTIGRAVITY, PROVIDER_CLAUDE_CODE, PROVIDER_AGNES, PROVIDER_CODEX, PROVIDER_CUSTOM];

const KNOWN_QUOTA_WINDOW = {
  source: "provider_evidence",
  unit: "requests",
  window_type: "rolling",
  window_key: "gem",
  state: "available",
  freshness: "fresh",
  used: 120,
  remaining: 880,
  total: 1000,
  limit_value: 1000,
  reserved: 0,
  reset_at: Math.floor(Date.now() / 1000) + 2 * 3600 + 21 * 60,
  observed_at: new Date(Date.now() - 30_000).toISOString(),
};

const UNKNOWN_QUOTA_WINDOW = {
  source: "provider_evidence",
  unit: "credits",
  window_type: "rolling",
  window_key: "opt",
  state: "unknown",
  freshness: "unknown",
  used: null,
  remaining: null,
  total: null,
  limit_value: null,
  reserved: 0,
  reset_at: null,
  observed_at: new Date(Date.now() - 30_000).toISOString(),
};

function account(overrides: Record<string, unknown>) {
  return {
    id: "acct-default",
    provider: "opencode-zen",
    external_id: "ext-default",
    auth_type: "api_key",
    connection_state: "connected",
    health_state: "healthy",
    reauth_in_progress: false,
    identity: { email: undefined, plan: undefined },
    funding: { funding: "free", source: "owner_policy", locked: false, version: "v1" },
    display_status: "healthy",
    eligibility: { eligible: true },
    quota: [],
    last_health_check_at: new Date(Date.now() - 60_000).toISOString(),
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  };
}

const ACCOUNT_HEALTHY = account({
  id: "acct-1",
  external_id: "key_9c41e8b0f2",
  identity: { email: "ipfox111@example.test", plan: "Pro" },
  quota: [KNOWN_QUOTA_WINDOW, UNKNOWN_QUOTA_WINDOW],
});
const ACCOUNT_DEGRADED = account({
  id: "acct-2",
  external_id: "key_11ab009d",
  health_state: "degraded",
  display_status: "degraded",
  funding: { funding: "paid", source: "provider_evidence", locked: false, version: "v2" },
});
const ACCOUNT_STOPPED = account({
  id: "acct-3",
  external_id: "key_77aa02b9",
  connection_state: "stopped",
  display_status: "stopped",
  funding: { funding: "unknown", source: "owner_policy", locked: false, version: "v3" },
});

function offeringCapability(overrides: Record<string, unknown> = {}) {
  return {
    operation: "chat",
    effective: true,
    state: "discovered",
    truth: "unknown",
    routable: false,
    ...overrides,
  };
}

function offering(overrides: Record<string, unknown>) {
  return {
    account_id: "acct-1",
    provider_id: "opencode-zen",
    provider_model_id: "zen/model-a",
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
  };
}

// acct-1 sees two models (one WORKING with a probeable op, one untested,
// unprobeable); acct-2 sees model-a again (FAILED). Distinct across the
// provider = 2; provider-level working = 1.
const OFFERINGS = [
  offering({
    provider_model_id: "zen/model-a",
    display_name: "Model A",
    capabilities: [
      offeringCapability({ truth: "supported", state: "certified", routable: true, offering_operation_id: "op-a" }),
      offeringCapability({ operation: "tools" }),
    ],
  }),
  offering({
    provider_model_id: "zen/model-b",
    display_name: "Model B",
    capabilities: [offeringCapability()],
  }),
  offering({
    account_id: "acct-2",
    provider_model_id: "zen/model-a",
    display_name: "Model A",
    capabilities: [offeringCapability({ truth: "unsupported", offering_operation_id: "op-b" })],
  }),
];

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function baseHandlers(overrides: Record<string, () => Response> = {}) {
  return createFetchMock({
    "GET /api/control/v1/providers": () => jsonResponse(200, { data: { providers: ALL_PROVIDERS } }),
    "GET /api/control/v1/accounts?limit=200": () =>
      jsonResponse(200, { data: { accounts: [ACCOUNT_HEALTHY, ACCOUNT_DEGRADED, ACCOUNT_STOPPED] } }),
    "GET /api/control/v1/offerings?limit=200": () => jsonResponse(200, { data: OFFERINGS }),
    ...overrides,
  });
}

async function renderFleet(
  overrides: Record<string, () => Response> = {},
  props: Partial<ComponentProps<typeof FleetOverview>> = {},
) {
  const fetchMock = baseHandlers(overrides);
  vi.stubGlobal("fetch", fetchMock);
  render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} {...props} />);
  await screen.findByText("OpenCode Zen");
  return fetchMock;
}

function expandProvider(name: string) {
  fireEvent.click(screen.getByRole("button", { name: new RegExp(`expand ${name} accounts`, "i") }));
}

/** Scopes queries to one account row, identified by its (unique, non-secret)
 * external_id. */
function accountRow(externalId: string): HTMLElement {
  return screen.getByText(externalId).closest(".vnd-account") as HTMLElement;
}

/** Scopes queries to one stat card by its label. */
function statCard(label: string): HTMLElement {
  return screen.getByText(label, { selector: ".vn-stat-label" }).closest(".vn-stat") as HTMLElement;
}

describe("FleetOverview — All Integrations catalog (default view)", () => {
  it("renders one card per catalog entry with per-slug marketing meta and badges", async () => {
    await renderFleet();

    // The codex slug renders its marketing display name, CTA, and badge.
    screen.getByText("OpenAI Codex / ChatGPT");
    screen.getByRole("button", { name: /login with chatgpt/i });
    screen.getByText("CHATGPT OAUTH");

    // Connected providers show the pill + linked count; site domain lines
    // come from the meta map.
    const oczCard = screen.getByText("OpenCode Zen").closest(".vn-provider-card") as HTMLElement;
    within(oczCard).getByText("CONNECTED");
    within(oczCard).getByText("3 accounts linked");
    within(oczCard).getByText("opencode.ai");
    // Marketing description replaces the server's terse one for known slugs.
    within(oczCard).getByText(/OpenAI-compatible free gateway from OpenCode/);

    // Unknown slug (custom) falls back to the server's own copy.
    const customCard = screen.getByText("Custom (OpenAI-compatible)").closest(".vn-provider-card") as HTMLElement;
    within(customCard).getByText("Generic OpenAI-compatible endpoint; configured per account.");
    within(customCard).getByText("OPENAI COMPATIBLE");
  });

  it("renders official provider logos, including Agnes", async () => {
    await renderFleet();

    const logo = screen.getByRole("img", { name: "OpenCode Zen" });
    expect(logo.tagName).toBe("IMG");
    expect(logo.getAttribute("src")).toBe("/providers/opencode-zen.png");
    expect(screen.getByRole("img", { name: "Claude Code" }).getAttribute("src")).toBe("/providers/claude-code.png");

    const agnes = screen.getByRole("img", { name: "Agnes AI" });
    expect(agnes.tagName).toBe("IMG");
    expect(agnes.getAttribute("src")).toBe("/providers/agnes-ai.png");
  });

  it("shows the setup-required note naming only the missing env var NAMES, never values", async () => {
    await renderFleet();

    const note = screen.getByRole("note");
    expect(note.textContent).toMatch(/^Setup required\. Provide: /);
    screen.getByText("VENOM_ANTIGRAVITY_CLIENT_SECRET");
    screen.getByText("VENOM_ANTIGRAVITY_CLIENT_ID");
  });

  it("renders the per-state actions: Connected (disabled), Connect Integration, Setup required, Integration unavailable", async () => {
    await renderFleet();

    const oczCard = screen.getByText("OpenCode Zen").closest(".vn-provider-card") as HTMLElement;
    const connectedButton = within(oczCard).getByRole("button", { name: /^connected$/i });
    expect((connectedButton as HTMLButtonElement).disabled).toBe(true);

    const agnesCard = screen.getByText("Agnes AI").closest(".vn-provider-card") as HTMLElement;
    const connectButton = within(agnesCard).getByRole("button", { name: /connect integration/i });
    expect((connectButton as HTMLButtonElement).disabled).toBe(false);

    const antigravityCard = screen.getByText("Antigravity").closest(".vn-provider-card") as HTMLElement;
    const setupButton = within(antigravityCard).getByRole("button", { name: /^setup required$/i });
    expect((setupButton as HTMLButtonElement).disabled).toBe(true);

    const customCard = screen.getByText("Custom (OpenAI-compatible)").closest(".vn-provider-card") as HTMLElement;
    const unavailableButton = within(customCard).getByRole("button", { name: /integration unavailable/i });
    expect((unavailableButton as HTMLButtonElement).disabled).toBe(true);

    // No account disclosure in the catalog view — accounts are managed
    // from Active Providers.
    expect(screen.queryByRole("button", { name: /expand .* accounts/i })).toBeNull();
  });

  it("filters the integration grid live by search, with a clearable no-match state", async () => {
    await renderFleet();

    const searchbox = screen.getByRole("searchbox", { name: /search integrations/i });
    const searchControl = searchbox.closest(".vn-search") as HTMLElement;
    fireEvent.change(searchbox, { target: { value: "claude" } });
    screen.getByText("Claude Code");
    expect(screen.queryByText("OpenCode Zen")).toBeNull();
    expect(screen.queryByText("Agnes AI")).toBeNull();

    fireEvent.click(within(searchControl).getByRole("button", { name: /clear search/i }));
    screen.getByText("OpenCode Zen");
    screen.getByText("Claude Code");

    fireEvent.change(searchbox, { target: { value: "no-such-integration" } });
    screen.getByText(/no integrations found/i);
    fireEvent.click(within(searchControl).getByRole("button", { name: /clear search/i }));
    screen.getByText("OpenCode Zen");
  });

  it("scopes the grid with exactly the All / OAuth / API Key tabs; custom stays under All only", async () => {
    await renderFleet();

    const tabs = screen.getByRole("group", { name: /filter providers/i });
    expect(within(tabs).getAllByRole("button").map((button) => button.textContent)).toEqual([
      "All",
      "OAuth",
      "API Key",
    ]);

    // All is the selected default — the tab an owner can always return to.
    expect(within(tabs).getByRole("button", { name: "All" }).getAttribute("aria-pressed")).toBe("true");
    screen.getByText("OpenCode Zen");
    screen.getByText("Antigravity");
    screen.getByText("Custom (OpenAI-compatible)");

    fireEvent.click(within(tabs).getByRole("button", { name: "OAuth" }));
    screen.getByText("Antigravity");
    screen.getByText("Claude Code");
    screen.getByText("OpenAI Codex / ChatGPT");
    expect(screen.queryByText("OpenCode Zen")).toBeNull();
    expect(screen.queryByText("Custom (OpenAI-compatible)")).toBeNull();

    fireEvent.click(within(tabs).getByRole("button", { name: "API Key" }));
    screen.getByText("OpenCode Zen");
    screen.getByText("Agnes AI");
    expect(screen.queryByText("Antigravity")).toBeNull();
    expect(screen.queryByText("Custom (OpenAI-compatible)")).toBeNull();

    fireEvent.click(within(tabs).getByRole("button", { name: "All" }));
    screen.getByText("Custom (OpenAI-compatible)");
  });
});

describe("FleetOverview — contextual stat cards", () => {
  it("recomputes all four cards from the current view + auth filter scope", async () => {
    await renderFleet();

    // All Integrations / All: catalog count, every account, distinct models.
    expect(statCard("Providers").textContent).toContain("6");
    expect(statCard("Providers").textContent).toContain("all integrations");
    expect(statCard("Accounts").textContent).toContain("3");
    expect(statCard("Accounts").textContent).toContain("across 1 provider");
    expect(statCard("Healthy").textContent).toContain("1/3");
    expect(statCard("Models").textContent).toContain("2");
    expect(statCard("Models").textContent).toContain("1 working · unique");

    // OAuth filter: no OAuth provider has accounts.
    const tabs = screen.getByRole("group", { name: /filter providers/i });
    fireEvent.click(within(tabs).getByRole("button", { name: "OAuth" }));
    expect(statCard("Providers").textContent).toContain("3");
    expect(statCard("Accounts").textContent).toContain("0");
    expect(statCard("Healthy").textContent).toContain("0/0");

    fireEvent.click(within(tabs).getByRole("button", { name: "API Key" }));
    expect(statCard("Providers").textContent).toContain("2");
    expect(statCard("Accounts").textContent).toContain("3");
  });

  it("switches PROVIDERS to connected-integrations in the active view and shows the all-healthy badge only when earned", async () => {
    vi.stubGlobal("fetch", baseHandlers({
      "GET /api/control/v1/accounts?limit=200": () =>
        jsonResponse(200, { data: { accounts: [ACCOUNT_HEALTHY] } }),
    }));
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);
    await screen.findByText("OpenCode Zen");

    expect(statCard("Providers").textContent).toContain("1");
    expect(statCard("Providers").textContent).toContain("connected integrations");
    expect(statCard("Healthy").textContent).toContain("1/1");
    within(statCard("Healthy")).getByText("all healthy");
  });

  it("renders MODELS as an honest dash — never 0 — while offerings are unavailable, and surfaces the failure once", async () => {
    await renderFleet({
      "GET /api/control/v1/offerings?limit=200": () =>
        jsonResponse(500, { error: { code: "internal", message: "boom", request_id: "r9", retryable: true } }),
    });

    const models = statCard("Models");
    expect(within(models).getByText("—")).toBeTruthy();
    expect(models.textContent).not.toContain("0");
    screen.getByText(/could not load model offerings/i);

    // The rest of the page still works.
    screen.getByText("OpenCode Zen");
    screen.getByText("Agnes AI");
  });
});

describe("FleetOverview — Active Providers rows", () => {
  it("renders one row per connected provider with health dot, badge, counts, and the three row actions", async () => {
    await renderFleet({}, { view: "active" });

    // Only the connected provider renders as a row.
    expect(screen.queryByText("Agnes AI")).toBeNull();
    expect(screen.queryByText("Antigravity")).toBeNull();

    screen.getByText("API KEY");
    screen.getByTitle("1/3 accounts healthy");
    screen.getByText(/unique models/);
    screen.getByRole("button", { name: /sync all accounts/i });
    screen.getByRole("button", { name: /refresh models for every account/i });
    screen.getByRole("button", { name: /^add account$/i });
    screen.getByRole("link", { name: /open opencode\.ai in a new tab/i });
  });

  it("wires zap to POST /providers/{id}/sync and refetches", async () => {
    const fetchMock = await renderFleet(
      {
        "POST /api/control/v1/providers/opencode-zen/sync": () =>
          jsonResponse(200, { data: { provider: "opencode-zen", synced: 3, skipped: 0 } }),
      },
      { view: "active" },
    );

    fireEvent.click(screen.getByRole("button", { name: /sync all accounts/i }));

    await waitFor(() => {
      const syncCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/providers/opencode-zen/sync"));
      expect(syncCall).toBeTruthy();
    });
    // The refetch happened: providers were listed at least twice.
    await waitFor(() => {
      const providerReads = fetchMock.mock.calls.filter(([input]) => String(input).endsWith("/providers"));
      expect(providerReads.length).toBeGreaterThanOrEqual(2);
    });
  });

  it("wires refresh-cw to a discovery job per account, polled to terminal", async () => {
    const discovered: string[] = [];
    const fetchMock = await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/discover": () => {
          discovered.push("acct-1");
          return jsonResponse(202, { data: { job_id: "job-1" } });
        },
        "POST /api/control/v1/accounts/acct-2/discover": () => {
          discovered.push("acct-2");
          return jsonResponse(202, { data: { job_id: "job-1" } });
        },
        "POST /api/control/v1/accounts/acct-3/discover": () => {
          discovered.push("acct-3");
          return jsonResponse(202, { data: { job_id: "job-1" } });
        },
        "GET /api/control/v1/jobs/job-1": () =>
          jsonResponse(200, { data: { job_id: "job-1", kind: "discovery", status: "completed" } }),
      },
      { view: "active" },
    );

    fireEvent.click(screen.getByRole("button", { name: /refresh models for every account/i }));

    await waitFor(() => expect(discovered).toEqual(["acct-1", "acct-2", "acct-3"]));
    await waitFor(() => {
      const jobReads = fetchMock.mock.calls.filter(([input]) => String(input).endsWith("/jobs/job-1"));
      expect(jobReads.length).toBeGreaterThanOrEqual(3);
    });
  });

  it("opens the connect dialog from + Add account (how a 2nd/3rd account is added)", async () => {
    await renderFleet({}, { view: "active" });

    fireEvent.click(screen.getByRole("button", { name: /^add account$/i }));
    await screen.findByRole("dialog", { name: /connect opencode zen/i });
  });

  it("expands into numbered account rows with identity, plan badge, and compact quota meters", async () => {
    await renderFleet({}, { view: "active" });
    expandProvider("OpenCode Zen");

    await screen.findByTitle("display_status: healthy");
    screen.getByTitle("display_status: degraded");
    screen.getByTitle("display_status: stopped");

    const row = accountRow("key_9c41e8b0f2");
    within(row).getByText("ipfox111@example.test");
    within(row).getByText("PRO");

    // The known window renders its real percentage and reset countdown…
    within(row).getByText("GEM");
    within(row).getByText("12%");
    expect(within(row).getByText(/Resets in/).textContent).toMatch(/Resets in 2 hr \d+ min/);
    // …the unknown window renders the state word over a hatched bar,
    // never a fabricated 0%.
    within(row).getByText("OPT");
    within(row).getByText("unknown");
    expect(within(row).queryByText("0%")).toBeNull();

    // Meta line: real relative instants (never "—" when both are known).
    expect(within(row).getByText(/Quota: .* · Checked: .*/).textContent).toMatch(
      /Quota: .+ ago · Checked: .+ ago/,
    );
  });

  it("wires the account sync action to health + quota (job polled), and fetch-models to discovery", async () => {
    const fetchMock = await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/health": () => jsonResponse(200, { data: ACCOUNT_HEALTHY }),
        "POST /api/control/v1/accounts/acct-1/quota": () =>
          jsonResponse(202, { data: { job_id: "job-q", status_url: "/api/control/v1/jobs/job-q" } }),
        "POST /api/control/v1/accounts/acct-1/discover": () => jsonResponse(202, { data: { job_id: "job-d" } }),
        "GET /api/control/v1/jobs/job-q": () =>
          jsonResponse(200, { data: { job_id: "job-q", kind: "quota_sync", status: "completed" } }),
        "GET /api/control/v1/jobs/job-d": () =>
          jsonResponse(200, { data: { job_id: "job-d", kind: "discovery", status: "completed" } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /sync: health · plan · usage/i }));
    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/health"))).toBe(true);
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/quota"))).toBe(true);
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/jobs/job-q"))).toBe(true);
    });

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /fetch models from provider/i }));
    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/discover"))).toBe(true);
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/jobs/job-d"))).toBe(true);
    });
  });

  it("maps the power toggle by connection_state: connected -> stop, stopped -> resume", async () => {
    const fetchMock = await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/stop": () =>
          jsonResponse(200, { data: account({ id: "acct-1", connection_state: "stopped", display_status: "stopped" }) }),
        "POST /api/control/v1/accounts/acct-3/resume": () => jsonResponse(200, { data: account({ id: "acct-3" }) }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /^disable account$/i }));
    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/stop"))).toBe(true);
    });

    fireEvent.click(within(accountRow("key_77aa02b9")).getByRole("button", { name: /^enable account$/i }));
    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-3/resume"))).toBe(true);
    });
  });

  it("shows the per-account model count and opens the Model Test Report; a zero-model account's chip is disabled", async () => {
    await renderFleet({}, { view: "active" });
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    const row1Chip = within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /open model test report/i });
    expect(row1Chip.textContent).toContain("2");

    const row3Chip = within(accountRow("key_77aa02b9")).getByRole("button", { name: /open model test report/i });
    expect((row3Chip as HTMLButtonElement).disabled).toBe(true);
    expect(row3Chip.getAttribute("title")).toBe("No models discovered yet");

    fireEvent.click(row1Chip);
    await screen.findByRole("dialog", { name: /model test report: opencode zen/i });
  });

  it("explains an empty Active view without offering a no-op search reset", async () => {
    vi.stubGlobal("fetch", baseHandlers({
      "GET /api/control/v1/accounts?limit=200": () => jsonResponse(200, { data: { accounts: [] } }),
    }));
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);

    await screen.findByText("No active providers");
    expect(screen.queryByRole("button", { name: /clear search/i })).toBeNull();
  });

  it("reports the breadcrumb-chip counts (active providers / all integrations) once data loads", async () => {
    vi.stubGlobal("fetch", baseHandlers());
    const onCounts = vi.fn();
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onCounts={onCounts} />);

    await screen.findByText("OpenCode Zen");
    await waitFor(() => expect(onCounts).toHaveBeenCalledWith({ active: 1, total: 6 }));
  });

  it("has zero axe violations in both views, with an expanded provider", async () => {
    const fetchMock = baseHandlers();
    vi.stubGlobal("fetch", fetchMock);
    const { container, rerender } = render(
      <FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />,
    );

    await screen.findByText("OpenCode Zen");
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");
    await assertNoAxeViolations(container);

    rerender(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="all" />);
    await screen.findByText("Agnes AI");
    await assertNoAxeViolations(container);
  });
});

describe("FleetOverview — API-key connect (image 9)", () => {
  it("renders the documented dialog: textarea, billing cards, Save & encrypt gating, and the encryption caption", async () => {
    await renderFleet();

    const agnesCard = screen.getByText("Agnes AI").closest(".vn-provider-card") as HTMLElement;
    fireEvent.click(within(agnesCard).getByRole("button", { name: /connect integration/i }));
    const dialog = await screen.findByRole("dialog", { name: /^connect agnes ai$/i });

    within(dialog).getByText(/encrypted with AES-256-GCM before storage and never shown again/i);
    within(dialog).getByText(/stored encrypted\. a health check runs immediately after connect/i);

    const textarea = dialog.querySelector("textarea") as HTMLTextAreaElement;
    expect(textarea).toBeTruthy();
    expect(textarea.getAttribute("placeholder")).toBe("sk-…");
    expect(textarea.getAttribute("autocomplete")).toBe("off");
    expect(textarea.getAttribute("spellcheck")).toBe("false");

    // The four billing cards, inherit selected by default.
    const radios = within(dialog).getAllByRole("radio");
    expect(radios).toHaveLength(4);
    expect((within(dialog).getByRole("radio", { name: /inherit from provider/i }) as HTMLInputElement).checked).toBe(true);

    // No label field — the connect body has no such field server-side.
    expect(within(dialog).queryByLabelText(/label/i)).toBeNull();

    // Save & encrypt is gated on a non-empty key.
    const save = within(dialog).getByRole("button", { name: /save & encrypt/i });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(textarea, { target: { value: "sk-x" } });
    expect((save as HTMLButtonElement).disabled).toBe(false);
  });

  it("maps the billing selection to the funding body — inherit OMITS the field — and never leaks the key", async () => {
    const consoleSpies = (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m).mockImplementation(() => {}));

    const fetchMock = await renderFleet({
      "POST /api/control/v1/providers/agnes-ai/accounts": () =>
        jsonResponse(201, {
          data: { id: "acct-new", provider: "agnes-ai", external_id: "ext-new", connection_state: "connected", health_state: "healthy", funding: "free", display_status: "healthy" },
        }),
    });

    const agnesCard = screen.getByText("Agnes AI").closest(".vn-provider-card") as HTMLElement;
    fireEvent.click(within(agnesCard).getByRole("button", { name: /connect integration/i }));
    const dialog = await screen.findByRole("dialog", { name: /^connect agnes ai$/i });

    const secretKey = "sk-test-CANARY-KEY-abc123";
    const textarea = dialog.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: secretKey } });
    fireEvent.click(within(dialog).getByRole("button", { name: /save & encrypt/i }));

    // Cleared synchronously at submit time.
    expect(textarea.value).toBe("");

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    const connectCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/providers/agnes-ai/accounts"));
    expect(connectCall).toBeTruthy();
    const [, init] = connectCall as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(init.headers["Idempotency-Key"]).toBeTruthy();
    expect(init.headers["X-CSRF-Token"]).toBe(CSRF_TOKEN);
    const body = JSON.parse(init.body as string);
    expect(body.api_key).toBe(secretKey);
    // Inherit from provider (the default) = funding omitted, not "".
    expect("funding" in body).toBe(false);

    expect(document.body.innerHTML).not.toContain(secretKey);
    for (const spy of consoleSpies) {
      for (const call of spy.mock.calls) {
        for (const arg of call) {
          if (typeof arg === "string") expect(arg).not.toContain(secretKey);
        }
      }
    }
  });

  it("sends the chosen billing classification when one is picked", async () => {
    const fetchMock = await renderFleet({
      "POST /api/control/v1/providers/agnes-ai/accounts": () =>
        jsonResponse(201, {
          data: { id: "acct-new", provider: "agnes-ai", external_id: "ext-new", connection_state: "connected", health_state: "healthy", funding: "paid", display_status: "healthy" },
        }),
    });

    const agnesCard = screen.getByText("Agnes AI").closest(".vn-provider-card") as HTMLElement;
    fireEvent.click(within(agnesCard).getByRole("button", { name: /connect integration/i }));
    const dialog = await screen.findByRole("dialog", { name: /^connect agnes ai$/i });

    fireEvent.change(dialog.querySelector("textarea") as HTMLTextAreaElement, { target: { value: "sk-key" } });
    fireEvent.click(within(dialog).getByRole("radio", { name: /paid/i }));
    fireEvent.click(within(dialog).getByRole("button", { name: /save & encrypt/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const connectCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/providers/agnes-ai/accounts"));
    const [, init] = connectCall as [unknown, RequestInit];
    expect(JSON.parse(init.body as string).funding).toBe("paid");
  });
});

describe("FleetOverview — OAuth connect (image 10)", () => {
  it(
    "begins OAuth, opens the authorize URL as a popup, offers re-open, and polls to completed",
    async () => {
      const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);

      const fetchMock = await renderFleet({
        "POST /api/control/v1/providers/claude-code/oauth/begin": () =>
          jsonResponse(202, {
            data: {
              transaction_id: "tx-1",
              authorize_url: "https://provider.example/authorize",
              expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
            },
          }),
        "GET /api/control/v1/oauth/tx-1/status": () => jsonResponse(200, { data: { status: "completed", account_id: "acct-new" } }),
      });

      const claudeCodeCard = screen.getByText("Claude Code").closest(".vn-provider-card") as HTMLElement;
      fireEvent.click(within(claudeCodeCard).getByRole("button", { name: /connect integration/i }));

      const dialog = await screen.findByRole("dialog", { name: /^connect claude code$/i });
      within(dialog).getByText(/sign in via the popup window/i);
      fireEvent.click(within(dialog).getByRole("button", { name: /continue with claude code/i }));

      await screen.findByText(/waiting for authorization in popup…/i);
      expect(openSpy).toHaveBeenCalledWith("https://provider.example/authorize", "venom-oauth", "popup,width=560,height=720");

      // Re-open re-opens the SAME authorize URL (also the popup-blocked
      // recovery path — window.open returned null above).
      fireEvent.click(screen.getByRole("button", { name: /re-open sign-in window/i }));
      expect(openSpy).toHaveBeenCalledTimes(2);
      expect(openSpy.mock.calls[1]).toEqual(["https://provider.example/authorize", "venom-oauth", "popup,width=560,height=720"]);

      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), { timeout: 5000 });

      const beginCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/claude-code/oauth/begin"));
      expect(beginCall).toBeTruthy();
    },
    8000,
  );
});

describe("FleetOverview — credential reveal", () => {
  it("reveals the secret, then hide removes it from the DOM, and no secret ever reaches console", async () => {
    const consoleSpies = (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m).mockImplementation(() => {}));
    const canary = "CANARY-REVEALED-SECRET-9f2e";

    await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/reveal": () => new Response(canary, { status: 200 }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /reveal credential/i }));

    await screen.findByText(canary);
    expect(document.body.innerHTML).toContain(canary);

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /^hide credential$/i }));

    await waitFor(() => expect(document.body.innerHTML).not.toContain(canary));

    for (const spy of consoleSpies) {
      for (const call of spy.mock.calls) {
        for (const arg of call) {
          if (typeof arg === "string") expect(arg).not.toContain(canary);
        }
      }
    }
  });

  it("opens the reverification prompt when reveal comes back reverification_required, then retries reveal on success", async () => {
    let revealAttempts = 0;
    const canary = "CANARY-AFTER-REVERIFY-77bb";

    await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/reveal": () => {
          revealAttempts += 1;
          if (revealAttempts === 1) {
            return jsonResponse(401, { error: { code: "reverification_required", message: "re-verification required", request_id: "r1", retryable: false } });
          }
          return new Response(canary, { status: 200 });
        },
        "POST /api/control/v1/auth/reverify": () => jsonResponse(200, { data: { reverify_fresh_until: "2026-07-24T00:05:00Z" } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /reveal credential/i }));

    const passwordInput = await screen.findByLabelText(/owner password/i);
    fireEvent.change(passwordInput, { target: { value: "the-owner-password" } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    await screen.findByText(canary);
    expect(revealAttempts).toBe(2);
  });

  it("surfaces a locked-out state in the reverify prompt and never reveals when reverify is rate-limited", async () => {
    let revealAttempts = 0;
    const password = "reverify-locked-out-password";
    const canary = "CANARY-NEVER-REVEALED-WHILE-LOCKED";

    await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/reveal": () => {
          revealAttempts += 1;
          if (revealAttempts === 1) {
            return jsonResponse(401, { error: { code: "reverification_required", message: "re-verification required", request_id: "r1", retryable: false } });
          }
          return new Response(canary, { status: 200 });
        },
        "POST /api/control/v1/auth/reverify": () =>
          jsonResponse(429, { error: { code: "locked_out", message: "too many failed attempts, try again later", request_id: "r2", retryable: true, retry_after: 30 } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /reveal credential/i }));
    const passwordInput = await screen.findByLabelText(/owner password/i);
    fireEvent.change(passwordInput, { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    await screen.findByText(/too many failed attempts/i);
    expect(revealAttempts).toBe(1);
    expect(screen.queryByText(canary)).toBeNull();
    expect(document.body.innerHTML).not.toContain(password);
    expect(document.body.innerHTML).not.toContain(canary);
  });
});

describe("FleetOverview — sync tolerates quota_unsupported", () => {
  it("treats a 409 quota_unsupported as benign: muted caption, refetch, NO error banner", async () => {
    const fetchMock = await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/health": () => jsonResponse(200, { data: ACCOUNT_HEALTHY }),
        "POST /api/control/v1/accounts/acct-1/quota": () =>
          jsonResponse(409, { error: { code: "quota_unsupported", message: "this provider has no quota capability", request_id: "r5", retryable: false } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /sync: health · plan · usage/i }));

    await screen.findByText(/quota sync skipped — this provider has no quota capability/i);
    // The health refresh ran and the page refetched — the sync SUCCEEDED.
    expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/health"))).toBe(true);
    await waitFor(() => {
      const providerReads = fetchMock.mock.calls.filter(([input]) => String(input).endsWith("/providers"));
      expect(providerReads.length).toBeGreaterThanOrEqual(2);
    });
    // No error banner anywhere: the typed code never renders as a failure.
    expect(screen.queryByText(/quota_unsupported/)).toBeNull();
  });

  it("still surfaces every OTHER quota failure verbatim", async () => {
    await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/health": () => jsonResponse(200, { data: ACCOUNT_HEALTHY }),
        "POST /api/control/v1/accounts/acct-1/quota": () =>
          jsonResponse(409, { error: { code: "credential_unavailable", message: "account has no active credential", request_id: "r6", retryable: false } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /sync: health · plan · usage/i }));

    await screen.findByText(/credential_unavailable/);
    expect(screen.queryByText(/quota sync skipped/i)).toBeNull();
  });
});

describe("FleetOverview — account row polish (fingerprint identities, deduped badges, no dead dashes)", () => {
  const HEX64 = "0123456789abcdef".repeat(4);
  const ACCOUNT_ZEN = account({
    id: "acct-z",
    external_id: HEX64,
    identity: { email: undefined, plan: "Free" },
    funding: { funding: "free", source: "owner_policy", locked: false, version: "v9" },
    quota: [],
  });

  async function renderZenRow() {
    await renderFleet(
      {
        "GET /api/control/v1/accounts?limit=200": () => jsonResponse(200, { data: { accounts: [ACCOUNT_ZEN] } }),
        "GET /api/control/v1/offerings?limit=200": () => jsonResponse(200, { data: [] }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    const headline = await screen.findByTitle(HEX64);
    return headline.closest(".vnd-account") as HTMLElement;
  }

  it("truncates a fingerprint external id to a short mono headline with the full value in the title", async () => {
    const row = await renderZenRow();

    const headline = within(row).getByTitle(HEX64);
    expect(headline.textContent).toBe(`${HEX64.slice(0, 12)}…`);
    // The raw 64-char hex never renders as TEXT anywhere.
    expect(document.body.textContent).not.toContain(HEX64);
  });

  it("suppresses the plan badge when it merely repeats the funding classification", async () => {
    const row = await renderZenRow();

    // One badge, not two: the synthetic "Free" plan echoes funding "free",
    // so only the FundingBadge (the real classification) renders.
    expect(within(row).queryByText("FREE")).toBeNull();
    within(row).getByText("Free");
  });

  it("renders no lone dash placeholders for empty quota windows or certification data", async () => {
    const row = await renderZenRow();

    expect(within(row).queryAllByText("—")).toHaveLength(0);
    expect(within(row).queryByTitle(/no quota windows tracked/i)).toBeNull();
    // The meta line still reports the absence honestly, once.
    expect(within(row).getByText(/Quota: — · Checked: .+/)).toBeTruthy();
  });

  it("gives Fetch models and the model-report chip DISTINCT icons", async () => {
    const row = await renderZenRow();

    const fetchButton = within(row).getByRole("button", { name: /fetch models from provider/i });
    const reportChip = within(row).getByRole("button", { name: /open model test report/i });
    expect(fetchButton.querySelector(".vn-icon--download")).toBeTruthy();
    expect(reportChip.querySelector(".vn-icon--box")).toBeTruthy();
    expect(fetchButton.querySelector(".vn-icon--box")).toBeNull();
  });
});

describe("FleetOverview — funding override", () => {
  it("sends expected_version and surfaces funding_locked", async () => {
    const fetchMock = await renderFleet(
      {
        "PUT /api/control/v1/accounts/acct-1/funding": () =>
          jsonResponse(409, { error: { code: "funding_locked", message: "current funding is locked and cannot be overridden", request_id: "r2", retryable: false } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /override funding/i }));
    await screen.findByRole("dialog", { name: /override funding classification/i });

    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await screen.findByText(/funding_locked/i);

    const putCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/accounts/acct-1/funding"));
    expect(putCall).toBeTruthy();
    const [, init] = putCall as [unknown, RequestInit];
    expect(JSON.parse(init.body as string).expected_version).toBe("v1");
  });
});

describe("FleetOverview — disconnect", () => {
  it("requires the destructive confirmation before calling DELETE /accounts/{id}", async () => {
    const fetchMock = await renderFleet(
      {
        "DELETE /api/control/v1/accounts/acct-1": () => jsonResponse(200, { data: account({ id: "acct-1", connection_state: "disconnected", display_status: "disconnected" }) }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /disconnect account/i }));

    const dialog = await screen.findByRole("dialog", { name: /disconnect key_9c41e8b0f2/i });
    const confirmButton = within(dialog).getByRole("button", { name: /disconnect account/i });
    expect((confirmButton as HTMLButtonElement).disabled).toBe(true);

    const typeInput = dialog.querySelector("input") as HTMLInputElement;
    fireEvent.change(typeInput, { target: { value: "disconnect" } });
    expect((confirmButton as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(confirmButton);

    await waitFor(() => {
      const deleteCall = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "DELETE");
      expect(deleteCall).toBeTruthy();
    });
  });
});
