// P6-TEST-001's deterministic control-plane fixtures.
//
// ONE source of truth for both runners:
//   - the vitest/jsdom flow journeys (src/test/*.flow.test.tsx, via
//     src/test/flows.ts), and
//   - the Playwright critical-flow / a11y / visual suites, which serve these
//     same bodies through page.route().
//
// Keeping them in one module is what makes the two layers comparable: a
// fixture drift that made the browser suite pass and the jsdom suite fail
// (or the reverse) would be a bug in the harness, not a finding about the app.
//
// This module is DELIBERATELY dependency-free — no jsdom, no React, no
// vitest, no Playwright. Both runners import it, and a stray import of a
// test-runner global here would break whichever one did not provide it.
//
// DETERMINISM RULES every fixture below obeys, because the visual baselines
// depend on them:
//   - every id is a fixed literal, never generated;
//   - every timestamp is a fixed literal, never `new Date()`;
//   - every list is in a fixed order, never sorted by a live clock.
// A fixture that violated any of these would make a screenshot flake, and a
// flaky baseline is worse than no baseline.

import { SENTINELS } from "../../src/test/noSecrets";

/** The frozen wall clock every suite pins, as epoch milliseconds
 * (2026-01-15T12:00:00Z). Surfaces that render a relative time ("3 minutes
 * ago") read the clock, so the clock is part of the fixture set: Playwright
 * pins it with page.clock, and the jsdom flows never assert on rendered
 * relative times. */
export const FROZEN_NOW_MS = Date.UTC(2026, 0, 15, 12, 0, 0);

/** ISO-8601 instants derived from the frozen clock, so a fixture's
 * "5 minutes ago" is stable across runs. */
const T = {
  now: new Date(FROZEN_NOW_MS).toISOString(),
  fiveMinAgo: new Date(FROZEN_NOW_MS - 5 * 60_000).toISOString(),
  anHourAgo: new Date(FROZEN_NOW_MS - 60 * 60_000).toISOString(),
  yesterday: new Date(FROZEN_NOW_MS - 24 * 60 * 60_000).toISOString(),
} as const;

const EPOCH = {
  now: Math.floor(FROZEN_NOW_MS / 1000),
  anHourAgo: Math.floor(FROZEN_NOW_MS / 1000) - 3600,
  aDayAgo: Math.floor(FROZEN_NOW_MS / 1000) - 86_400,
} as const;

/** The session the authenticated fixtures hand out. Both expiries sit well
 * past the frozen clock so no suite ever races a session-expiry banner. */
export const SESSION_FIXTURE = {
  idle_expires_at: new Date(FROZEN_NOW_MS + 30 * 60_000).toISOString(),
  absolute_expires_at: new Date(FROZEN_NOW_MS + 8 * 60 * 60_000).toISOString(),
};

export const CSRF_TOKEN_FIXTURE = "csrf-token-fixture-0001";

/** The account id the whole fixture set is keyed on. */
export const ACCOUNT_ID = "acct-fixture-0001";
/** The request id the Overview activity link deep-links into
 * (`/diagnostics/routes/{request_id}` — shell/route.pathForRoute). */
export const REQUEST_ID = "req-fixture-0001";
export const OFFERING_OPERATION_ID = "offop-fixture-0001";
export const API_KEY_ID = "key-fixture-0001";

/** The non-secret 4-hex display fragment the keys surface renders (see
 * noSecrets.ts's RAW_VENOM_KEY_PATTERN for why 4 is safe and 8+ is not). */
export const KEY_PREFIX_FIXTURE = "vk_live_c0ff";

// --- Envelope helpers -------------------------------------------------------

/** The documented success envelope (09 §1). */
export function data<T>(value: T): { data: T } {
  return { data: value };
}

/** The documented error envelope (09 §1 / §5.8). */
export function apiError(code: string, message: string, retryable = false): { error: Record<string, unknown> } {
  return { error: { code, message, request_id: "req-fixture-error", retryable } };
}

// --- Auth -------------------------------------------------------------------

export const AUTH_STATUS_SETUP_COMPLETE = data({ setup_complete: true });
export const AUTH_STATUS_FIRST_RUN = data({ setup_complete: false });

