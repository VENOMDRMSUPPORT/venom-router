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

    await screen.findByRole("button", { name: /sign out/i });

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
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);

    await screen.findByRole("button", { name: /sign in/i });

    const password = "the-owners-real-password";
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    expect(inputValue(screen.getByLabelText(/owner password/i))).toBe("");

    await screen.findByRole("button", { name: /sign out/i });

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

describe("AuthGate — session expiry and reverify", () => {
  async function renderAuthenticated(extraHandlers: Record<string, () => Response> = {}) {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: true } }),
      "GET /api/control/v1/auth/session": () =>
        jsonResponse(200, { data: { session: SESSION_TIMES }, csrf_token: "csrf-live-token" }),
      ...extraHandlers,
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);
    await screen.findByRole("button", { name: /sign out/i });
    return fetchMock;
  }

  it("routes back to Login when a reverify call comes back session_expired, retaining no secret", async () => {
    await renderAuthenticated({
      "POST /api/control/v1/auth/reverify": () =>
        jsonResponse(401, { error: { code: "session_expired", message: "session expired", request_id: "r4", retryable: false } }),
    });

    fireEvent.click(screen.getByRole("button", { name: /run sensitive action/i }));
    const dialogPasswordInput = await screen.findByLabelText(/owner password/i);

    const password = "password-typed-right-before-expiry";
    fireEvent.change(dialogPasswordInput, { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    await screen.findByRole("button", { name: /sign in/i });

    expect(screen.queryByRole("button", { name: /sign out/i })).toBeNull();
    expect(document.body.innerHTML).not.toContain(password);
  });

  it("gates the stub sensitive action behind reverify success, sending the exact session CSRF token", async () => {
    const fetchMock = await renderAuthenticated({
      "POST /api/control/v1/auth/reverify": () =>
        jsonResponse(200, { data: { reverify_fresh_until: "2026-07-24T00:05:00Z" } }),
    });

    expect(screen.queryByText(/sensitive action executed/i)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /run sensitive action/i }));
    const dialogPasswordInput = await screen.findByLabelText(/owner password/i);
    fireEvent.change(dialogPasswordInput, { target: { value: "owner-password-for-reverify" } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    await screen.findByText(/sensitive action executed/i);

    const reverifyCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/auth/reverify"));
    expect(reverifyCall).toBeTruthy();
    const [, init] = reverifyCall as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(init.headers["X-CSRF-Token"]).toBe("csrf-live-token");
  });

  it("shows a locked-out reverify state with retry_after and never reveals the password", async () => {
    await renderAuthenticated({
      "POST /api/control/v1/auth/reverify": () =>
        jsonResponse(429, {
          error: {
            code: "locked_out",
            message: "too many failed attempts, try again later",
            request_id: "r5",
            retryable: true,
            retry_after: 60,
          },
        }),
    });

    fireEvent.click(screen.getByRole("button", { name: /run sensitive action/i }));
    const dialogPasswordInput = await screen.findByLabelText(/owner password/i);
    const password = "wrong-reverify-password";
    fireEvent.change(dialogPasswordInput, { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));

    await screen.findByText(/too many failed attempts/i);
    await screen.findByText(/60s/);
    expect(document.body.innerHTML).not.toContain(password);
  });
});

describe("AuthGate — no secret ever reaches console output", () => {
  it("never logs any password across setup, sign-out, login, and reverify", async () => {
    const consoleMethods = ["log", "info", "warn", "error", "debug"] as const;
    const spies = consoleMethods.map((method) => vi.spyOn(console, method).mockImplementation(() => {}));

    const setupPassword = "NEVERLOG-SETUP-PASSWORD-1";
    const loginPassword = "NEVERLOG-LOGIN-PASSWORD-2";
    const reverifyPassword = "NEVERLOG-REVERIFY-PASSWORD-3";

    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: false } }),
      "POST /api/control/v1/auth/setup": () =>
        jsonResponse(200, { data: { session: SESSION_TIMES }, csrf_token: "csrf-setup" }),
      "POST /api/control/v1/auth/logout": () => jsonResponse(200, { data: { logged_out: true } }),
      "POST /api/control/v1/auth/login": () =>
        jsonResponse(200, { data: { session: SESSION_TIMES }, csrf_token: "csrf-login" }),
      "POST /api/control/v1/auth/reverify": () =>
        jsonResponse(200, { data: { reverify_fresh_until: "2026-07-24T00:05:00Z" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AuthGate />);

    await screen.findByText(/welcome to venom router/i);
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: setupPassword } });
    fireEvent.change(screen.getByLabelText(/confirm password/i), { target: { value: setupPassword } });
    fireEvent.click(screen.getByRole("button", { name: /create password/i }));
    await screen.findByRole("button", { name: /sign out/i });

    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));
    await screen.findByRole("button", { name: /sign in/i });

    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: loginPassword } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    await screen.findByRole("button", { name: /sign out/i });

    fireEvent.click(screen.getByRole("button", { name: /run sensitive action/i }));
    const dialogPasswordInput = await screen.findByLabelText(/owner password/i);
    fireEvent.change(dialogPasswordInput, { target: { value: reverifyPassword } });
    fireEvent.click(screen.getByRole("button", { name: /^verify$/i }));
    await screen.findByText(/sensitive action executed/i);

    const secrets = [setupPassword, loginPassword, reverifyPassword];
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
    expect(document.body.innerHTML).not.toContain(reverifyPassword);
  });
});
