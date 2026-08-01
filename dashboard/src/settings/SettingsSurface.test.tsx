import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { THEMES } from "@venom/design-system/themes";
import { ACCENTS } from "@venom/design-system/customizer";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { FullSettings } from "../api/controlClient";
import SettingsSurface from "./SettingsSurface";

const CSRF_TOKEN = "settings-csrf-token";
const SETTINGS_URL = "GET /api/control/v1/settings";
const PUT_URL = "PUT /api/control/v1/settings";

function settings(overrides: Partial<FullSettings> = {}): FullSettings {
  return {
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
    effective_config: { bind: "127.0.0.1:8081", data_plane_bind: null },
    ...overrides,
  };
}

function mockSettings(extra: Record<string, () => Response> = {}, body = settings()): ReturnType<typeof createFetchMock> {
  const mock = createFetchMock({
    [SETTINGS_URL]: () => jsonResponse(200, { data: body }),
    [PUT_URL]: () => jsonResponse(200, { data: body }),
    ...extra,
  });
  vi.stubGlobal("fetch", mock);
  return mock;
}

function renderSurface() {
  return render(<SettingsSurface csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} />);
}

/** Reads the body of the single PUT the surface issued. */
function putBody(mock: ReturnType<typeof createFetchMock>): Record<string, unknown> {
  const call = mock.mock.calls.find(
    ([input, init]) => String(input) === "/api/control/v1/settings" && (init as RequestInit)?.method === "PUT",
  );
  if (!call) throw new Error("no PUT was issued");
  return JSON.parse((call[1] as RequestInit).body as string);
}

async function saveAndSettle(mock: ReturnType<typeof createFetchMock>): Promise<Record<string, unknown>> {
  fireEvent.click(screen.getByRole("button", { name: /save/i }));
  await waitFor(() => putBody(mock));
  return putBody(mock);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("SettingsSurface — round trip", () => {
  it("loads the current settings into the form", async () => {
    mockSettings();
    renderSurface();

    await waitFor(() => expect(screen.getByLabelText(/theme/i)).toHaveProperty("value", "venom-dark"));
    expect(screen.getByLabelText(/density/i)).toHaveProperty("value", "comfortable");
    expect(screen.getByLabelText(/quota staleness/i)).toHaveProperty("value", "900");
  });

  it("PUTs an edited appearance field and reflects the server's answer", async () => {
    const mock = mockSettings({}, settings());
    renderSurface();
    await waitFor(() => screen.getByLabelText(/theme/i));

    fireEvent.change(screen.getByLabelText(/theme/i), { target: { value: "venom-light" } });
    const body = await saveAndSettle(mock);

    expect(body.theme).toBe("venom-light");
    // The five appearance fields are REQUIRED by the PUT contract
    // (internal/httpapi/settings.go's settingsUpdateRequest) — they are not
    // pointers there, so a partial appearance body is a 400.
    for (const required of ["theme", "density", "accent", "radius_px", "spacing_scale"]) {
      expect(body, `${required} is required by the contract`).toHaveProperty(required);
    }
  });

  it("PUTs an edited operational field as a number", async () => {
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/quota staleness/i));

    fireEvent.change(screen.getByLabelText(/quota staleness/i), { target: { value: "1200" } });
    const body = await saveAndSettle(mock);

    expect(body.quota_staleness_seconds).toBe(1200);
  });

  it("sends the CSRF token", async () => {
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/theme/i));

    fireEvent.change(screen.getByLabelText(/theme/i), { target: { value: "venom-light" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => {
      const call = mock.mock.calls.find(
        ([input, init]) => String(input) === "/api/control/v1/settings" && (init as RequestInit)?.method === "PUT",
      );
      expect(call).toBeTruthy();
    });
    const call = mock.mock.calls.find(
      ([input, init]) => String(input) === "/api/control/v1/settings" && (init as RequestInit)?.method === "PUT",
    ) as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(call[1].headers["X-CSRF-Token"]).toBe(CSRF_TOKEN);
  });
});

