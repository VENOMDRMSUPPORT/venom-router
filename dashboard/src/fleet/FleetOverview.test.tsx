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

const ALL_PROVIDERS = [
  PROVIDER_OCZ,
  PROVIDER_ANTIGRAVITY,
  PROVIDER_CLAUDE_CODE,
  PROVIDER_AGNES,
  PROVIDER_CODEX,
  PROVIDER_CUSTOM,
];

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
    cost: {
      is_free: null,
      conflict: false,
      confidence: 0,
      exact_identity_match: false,
      stale: false,
    },
    classification: "general",
    tiers: {},
    ...overrides,
  };
}

// acct-1 sees two models (one WORKING, one untested); acct-2 sees model-a
// again (FAILED). Distinct across the provider = 2; provider-level
// working = 1. (No probing happens here — chat op ids are NOT probe
// targets; see modelStatus.probeTarget.)
const OFFERINGS = [
  offering({
    provider_model_id: "zen/model-a",
    display_name: "Model A",
    capabilities: [
      offeringCapability({
        truth: "supported",
        state: "certified",
        routable: true,
        offering_operation_id: "op-a",
      }),
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
    "GET /api/control/v1/providers": () =>
      jsonResponse(200, { data: { providers: ALL_PROVIDERS } }),
    "GET /api/control/v1/accounts?limit=200": () =>
      jsonResponse(200, {
        data: { accounts: [ACCOUNT_HEALTHY, ACCOUNT_DEGRADED, ACCOUNT_STOPPED] },
      }),
    "GET /api/control/v1/offerings?limit=200": () => jsonResponse(200, { data: OFFERINGS }),
    ...overrides,
  });
}

/** Renders the page on whatever tab it opens on, touching nothing. Use this
 * when the DEFAULT tab is the thing under test; every other test wants
 * `renderFleet`. */
async function renderFleetUntouched(
  overrides: Record<string, () => Response> = {},
  props: Partial<ComponentProps<typeof FleetOverview>> = {},
) {
  const fetchMock = baseHandlers(overrides);
  vi.stubGlobal("fetch", fetchMock);
  render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} {...props} />);
  await screen.findByRole("button", { name: "OAuth Providers" });
  return fetchMock;
}

/** Renders the page and switches to the API Key Providers tab, because every
 * account in this fixture sits on a key-authenticated provider and the page
 * now opens on OAuth. The switch is a real CLICK rather than a controlled
 * `category` prop on purpose: a controlled category with no parent to update
 * it would make every later tab click in a test silently inert. */
async function renderFleet(
  overrides: Record<string, () => Response> = {},
  props: Partial<ComponentProps<typeof FleetOverview>> = {},
) {
  const fetchMock = await renderFleetUntouched(overrides, props);
  fireEvent.click(screen.getByRole("button", { name: "API Key Providers" }));
  await screen.findByText("OpenCode Zen");
  return fetchMock;
}

function expandProvider(name: string) {
  fireEvent.click(screen.getByRole("button", { name: new RegExp(`expand ${name} accounts`, "i") }));
}

/** Scopes queries to one account row by its stable internal account id. The
 * external_id is deliberately absent from the DOM entirely (it is neither
 * shown nor used as a hook), so rows are targeted by the non-sensitive
 * `data-account-id` the row carries. */
function accountRow(accountId: string): HTMLElement {
  return document.querySelector(`.vnd-account[data-account-id="${accountId}"]`) as HTMLElement;
}

/** Scopes queries to one stat card by its label. */
function statCard(label: string): HTMLElement {
  return screen.getByText(label, { selector: ".vn-stat-label" }).closest(".vn-stat") as HTMLElement;
}

/** Clicks one of the two auth tabs. Catalog assertions have to stand on the
 * tab that actually contains the provider under test now that the tabs
 * partition the catalog. */
function switchToTab(label: "OAuth Providers" | "API Key Providers") {
  fireEvent.click(screen.getByRole("button", { name: label }));
}

/** Every provider name currently rendered in the catalog CARD GRID. Used to
 * assert the auth tabs PARTITION the catalog (union covers it, intersection
 * is empty) rather than hardcoding which slug lands in which tab. */
function gridProviderNames(): string[] {
  return Array.from(document.querySelectorAll(".vn-provider-card-title h3")).map((h) =>
    (h.textContent ?? "").trim(),
  );
}

/** Derived from the fixture, never restated: a provider added to
 * ALL_PROVIDERS must keep the partition assertion honest. */
const CATALOG_PROVIDER_COUNT = ALL_PROVIDERS.length;