/** setup/login/session all return the session plus its bound CSRF token. */
export const AUTH_SESSION_LIVE = {
  ...data({ session: SESSION_FIXTURE }),
  csrf_token: CSRF_TOKEN_FIXTURE,
};

// --- Settings ---------------------------------------------------------------

/** GET /settings' full payload. Appearance is pinned to the package defaults
 * so a visual baseline captured with no theme override matches the shell's
 * own boot state. */
export const FULL_SETTINGS = {
  theme: "venom-dark",
  density: "comfortable",
  accent: "mono",
  radius_px: 6,
  spacing_scale: 1,
  enrichment_enabled: false,
  quota_staleness_seconds: 300,
  probe_max_in_flight_per_provider: 2,
  probe_expensive_enabled: false,
  probe_per_account_window_seconds: 3600,
  effective_config: { bind: "127.0.0.1:8081", data_plane_bind: null },
};

// --- Providers --------------------------------------------------------------

export const PROVIDERS = [
  {
    id: "opencode-zen",
    display_name: "OpenCode Zen",
    description: "OpenAI-compatible aggregator with a free tier.",
    auth_mode: "api_key",
    base_url: "https://opencode.ai/zen",
    funding: { mode: "free", locked: false, non_expiring: true, fixed: null },
    capabilities: ["chat", "streaming"],
    configured: true,
  },
  {
    id: "antigravity",
    display_name: "Antigravity",
    description: "OAuth-enrolled provider.",
    auth_mode: "oauth2",
    funding: { mode: "paid", locked: false, non_expiring: false, fixed: null },
    capabilities: ["chat"],
    configured: false,
    missing_env: ["VENOM_ANTIGRAVITY_CLIENT_ID", "VENOM_ANTIGRAVITY_CLIENT_SECRET"],
  },
];

// --- Accounts ---------------------------------------------------------------

const QUOTA_WINDOW = {
  source: "provider_evidence",
  unit: "requests",
  window_type: "rolling",
  window_key: "daily",
  state: "available",
  freshness: "fresh",
  used: 120,
  remaining: 880,
  total: 1000,
  limit_value: 1000,
  reserved: 0,
  reset_at: EPOCH.now + 3600,
  observed_at: T.fiveMinAgo,
};

/**
 * The one connected account. NOTE `external_id` carries a sentinel value:
 * the Fleet surface NO LONGER renders it anywhere — an account is identified
 * by its owner-set label, its identity email, or a numbered default (see
 * AccountRow), and the row's test hook is the internal account id, not the
 * external_id. So the sentinel must not surface on ANY shell surface, which
 * is what the cross-surface leak assertion in the journey now proves.
 */
export const ACCOUNT = {
  id: ACCOUNT_ID,
  provider: "opencode-zen",
  external_id: SENTINELS.accountExternalID,
  display_name: "Zen production",
  auth_type: "api_key",
  connection_state: "connected",
  health_state: "healthy",
  reauth_in_progress: false,
  identity: { email: "owner@example.test", plan: "free" },
  funding: { funding: "free", source: "provider_policy", locked: false, version: "v1" },
  display_status: "healthy",
  eligibility: { eligible: true },
  quota: [QUOTA_WINDOW],
  last_health_check_at: T.fiveMinAgo,
  created_at: T.yesterday,
  updated_at: T.fiveMinAgo,
};

export const ACCOUNTS_PAGE = data({ accounts: [ACCOUNT] });

// --- Models / offerings -----------------------------------------------------

const CAPABILITY = {
  operation: "chat",
  effective: true,
  state: "certified",
  truth: "supported",
  routable: true,
  offering_operation_id: OFFERING_OPERATION_ID,
  // Never omitted on the wire (internal/httpapi/models.go's capabilityJSON:
  // "" is itself a meaningful closed value, not an absent key) — default to
  // "" here to match that shape rather than fabricating "probed"/"declared".
  provenance: "",
};