describe("SettingsSurface — the PUT contract's optional-pointer semantics", () => {
  // The operational fields are POINTERS server-side and "absent means leave
  // unchanged, never reset to the default". Sending an untouched field back —
  // even at its current value — turns a read into a write, and sending it as an
  // explicit null is a 400. Both are avoided by omitting it entirely.
  it("omits every operational field the owner did not touch", async () => {
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/theme/i));

    // Touch ONLY the theme.
    fireEvent.change(screen.getByLabelText(/theme/i), { target: { value: "venom-light" } });
    const body = await saveAndSettle(mock);

    for (const operational of [
      "enrichment_enabled",
      "quota_staleness_seconds",
      "probe_max_in_flight_per_provider",
      "probe_expensive_enabled",
      "probe_per_account_window_seconds",
    ]) {
      expect(
        Object.prototype.hasOwnProperty.call(body, operational),
        `${operational} was not touched, so it must be OMITTED (absent = unchanged); sending it makes a read into a write`,
      ).toBe(false);
    }
  });

  it("never sends an untouched optional field as an explicit null", async () => {
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/theme/i));

    fireEvent.change(screen.getByLabelText(/theme/i), { target: { value: "venom-light" } });
    const body = await saveAndSettle(mock);

    // A JSON null on any of these is a 400 by contract.
    for (const [key, value] of Object.entries(body)) {
      expect(value, `${key} must never be null`).not.toBeNull();
    }
  });

  it("includes only the operational fields that WERE touched", async () => {
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/probe max in flight/i));

    fireEvent.change(screen.getByLabelText(/probe max in flight/i), { target: { value: "5" } });
    const body = await saveAndSettle(mock);

    expect(body.probe_max_in_flight_per_provider).toBe(5);
    expect(Object.prototype.hasOwnProperty.call(body, "quota_staleness_seconds")).toBe(false);
  });
});

describe("SettingsSurface — effective_config is read-only", () => {
  it("displays the binds", async () => {
    mockSettings({}, settings({ effective_config: { bind: "127.0.0.1:9000", data_plane_bind: "0.0.0.0:9001" } }));
    renderSurface();

    const panel = await screen.findByTestId("effective-config");
    expect(panel.textContent ?? "").toContain("127.0.0.1:9000");
    expect(panel.textContent ?? "").toContain("0.0.0.0:9001");
  });

  it("renders an absent data-plane bind as sharing the control listener, not as blank", async () => {
    mockSettings();
    renderSurface();

    const panel = await screen.findByTestId("effective-config");
    // config.Config.DataPlaneBind's documented default meaning.
    expect(panel.textContent ?? "").toMatch(/shares the control listener|not configured/i);
  });

  it("NEVER submits effective_config", async () => {
    // It is read-only by contract — a PUT carrying it is a 400, and it is not a
    // settable field in the first place.
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/theme/i));

    fireEvent.change(screen.getByLabelText(/theme/i), { target: { value: "venom-light" } });
    const body = await saveAndSettle(mock);

    expect(Object.prototype.hasOwnProperty.call(body, "effective_config")).toBe(false);
    expect(Object.prototype.hasOwnProperty.call(body, "bind")).toBe(false);
    expect(Object.prototype.hasOwnProperty.call(body, "data_plane_bind")).toBe(false);
  });

  it("offers no editing control for the binds", async () => {
    mockSettings();
    renderSurface();

    const panel = await screen.findByTestId("effective-config");
    expect(panel.querySelectorAll("input").length).toBe(0);
    expect(panel.querySelectorAll("select").length).toBe(0);
    expect(panel.textContent ?? "").toMatch(/read-only|restart|command line/i);
  });
});

describe("SettingsSurface — vocabularies come from the package, not a literal", () => {
  // Another agent owns the theme vocabulary. Hardcoding a list here would either
  // offer a theme the server rejects or hide one it accepts, and the failure would
  // be silent either way.
  it("offers exactly the design system's THEMES", async () => {
    mockSettings();
    renderSurface();

    const select = (await waitFor(() => screen.getByLabelText(/theme/i))) as HTMLSelectElement;
    const offered = [...select.options].map((o) => o.value).sort();
    expect(offered).toEqual([...THEMES].sort());
  });

  it("offers exactly the design system's ACCENTS", async () => {
    mockSettings();
    renderSurface();

    const select = (await waitFor(() => screen.getByLabelText(/accent/i))) as HTMLSelectElement;
    const offered = [...select.options].map((o) => o.value).sort();
    expect(offered).toEqual([...ACCENTS].sort());
  });
});

