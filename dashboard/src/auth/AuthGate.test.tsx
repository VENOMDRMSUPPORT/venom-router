import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import AuthGate from "./AuthGate";

const SESSION_TIMES = { idle_expires_at: "2026-07-24T00:30:00Z", absolute_expires_at: "2026-07-24T12:00:00Z" };

// No @testing-library/jest-dom in this stack — these two helpers stand in
// for its `toHaveValue`/`toBeDisabled` matchers against plain DOM nodes.
function inputValue(el: HTMLElement): string {
  return (el as HTMLInputElement).value;
}

function isDisabled(el: HTMLElement): boolean {
  return (el as HTMLButtonElement | HTMLInputElement).disabled;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("AuthGate — first-run setup", () => {
  it("renders First-run setup when status says setup is incomplete, and a successful setup enters the authenticated area", async () => {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: false } }),
      "POST /api/control/v1/auth/setup": () =>
        jsonResponse(200, { data: { session: SESSION_TIMES }, csrf_token: "csrf-setup-token" }),
      "GET /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-dark", density: "comfortable" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);

    await screen.findByText(/welcome to venom router/i);

    const password = "a-brand-new-owner-password";
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: password } });
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: /create password/i }));

    // Cleared immediately at submit time, not only once the request settles.
    expect(inputValue(screen.getByLabelText(/owner password/i))).toBe("");
    expect(inputValue(screen.getByLabelText(/confirm password/i))).toBe("");

    // P2b-UI-001: the authenticated area is now the real app shell (an
    // AuthenticatedArea placeholder no longer exists) — its owner menu is
    // the reachable, stable proof that AuthGate entered the authenticated
    // state.
    await screen.findByRole("button", { name: /owner menu/i });

    expect(document.body.innerHTML).not.toContain(password);
  });

  it("rejects a too-short password client-side without calling the API", async () => {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: false } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);
    await screen.findByText(/welcome to venom router/i);

    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: "short" } });
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: "short" } });
    fireEvent.click(screen.getByRole("button", { name: /create password/i }));

    await screen.findByText(/at least 12 characters/i);
    expect(fetchMock).toHaveBeenCalledTimes(1); // only the initial status GET
  });
});

describe("AuthGate — login", () => {
  it("renders Login when setup is complete but there is no live session, and a successful login enters the authenticated area", async () => {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: true } }),
      "GET /api/control/v1/auth/session": () =>
        jsonResponse(401, { error: { code: "session_expired", message: "session expired", request_id: "r1", retryable: false } }),
      "POST /api/control/v1/auth/login": () =>
        jsonResponse(200, { data: { session: SESSION_TIMES }, csrf_token: "csrf-login-token" }),
      "GET /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-dark", density: "comfortable" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);

    await screen.findByRole("button", { name: /sign in/i });

    const password = "the-owners-real-password";
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    expect(inputValue(screen.getByLabelText(/owner password/i))).toBe("");

    // P2b-UI-001: the real app shell's owner menu is the reachable proof
    // of the authenticated state (see the first-run test's own note).
    await screen.findByRole("button", { name: /owner menu/i });

    expect(document.body.innerHTML).not.toContain(password);
  });

  it("shows a generic invalid_credentials error on a wrong password", async () => {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: true } }),
      "GET /api/control/v1/auth/session": () =>
        jsonResponse(401, { error: { code: "session_expired", message: "session expired", request_id: "r1", retryable: false } }),
      "POST /api/control/v1/auth/login": () =>
        jsonResponse(401, { error: { code: "invalid_credentials", message: "invalid credentials", request_id: "r2", retryable: false } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);
    await screen.findByRole("button", { name: /sign in/i });

    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: "wrong-guess" } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await screen.findByText(/invalid_credentials/i);
    // The exact same generic message regardless of whether setup exists —
    // never a hint like "no owner configured yet".
    screen.getByText(/invalid credentials/i); // throws if absent — assertion by presence
    expect(screen.queryByText(/setup/i)).toBeNull();
  });

  it("locks out after retry_after, disabling submit until it elapses", async () => {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: true } }),
      "GET /api/control/v1/auth/session": () =>
        jsonResponse(401, { error: { code: "session_expired", message: "session expired", request_id: "r1", retryable: false } }),
      "POST /api/control/v1/auth/login": () =>
        jsonResponse(429, {
          error: {
            code: "locked_out",
            message: "too many failed attempts, try again later",
            request_id: "r3",
            retryable: true,
            retry_after: 5,
          },
        }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);
    await screen.findByRole("button", { name: /sign in/i });

    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: "does-not-matter" } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await screen.findByText(/locked_out/i);
    // Match the ticking countdown loosely (any digit count) rather than a
    // specific second — the real 1-second interval can already have
    // ticked once by the time this assertion runs.
    await screen.findByText(/retry after \d+s/);

    const submitButton = screen.getByRole("button", { name: /sign in/i });
    expect(isDisabled(submitButton)).toBe(true);
    // No secret survives a locked-out response either.
    expect(inputValue(screen.getByLabelText(/owner password/i))).toBe("");

    await new Promise((resolve) => setTimeout(resolve, 5200));

    // The field was cleared on submit — repopulate it to isolate "does the
    // lockout itself still block submit" from "is the field simply empty".
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: "a-fresh-attempt" } });
    await waitFor(() => expect(isDisabled(submitButton)).toBe(false));
  }, 10000);
});

