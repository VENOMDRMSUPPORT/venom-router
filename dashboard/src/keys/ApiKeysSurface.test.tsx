import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { ApiKeySummary } from "../api/controlClient";
import ApiKeysSurface from "./ApiKeysSurface";

const CSRF_TOKEN = "keys-csrf-token";
const KEYS_URL = "GET /api/control/v1/keys";
const CREATE_URL = "POST /api/control/v1/keys";

/** A realistic raw key. Every secret assertion below searches for THIS exact
 * string, so a leak anywhere is caught by value, not by shape. */
const RAW_KEY = "vk_live_9f3c1d77aa2b4e6c8051d3f4b7e2a9c6";

function key(overrides: Partial<ApiKeySummary> = {}): ApiKeySummary {
  return {
    id: "key-1",
    label: "Production",
    rpm_limit: 60,
    key_prefix: "vk_live_9f3c",
    created_at: 1_800_000_000,
    last_used_at: 1_800_003_600,
    revoked_at: null,
    ...overrides,
  };
}

function mockKeys(keys: ApiKeySummary[], extra: Record<string, () => Response> = {}): void {
  vi.stubGlobal(
    "fetch",
    createFetchMock({
      [KEYS_URL]: () => jsonResponse(200, { data: keys }),
      ...extra,
    }),
  );
}

function mockKeysError(status: number, code: string, message: string): void {
  vi.stubGlobal(
    "fetch",
    createFetchMock({
      [KEYS_URL]: () =>
        jsonResponse(status, { error: { code, message, request_id: "req-1", retryable: false } }),
    }),
  );
}