describe("SettingsSurface — validation errors", () => {
  it("renders a field-named API error inline on THAT field, not as a page banner", async () => {
    // The edited value is one the FORM accepts (8 is in range), so the request is
    // actually sent and the inline error can only come from attributing the API's
    // message. Using an out-of-range value here would let the form's own bound
    // check produce the same field error, and the test would pass even with
    // attribution disabled — it would prove nothing.
    mockSettings({
      [PUT_URL]: () =>
        jsonResponse(400, {
          error: {
            code: "validation_error",
            message: "radius_px must be an integer between 0 and 16",
            request_id: "r1",
            retryable: false,
          },
        }),
    });
    renderSurface();
    await waitFor(() => screen.getByLabelText(/theme/i));

    fireEvent.change(screen.getByLabelText(/corner radius/i), { target: { value: "8" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    // The message lands on the field the API named.
    const field = await screen.findByTestId("field-error-radius_px");
    expect(field.textContent ?? "").toMatch(/between 0 and 16/i);
    // And NOT as an unattributed page-level banner.
    expect(screen.queryByTestId("settings-form-error")).toBeNull();
  });

  it("falls back to a form-level error when the API's message names no known field", async () => {
    mockSettings({
      [PUT_URL]: () =>
        jsonResponse(400, {
          error: { code: "validation_error", message: "invalid request body", request_id: "r1", retryable: false },
        }),
    });
    renderSurface();
    await waitFor(() => screen.getByLabelText(/theme/i));

    fireEvent.change(screen.getByLabelText(/theme/i), { target: { value: "venom-light" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    // Unattributable, so it must NOT be pinned to an arbitrary field.
    const banner = await screen.findByTestId("settings-form-error");
    expect(banner.textContent ?? "").toMatch(/invalid request body/i);
  });

  it("refuses an out-of-range value in the form, while still deferring to the API's answer", async () => {
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/corner radius/i));

    fireEvent.change(screen.getByLabelText(/corner radius/i), { target: { value: "99" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    // The form caught it, so nothing was sent.
    await waitFor(() => expect(screen.getByTestId("field-error-radius_px").textContent ?? "").toMatch(/0 and 16/));
    expect(
      mock.mock.calls.find(
        ([input, init]) => String(input) === "/api/control/v1/settings" && (init as RequestInit)?.method === "PUT",
      ),
    ).toBeUndefined();
  });
});

describe("SettingsSurface — confirmation on a restart-affecting change", () => {
  it("confirms before enabling enrichment, and does not PUT until confirmed", async () => {
    // Enrichment turns on outbound calls to an external metadata source, so it is
    // not a silent appearance tweak.
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/enrichment/i));

    fireEvent.click(screen.getByLabelText(/enrichment/i));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    // A dialog stands between the click and the write.
    await screen.findByRole("dialog");
    expect(
      mock.mock.calls.find(
        ([input, init]) => String(input) === "/api/control/v1/settings" && (init as RequestInit)?.method === "PUT",
      ),
    ).toBeUndefined();

    fireEvent.click(screen.getByRole("button", { name: /^apply|^confirm/i }));
    await waitFor(() => expect(putBody(mock).enrichment_enabled).toBe(true));
  });

  it("cancelling the confirmation writes nothing", async () => {
    const mock = mockSettings();
    renderSurface();
    await waitFor(() => screen.getByLabelText(/enrichment/i));

    fireEvent.click(screen.getByLabelText(/enrichment/i));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    await screen.findByRole("dialog");
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));

    expect(
      mock.mock.calls.find(
        ([input, init]) => String(input) === "/api/control/v1/settings" && (init as RequestInit)?.method === "PUT",
      ),
    ).toBeUndefined();
  });
});

describe("SettingsSurface — loading, error, a11y", () => {
  it("renders a loading state before the settings arrive", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    renderSurface();
    expect(screen.getByRole("status").getAttribute("aria-label") ?? "").toMatch(/loading/i);
  });

  it("renders an error state rather than an empty form when the load fails", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [SETTINGS_URL]: () =>
          jsonResponse(500, {
            error: { code: "internal", message: "internal error", request_id: "r1", retryable: true },
          }),
      }),
    );
    const { container } = renderSurface();

    await waitFor(() => expect(container.textContent ?? "").toMatch(/could not load/i));
    // An empty form would invite the owner to submit defaults over their real config.
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });

  it("propagates a session expiry", async () => {
    const onSessionExpired = vi.fn();
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [SETTINGS_URL]: () =>
          jsonResponse(401, {
            error: { code: "session_expired", message: "session expired", request_id: "r2", retryable: false },
          }),
      }),
    );
    render(<SettingsSurface csrfToken={CSRF_TOKEN} onSessionExpired={onSessionExpired} />);
    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
  });

  it("has no axe violations", async () => {
    mockSettings();
    const { container } = renderSurface();
    await waitFor(() => screen.getByLabelText(/theme/i));
    await assertNoAxeViolations(container);
  });
});
