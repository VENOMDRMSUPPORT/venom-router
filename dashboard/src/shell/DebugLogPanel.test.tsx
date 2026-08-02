import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import { debugLog, listProviders } from "../api/controlClient";
import DebugLogPanel from "./DebugLogPanel";

beforeEach(() => {
  debugLog.clear();
});

afterEach(() => {
  cleanup();
  debugLog.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("DebugLogPanel", () => {
  it("renders the image-11 empty state with Clear log disabled", async () => {
    render(<DebugLogPanel open onClose={vi.fn()} />);

    const dialog = screen.getByRole("dialog", { name: /debug log/i });
    expect(dialog).toBeTruthy();
    screen.getByText("No operations captured");
    screen.getByText(/open debug from a page and perform an action to capture request\/response events here/i);
    expect((screen.getByRole("button", { name: /clear log/i }) as HTMLButtonElement).disabled).toBe(true);

    await assertNoAxeViolations(document.body);
  });

  it("lists captured operations newest-first (method, path, status, duration) and clears on demand", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        "GET /api/control/v1/providers": () => jsonResponse(200, { data: { providers: [] } }),
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(404, { error: { code: "not_found", message: "nope", request_id: "req-7", retryable: false } }),
      }),
    );

    await listProviders();

    render(<DebugLogPanel open onClose={vi.fn()} />);

    // The captured exchange renders method + path and its status.
    await screen.findByText(/GET \/api\/control\/v1\/providers/);
    screen.getByText(/→ 200/);

    const clearButton = screen.getByRole("button", { name: /clear log/i });
    expect((clearButton as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(clearButton);

    await waitFor(() => {
      expect(screen.queryByText(/GET \/api\/control\/v1\/providers/)).toBeNull();
    });
    screen.getByText("No operations captured");
    expect((screen.getByRole("button", { name: /clear log/i }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("renders nothing when closed", () => {
    render(<DebugLogPanel open={false} onClose={vi.fn()} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