/** Fills the create form and submits it. */
async function submitCreate(label: string): Promise<void> {
  fireEvent.change(screen.getByLabelText(/label/i), { target: { value: label } });
  fireEvent.click(screen.getByRole("button", { name: /create key/i }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
  sessionStorage.clear();
});

describe("ApiKeysSurface", () => {
  it("lists a key's non-secret projection", async () => {
    mockKeys([key()]);
    render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText("Production")).toBeTruthy());

    const row = screen.getByTestId("api-key-key-1");
    const text = row.textContent ?? "";
    expect(text).toMatch(/vk_live_9f3c/);
    expect(text).toMatch(/60/);
  });

  it("never renders a raw key in the list", async () => {
    // The server cannot return raw_key on a list, but a client bug could
    // stash one from a create and render it here. Seed a list response that
    // ALSO carries a stray raw_key and assert it never reaches the DOM.
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [KEYS_URL]: () => jsonResponse(200, { data: [{ ...key(), raw_key: RAW_KEY }] }),
      }),
    );
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText("Production")).toBeTruthy());

    expect(container.innerHTML).not.toContain(RAW_KEY);
    // Not even a fragment beyond the published prefix.
    expect(container.innerHTML).not.toContain("9f3c1d77");
  });

  it("shows the raw key exactly once and clears it on dismiss", async () => {
    mockKeys([], {
      [CREATE_URL]: () =>
        jsonResponse(201, {
          data: { id: "key-new", label: "CI", rpm_limit: null, raw_key: RAW_KEY },
        }),
    });
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByRole("button", { name: /create key/i })).toBeTruthy());

    await submitCreate("CI");
    await waitFor(() => expect(container.textContent ?? "").toContain(RAW_KEY));
    expect(container.textContent ?? "").toMatch(/shown\s*once/i);

    fireEvent.click(screen.getByRole("button", { name: /i stored the key/i }));

    // After dismiss the raw value must be gone from the WHOLE container —
    // text and markup, not just the field that displayed it.
    await waitFor(() => expect(container.textContent ?? "").not.toContain(RAW_KEY));
    expect(container.innerHTML).not.toContain(RAW_KEY);
  });

  it("leaves no fragment of the raw key in localStorage or sessionStorage", async () => {
    mockKeys([], {
      [CREATE_URL]: () =>
        jsonResponse(201, {
          data: { id: "key-new", label: "CI", rpm_limit: null, raw_key: RAW_KEY },
        }),
    });
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByRole("button", { name: /create key/i })).toBeTruthy());

    await submitCreate("CI");
    await waitFor(() => expect(container.textContent ?? "").toContain(RAW_KEY));
    fireEvent.click(screen.getByRole("button", { name: /i stored the key/i }));
    await waitFor(() => expect(container.textContent ?? "").not.toContain(RAW_KEY));

    const dump = JSON.stringify({
      local: { ...localStorage },
      session: { ...sessionStorage },
    });
    expect(dump).not.toContain(RAW_KEY);
    expect(dump).not.toContain("9f3c1d77");
  });

  it("requires the confirmation before deleting, and cancelling calls nothing", async () => {
    let deleteCalls = 0;
    mockKeys([key()], {
      "DELETE /api/control/v1/keys/key-1": () => {
        deleteCalls += 1;
        return jsonResponse(200, { data: { id: "key-1", status: "revoked" } });
      },
    });
    render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText("Production")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /revoke/i }));
    // The dialog is open but nothing has been called yet.
    expect(deleteCalls).toBe(0);

    fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    expect(deleteCalls).toBe(0);

    // Re-open and confirm this time.
    fireEvent.click(screen.getByRole("button", { name: /revoke/i }));
    fireEvent.click(screen.getByRole("button", { name: /revoke key/i }));
    await waitFor(() => expect(deleteCalls).toBe(1));
  });

  it("renders an absent RPM limit as unlimited, never as 0", async () => {
    mockKeys([key({ id: "key-nolimit", rpm_limit: null })]);
    render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("api-key-key-nolimit")).toBeTruthy());

    const rpm = screen.getByTestId("api-key-rpm-key-nolimit");
    expect(rpm.textContent ?? "").not.toMatch(/\b0\b/);
    expect(rpm.textContent ?? "").toMatch(/no limit/i);
  });

  it("renders an unused key as never used, never as a fabricated timestamp", async () => {
    mockKeys([key({ id: "key-unused", last_used_at: null })]);
    render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("api-key-key-unused")).toBeTruthy());
    expect(screen.getByTestId("api-key-key-unused").textContent ?? "").toMatch(/never used/i);
  });

  it("marks a revoked key as revoked", async () => {
    mockKeys([key({ id: "key-revoked", revoked_at: 1_800_007_200 })]);
    render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("api-key-key-revoked")).toBeTruthy());
    expect(screen.getByTestId("api-key-key-revoked").textContent ?? "").toMatch(/revoked/i);
  });

  it("renders a typed field error from the API rather than a raw dump", async () => {
    mockKeys([], {
      [CREATE_URL]: () =>
        jsonResponse(400, {
          error: {
            code: "validation_error",
            message: "label is required",
            request_id: "req-9",
            retryable: false,
          },
        }),
    });
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByRole("button", { name: /create key/i })).toBeTruthy());

    await submitCreate("bad");
    await waitFor(() => expect(container.textContent ?? "").toMatch(/label is required/i));
    // The typed code is surfaced, and no JSON envelope is dumped.
    expect(container.textContent ?? "").toMatch(/validation_error/);
    expect(container.textContent ?? "").not.toMatch(/request_id/);
  });

  it("renders a loading state before the keys arrive", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    expect(screen.getByRole("status").getAttribute("aria-label") ?? "").toMatch(/loading/i);
  });

  it("renders an empty state when no keys exist", async () => {
    mockKeys([]);
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no api keys/i));
  });

  it("renders an error state instead of an empty list when the API fails", async () => {
    mockKeysError(500, "internal", "internal error");
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(container.textContent ?? "").toMatch(/could not load/i));
    expect(container.textContent ?? "").not.toMatch(/no api keys/i);
  });

  it("propagates a session expiry", async () => {
    const onSessionExpired = vi.fn();
    mockKeysError(401, "session_expired", "session expired");
    render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={onSessionExpired} />);
    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
  });

  it("has no axe violations on the list and create form", async () => {
    mockKeys([key(), key({ id: "key-2", label: "Staging", rpm_limit: null })]);
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByText("Production")).toBeTruthy());
    await assertNoAxeViolations(container);
  });

  it("has no axe violations in the one-time reveal state", async () => {
    mockKeys([], {
      [CREATE_URL]: () =>
        jsonResponse(201, {
          data: { id: "key-new", label: "CI", rpm_limit: null, raw_key: RAW_KEY },
        }),
    });
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(screen.getByRole("button", { name: /create key/i })).toBeTruthy());
    await submitCreate("CI");
    await waitFor(() => expect(container.textContent ?? "").toContain(RAW_KEY));
    await assertNoAxeViolations(container);
  });

  it("has no axe violations when empty", async () => {
    mockKeys([]);
    const { container } = render(<ApiKeysSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
    await waitFor(() => expect(container.textContent ?? "").toMatch(/no api keys/i));
    await assertNoAxeViolations(container);
  });
});
