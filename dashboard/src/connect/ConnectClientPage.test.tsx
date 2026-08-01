import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import ConnectClientPage from "./ConnectClientPage";
import { CLIENT_TARGETS, KEY_PLACEHOLDER } from "./generators";

const SETTINGS_URL = "GET /api/control/v1/settings";
const KEYS_URL = "GET /api/control/v1/keys";
const REAL_KEY = "vk_live_9f3c1d77aa2b4e6c8051d3f4b7e2a9c6";

function settingsBody(bind = "127.0.0.1:8081", dataPlaneBind: string | null = null) {
  return {
    data: {
      theme: "venom-dark",
      density: "comfortable",
      accent: "mono",
      radius_px: 6,
      spacing_scale: 1,
      enrichment_enabled: false,
      quota_staleness_seconds: 900,
      probe_max_in_flight_per_provider: 2,
      probe_expensive_enabled: false,
      probe_per_account_window_seconds: 3600,
      effective_config: { bind, data_plane_bind: dataPlaneBind },
    },
  };
}

function mockAll(overrides: Record<string, () => Response> = {}): void {
  vi.stubGlobal(
    "fetch",
    createFetchMock({
      [SETTINGS_URL]: () => jsonResponse(200, settingsBody()),
      [KEYS_URL]: () => jsonResponse(200, { data: [] }),
      ...overrides,
    }),
  );
}

function renderPage() {
  return render(<ConnectClientPage csrfToken="connect-csrf" onSessionExpired={vi.fn()} />);
}

/** Types a key into the masked input and opts into including it. */
async function optInWithKey(key: string): Promise<void> {
  fireEvent.change(screen.getByTestId("connect-key-input"), { target: { value: key } });
  await waitFor(() => expect(screen.getByTestId("connect-include-key")).toHaveProperty("disabled", false));
  fireEvent.click(screen.getByTestId("connect-include-key"));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
  sessionStorage.clear();
});

describe("ConnectClientPage — quick start", () => {
  it("shows the four quick-start steps", async () => {
    mockAll();
    renderPage();

    const quickStart = await screen.findByTestId("quick-start");
    const text = quickStart.textContent ?? "";
    expect(text).toMatch(/create an api key/i);
    expect(text).toMatch(/connect at least one provider/i);
    expect(text).toMatch(/point your client at/i);
    expect(text).toMatch(/diagnostics/i);
  });

  it("derives the base URL from effective_config rather than hardcoding the port", async () => {
    mockAll({ [SETTINGS_URL]: () => jsonResponse(200, settingsBody("127.0.0.1:7777")) });
    renderPage();

    await waitFor(() =>
      expect(screen.getByTestId("quick-start-base-url").textContent ?? "").toContain("7777"),
    );
    expect(screen.getByTestId("quick-start-base-url").textContent ?? "").not.toContain("8081");
  });

  it("prefers a dedicated data-plane bind when one is configured", async () => {
    mockAll({ [SETTINGS_URL]: () => jsonResponse(200, settingsBody("127.0.0.1:8081", "127.0.0.1:9090")) });
    renderPage();

    await waitFor(() =>
      expect(screen.getByTestId("quick-start-base-url").textContent ?? "").toContain("9090"),
    );
  });

  it("says so when it fell back to the documented default", async () => {
    // Showing the default silently would let an owner who moved the port paste an
    // address that cannot work, with no hint why.
    mockAll({
      [SETTINGS_URL]: () =>
        jsonResponse(500, {
          error: { code: "internal", message: "internal error", request_id: "r1", retryable: true },
        }),
    });
    renderPage();

    const note = await screen.findByTestId("base-url-fallback-note");
    expect(note.textContent ?? "").toMatch(/could not read the configured bind/i);
  });

  it("names the three tier model ids", async () => {
    mockAll();
    renderPage();

    const quickStart = await screen.findByTestId("quick-start");
    for (const id of ["venom/lite", "venom/pro", "venom/max"]) {
      expect(quickStart.textContent ?? "", `${id} must appear`).toContain(id);
    }
  });
});