describe("FleetOverview — All Integrations catalog (default view)", () => {
  it("renders one card per catalog entry with per-slug marketing meta and badges", async () => {
    // Spans BOTH tabs: codex is OAuth, the other two are key-authenticated.
    await renderFleetUntouched();

    // The codex slug renders its marketing display name, CTA, and badge.
    screen.getByText("OpenAI Codex / ChatGPT");
    screen.getByRole("button", { name: /login with chatgpt/i });
    screen.getByText("CHATGPT OAUTH");

    switchToTab("API Key Providers");

    // Connected providers show the pill + linked count; site domain lines
    // come from the meta map.
    const oczCard = screen.getByText("OpenCode Zen").closest(".vn-provider-card") as HTMLElement;
    within(oczCard).getByText("CONNECTED");
    within(oczCard).getByText("3 accounts linked");
    within(oczCard).getByText("opencode.ai");
    // Marketing description replaces the server's terse one for known slugs.
    within(oczCard).getByText(/OpenAI-compatible free gateway from OpenCode/);

    // Unknown slug (custom) falls back to the server's own copy.
    const customCard = screen
      .getByText("Custom (OpenAI-compatible)")
      .closest(".vn-provider-card") as HTMLElement;
    within(customCard).getByText("Generic OpenAI-compatible endpoint; configured per account.");
    within(customCard).getByText("OPENAI COMPATIBLE");
  });

  it("renders official provider logos, including Agnes", async () => {
    await renderFleetUntouched();

    expect(screen.getByRole("img", { name: "Claude Code" }).getAttribute("src")).toBe(
      "/providers/claude-code.png",
    );

    switchToTab("API Key Providers");

    const logo = screen.getByRole("img", { name: "OpenCode Zen" });
    expect(logo.tagName).toBe("IMG");
    expect(logo.getAttribute("src")).toBe("/providers/opencode-zen.png");

    const agnes = screen.getByRole("img", { name: "Agnes AI" });
    expect(agnes.tagName).toBe("IMG");
    expect(agnes.getAttribute("src")).toBe("/providers/agnes-ai.png");
  });

  it("shows the setup-required note naming only the missing env var NAMES, never values", async () => {
    // Antigravity (the provider with missing env) is an OAuth entry.
    await renderFleetUntouched();

    const note = screen.getByRole("note");
    expect(note.textContent).toMatch(/^Setup required\. Provide: /);
    screen.getByText("VENOM_ANTIGRAVITY_CLIENT_SECRET");
    screen.getByText("VENOM_ANTIGRAVITY_CLIENT_ID");
  });

  it("renders the per-state actions: Connected (disabled), Connect Integration, Setup required, Integration unavailable", async () => {
    // Setup-required lives on the OAuth tab; the other three states are all
    // key-authenticated entries.
    await renderFleetUntouched();

    const antigravityCard = screen
      .getByText("Antigravity")
      .closest(".vn-provider-card") as HTMLElement;
    const setupButton = within(antigravityCard).getByRole("button", { name: /^setup required$/i });
    expect((setupButton as HTMLButtonElement).disabled).toBe(true);

    switchToTab("API Key Providers");

    const oczCard = screen.getByText("OpenCode Zen").closest(".vn-provider-card") as HTMLElement;
    const connectedButton = within(oczCard).getByRole("button", { name: /^connected$/i });
    expect((connectedButton as HTMLButtonElement).disabled).toBe(true);

    const agnesCard = screen.getByText("Agnes AI").closest(".vn-provider-card") as HTMLElement;
    const connectButton = within(agnesCard).getByRole("button", { name: /connect integration/i });
    expect((connectButton as HTMLButtonElement).disabled).toBe(false);

    const customCard = screen
      .getByText("Custom (OpenAI-compatible)")
      .closest(".vn-provider-card") as HTMLElement;
    const unavailableButton = within(customCard).getByRole("button", {
      name: /integration unavailable/i,
    });
    expect((unavailableButton as HTMLButtonElement).disabled).toBe(true);

    // No account disclosure in the catalog view — accounts are managed
    // from Active Providers.
    expect(screen.queryByRole("button", { name: /expand .* accounts/i })).toBeNull();
  });

  it("filters the integration grid live by search, with a clearable no-match state", async () => {
    await renderFleetUntouched();

    const searchbox = screen.getByRole("searchbox", { name: /search integrations/i });
    const searchControl = searchbox.closest(".vn-search") as HTMLElement;
    // Deliberately asserted against providers on the SAME tab: an absent
    // provider from the OTHER tab would be hidden by the tab filter, so it
    // would prove nothing about the search.
    fireEvent.change(searchbox, { target: { value: "claude" } });
    screen.getByText("Claude Code");
    // Codex, not Antigravity: the search also matches the marketing
    // DESCRIPTION, and Antigravity's mentions Claude — so it is a legitimate
    // hit here and would make a useless negative.
    expect(screen.queryByText("OpenAI Codex / ChatGPT")).toBeNull();

    fireEvent.click(within(searchControl).getByRole("button", { name: /clear search/i }));
    screen.getByText("OpenAI Codex / ChatGPT");
    screen.getByText("Claude Code");

    fireEvent.change(searchbox, { target: { value: "no-such-integration" } });
    screen.getByText(/no integrations found/i);
    fireEvent.click(within(searchControl).getByRole("button", { name: /clear search/i }));
    screen.getByText("Claude Code");
  });

  it("offers exactly two tabs that PARTITION the catalog, defaults to OAuth, and leaves no provider unreachable", async () => {
    await renderFleetUntouched();

    const tabs = screen.getByRole("group", { name: /filter providers/i });
    expect(
      within(tabs)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual(["OAuth Providers", "API Key Providers"]);

    // OAuth is the default tab — where the owner's real fleet lives.
    expect(
      within(tabs).getByRole("button", { name: "OAuth Providers" }).getAttribute("aria-pressed"),
    ).toBe("true");
    screen.getByText("Antigravity");
    screen.getByText("Claude Code");
    screen.getByText("OpenAI Codex / ChatGPT");
    expect(screen.queryByText("OpenCode Zen")).toBeNull();
    expect(screen.queryByText("Custom (OpenAI-compatible)")).toBeNull();

    // The key-authenticated tab is the COMPLEMENT of OAuth, not an
    // `auth_mode === "api_key"` equality test — so `custom_openai`, which
    // used to be visible ONLY under the removed "All" tab, lands here
    // instead of falling through both tabs and disappearing.
    fireEvent.click(within(tabs).getByRole("button", { name: "API Key Providers" }));
    screen.getByText("OpenCode Zen");
    screen.getByText("Agnes AI");
    screen.getByText("Custom (OpenAI-compatible)");
    expect(screen.queryByText("Antigravity")).toBeNull();

    // Totality, stated as a count so a NEW auth_mode cannot go unnoticed:
    // the two tabs together must account for every catalog entry.
    const apiKeyNames = gridProviderNames();
    fireEvent.click(within(tabs).getByRole("button", { name: "OAuth Providers" }));
    const oauthNames = gridProviderNames();
    expect(new Set([...oauthNames, ...apiKeyNames]).size).toBe(CATALOG_PROVIDER_COUNT);
    expect(oauthNames.filter((name) => apiKeyNames.includes(name))).toEqual([]);
  });

  it("returns to Active Providers after a connect, so the new account is actually visible", async () => {
    // Connecting happens from the catalog GRID, which renders integrations
    // and never accounts — so staying there hides the very thing that was
    // just created. The page must hand the owner back to the row list.
    const onViewChange = vi.fn();
    await renderFleetUntouched(
      {
        "POST /api/control/v1/providers/agnes-ai/accounts": () =>
          jsonResponse(201, { data: { account: { id: "acct-new" } } }),
      },
      { view: "all", onViewChange },
    );

    switchToTab("API Key Providers");
    const agnesCard = screen.getByText("Agnes AI").closest(".vn-provider-card") as HTMLElement;
    fireEvent.click(within(agnesCard).getByRole("button", { name: /connect integration/i }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByRole("textbox", { name: /api key/i }), {
      target: { value: "sk-journey-key" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: /save & encrypt/i }));

    // The view switch is the assertion: without it the owner is left staring
    // at the catalog wondering whether the connect worked.
    await waitFor(() => expect(onViewChange).toHaveBeenCalledWith("active"));
  });
});

describe("FleetOverview — contextual stat cards", () => {
  it("recomputes all four cards from the current view + auth filter scope", async () => {
    await renderFleetUntouched();

    // All Integrations / OAuth Providers (the default tab): the three OAuth
    // catalog entries, and no accounts — the fixture's accounts are all on
    // key-authenticated providers.
    expect(statCard("Providers").textContent).toContain("3");
    expect(statCard("Providers").textContent).toContain("all integrations");
    expect(statCard("Accounts").textContent).toContain("0");
    expect(statCard("Healthy").textContent).toContain("0/0");

    // API Key Providers: the other three entries (including the
    // custom_openai one), carrying every account the fixture has. Three
    // accounts exist but only TWO are counted — ACCOUNT_STOPPED is disabled,
    // and a disabled account reads as if it were not there (see the
    // disabled-account counting test below).
    const tabs = screen.getByRole("group", { name: /filter providers/i });
    fireEvent.click(within(tabs).getByRole("button", { name: "API Key Providers" }));
    expect(statCard("Providers").textContent).toContain("3");
    expect(statCard("Accounts").textContent).toContain("2");
    expect(statCard("Accounts").textContent).toContain("across 1 provider");
    expect(statCard("Healthy").textContent).toContain("1/2");
    // The tile headlines the verified-working count, not the raw discovered
    // total — the total stays visible in the meta line.
    expect(statCard("Working Models").querySelector(".vn-stat-value")?.textContent).toBe("1");
    expect(statCard("Working Models").textContent).toContain("2 discovered");

    fireEvent.click(within(tabs).getByRole("button", { name: "OAuth Providers" }));
    expect(statCard("Accounts").textContent).toContain("0");
  });

  it("counts a DISABLED account as if it were not there, in every counter, while keeping its row", async () => {
    // An account the owner turned off is not part of the fleet: it cannot
    // serve a request, so counting it drags "healthy" down forever and reports
    // work "requiring action" that the owner has already decided about. It
    // must still RENDER though — otherwise there is nothing left to click to
    // turn it back on — so this is a counting scope, not a filter on the list.
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(200, { data: { accounts: [ACCOUNT_HEALTHY, ACCOUNT_STOPPED] } }),
      }),
    );
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);
    await screen.findByRole("button", { name: "API Key Providers" });
    switchToTab("API Key Providers");
    await screen.findByText("OpenCode Zen");

    // Two accounts exist; ONE counts.
    expect(statCard("Accounts").textContent).toContain("1");
    expect(statCard("Healthy").textContent).toContain("1/1");
    within(statCard("Healthy")).getByText("all healthy");

    // The provider header agrees — nothing "requires action".
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");
    expect(screen.getByText(/require action/).textContent).toMatch(/^0 require action$/);

    // ...and the disabled row is still on screen, with its own way back.
    const stoppedRow = accountRow("acct-3");
    expect(stoppedRow).not.toBeNull();
    within(stoppedRow).getByText(/turned off/i);
  });

  it("switches PROVIDERS to connected-integrations in the active view and shows the all-healthy badge only when earned", async () => {
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(200, { data: { accounts: [ACCOUNT_HEALTHY] } }),
      }),
    );
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);
    // The connected provider is key-authenticated, so step onto that tab.
    await screen.findByRole("button", { name: "API Key Providers" });
    switchToTab("API Key Providers");
    await screen.findByText("OpenCode Zen");

    expect(statCard("Providers").textContent).toContain("1");
    expect(statCard("Providers").textContent).toContain("connected integrations");
    expect(statCard("Healthy").textContent).toContain("1/1");
    within(statCard("Healthy")).getByText("all healthy");
  });

  it("renders MODELS as an honest dash — never 0 — while offerings are unavailable, and surfaces the failure once", async () => {
    await renderFleet({
      "GET /api/control/v1/offerings?limit=200": () =>
        jsonResponse(500, {
          error: { code: "internal", message: "boom", request_id: "r9", retryable: true },
        }),
    });

    const models = statCard("Working Models");
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
    // 1/2, not 1/3: the aggregate is scoped to the COUNTED accounts, so the
    // owner's disabled account cannot hold the provider at "warning" forever.
    screen.getByTitle("1/2 accounts healthy");
    screen.getByText(/\d+ Live Models/);
    screen.getByText(/\d+ accounts/);
    screen.getByRole("button", { name: /sync all accounts/i });
    screen.getByRole("button", { name: /refresh models for every account/i });
    screen.getByRole("button", { name: /^add account$/i });
    screen.getByRole("link", { name: /open opencode\.ai in a new tab/i });
  });

  it("drops a provider from the active view once its only account is disconnected", async () => {
    // A disconnected account's row is retained server-side for history, but a
    // provider whose ONLY account is disconnected is no longer active — it must
    // vanish from Active Providers and fall back to the empty state (the owner
    // finds it again under All Integrations).
    const disconnected = account({
      id: "acct-gone",
      connection_state: "disconnected",
      display_status: "disconnected",
    });
    const fetchMock = baseHandlers({
      "GET /api/control/v1/accounts?limit=200": () =>
        jsonResponse(200, { data: { accounts: [disconnected] } }),
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);

    // The empty copy names the CURRENT tab, so it must not claim the whole
    // fleet is empty (see the tab-scoped empty-state test below).
    await screen.findByText(/No OAuth Providers are connected/i);
    switchToTab("API Key Providers");
    await screen.findByText(/No API Key Providers are connected/i);
    expect(screen.queryByText("OpenCode Zen")).toBeNull();
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
      const syncCall = fetchMock.mock.calls.find(([input]) =>
        String(input).endsWith("/providers/opencode-zen/sync"),
      );
      expect(syncCall).toBeTruthy();
    });
    // The refetch happened: providers were listed at least twice.
    await waitFor(() => {
      const providerReads = fetchMock.mock.calls.filter(([input]) =>
        String(input).endsWith("/providers"),
      );
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
      const jobReads = fetchMock.mock.calls.filter(([input]) =>
        String(input).endsWith("/jobs/job-1"),
      );
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

    const row = accountRow("acct-1");
    within(row).getByText("ipfox111@example.test");
    within(row).getByText("PRO");

    // The known window renders its real percentage and reset countdown…
    within(row).getByText("GEM");
    within(row).getByText("12%");
    expect(within(row).getByText(/Resets in/).textContent).toMatch(/Resets in 2 hr \d+ min/);
    // Non-live provider evidence is omitted entirely, never presented as a
    // historical meter on the operational account surface.
    expect(within(row).queryByText("OPT")).toBeNull();
    expect(within(row).queryByText("unknown")).toBeNull();
    expect(within(row).queryByText("0%")).toBeNull();

    // Meta line names both independent live observations explicitly.
    expect(within(row).getByText(/Usage updated .* · Health checked .*/).textContent).toMatch(
      /Usage updated .+ ago · Health checked .+ ago/,
    );
  });

  it("wires the account sync action to health + quota (job polled), and fetch-models to discovery", async () => {
    const fetchMock = await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/health": () =>
          jsonResponse(200, { data: ACCOUNT_HEALTHY }),
        "POST /api/control/v1/accounts/acct-1/quota": () =>
          jsonResponse(202, {
            data: { job_id: "job-q", status_url: "/api/control/v1/jobs/job-q" },
          }),
        "POST /api/control/v1/accounts/acct-1/discover": () =>
          jsonResponse(202, { data: { job_id: "job-d" } }),
        "GET /api/control/v1/jobs/job-q": () =>
          jsonResponse(200, { data: { job_id: "job-q", kind: "quota_sync", status: "completed" } }),
        "GET /api/control/v1/jobs/job-d": () =>
          jsonResponse(200, { data: { job_id: "job-d", kind: "discovery", status: "completed" } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /sync: health · plan · usage/i }),
    );
    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/health")),
      ).toBe(true);
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/quota")),
      ).toBe(true);
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/jobs/job-q"))).toBe(
        true,
      );
    });

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /fetch models from provider/i }),
    );
    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/discover")),
      ).toBe(true);
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/jobs/job-d"))).toBe(
        true,
      );
    });
  });

  it("maps the power toggle by connection_state: connected -> stop, stopped -> resume", async () => {
    const fetchMock = await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/stop": () =>
          jsonResponse(200, {
            data: account({ id: "acct-1", connection_state: "stopped", display_status: "stopped" }),
          }),
        "POST /api/control/v1/accounts/acct-3/resume": () =>
          jsonResponse(200, { data: account({ id: "acct-3" }) }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /^disable account$/i }),
    );
    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/stop")),
      ).toBe(true);
    });

    fireEvent.click(
      within(accountRow("acct-3")).getByRole("button", { name: /^enable account$/i }),
    );
    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-3/resume")),
      ).toBe(true);
    });
  });

  it("shows the per-account model count and opens the Model Test Report; a zero-model account's chip is disabled", async () => {
    await renderFleet({}, { view: "active" });
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    const row1Chip = within(accountRow("acct-1")).getByRole("button", {
      name: /open model test report/i,
    });
    expect(row1Chip.textContent).toContain("2");

    const row3Chip = within(accountRow("acct-3")).getByRole("button", {
      name: /open model test report/i,
    });
    expect((row3Chip as HTMLButtonElement).disabled).toBe(true);
    expect(row3Chip.getAttribute("title")).toBe("No live models");

    fireEvent.click(row1Chip);
    await screen.findByRole("dialog", { name: /model report: opencode zen/i });
  });

  it("explains an empty Active view without offering a no-op search reset", async () => {
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(200, { data: { accounts: [] } }),
      }),
    );
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);

    // Scoped to the tab, not the whole fleet: with two tabs there is always
    // a category filter in effect, so an unscoped "No active providers"
    // would be a claim the page cannot support.
    await screen.findByText("No active OAuth Providers");
    expect(screen.queryByRole("button", { name: /clear search/i })).toBeNull();

    switchToTab("API Key Providers");
    await screen.findByText("No active API Key Providers");
    expect(screen.queryByRole("button", { name: /clear search/i })).toBeNull();
  });

  it("reports the breadcrumb-chip counts (active providers / all integrations) once data loads", async () => {
    vi.stubGlobal("fetch", baseHandlers());
    const onCounts = vi.fn();
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onCounts={onCounts} />);

    // The breadcrumb chips count the WHOLE fleet, not the current tab — so
    // they are unaffected by which tab is open.
    await screen.findByText("Antigravity");
    await waitFor(() => expect(onCounts).toHaveBeenCalledWith({ active: 1, total: 6 }));
  });

  it("has zero axe violations in both views, with an expanded provider", async () => {
    const fetchMock = baseHandlers();
    vi.stubGlobal("fetch", fetchMock);
    const { container, rerender } = render(
      <FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />,
    );

    await screen.findByRole("button", { name: "API Key Providers" });
    switchToTab("API Key Providers");
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
    expect(
      (within(dialog).getByRole("radio", { name: /inherit from provider/i }) as HTMLInputElement)
        .checked,
    ).toBe(true);

    // An OPTIONAL label field is present; leaving it empty is fine (the
    // connect body simply omits the label).
    const labelField = within(dialog).getByLabelText(/label/i) as HTMLInputElement;
    expect(labelField).toBeTruthy();
    expect(labelField.value).toBe("");

    // Save & encrypt is gated on a non-empty key.
    const save = within(dialog).getByRole("button", { name: /save & encrypt/i });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(textarea, { target: { value: "sk-x" } });
    expect((save as HTMLButtonElement).disabled).toBe(false);
  });

  it("maps the billing selection to the funding body — inherit OMITS the field — and never leaks the key", async () => {
    const consoleSpies = (["log", "info", "warn", "error", "debug"] as const).map((m) =>
      vi.spyOn(console, m).mockImplementation(() => {}),
    );

    const fetchMock = await renderFleet({
      "POST /api/control/v1/providers/agnes-ai/accounts": () =>
        jsonResponse(201, {
          data: {
            id: "acct-new",
            provider: "agnes-ai",
            external_id: "ext-new",
            connection_state: "connected",
            health_state: "healthy",
            funding: "free",
            display_status: "healthy",
          },
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

    const connectCall = fetchMock.mock.calls.find(([input]) =>
      String(input).endsWith("/providers/agnes-ai/accounts"),
    );
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
          data: {
            id: "acct-new",
            provider: "agnes-ai",
            external_id: "ext-new",
            connection_state: "connected",
            health_state: "healthy",
            funding: "paid",
            display_status: "healthy",
          },
        }),
    });

    const agnesCard = screen.getByText("Agnes AI").closest(".vn-provider-card") as HTMLElement;
    fireEvent.click(within(agnesCard).getByRole("button", { name: /connect integration/i }));
    const dialog = await screen.findByRole("dialog", { name: /^connect agnes ai$/i });

    fireEvent.change(dialog.querySelector("textarea") as HTMLTextAreaElement, {
      target: { value: "sk-key" },
    });
    fireEvent.click(within(dialog).getByRole("radio", { name: /paid/i }));
    fireEvent.click(within(dialog).getByRole("button", { name: /save & encrypt/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const connectCall = fetchMock.mock.calls.find(([input]) =>
      String(input).endsWith("/providers/agnes-ai/accounts"),
    );
    const [, init] = connectCall as [unknown, RequestInit];
    expect(JSON.parse(init.body as string).funding).toBe("paid");
  });

  it("sends the optional label in the connect body when one is typed", async () => {
    const fetchMock = await renderFleet({
      "POST /api/control/v1/providers/agnes-ai/accounts": () =>
        jsonResponse(201, {
          data: {
            id: "acct-new",
            provider: "agnes-ai",
            external_id: "ext-new",
            connection_state: "connected",
            health_state: "healthy",
            funding: "free",
            display_status: "healthy",
          },
        }),
    });

    const agnesCard = screen.getByText("Agnes AI").closest(".vn-provider-card") as HTMLElement;
    fireEvent.click(within(agnesCard).getByRole("button", { name: /connect integration/i }));
    const dialog = await screen.findByRole("dialog", { name: /^connect agnes ai$/i });

    fireEvent.change(dialog.querySelector("textarea") as HTMLTextAreaElement, {
      target: { value: "sk-key" },
    });
    fireEvent.change(within(dialog).getByLabelText(/label/i), {
      target: { value: "  Main key  " },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: /save & encrypt/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const connectCall = fetchMock.mock.calls.find(([input]) =>
      String(input).endsWith("/providers/agnes-ai/accounts"),
    );
    const [, init] = connectCall as [unknown, RequestInit];
    // Trimmed, never the raw padded value.
    expect(JSON.parse(init.body as string).label).toBe("Main key");
  });
});

describe("FleetOverview — OAuth connect (image 10)", () => {
  it("begins OAuth, opens the authorize URL as a popup, offers re-open, and polls to completed", async () => {
    const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);

    // claude-code is an OAuth provider, so this stays on the default tab.
    const fetchMock = await renderFleetUntouched({
      "POST /api/control/v1/providers/claude-code/oauth/begin": () =>
        jsonResponse(202, {
          data: {
            transaction_id: "tx-1",
            authorize_url: "https://provider.example/authorize",
            expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
          },
        }),
      "GET /api/control/v1/oauth/tx-1/status": () =>
        jsonResponse(200, { data: { status: "completed", account_id: "acct-new" } }),
    });

    const claudeCodeCard = screen
      .getByText("Claude Code")
      .closest(".vn-provider-card") as HTMLElement;
    fireEvent.click(within(claudeCodeCard).getByRole("button", { name: /connect integration/i }));

    const dialog = await screen.findByRole("dialog", { name: /^connect claude code$/i });
    within(dialog).getByText(/sign in via the popup window/i);
    fireEvent.click(within(dialog).getByRole("button", { name: /continue with claude code/i }));

    await screen.findByText(/waiting for authorization in popup…/i);
    expect(openSpy).toHaveBeenCalledWith(
      "https://provider.example/authorize",
      "venom-oauth",
      "popup,width=560,height=720",
    );

    // Re-open re-opens the SAME authorize URL (also the popup-blocked
    // recovery path — window.open returned null above).
    fireEvent.click(screen.getByRole("button", { name: /re-open sign-in window/i }));
    expect(openSpy).toHaveBeenCalledTimes(2);
    expect(openSpy.mock.calls[1]).toEqual([
      "https://provider.example/authorize",
      "venom-oauth",
      "popup,width=560,height=720",
    ]);

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), { timeout: 5000 });

    const beginCall = fetchMock.mock.calls.find(([input]) =>
      String(input).endsWith("/claude-code/oauth/begin"),
    );
    expect(beginCall).toBeTruthy();
  }, 8000);
});

