import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { AccountProjection, DisplayStatus } from "../api/controlClient";
import TokenHealthSurface, { DISPLAY_STATUSES } from "./TokenHealthSurface";

const CSRF_TOKEN = "health-csrf-token";
const ACCOUNTS_URL = "GET /api/control/v1/accounts?limit=200";

function account(overrides: Partial<AccountProjection> = {}): AccountProjection {
  return {
    id: "acct-1",
    provider: "opencode-zen",
    external_id: "acct-1",
    display_name: "Primary account",
    auth_type: "api_key",
    connection_state: "connected",
    health_state: "healthy",
    reauth_in_progress: false,
    identity: {},
    funding: null,
    display_status: "healthy",
    eligibility: { eligible: true },
    quota: [],
    last_health_check_at: "2026-08-01T09:00:00Z",
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

function mockAccounts(accounts: AccountProjection[], extra: Record<string, () => Response> = {}): void {
  vi.stubGlobal(
    "fetch",
    createFetchMock({
      [ACCOUNTS_URL]: () => jsonResponse(200, { data: { accounts } }),
      ...extra,
    }),
  );
}

function mockAccountsError(status: number, code: string, message: string): void {
  vi.stubGlobal(
    "fetch",
    createFetchMock({
      [ACCOUNTS_URL]: () =>
        jsonResponse(status, { error: { code, message, request_id: "req-1", retryable: false } }),
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("TokenHealthSurface", () => {
  // The FULL display_status vocabulary (docs/02 §3 / the design system's own
  // DisplayStatus union). Each value must render its OWN accessible text —
  // never colour alone, and never two states sharing a label.
  const STATUS_LABELS: Record<DisplayStatus, RegExp> = {
    connecting: /Connecting/i,
    healthy: /Healthy/i,
    degraded: /Degraded/i,
    unavailable: /Unavailable/i,
    expired: /Credential expired/i,
    unknown: /Unknown/i,
    reauthenticating: /Reauthenticating/i,
    cooling_down: /Cooling down/i,
    stopped: /Stopped/i,
    disconnected: /Disconnected/i,
  };

  it("covers exactly the display_status vocabulary the API can emit", () => {
    // Guards against the surface silently falling behind the domain: if a
    // new display_status lands, this fails until it is handled here too.
    expect([...DISPLAY_STATUSES].sort()).toEqual(Object.keys(STATUS_LABELS).sort());
  });

  it.each(Object.keys(STATUS_LABELS) as DisplayStatus[])(
    "renders %s with its own accessible text",
    async (status) => {
      mockAccounts([account({ display_status: status })]);
      render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
      await waitFor(() => expect(screen.getByTestId("health-account-acct-1")).toBeTruthy());

      const row = screen.getByTestId("health-account-acct-1");
      expect(row.textContent ?? "").toMatch(STATUS_LABELS[status]);
    },
  );

  it("gives every display_status a DISTINCT label", async () => {
    // Renders all ten at once and asserts ten different status texts — the
    // assertion that fails if two states are collapsed onto one label.
    mockAccounts(
      DISPLAY_STATUSES.map((status, i) =>
        account({ id: `acct-${i}`, display_name: `Account ${i}`, display_status: status }),
      ),
    );
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() =>
      expect(screen.getByTestId(`health-account-acct-${DISPLAY_STATUSES.length - 1}`)).toBeTruthy(),
    );

    const labels = DISPLAY_STATUSES.map((_, i) => {
      const row = screen.getByTestId(`health-account-acct-${i}`);
      return within(row).getByTestId("health-status-label").textContent ?? "";
    });
    expect(new Set(labels).size).toBe(DISPLAY_STATUSES.length);
  });

  it("renders an unrecognized status as an explicit unknown, never as healthy", async () => {
    // A literal junk value, cast only at this test boundary — the server's
    // vocabulary is closed, but a future server must not be able to make this
    // console silently claim an account is fine.
    mockAccounts([account({ display_status: "totally-made-up" as DisplayStatus })]);
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("health-account-acct-1")).toBeTruthy());

    const row = screen.getByTestId("health-account-acct-1");
    // Scope to the display_status slot: connection_state and health_state are
    // SEPARATE axes (docs/02 §3) and this fixture's health_state is genuinely
    // healthy, so a whole-row assertion would be testing the wrong axis.
    const statusLabel = within(row).getByTestId("health-status-label");
    expect(statusLabel.textContent ?? "").toMatch(/Unrecognized status/i);
    expect(statusLabel.textContent ?? "").not.toMatch(/Healthy/i);
    // And the raw value is surfaced so the operator can report it, rather
    // than being swallowed.
    expect(statusLabel.textContent ?? "").toMatch(/totally-made-up/);
  });

  it("renders the cooldown state with textual state, not a bare countdown", async () => {
    mockAccounts([account({ display_status: "cooling_down" })]);
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("health-account-acct-1")).toBeTruthy());

    const row = screen.getByTestId("health-account-acct-1");
    expect(row.textContent ?? "").toMatch(/Cooling down/i);
    // The accounts projection carries no retry-after, so the surface must say
    // the retry time is unknown rather than invent or omit one.
    expect(row.textContent ?? "").toMatch(/retry time unknown/i);
  });

  it("prompts the fix action for expired and calls the client function", async () => {
    let refreshCalls = 0;
    mockAccounts([account({ display_status: "expired", health_state: "expired" })], {
      "POST /api/control/v1/accounts/acct-1/health": () => {
        refreshCalls += 1;
        return jsonResponse(200, {
          data: { ...account({ display_status: "healthy", health_state: "healthy" }) },
        });
      },
    });
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("health-account-acct-1")).toBeTruthy());

    const fix = screen.getByRole("button", { name: /re-check credential/i });
    fireEvent.click(fix);
    await waitFor(() => expect(refreshCalls).toBe(1));
  });

  it("prompts the fix action for reauthenticating too", async () => {
    mockAccounts([account({ display_status: "reauthenticating", reauth_in_progress: true })]);
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("health-account-acct-1")).toBeTruthy());
    expect(screen.getByRole("button", { name: /re-check credential/i })).toBeTruthy();
  });

  it("does NOT offer the fix action for a healthy account", async () => {
    mockAccounts([account({ display_status: "healthy" })]);
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("health-account-acct-1")).toBeTruthy());
    expect(screen.queryByRole("button", { name: /re-check credential/i })).toBeNull();
  });

  it("shows the eligibility reason code when an account is ineligible", async () => {
    mockAccounts([
      account({ display_status: "degraded", eligibility: { eligible: false, reason: "credential_expired" } }),
    ]);
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("health-account-acct-1")).toBeTruthy());
    expect(screen.getByTestId("health-account-acct-1").textContent ?? "").toMatch(/credential_expired/);
  });

  it("reports when the last health check time is unknown rather than inventing one", async () => {
    mockAccounts([account({ last_health_check_at: undefined })]);
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("health-account-acct-1")).toBeTruthy());
    expect(screen.getByTestId("health-account-acct-1").textContent ?? "").toMatch(/never checked/i);
  });

  it("renders a loading state before the accounts arrive", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    expect(screen.getByRole("status").getAttribute("aria-label") ?? "").toMatch(/loading/i);
  });

  it("renders an empty state when there are no accounts", async () => {
    mockAccounts([]);
    const { container } = render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no connected accounts/i));
  });

  it("renders an error state instead of an empty list when the API fails", async () => {
    mockAccountsError(500, "internal", "internal error");
    const { container } = render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(container.textContent ?? "").toMatch(/could not load/i));
    expect(container.textContent ?? "").not.toMatch(/no connected accounts/i);
  });

  it("propagates a session expiry and renders nothing stale", async () => {
    const onSessionExpired = vi.fn();
    mockAccountsError(401, "session_expired", "session expired");
    const { container } = render(
      <TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={onSessionExpired} />,
    );
    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
    expect(container.textContent ?? "").not.toMatch(/Primary account/);
  });

  it("has no axe violations when populated", async () => {
    mockAccounts(
      DISPLAY_STATUSES.map((status, i) =>
        account({ id: `acct-${i}`, display_name: `Account ${i}`, display_status: status }),
      ),
    );
    const { container } = render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("health-account-acct-0")).toBeTruthy());
    await assertNoAxeViolations(container);
  });

  it("has no axe violations when empty", async () => {
    mockAccounts([]);
    const { container } = render(<TokenHealthSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no connected accounts/i));
    await assertNoAxeViolations(container);
  });
});