describe("ConnectClientPage — the key is never in generated output by default", () => {
  it("shows the placeholder before any key is entered", async () => {
    mockAll();
    renderPage();

    const config = await screen.findByTestId("generated-config");
    expect(config.textContent ?? "").toContain(KEY_PLACEHOLDER);
  });

  it("STILL shows the placeholder when a key is entered but not opted into", async () => {
    // The load-bearing case: the key is in memory and must not reach the output.
    mockAll();
    const { container } = renderPage();
    await screen.findByTestId("generated-config");

    fireEvent.change(screen.getByTestId("connect-key-input"), { target: { value: REAL_KEY } });

    const config = screen.getByTestId("generated-config");
    expect(config.textContent ?? "").toContain(KEY_PLACEHOLDER);
    expect(config.textContent ?? "").not.toContain(REAL_KEY);
    // And it appears nowhere in the markup except its own masked input's value.
    const outsideInput = container.innerHTML.replace(
      new RegExp(`value="${REAL_KEY}"`, "g"),
      "value=\"REDACTED\"",
    );
    expect(outsideInput).not.toContain(REAL_KEY);
  });

  it("includes the key only after the explicit opt-in", async () => {
    mockAll();
    renderPage();
    await screen.findByTestId("generated-config");

    await optInWithKey(REAL_KEY);

    await waitFor(() =>
      expect(screen.getByTestId("generated-config").textContent ?? "").toContain(REAL_KEY),
    );
    expect(screen.getByTestId("generated-config").textContent ?? "").not.toContain(KEY_PLACEHOLDER);
  });

  it("cannot opt in without a key", async () => {
    mockAll();
    renderPage();
    await screen.findByTestId("generated-config");

    expect(screen.getByTestId("connect-include-key")).toHaveProperty("disabled", true);
  });

  it("never writes the key to localStorage or sessionStorage, even when opted in", async () => {
    mockAll();
    renderPage();
    await screen.findByTestId("generated-config");

    await optInWithKey(REAL_KEY);
    await waitFor(() =>
      expect(screen.getByTestId("generated-config").textContent ?? "").toContain(REAL_KEY),
    );

    const dump = JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } });
    expect(dump).not.toContain(REAL_KEY);
    expect(dump).not.toContain("vk_live_");
  });

  it("keeps the key out of the masked input's rendered TEXT", async () => {
    mockAll();
    const { container } = renderPage();
    await screen.findByTestId("generated-config");

    fireEvent.change(screen.getByTestId("connect-key-input"), { target: { value: REAL_KEY } });

    // textContent excludes input values, so this asserts the key is never printed
    // as page text.
    expect(container.textContent ?? "").not.toContain(REAL_KEY);
    expect((screen.getByTestId("connect-key-input") as HTMLInputElement).type).toBe("password");
  });
});

describe("ConnectClientPage — the client catalog", () => {
  it("offers every target in the catalog", async () => {
    mockAll();
    renderPage();

    await screen.findByTestId("client-catalog");
    for (const target of CLIENT_TARGETS) {
      expect(screen.getByRole("button", { name: target.label })).toBeTruthy();
    }
  });

  it("switches the generated config when a different target is chosen", async () => {
    mockAll();
    renderPage();
    await screen.findByTestId("generated-config");

    // The default target is Claude Code (Anthropic env names).
    expect(screen.getByTestId("generated-config").textContent ?? "").toContain("ANTHROPIC_BASE_URL");

    fireEvent.click(screen.getByRole("button", { name: "Codex" }));

    await waitFor(() =>
      expect(screen.getByTestId("generated-config").textContent ?? "").toContain("OPENAI_BASE_URL"),
    );
    expect(screen.getByTestId("generated-config").textContent ?? "").not.toContain("ANTHROPIC_BASE_URL");
  });

  it("explains why a target uses the shape it does", async () => {
    mockAll();
    renderPage();
    const note = await screen.findByTestId("target-note");
    expect((note.textContent ?? "").length).toBeGreaterThan(10);
  });
});

describe("ConnectClientPage — accessibility", () => {
  it("has no axe violations", async () => {
    mockAll();
    const { container } = renderPage();
    await screen.findByTestId("generated-config");
    await assertNoAxeViolations(container);
  });
});