describe("FleetOverview — credential reveal", () => {
  it("reveals the secret (reverify-first), then hide removes it from the DOM, and no secret ever reaches console", async () => {
    const consoleSpies = (["log", "info", "warn", "error", "debug"] as const).map((m) =>
      vi.spyOn(console, m).mockImplementation(() => {}),
    );
    const canary = "CANARY-REVEALED-SECRET-9f2e";

    await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/reveal": () => new Response(canary, { status: 200 }),
        "POST /api/control/v1/auth/reverify": () =>
          jsonResponse(200, { data: { reverify_fresh_until: "2026-07-24T00:05:00Z" } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    // Reveal-first: the eye opens the reverify prompt; only a successful
    // reverify actually reveals the secret (no speculative reveal → no console 401).
    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /reveal credential/i }),
    );
    const passwordInput = await screen.findByLabelText(/owner password/i);
    fireEvent.change(passwordInput, { target: { value: "the-owner-password" } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    await screen.findByText(canary);
    expect(document.body.innerHTML).toContain(canary);

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /^hide credential$/i }),
    );

    await waitFor(() => expect(document.body.innerHTML).not.toContain(canary));

    for (const spy of consoleSpies) {
      for (const call of spy.mock.calls) {
        for (const arg of call) {
          if (typeof arg === "string") expect(arg).not.toContain(canary);
        }
      }
    }
  });

  it("requires re-verification before revealing, and reveals exactly once on success", async () => {
    let revealAttempts = 0;
    const canary = "CANARY-AFTER-REVERIFY-77bb";

    await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/reveal": () => {
          revealAttempts += 1;
          return new Response(canary, { status: 200 });
        },
        "POST /api/control/v1/auth/reverify": () =>
          jsonResponse(200, { data: { reverify_fresh_until: "2026-07-24T00:05:00Z" } }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /reveal credential/i }),
    );

    // The reveal endpoint must NOT be hit before a successful reverify.
    expect(revealAttempts).toBe(0);

    const passwordInput = await screen.findByLabelText(/owner password/i);
    fireEvent.change(passwordInput, { target: { value: "the-owner-password" } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    await screen.findByText(canary);
    // Reveal-first calls reveal once, after the challenge — never speculatively.
    expect(revealAttempts).toBe(1);
  });

  it("surfaces a locked-out state in the reverify prompt and never reveals when reverify is rate-limited", async () => {
    let revealAttempts = 0;
    const password = "reverify-locked-out-password";
    const canary = "CANARY-NEVER-REVEALED-WHILE-LOCKED";

    await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/reveal": () => {
          revealAttempts += 1;
          return new Response(canary, { status: 200 });
        },
        "POST /api/control/v1/auth/reverify": () =>
          jsonResponse(429, {
            error: {
              code: "locked_out",
              message: "too many failed attempts, try again later",
              request_id: "r2",
              retryable: true,
              retry_after: 30,
            },
          }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /reveal credential/i }),
    );
    const passwordInput = await screen.findByLabelText(/owner password/i);
    fireEvent.change(passwordInput, { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    await screen.findByText(/too many failed attempts/i);
    // Reveal-first + locked-out reverify: the reveal endpoint is NEVER hit.
    expect(revealAttempts).toBe(0);
    expect(screen.queryByText(canary)).toBeNull();
    expect(document.body.innerHTML).not.toContain(password);
    expect(document.body.innerHTML).not.toContain(canary);
  });
});