const OFFERING = {
  account_id: ACCOUNT_ID,
  provider_id: "opencode-zen",
  provider_model_id: "zen/grok-code",
  display_name: "Grok Code",
  availability: "available",
  effective_context_tokens: 128_000,
  // A real ContextProvenance value (internal/models.ContextNative) — the
  // matching native_context_tokens below on the group makes "native" the
  // honest choice; "provider_evidence" is not a value the backend ever
  // serializes (see internal/models/effective.go).
  context_provenance: "native",
  capabilities: [CAPABILITY],
  quality_score: 0.82,
  quality_known: true,
  cost: {
    is_free: true,
    source: "provider_policy",
    conflict: false,
    confidence: 0.9,
    exact_identity_match: true,
    stale: false,
  },
  classification: "general",
  tiers: {
    lite: { eligible: true, stale: false, penalty: false },
    pro: { eligible: true, stale: false, penalty: false },
    max: { eligible: false, stale: false, penalty: false, reasons: ["context_ceiling"] },
  },
};

export const MODEL_GROUPS = data([
  {
    model_id: "grok-code",
    display_name: "Grok Code",
    native_context_tokens: 128_000,
    quality_rating: 0.82,
    offerings: [OFFERING],
  },
]);

export const OFFERINGS_PAGE = data([OFFERING]);

export const CERTIFICATION = data({
  offering_operation_id: OFFERING_OPERATION_ID,
  account_id: ACCOUNT_ID,
  provider_model_id: "zen/grok-code",
  operation: "chat",
  state: "certified",
  capability_truth: "supported",
  version: 3,
  certified_at: T.anHourAgo,
  certified_and_supported: true,
  probe_execution: "succeeded",
  review_reasons: [],
});

// --- Routing policy ---------------------------------------------------------

export const ROUTING_POLICY = data({
  tiers: [
    {
      tier: "lite",
      funding: "free_only",
      context_ceiling_tokens: 32_000,
      thinking_ceiling: "none",
      attempt_budget: 2,
      scored: false,
      weights: null,
      competitive_band: null,
      latency_tie_break_only: false,
    },
    {
      tier: "pro",
      funding: "free_and_paid",
      context_ceiling_tokens: 200_000,
      thinking_ceiling: "medium",
      attempt_budget: 3,
      scored: true,
      weights: {
        quality: 0.4,
        reliability: 0.2,
        quota_headroom: 0.15,
        evidence_confidence: 0,
        cost_class: 0.15,
        latency: 0.1,
      },
      competitive_band: 0.05,
      latency_tie_break_only: false,
    },
    {
      tier: "max",
      funding: "free_and_paid",
      context_ceiling_tokens: 1_000_000,
      thinking_ceiling: "ultra",
      attempt_budget: 4,
      scored: true,
      weights: {
        quality: 0.5,
        reliability: 0.2,
        quota_headroom: 0.15,
        evidence_confidence: 0.15,
        cost_class: 0,
        latency: 0,
      },
      competitive_band: 0.08,
      latency_tie_break_only: true,
    },
  ],
});

// --- Diagnostics ------------------------------------------------------------

export const ROUTE_DECISIONS = data([
  {
    request_id: REQUEST_ID,
    decision_id: "dec-fixture-0001",
    tier: "pro",
    workload_profile_bucket: "general",
    created_at: T.fiveMinAgo,
    candidates: { total: 3, eligible_groups: 1, group_keys: ["grok-code"] },
    exclusion_reasons: { context_ceiling: 2 },
    chosen: { provider_id: "opencode-zen", provider_model_id: "zen/grok-code", funding: "free" },
    scores: { "zen/grok-code": 0.82 },
    thinking: { requested: "medium", applied: "none", tier_clamped: true, certified_clamped: false },
    outcome: { terminal_status: "success", total_latency_ms: 842 },
  },
]);

export const ROUTE_EXPLANATION = data({
  request_id: REQUEST_ID,
  decision_id: "dec-fixture-0001",
  tier: "pro",
  workload_profile_bucket: "general",
  created_at: T.fiveMinAgo,
  candidates: { total: 3, eligible_groups: 1, group_keys: ["grok-code"] },
  exclusion_reasons: { context_ceiling: 2 },
  chosen: { provider_id: "opencode-zen", provider_model_id: "zen/grok-code", funding: "free" },
  scores: { "zen/grok-code": 0.82 },
  thinking: { requested: "medium", applied: "none", tier_clamped: true, certified_clamped: false },
  attempts: [
    {
      attempt: 1,
      provider_id: "opencode-zen",
      account_id: ACCOUNT_ID,
      offering_operation_id: OFFERING_OPERATION_ID,
      status: "success",
      latency_ms: 842,
      thinking_clamped: true,
      reservation_id: "resv-fixture-0001",
      started_at: T.fiveMinAgo,
      finished_at: T.fiveMinAgo,
    },
  ],
});

