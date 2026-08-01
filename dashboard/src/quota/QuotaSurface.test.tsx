import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { AccountProjection, QuotaWindow } from "../api/controlClient";
import QuotaSurface from "./QuotaSurface";

const ACCOUNTS_URL = "GET /api/control/v1/accounts?limit=200";

function window_(overrides: Partial<QuotaWindow> = {}): QuotaWindow {
  return {
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
    ...overrides,
  };
}

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
    quota: [window_()],
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

/** Installs a fetch mock returning the given accounts from GET /accounts. */
function mockAccounts(accounts: AccountProjection[]): void {
  vi.stubGlobal(
    "fetch",
    createFetchMock({
      [ACCOUNTS_URL]: () => jsonResponse(200, { data: { accounts } }),
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

describe("QuotaSurface", () => {
  // The state matrix (Design_System/states/state-matrix.md): every domain
  // state must render with TEXT, not colour alone, and each must be
  // distinguishable from the others by that text.
  const STATE_CASES: Array<{
    name: string;
    window: QuotaWindow;
    expectText: RegExp;
  }> = [
    {
      name: "available renders its figures",
      window: window_({ state: "available", used: 10, total: 100 }),
      expectText: /10\s*\/\s*100/,
    },
    {
      name: "insufficient is labelled",
      window: window_({ state: "insufficient", used: 95, total: 100 }),
      expectText: /insufficient/i,
    },
    {
      name: "exhausted is labelled",
      window: window_({ state: "exhausted", used: 100, total: 100 }),
      expectText: /exhausted/i,
    },
    {
      name: "unknown is labelled and never a number",
      window: window_({ state: "unknown", used: null, total: null, remaining: null }),
      expectText: /never rendered as a number/i,
    },
    {
      name: "stale says it is treated as unknown",
      window: window_({ state: "stale", freshness: "stale" }),
      expectText: /treated as unknown/i,
    },
  ];

  it.each(STATE_CASES)("$name", async ({ window: w, expectText }) => {
    mockAccounts([account({ quota: [w] })]);
    const { container } = render(<QuotaSurface onSessionExpired={vi.fn()} />);

    await waitFor(() => expect(screen.getByText(/Primary account/)).toBeTruthy());
    expect(container.textContent ?? "").toMatch(expectText);
  });

  it("renders unknown and stale with DIFFERENT text", async () => {
    mockAccounts([
      account({
        id: "acct-1",
        quota: [
          window_({ window_key: "unknown-window", state: "unknown", used: null, total: null }),
          window_({ window_key: "stale-window", state: "stale", freshness: "stale" }),
        ],
      }),
    ]);
    render(<QuotaSurface onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText(/Primary account/)).toBeTruthy());

    // Assert per WINDOW, not on the whole page: the design system renders
    // adjacent labels with no separating whitespace, so a page-wide regex
    // would be matching accidental substring joins rather than each window's
    // own wording.
    const unknownWindow = screen.getByTestId("quota-window-unknown-window");
    const staleWindow = screen.getByTestId("quota-window-stale-window");

    expect(unknownWindow.textContent ?? "").toMatch(/never rendered as a number/i);
    expect(staleWindow.textContent ?? "").toMatch(/treated as unknown/i);

    // ...and the two must not collapse onto the same wording. This is the
    // assertion that fails if `stale` is mapped to the `unknown` badge.
    expect(staleWindow.textContent ?? "").not.toMatch(/never rendered as a number/i);
    expect(unknownWindow.textContent ?? "").not.toMatch(/treated as unknown/i);
  });

  it("never fabricates a number for a window with no total", async () => {
    // The realistic fabrication case. The server reports state `available`
    // (it derived headroom from `remaining`) while `total` and `used` are
    // genuinely unknown. A meter that coerced those nulls to 0 would render
    // "0 / 0" — a fabricated, and actively misleading, pair of numbers.
    //
    // A `state: "unknown"` window would NOT prove this: QuotaWindowMeter
    // short-circuits on the state before it ever looks at `total`, so the
    // coercion would be invisible.
    mockAccounts([
      account({
        quota: [
          window_({
            window_key: "no-total",
            state: "available",
            used: null,
            remaining: 90,
            total: null,
            limit_value: null,
          }),
        ],
      }),
    ]);
    const { container } = render(<QuotaSurface onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText(/Primary account/)).toBeTruthy());

    const meter = container.querySelector("[data-testid='quota-window-no-total']");
    expect(meter).toBeTruthy();
    const text = meter?.textContent ?? "";
    // A nil used/total must never surface as a number in that window.
    expect(text).not.toMatch(/0\s*\/\s*0/);
    expect(text).toMatch(/never rendered as a number/i);
    // And the meter element itself must not claim a numeric reading.
    expect(meter?.querySelector("[role='meter']")).toBeNull();
  });

  it("distinguishes local-safety budgets from provider evidence", async () => {
    mockAccounts([
      account({
        quota: [
          window_({ source: "provider_evidence", window_key: "5h" }),
          window_({
            source: "local_safety",
            window_type: "concurrency",
            window_key: "concurrency",
            unit: "concurrency",
            limit_value: 4,
            reserved: 1,
          }),
        ],
      }),
    ]);
    const { container } = render(<QuotaSurface onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText(/Primary account/)).toBeTruthy());

    const text = container.textContent ?? "";
    expect(text).toMatch(/provider_evidence|provider evidence/i);
    expect(text).toMatch(/local safety/i);
  });

  it("renders a loading state before the accounts arrive", () => {
    // A fetch that never settles keeps the surface in its loading state.
    // The design system's Spinner carries its wording in aria-label (it has
    // no visible text), so this asserts the ACCESSIBLE name.
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    render(<QuotaSurface onSessionExpired={vi.fn()} />);
    const status = screen.getByRole("status");
    expect(status.getAttribute("aria-label") ?? "").toMatch(/loading/i);
  });

  it("renders an empty state when there are no accounts at all", async () => {
    mockAccounts([]);
    const { container } = render(<QuotaSurface onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no connected accounts/i));
  });

  it("renders a per-account empty state when an account tracks no windows", async () => {
    mockAccounts([account({ quota: [] })]);
    const { container } = render(<QuotaSurface onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText(/Primary account/)).toBeTruthy());
    expect(container.textContent ?? "").toMatch(/no quota evidence/i);
  });

  it("renders an error state instead of an empty list when the API fails", async () => {
    mockAccountsError(500, "internal", "internal error");
    const { container } = render(<QuotaSurface onSessionExpired={vi.fn()} />);

    await waitFor(() => expect(container.textContent ?? "").toMatch(/could not load/i));
    // An error must NEVER be presented as "no quota".
    expect(container.textContent ?? "").not.toMatch(/no connected accounts/i);
  });

  it("propagates a session expiry and renders nothing stale", async () => {
    const onSessionExpired = vi.fn();
    mockAccountsError(401, "session_expired", "session expired");
    const { container } = render(<QuotaSurface onSessionExpired={onSessionExpired} />);

    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
    expect(container.textContent ?? "").not.toMatch(/Primary account/);
    expect(container.textContent ?? "").not.toMatch(/could not load/i);
  });

  it("has no axe violations when populated", async () => {
    mockAccounts([
      account({
        quota: [
          window_({ window_key: "5h" }),
          window_({ window_key: "unknown-window", state: "unknown", used: null, total: null }),
          window_({ window_key: "stale-window", state: "stale", freshness: "stale" }),
        ],
      }),
    ]);
    const { container } = render(<QuotaSurface onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText(/Primary account/)).toBeTruthy());
    await assertNoAxeViolations(container);
  });

  it("has no axe violations when empty", async () => {
    mockAccounts([]);
    const { container } = render(<QuotaSurface onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no connected accounts/i));
    await assertNoAxeViolations(container);
  });

  it("groups windows under their own account", async () => {
    mockAccounts([
      account({ id: "acct-1", display_name: "Account One", quota: [window_({ window_key: "one" })] }),
      account({ id: "acct-2", display_name: "Account Two", quota: [window_({ window_key: "two" })] }),
    ]);
    render(<QuotaSurface onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText(/Account Two/)).toBeTruthy());

    const one = screen.getByTestId("quota-account-acct-1");
    const two = screen.getByTestId("quota-account-acct-2");
    expect(within(one).getByText(/one/)).toBeTruthy();
    expect(within(two).getByText(/two/)).toBeTruthy();
    expect(within(one).queryByText(/^two$/)).toBeNull();
  });
});