describe("FleetOverview — sync tolerates quota_unsupported", () => {
  it("treats a 409 quota_unsupported as benign: muted caption, refetch, NO error banner", async () => {
    const fetchMock = await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/health": () =>
          jsonResponse(200, { data: ACCOUNT_HEALTHY }),
        "POST /api/control/v1/accounts/acct-1/quota": () =>
          jsonResponse(409, {
            error: {
              code: "quota_unsupported",
              message: "this provider has no quota capability",
              request_id: "r5",
              retryable: false,
            },
          }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /sync: health · plan · usage/i }),
    );

    await screen.findByText(/quota sync skipped — this provider has no quota capability/i);
    // The health refresh ran and the page refetched — the sync SUCCEEDED.
    expect(
      fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/health")),
    ).toBe(true);
    await waitFor(() => {
      const providerReads = fetchMock.mock.calls.filter(([input]) =>
        String(input).endsWith("/providers"),
      );
      expect(providerReads.length).toBeGreaterThanOrEqual(2);
    });
    // No error banner anywhere: the typed code never renders as a failure.
    expect(screen.queryByText(/quota_unsupported/)).toBeNull();
  });

  it("still surfaces every OTHER quota failure verbatim", async () => {
    await renderFleet(
      {
        "POST /api/control/v1/accounts/acct-1/health": () =>
          jsonResponse(200, { data: ACCOUNT_HEALTHY }),
        "POST /api/control/v1/accounts/acct-1/quota": () =>
          jsonResponse(409, {
            error: {
              code: "credential_unavailable",
              message: "account has no active credential",
              request_id: "r6",
              retryable: false,
            },
          }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /sync: health · plan · usage/i }),
    );

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
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(200, { data: { accounts: [ACCOUNT_ZEN] } }),
        "GET /api/control/v1/offerings?limit=200": () => jsonResponse(200, { data: [] }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    const headline = await screen.findByText("#account 01");
    return headline.closest(".vnd-account") as HTMLElement;
  }

  it("never renders the opaque fingerprint external id — headline falls back to the numbered default", async () => {
    const row = await renderZenRow();

    // No label and no email: the headline is the numbered default, and the
    // raw 64-char hex never renders as text OR in any title attribute.
    within(row).getByText("#account 01");
    expect(document.body.textContent).not.toContain(HEX64);
    expect(row.querySelector(`[title*="${HEX64}"]`)).toBeNull();
  });

  it("suppresses the plan badge when it merely repeats the funding classification", async () => {
    const row = await renderZenRow();

    // One badge, not two: the synthetic "Free" plan echoes funding "free",
    // so only the FundingBadge renders — and for a free account it reads
    // "Free / ∞" (unlimited), not a bare "Free".
    expect(within(row).queryByText("FREE")).toBeNull();
    within(row).getByText("Free / ∞");
  });

  it("renders no lone dash placeholders for empty quota windows or certification data", async () => {
    const row = await renderZenRow();

    expect(within(row).queryAllByText("—")).toHaveLength(0);
    expect(within(row).queryByTitle(/no quota windows tracked/i)).toBeNull();
    // Free account: health freshness in the meta line; the unlimited
    // nature lives in the "Free / ∞" funding badge, not a separate label.
    expect(within(row).getByText(/^Free · Health checked .+/)).toBeTruthy();
    expect(within(row).queryByText("∞ Unlimited")).toBeNull();
  });

  it("gives Fetch models and the model-report chip DISTINCT icons", async () => {
    const row = await renderZenRow();

    const fetchButton = within(row).getByRole("button", { name: /fetch models from provider/i });
    const reportChip = within(row).getByRole("button", { name: /open model test report/i });
    expect(fetchButton.querySelector(".vn-icon--download")).toBeTruthy();
    expect(reportChip.querySelector(".vn-icon--flask-conical")).toBeTruthy();
    expect(fetchButton.querySelector(".vn-icon--flask-conical")).toBeNull();
  });
});