export const RECONCILIATION = data([
  {
    reservation_id: "resv-fixture-0002",
    account_id: ACCOUNT_ID,
    request_id: "req-fixture-0002",
    attempt_id: "att-fixture-0002",
    state: "reconciliation_pending",
    attempts: 1,
    leased: false,
    dispatched_at: EPOCH.anHourAgo,
    expires_at: EPOCH.now + 1800,
    rebaseline_flagged: false,
    allocations: [
      {
        window_id: "win-fixture-0001",
        unit: "requests",
        estimated_cost: 1,
        actual_cost: null,
        actual_confidence: null,
        state: "pending",
      },
    ],
  },
]);

// --- Usage ------------------------------------------------------------------

function metric(sum: number | null, average: number | null, known: number, unknown: number) {
  return { sum, average, known_count: known, unknown_count: unknown };
}

function usageGroup(key: string | null, requests: number) {
  return {
    key,
    requests,
    tokens_in: metric(12_400, 620, 20, 0),
    tokens_out: metric(8_100, 405, 20, 0),
    latency_ms: metric(16_800, 840, 20, 0),
  };
}

export const USAGE = data({
  window: { from: EPOCH.aDayAgo, to: EPOCH.now },
  scanned: 20,
  limit: 500,
  truncated: false,
  totals: usageGroup(null, 20),
  by_account: [usageGroup(ACCOUNT_ID, 20)],
  by_model: [usageGroup("zen/grok-code", 20)],
  by_tier: [usageGroup("pro", 20)],
});

// --- API keys ---------------------------------------------------------------

export const API_KEYS_EMPTY = data([]);

export const API_KEY_SUMMARY = {
  id: API_KEY_ID,
  label: "My laptop",
  rpm_limit: 60,
  key_prefix: KEY_PREFIX_FIXTURE,
  created_at: EPOCH.anHourAgo,
  last_used_at: null,
  revoked_at: null,
};

export const API_KEYS_ONE = data([API_KEY_SUMMARY]);

/** POST /keys' 201 body — the ONLY payload in the whole API that carries a
 * raw key, and it carries the canary sentinel so the journey can prove the
 * value does not survive past its one-time reveal. */
export const API_KEY_CREATED = data({
  id: API_KEY_ID,
  label: "My laptop",
  rpm_limit: 60,
  raw_key: SENTINELS.rawVenomKey,
});

// --- Jobs -------------------------------------------------------------------

export const JOB_COMPLETED = data({
  job_id: "job-fixture-0001",
  kind: "discovery",
  status: "completed",
  started_at: T.anHourAgo,
  finished_at: T.anHourAgo,
});

export const JOB_ACCEPTED = data({ job_id: "job-fixture-0001", status_url: "/api/control/v1/jobs/job-fixture-0001" });

// --- The route table --------------------------------------------------------

/** A matcher for one stubbed route: an HTTP method plus a path PATTERN in
 * Go-ServeMux style (`{id}` matches one segment). Query strings are ignored —
 * every list endpoint takes cursor/limit params that must not change which
 * fixture answers. */
export interface RouteStub {
  readonly method: string;
  readonly path: string;
  readonly status: number;
  readonly body: unknown;
}

function get(path: string, body: unknown, status = 200): RouteStub {
  return { method: "GET", path, status, body };
}

function post(path: string, body: unknown, status = 200): RouteStub {
  return { method: "POST", path, status, body };
}

const BASE = "/api/control/v1";

/**
 * Every control-plane route the dashboard touches, with a deterministic
 * body. Ordered most-specific-first is NOT required — matchRoute scores
 * literal segments over `{param}` ones, exactly like Go 1.22's ServeMux.
 */
