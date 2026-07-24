import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
// connect flow test uses this one instead (its "Connect account" button
// must not be disabled).
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

function account(overrides: Record<string, unknown>) {
  return {
    id: "acct-default",
    provider: "opencode-zen",
    external_id: "ext-default",
    auth_type: "api_key",
    connection_state: "connected",
    health_state: "healthy",
    reauth_in_progress: false,
    identity: { email: undefined, plan: "Free" },
    funding: { funding: "free", source: "owner_policy", locked: false, version: "v1" },
    display_status: "healthy",
    eligibility: { eligible: true },
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  };
}

const ACCOUNT_HEALTHY = account({ id: "acct-1", external_id: "key_9c41e8b0f2" });
const ACCOUNT_DEGRADED = account({
  id: "acct-2",
  external_id: "key_11ab009d",
  health_state: "degraded",
  display_status: "degraded",
  funding: { funding: "paid", source: "provider_evidence", locked: false, version: "v2" },
});
const ACCOUNT_UNKNOWN = account({
  id: "acct-3",
  external_id: "key_77aa02b9",
  health_state: "unknown",
  display_status: "unknown",
  funding: { funding: "unknown", source: "owner_policy", locked: false, version: "v3" },
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function baseHandlers(overrides: Record<string, () => Response> = {}) {
  return createFetchMock({
    "GET /api/control/v1/providers": () =>
      jsonResponse(200, { data: { providers: [PROVIDER_OCZ, PROVIDER_ANTIGRAVITY, PROVIDER_CLAUDE_CODE] } }),
    "GET /api/control/v1/accounts?limit=200": () =>
      jsonResponse(200, { data: { accounts: [ACCOUNT_HEALTHY, ACCOUNT_DEGRADED, ACCOUNT_UNKNOWN] } }),
    ...overrides,
  });
}

async function renderFleet(overrides: Record<string, () => Response> = {}) {
  const fetchMock = baseHandlers(overrides);
  vi.stubGlobal("fetch", fetchMock);
  render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
  await screen.findByText("OpenCode Zen");
  return fetchMock;
}

function expandProvider(name: string) {
  fireEvent.click(screen.getByRole("button", { name: new RegExp(`expand ${name} accounts`, "i") }));
}

/** Scopes queries to one account row, identified by its (unique)
 * external_id — several rows share generic button labels like "Override
 * funding" or "Reveal credential", so tests that act on a specific
 * account must scope to its row rather than querying the whole page. */
function accountRow(externalId: string): HTMLElement {
  return screen.getByText(externalId).closest(".vn-fleet-account") as HTMLElement;
}

describe("FleetOverview — rendering", () => {
  it("renders provider -> account rows from GET /providers and GET /accounts, with distinct stat counts", async () => {
    await renderFleet();

    screen.getByText("Antigravity");
    expandProvider("OpenCode Zen");

    await screen.findByTitle("display_status: healthy");
    screen.getByTitle("display_status: degraded");
    screen.getByTitle("display_status: unknown");

    // unknown and degraded render with distinct labels, not collapsed
    // into a single generic state.
    expect(screen.getByTitle("display_status: unknown").textContent).toMatch(/unknown/i);
    expect(screen.getByTitle("display_status: degraded").textContent).toMatch(/degraded/i);
  });

  it("shows the setup-required state naming only the missing env var NAMES, never values", async () => {
    await renderFleet();

    screen.getByText(/setup required/i);
    screen.getByText("VENOM_ANTIGRAVITY_CLIENT_SECRET");
    screen.getByText("VENOM_ANTIGRAVITY_CLIENT_ID");
  });

  it("has zero axe violations", async () => {
    const fetchMock = baseHandlers();
    vi.stubGlobal("fetch", fetchMock);
    const { container } = render(<FleetOverview csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);

    await screen.findByText("OpenCode Zen");
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    await assertNoAxeViolations(container);
  });
});

describe("FleetOverview — API-key connect", () => {
  it("posts to /providers/{id}/accounts with an Idempotency-Key and never leaves the key in the DOM or console", async () => {
    const consoleSpies = (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m).mockImplementation(() => {}));

    const fetchMock = await renderFleet({
      "POST /api/control/v1/providers/opencode-zen/accounts": () =>
        jsonResponse(201, {
          data: { id: "acct-new", provider: "opencode-zen", external_id: "ext-new", connection_state: "connected", health_state: "healthy", funding: "free", display_status: "healthy" },
        }),
    });

    const oczCard = screen.getByText("OpenCode Zen").closest(".vn-panel") as HTMLElement;
    const oczConnect = screen.getAllByRole("button", { name: /connect account/i }).find((b) => oczCard.contains(b));
    fireEvent.click(oczConnect!);
    const dialog = await screen.findByRole("dialog", { name: /connect opencode zen account/i });

    const secretKey = "sk-test-CANARY-KEY-abc123";
    const input = dialog.querySelector("input[type=password]") as HTMLInputElement;
    fireEvent.change(input, { target: { value: secretKey } });

    fireEvent.click(screen.getByRole("button", { name: /validate & connect/i }));

    // Cleared synchronously at submit time.
    expect(input.value).toBe("");

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    const connectCall = fetchMock.mock.calls.find(([input2]) => String(input2).endsWith("/providers/opencode-zen/accounts"));
    expect(connectCall).toBeTruthy();
    const [, init] = connectCall as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(init.headers["Idempotency-Key"]).toBeTruthy();
    expect(init.headers["X-CSRF-Token"]).toBe(CSRF_TOKEN);
    expect(JSON.parse(init.body as string).api_key).toBe(secretKey);

    expect(document.body.innerHTML).not.toContain(secretKey);
    for (const spy of consoleSpies) {
      for (const call of spy.mock.calls) {
        for (const arg of call) {
          if (typeof arg === "string") expect(arg).not.toContain(secretKey);
        }
      }
    }
  });
});

describe("FleetOverview — OAuth connect", () => {
  it(
    "begins OAuth, opens the authorize URL, and polls to completed",
    async () => {
      vi.spyOn(window, "open").mockImplementation(() => null);

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

      // Every provider fixture renders a "Connect account" button; scope
      // to the one inside Claude Code's own card (the configured OAuth
      // provider — Antigravity's equivalent button is disabled, since it
      // is deliberately setup-required).
      const claudeCodeCard = screen.getByText("Claude Code").closest(".vn-panel") as HTMLElement;
      const connectButtons = screen.getAllByRole("button", { name: /connect account/i });
      const claudeCodeConnect = connectButtons.find((b) => claudeCodeCard.contains(b));
      fireEvent.click(claudeCodeConnect!);

      await screen.findByRole("dialog", { name: /connect claude code account/i });
      fireEvent.click(screen.getByRole("button", { name: /continue with claude code/i }));

      await screen.findByRole("status", { name: /waiting for the provider/i });
      expect(window.open).toHaveBeenCalledWith("https://provider.example/authorize", "_blank", "noopener,noreferrer");

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

    await renderFleet({
      "POST /api/control/v1/accounts/acct-1/reveal": () => new Response(canary, { status: 200 }),
    });
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

    await renderFleet({
      "POST /api/control/v1/accounts/acct-1/reveal": () => {
        revealAttempts += 1;
        if (revealAttempts === 1) {
          return jsonResponse(401, { error: { code: "reverification_required", message: "re-verification required", request_id: "r1", retryable: false } });
        }
        return new Response(canary, { status: 200 });
      },
      "POST /api/control/v1/auth/reverify": () => jsonResponse(200, { data: { reverify_fresh_until: "2026-07-24T00:05:00Z" } }),
    });
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

    await renderFleet({
      "POST /api/control/v1/accounts/acct-1/reveal": () => {
        revealAttempts += 1;
        // Always demands reverification; a correct client must never reach
        // a reveal-success on a locked_out reverify, so the plaintext body
        // below must never surface.
        if (revealAttempts === 1) {
          return jsonResponse(401, { error: { code: "reverification_required", message: "re-verification required", request_id: "r1", retryable: false } });
        }
        return new Response(canary, { status: 200 });
      },
      "POST /api/control/v1/auth/reverify": () =>
        jsonResponse(429, { error: { code: "locked_out", message: "too many failed attempts, try again later", request_id: "r2", retryable: true, retry_after: 30 } }),
    });
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(within(accountRow("key_9c41e8b0f2")).getByRole("button", { name: /reveal credential/i }));
    const passwordInput = await screen.findByLabelText(/owner password/i);
    fireEvent.change(passwordInput, { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    // The ReverifyModal maps a locked_out reverify onto the DS prompt's
    // locked state — a stable alert (not the ticking countdown, to avoid a
    // timer race). onSuccess is never called, so reveal is never retried
    // and the plaintext never appears; the typed password never leaks.
    await screen.findByText(/too many failed attempts/i);
    expect(revealAttempts).toBe(1);
    expect(screen.queryByText(canary)).toBeNull();
    expect(document.body.innerHTML).not.toContain(password);
    expect(document.body.innerHTML).not.toContain(canary);
  });
});

describe("FleetOverview — funding override", () => {
  it("sends expected_version and surfaces funding_locked", async () => {
    const fetchMock = await renderFleet({
      "PUT /api/control/v1/accounts/acct-1/funding": () =>
        jsonResponse(409, { error: { code: "funding_locked", message: "current funding is locked and cannot be overridden", request_id: "r2", retryable: false } }),
    });
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
    const fetchMock = await renderFleet({
      "DELETE /api/control/v1/accounts/acct-1": () => jsonResponse(200, { data: account({ id: "acct-1", connection_state: "disconnected", display_status: "disconnected" }) }),
    });
    expandProvider("OpenCode Zen");
    await screen.findByTitle("display_status: healthy");

    fireEvent.click(screen.getByRole("button", { name: /actions for key_9c41e8b0f2/i }));
    fireEvent.click(await screen.findByText(/disconnect/i));

    const dialog = await screen.findByRole("dialog", { name: /disconnect key_9c41e8b0f2/i });
    const confirmButton = screen.getByRole("button", { name: /disconnect account/i });
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