describe("FleetOverview — ClinePass OAuth subscription contract", () => {
  const PROVIDER_CLINEPASS = {
    id: "clinepass",
    display_name: "ClinePass",
    description: "Paid ClinePass OAuth accounts.",
    auth_mode: "oauth2" as const,
    funding: { mode: "fixed", locked: true, non_expiring: false, fixed: "paid" },
    capabilities: [],
    configured: true,
    missing_env: [],
  };
  const subscribed = account({
    id: "cline-subscribed",
    provider: "clinepass",
    auth_type: "oauth2",
    identity: { email: "subscribed@example.test", plan: "ClinePass" },
    funding: { funding: "unknown", source: "owner_override", locked: false, version: "old" },
    display_status: "healthy",
    health_state: "healthy",
    quota: [
      {
        ...KNOWN_QUOTA_WINDOW,
        unit: "credits",
        window_type: "balance",
        window_key: "balance",
        used: null,
        remaining: 0.17,
        total: null,
        limit_value: null,
        reset_at: null,
      },
    ],
  });
  const unsubscribed = account({
    id: "cline-unsubscribed",
    provider: "clinepass",
    auth_type: "oauth2",
    identity: { email: "unsubscribed@example.test", plan: "ClinePass" },
    funding: { funding: "paid", source: "provider_policy", locked: true, version: "new" },
    display_status: "degraded",
    health_state: "degraded",
    eligibility: { eligible: false, reason: "health_not_healthy" },
    quota: [
      {
        ...KNOWN_QUOTA_WINDOW,
        unit: "credits",
        window_type: "balance",
        window_key: "balance",
        used: null,
        remaining: 0.5,
        total: null,
        limit_value: null,
        reset_at: null,
        freshness: "stale",
      },
    ],
    last_health_error:
      "no active ClinePass subscription on this account — sign in with a subscribed account or remove it",
  });
  const clineOfferings = [
    offering({
      account_id: "cline-subscribed",
      provider_id: "clinepass",
      provider_model_id: "cline/model-live",
      capabilities: [
        offeringCapability({ truth: "supported", state: "certified", routable: true }),
      ],
    }),
    offering({
      account_id: "cline-unsubscribed",
      provider_id: "clinepass",
      provider_model_id: "cline/model-stale",
      capabilities: [
        offeringCapability({ truth: "supported", state: "certified", routable: true }),
      ],
    }),
  ];

  async function renderClinePassRows() {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/providers": () =>
        jsonResponse(200, { data: { providers: [PROVIDER_CLINEPASS] } }),
      "GET /api/control/v1/accounts?limit=200": () =>
        jsonResponse(200, { data: { accounts: [subscribed, unsubscribed] } }),
      "GET /api/control/v1/offerings?limit=200": () => jsonResponse(200, { data: clineOfferings }),
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);
    await screen.findByText("ClinePass");
    expandProvider("ClinePass");
  }

  it("shows subscription truth without stale usage or a misleading retryability badge", async () => {
    await renderClinePassRows();

    const liveRow = accountRow("cline-subscribed");
    within(liveRow).getByText("Pass active");
    within(liveRow).getByText("$0.17");
    expect(within(liveRow).queryByText("CLINEPASS")).toBeNull();
    expect(within(liveRow).queryByText("Paid")).toBeNull();
    expect(within(liveRow).queryByText("Unknown")).toBeNull();

    const rejectedRow = accountRow("cline-unsubscribed");
    expect(rejectedRow.classList.contains("vnd-account--subscription-required")).toBe(true);
    within(rejectedRow).getByText("Subscription required");
    expect(within(rejectedRow).queryByText("$0.50")).toBeNull();
    expect(within(rejectedRow).queryByText(/not retryable/i)).toBeNull();
    expect(within(rejectedRow).getByText(/Live updates paused · Usage unavailable/)).toBeTruthy();
    expect(within(rejectedRow).queryByText("CLINEPASS")).toBeNull();
    expect(within(rejectedRow).queryByText("Paid")).toBeNull();
  });

  it("uses warning aggregate health and excludes stale account models from the live total", async () => {
    await renderClinePassRows();

    expect(
      document.querySelector('.vnd-health-dot--warning[title="1/2 accounts healthy"]'),
    ).toBeTruthy();
    screen.getByText("1 Live Models");
    screen.getByText("2 accounts");
    screen.getByText("1 require action");
  });

  it("offers targeted OAuth reauthentication when a subscribed session is definitively expired", async () => {
    const expired = account({
      ...subscribed,
      display_status: "expired",
      health_state: "expired",
      eligibility: { eligible: false, reason: "credential_expired" },
      last_health_error: "OAuth session expired or was revoked — sign in again",
    });
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        "GET /api/control/v1/providers": () =>
          jsonResponse(200, { data: { providers: [PROVIDER_CLINEPASS] } }),
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(200, { data: { accounts: [expired] } }),
        "GET /api/control/v1/offerings?limit=200": () =>
          jsonResponse(200, { data: clineOfferings.slice(0, 1) }),
      }),
    );
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);
    await screen.findByText("ClinePass");
    expandProvider("ClinePass");

    within(accountRow("cline-subscribed")).getByRole("button", {
      name: "Reauthenticate account",
    });
    const expiredRow = accountRow("cline-subscribed");
    within(expiredRow).getByText("Sign-in required");
    expect(within(expiredRow).queryByText("$0.17")).toBeNull();
    expect(within(expiredRow).queryByText(/not retryable/i)).toBeNull();
    within(expiredRow).getByText("Live updates paused · Usage unavailable");
  });

  it("offers no Edit control on an OAuth account — its identity already names it", async () => {
    // For an OAuth account the Edit dialog holds ONE optional cosmetic field
    // (the label): funding is detected from the provider and is api-key-only.
    // The provider already returns the identity that names the row, so the
    // control opened a dialog with nothing worth deciding. API-key accounts
    // keep it — there the dialog also owns the funding classification.
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        "GET /api/control/v1/providers": () =>
          jsonResponse(200, { data: { providers: [PROVIDER_CLINEPASS] } }),
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(200, { data: { accounts: [subscribed] } }),
        "GET /api/control/v1/offerings?limit=200": () =>
          jsonResponse(200, { data: clineOfferings.slice(0, 1) }),
      }),
    );
    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);
    await screen.findByText("ClinePass");
    expandProvider("ClinePass");

    const row = accountRow("cline-subscribed");
    // The identity IS the name — that is what makes the label redundant here.
    within(row).getByText("subscribed@example.test");
    expect(within(row).queryByRole("button", { name: /edit account/i })).toBeNull();

    // The rest of the action cluster is untouched — only the Edit control goes.
    within(row).getByRole("button", { name: /sync: health/i });
    within(row).getByRole("button", { name: /fetch models from provider/i });
    within(row).getByRole("button", { name: /delete account/i });
  });

  it("finishes a reauth from the relayed callback code, because clinepass omits state", async () => {
    // ClinePass does NOT echo `state` back (OmitStateFromCallback), so the
    // backend CANNOT complete server-side: /callback renders a relay page that
    // postMessages the code to window.opener, and only the opener holds the
    // transaction id. Without this leg the relayed code is dropped, the
    // transaction expires unconsumed, and re-authenticating can never change
    // the account's health — which is exactly what happened live on
    // 2026-08-04 (three reauth_begin successes, three unconsumed transactions).
    const expired = account({
      ...subscribed,
      display_status: "expired",
      health_state: "expired",
      eligibility: { eligible: false, reason: "credential_expired" },
      last_health_error: "OAuth session expired or was revoked — sign in again",
    });
    const fetchMock = createFetchMock({
      "GET /api/control/v1/providers": () =>
        jsonResponse(200, { data: { providers: [PROVIDER_CLINEPASS] } }),
      "GET /api/control/v1/accounts?limit=200": () =>
        jsonResponse(200, { data: { accounts: [expired] } }),
      "GET /api/control/v1/offerings?limit=200": () =>
        jsonResponse(200, { data: clineOfferings.slice(0, 1) }),
      "POST /api/control/v1/accounts/cline-subscribed/reauth/begin": () =>
        jsonResponse(202, {
          data: {
            transaction_id: "tx-reauth-1",
            authorize_url: "https://app.cline.bot/authorize",
            expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
          },
        }),
      "POST /api/control/v1/oauth/complete": () =>
        jsonResponse(200, { data: { status: "completed", account_id: "cline-subscribed" } }),
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(window, "open").mockImplementation(
      () => ({ location: { assign: vi.fn() } }) as unknown as Window,
    );

    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);
    await screen.findByText("ClinePass");
    expandProvider("ClinePass");

    fireEvent.click(
      within(accountRow("cline-subscribed")).getByRole("button", {
        name: "Reauthenticate account",
      }),
    );
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([url]) => String(url).includes("/reauth/begin")),
      ).toBe(true),
    );

    // The relay page's postMessage, verbatim (renderOAuthRelayPage posts with
    // targetOrigin "*" because the callback is served by the backend origin
    // while the dashboard may be on the dev port).
    window.dispatchEvent(
      new MessageEvent("message", {
        data: { type: "venom_oauth_callback", data: { code: "relayed-code" } },
      }),
    );

    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([url]) =>
        String(url).endsWith("/api/control/v1/oauth/complete"),
      );
      expect(call, "the relayed code must be completed against the reauth transaction").toBeTruthy();
      expect(JSON.parse(String((call![1] as RequestInit).body))).toEqual({
        transaction_id: "tx-reauth-1",
        code: "relayed-code",
      });
    });
  });

  it("explains an identity mismatch by naming the account to sign in as, and never calls it not retryable", async () => {
    // The guard is CORRECT — it refuses to swap one account's credential into
    // another — but the raw `account_identity_mismatch` code plus a
    // "not retryable" badge told the owner nothing about the actual cause
    // (they had signed in as the other ClinePass account) and was untrue: the
    // action retries fine with the right account. `not retryable` on an
    // account-lifecycle state is explicitly forbidden.
    const expired = account({
      ...subscribed,
      display_status: "expired",
      health_state: "expired",
      last_health_error: "OAuth session expired or was revoked — sign in again",
    });
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        "GET /api/control/v1/providers": () =>
          jsonResponse(200, { data: { providers: [PROVIDER_CLINEPASS] } }),
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(200, { data: { accounts: [expired] } }),
        "GET /api/control/v1/offerings?limit=200": () =>
          jsonResponse(200, { data: clineOfferings.slice(0, 1) }),
        "POST /api/control/v1/accounts/cline-subscribed/reauth/begin": () =>
          jsonResponse(202, {
            data: {
              transaction_id: "tx-reauth-2",
              authorize_url: "https://app.cline.bot/authorize",
              expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
            },
          }),
        "POST /api/control/v1/oauth/complete": () =>
          jsonResponse(400, {
            error: {
              code: "account_identity_mismatch",
              message: "the OAuth code could not be completed",
              request_id: "r-mismatch",
              retryable: false,
            },
          }),
      }),
    );
    vi.spyOn(window, "open").mockImplementation(
      () => ({ location: { assign: vi.fn() } }) as unknown as Window,
    );

    render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} view="active" />);
    await screen.findByText("ClinePass");
    expandProvider("ClinePass");

    fireEvent.click(
      within(accountRow("cline-subscribed")).getByRole("button", {
        name: "Reauthenticate account",
      }),
    );
    // The relay listener only arms once Begin has returned the transaction id,
    // so dispatching before that would drop the message and pass for the wrong
    // reason.
    await screen.findByText("Complete sign-in to restore this account.");
    window.dispatchEvent(
      new MessageEvent("message", {
        data: { type: "venom_oauth_callback", data: { code: "other-accounts-code" } },
      }),
    );

    const row = accountRow("cline-subscribed");
    // Names the CAUSE and the FIX (which account to use) in ONE sentence, and
    // it lands in the row's own issue box — not a second stacked error panel,
    // so the row keeps the same shape it has when no action has failed.
    const issue = await within(row).findByText(/signed in (as|with) a different account/i);
    expect(issue.textContent).toMatch(/subscribed@example\.test/);
    expect(issue.closest(".vnd-account-issue-box")).not.toBeNull();

    // The raw machine code and the false "not retryable" badge are both gone.
    expect(within(row).queryByText(/not retryable/i)).toBeNull();
    expect(within(row).queryByText(/account_identity_mismatch/)).toBeNull();
  });
});