export const CONTROL_ROUTES: readonly RouteStub[] = [
  get(`${BASE}/auth/status`, AUTH_STATUS_SETUP_COMPLETE),
  get(`${BASE}/auth/session`, AUTH_SESSION_LIVE),
  post(`${BASE}/auth/login`, AUTH_SESSION_LIVE),
  post(`${BASE}/auth/setup`, AUTH_SESSION_LIVE),
  post(`${BASE}/auth/logout`, data({ status: "ok" })),
  post(`${BASE}/auth/reverify`, data({ reverify_fresh_until: T.now })),

  get(`${BASE}/settings`, data(FULL_SETTINGS)),
  { method: "PUT", path: `${BASE}/settings`, status: 200, body: data(FULL_SETTINGS) },

  get(`${BASE}/providers`, data({ providers: PROVIDERS })),
  get(`${BASE}/providers/{id}`, data(PROVIDERS[0])),
  post(`${BASE}/providers/{id}/accounts`, data({
    id: ACCOUNT_ID,
    provider: "opencode-zen",
    external_id: SENTINELS.accountExternalID,
    connection_state: "connected",
    health_state: "healthy",
    funding: "free",
    display_status: "healthy",
  }), 201),
  post(`${BASE}/providers/{id}/oauth/begin`, data({
    transaction_id: "tx-fixture-0001",
    authorize_url: "https://provider.example.test/authorize?tx=tx-fixture-0001",
    expires_at: T.now,
  })),
  get(`${BASE}/oauth/{transaction_id}/status`, data({ status: "completed", account_id: ACCOUNT_ID })),

  get(`${BASE}/accounts`, ACCOUNTS_PAGE),
  get(`${BASE}/accounts/{id}`, data(ACCOUNT)),
  post(`${BASE}/accounts/{id}/health`, data(ACCOUNT)),
  post(`${BASE}/accounts/{id}/stop`, data({ ...ACCOUNT, connection_state: "stopped", display_status: "stopped" })),
  post(`${BASE}/accounts/{id}/resume`, data(ACCOUNT)),
  post(`${BASE}/accounts/{id}/discover`, JOB_ACCEPTED, 202),
  post(`${BASE}/accounts/{id}/quota`, JOB_ACCEPTED, 202),

  get(`${BASE}/models`, MODEL_GROUPS),
  get(`${BASE}/offerings`, OFFERINGS_PAGE),
  get(`${BASE}/offerings/{id}/certification`, CERTIFICATION),
  post(`${BASE}/offerings/{id}/probe`, data({ job_id: "job-fixture-0001", status_url: "/api/control/v1/jobs/job-fixture-0001" }), 202),
  post(`${BASE}/models/{id}/benchmark`, JOB_ACCEPTED, 202),
  get(`${BASE}/jobs/{job_id}`, JOB_COMPLETED),

  get(`${BASE}/routing/policy`, ROUTING_POLICY),
  get(`${BASE}/usage`, USAGE),

  get(`${BASE}/diagnostics/routes`, ROUTE_DECISIONS),
  get(`${BASE}/diagnostics/routes/{request_id}`, ROUTE_EXPLANATION),
  get(`${BASE}/diagnostics/reconciliation`, RECONCILIATION),

  get(`${BASE}/keys`, API_KEYS_ONE),
  post(`${BASE}/keys`, API_KEY_CREATED, 201),
];

/**
 * Resolves `method` + `pathname` against the stub table using Go 1.22
 * ServeMux precedence: a pattern whose segments are all literal beats one
 * with a `{param}`, so `/accounts/{id}/health` wins over `/accounts/{id}`
 * and never the reverse.
 *
 * Returns undefined for an unmatched route — every caller turns that into a
 * LOUD failure rather than a silent empty response, because a surface
 * quietly receiving `undefined` is exactly how a vacuous test is born.
 */
export function matchRoute(method: string, pathname: string, table: readonly RouteStub[] = CONTROL_ROUTES): RouteStub | undefined {
  const segments = pathname.split("/").filter((s) => s !== "");
  let best: RouteStub | undefined;
  let bestLiterals = -1;

  for (const stub of table) {
    if (stub.method !== method) continue;
    const patternSegments = stub.path.split("/").filter((s) => s !== "");
    if (patternSegments.length !== segments.length) continue;

    let literals = 0;
    let matched = true;
    for (let i = 0; i < patternSegments.length; i += 1) {
      const p = patternSegments[i];
      if (p.startsWith("{") && p.endsWith("}")) continue;
      if (p !== segments[i]) {
        matched = false;
        break;
      }
      literals += 1;
    }
    if (matched && literals > bestLiterals) {
      best = stub;
      bestLiterals = literals;
    }
  }

  return best;
}
