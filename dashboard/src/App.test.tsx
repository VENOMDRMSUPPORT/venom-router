import { afterEach, describe, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { createFetchMock, jsonResponse } from "./test/fetchMock";
import App from "./App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// P2b-UI-002: App now renders the owner-auth gate (AuthGate) rather than
// SmokeInventory directly — this just proves the wiring: App reaches all
// the way down to a real auth screen driven by a mocked GET /auth/status.
describe("App", () => {
  it("renders the First-run setup screen when GET /auth/status reports setup incomplete", async () => {
    const fetchMock = createFetchMock({
      "GET /api/control/v1/auth/status": () => jsonResponse(200, { data: { setup_complete: false } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<App />);

    await screen.findByText(/welcome to venom router/i);
  });
});