describe("FleetOverview — funding override (via the Edit account dialog)", () => {
  it("sends expected_version and surfaces funding_locked when the funding is changed", async () => {
    const fetchMock = await renderFleet(
      {
        "PUT /api/control/v1/accounts/acct-1/funding": () =>
          jsonResponse(409, {
            error: {
              code: "funding_locked",
              message: "current funding is locked and cannot be overridden",
              request_id: "r2",
              retryable: false,
            },
          }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    // Funding is now edited inside the combined "Edit account" dialog.
    fireEvent.click(within(accountRow("acct-1")).getByRole("button", { name: /edit account/i }));
    const dialog = await screen.findByRole("dialog", { name: /edit account/i });

    // The funding write fires ONLY when the value actually changes — move
    // it off the account's current "free" so the PUT is triggered.
    fireEvent.change(within(dialog).getByRole("combobox"), { target: { value: "paid" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /^save$/i }));

    await screen.findByText(/funding_locked/i);

    const putCall = fetchMock.mock.calls.find(([input]) =>
      String(input).endsWith("/accounts/acct-1/funding"),
    );
    expect(putCall).toBeTruthy();
    const [, init] = putCall as [unknown, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.expected_version).toBe("v1");
    expect(body.funding).toBe("paid");
  });

  it("PATCHes the label (and skips the funding write) when only the label changes", async () => {
    const fetchMock = await renderFleet(
      {
        "PATCH /api/control/v1/accounts/acct-1": () => jsonResponse(200, { data: ACCOUNT_HEALTHY }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("acct-1")).getByRole("button", { name: /edit account/i }));
    const dialog = await screen.findByRole("dialog", { name: /edit account/i });

    fireEvent.change(within(dialog).getByLabelText(/label/i), { target: { value: "Primary" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /^save$/i }));

    await waitFor(() => {
      const patchCall = fetchMock.mock.calls.find(
        ([input, init]) =>
          String(input).endsWith("/accounts/acct-1") && (init as RequestInit)?.method === "PATCH",
      );
      expect(patchCall).toBeTruthy();
      expect(JSON.parse((patchCall![1] as RequestInit).body as string).label).toBe("Primary");
    });
    // Funding was untouched, so no funding write fired.
    expect(
      fetchMock.mock.calls.some(([input]) => String(input).endsWith("/accounts/acct-1/funding")),
    ).toBe(false);
  });
});

describe("FleetOverview — disconnect", () => {
  it("requires the destructive confirmation before calling DELETE /accounts/{id}", async () => {
    const fetchMock = await renderFleet(
      {
        "DELETE /api/control/v1/accounts/acct-1": () =>
          jsonResponse(200, {
            data: account({
              id: "acct-1",
              connection_state: "disconnected",
              display_status: "disconnected",
            }),
          }),
      },
      { view: "active" },
    );
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(
      within(accountRow("acct-1")).getByRole("button", { name: /delete account/i }),
    );

    // The confirmation now titles the account by its display name (email
    // here), never the opaque external_id.
    const dialog = await screen.findByRole("dialog", { name: /delete ipfox111@example.test/i });
    const confirmButton = within(dialog).getByRole("button", { name: /delete account/i });
    expect((confirmButton as HTMLButtonElement).disabled).toBe(true);

    const typeInput = dialog.querySelector("input") as HTMLInputElement;
    fireEvent.change(typeInput, { target: { value: "delete" } });
    expect((confirmButton as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(confirmButton);

    await waitFor(() => {
      const deleteCall = fetchMock.mock.calls.find(
        ([, init]) => (init as RequestInit | undefined)?.method === "DELETE",
      );
      expect(deleteCall).toBeTruthy();
    });
  });
});