// P2b-UI-001 note: AuthenticatedArea's "Run sensitive action (stub)" demo
// button (and the reverify gate it exercised) is gone along with that
// placeholder component — the real app shell (AppShell) has no such demo
// surface. The underlying guarantee this block used to prove via that
// stub — "an authenticated mutating call that comes back session_expired
// routes back to Login with no secret retained, and a mutating call
// correctly threads the session's exact CSRF token" — is still real and
// still proven below, now via the shell's own real mutating call
// (PUT /settings, from a ThemeSwitcher change) instead of the removed
// demo. The reverify-specific gate itself (reverification_required ->
// ReverifyModal -> reverify -> success/locked_out) is exercised by its
// real production caller now: the credential-reveal flow in
// src/fleet/ProviderFleet.test.tsx (P2b-UI-003), which reuses this same
// ReverifyModal component.
describe("AuthGate — session expiry via an authenticated mutating call", () => {
  async function renderAuthenticated(extraHandlers: Record<string, () => Response> = {}) {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: true } }),
      "GET /api/control/v1/auth/session": () =>
        jsonResponse(200, { data: { session: SESSION_TIMES }, csrf_token: "csrf-live-token" }),
      "GET /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-dark", density: "comfortable" } }),
      ...extraHandlers,
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);
    await screen.findByRole("button", { name: /owner menu/i });
    return fetchMock;
  }

  it("routes back to Login when an authenticated PUT /settings comes back session_expired", async () => {
    await renderAuthenticated({
      "PUT /api/control/v1/settings": () =>
        jsonResponse(401, { error: { code: "session_expired", message: "session expired", request_id: "r4", retryable: false } }),
    });

    fireEvent.click(screen.getByRole("radio", { name: /light/i }));

    await screen.findByRole("button", { name: /sign in/i });

    expect(screen.queryByRole("button", { name: /owner menu/i })).toBeNull();
  });

  it("sends the exact session CSRF token on an authenticated PUT /settings call", async () => {
    const fetchMock = await renderAuthenticated({
      "PUT /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-light", density: "comfortable" } }),
    });

    fireEvent.click(screen.getByRole("radio", { name: /light/i }));

    // There are two /settings calls (the initial GET, then this PUT); take
    // the one whose init carries method PUT.
    await waitFor(() => {
      const putCall = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "PUT");
      expect(putCall).toBeTruthy();
    });

    const putCall = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "PUT");
    const [, init] = putCall as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(init.headers["X-CSRF-Token"]).toBe("csrf-live-token");
  });
});

describe("AuthGate — no secret ever reaches console output", () => {
  it("never logs any password across setup, sign-out, login, and reverify", async () => {
    const consoleMethods = ["log", "info", "warn", "error", "debug"] as const;
    const spies = consoleMethods.map((method) => vi.spyOn(console, method).mockImplementation(() => {}));

    const setupPassword = "NEVERLOG-SETUP-PASSWORD-1";
    const loginPassword = "NEVERLOG-LOGIN-PASSWORD-2";

    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: false } }),
      "POST /api/control/v1/auth/setup": () =>
        jsonResponse(200, { data: { session: SESSION_TIMES }, csrf_token: "csrf-setup" }),
      "POST /api/control/v1/auth/logout": () => jsonResponse(200, { data: { logged_out: true } }),
      "POST /api/control/v1/auth/login": () =>
        jsonResponse(200, { data: { session: SESSION_TIMES }, csrf_token: "csrf-login" }),
      "GET /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-dark", density: "comfortable" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);

    await screen.findByText(/welcome to venom router/i);
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: setupPassword } });
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: setupPassword } });
    fireEvent.click(screen.getByRole("button", { name: /create password/i }));
    await screen.findByRole("button", { name: /owner menu/i });

    fireEvent.click(screen.getByRole("button", { name: /owner menu/i }));
    fireEvent.click(await screen.findByText(/sign out/i));
    await screen.findByRole("button", { name: /sign in/i });

    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: loginPassword } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    await screen.findByRole("button", { name: /owner menu/i });

    const secrets = [setupPassword, loginPassword];
    for (const spy of spies) {
      for (const call of spy.mock.calls) {
        for (const arg of call) {
          if (typeof arg === "string") {
            for (const secret of secrets) {
              expect(arg).not.toContain(secret);
            }
          }
        }
      }
    }
    expect(document.body.innerHTML).not.toContain(setupPassword);
    expect(document.body.innerHTML).not.toContain(loginPassword);
  });
});
