import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import { DENSITY_LABELS, THEME_LABELS } from "../theme-runtime";
import AppShell from "./AppShell";
import { NAV, NAV_GROUPS } from "./nav";

const SESSION = {
  idleExpiresAt: new Date(Date.now() + 24 * 60_000).toISOString(),
  absoluteExpiresAt: new Date(Date.now() + 9 * 60 * 60_000).toISOString(),
};
const CSRF_TOKEN = "shell-csrf-token";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function baseHandlers(overrides: Record<string, () => Response> = {}) {
  return createFetchMock({
    "GET /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-dark", density: "comfortable" } }),
    "POST /api/control/v1/auth/logout": () => jsonResponse(200, { data: { logged_out: true } }),
    // The Providers nav destination mounts the real Provider Fleet
    // (P2b-UI-003) — these two are here purely so a test that happens to
    // navigate there doesn't hit an unmapped-route throw; the fleet's own
    // behavior is covered in src/fleet/*.test.tsx.
    "GET /api/control/v1/providers": () => jsonResponse(200, { data: { providers: [] } }),
    "GET /api/control/v1/accounts?limit=200": () => jsonResponse(200, { data: { accounts: [] } }),
    ...overrides,
  });
}

describe("AppShell — navigation", () => {
  it("renders all four nav groups and every item within them", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />);

    await screen.findByRole("link", { name: /overview/i });

    const nav = within(screen.getByRole("navigation", { name: /primary/i }));
    for (const group of NAV_GROUPS) {
      nav.getByText(group, { selector: ".vn-nav-group" });
    }
    for (const item of NAV) {
      nav.getByRole("link", { name: new RegExp(item.label, "i") });
    }
  });

  it("marks the active nav item with aria-current=page and switching nav updates it", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />);

    const overviewLink = await screen.findByRole("link", { name: /overview/i });
    expect(overviewLink.getAttribute("aria-current")).toBe("page");

    const providersLink = screen.getByRole("link", { name: /providers/i });
    expect(providersLink.getAttribute("aria-current")).toBeNull();

    fireEvent.click(providersLink);

    expect(screen.getByRole("link", { name: /providers/i }).getAttribute("aria-current")).toBe("page");
    expect(screen.getByRole("link", { name: /overview/i }).getAttribute("aria-current")).toBeNull();
  });
});

describe("AppShell — settings restore + persistence", () => {
  it("calls GET /settings on mount and applies the returned theme/density before rendering content", async () => {
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-light", density: "compact" } }),
      }),
    );

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />);

    await screen.findByRole("link", { name: /overview/i });

    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-light");
    expect(document.documentElement.getAttribute("data-density")).toBe("compact");
  });

  it("falls back to the already-applied defaults and shows a non-blocking notice when GET /settings fails", async () => {
    document.documentElement.setAttribute("data-theme", "venom-dark");
    document.documentElement.setAttribute("data-density", "comfortable");

    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/settings": () =>
          jsonResponse(500, { error: { code: "internal", message: "internal error", request_id: "r1", retryable: true } }),
      }),
    );

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />);

    await screen.findByRole("link", { name: /overview/i });
    await screen.findByText(/could not load your saved appearance settings/i);

    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-dark");
    expect(document.documentElement.getAttribute("data-density")).toBe("comfortable");
  });

  it("changing the ThemeSwitcher applies immediately and PUTs /settings with X-CSRF-Token", async () => {
    const fetchMock = baseHandlers({
      "PUT /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-hc", density: "comfortable" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />);
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("radio", { name: THEME_LABELS["venom-hc"] }));

    // Applied immediately, before the PUT settles.
    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-hc");

    await waitFor(() => {
      const putCall = fetchMock.mock.calls.find(
        ([input, init]) => String(input) === "/api/control/v1/settings" && init?.method === "PUT",
      );
      expect(putCall).toBeTruthy();
    });

    const putCall = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/control/v1/settings" && init?.method === "PUT",
    );
    const [, init] = putCall as [unknown, RequestInit & { headers: Record<string, string> }];
    expect(init.headers["X-CSRF-Token"]).toBe(CSRF_TOKEN);
    expect(JSON.parse(init.body as string)).toEqual({ theme: "venom-hc", density: "comfortable" });
  });

  it("changing the DensityToggle applies immediately and PUTs /settings", async () => {
    const fetchMock = baseHandlers({
      "PUT /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-dark", density: "compact" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />);
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("radio", { name: DENSITY_LABELS["compact"] }));

    expect(document.documentElement.getAttribute("data-density")).toBe("compact");

    await waitFor(() => {
      const putCall = fetchMock.mock.calls.find(
        ([input, init]) => String(input) === "/api/control/v1/settings" && init?.method === "PUT",
      );
      expect(putCall).toBeTruthy();
    });
  });

  it("routes a session_expired PUT /settings response to onSessionExpired", async () => {
    const onSessionExpired = vi.fn();
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "PUT /api/control/v1/settings": () =>
          jsonResponse(401, { error: { code: "session_expired", message: "session expired", request_id: "r2", retryable: false } }),
      }),
    );

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={onSessionExpired} onLoggedOut={vi.fn()} />);
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("radio", { name: THEME_LABELS["venom-light"] }));

    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
  });
});

describe("AppShell — sign out", () => {
  it("calls logout then onLoggedOut", async () => {
    const onLoggedOut = vi.fn();
    const fetchMock = baseHandlers();
    vi.stubGlobal("fetch", fetchMock);

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={onLoggedOut} />);
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("button", { name: /owner menu/i }));
    fireEvent.click(await screen.findByText(/sign out/i));

    await waitFor(() => expect(onLoggedOut).toHaveBeenCalledTimes(1));

    const logoutCall = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/auth/logout"));
    expect(logoutCall).toBeTruthy();
  });
});

describe("AppShell — accessibility and storage guarantees", () => {
  it("has zero axe violations once settings have resolved", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    const { container } = render(
      <AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />,
    );
    await screen.findByRole("link", { name: /overview/i });

    await assertNoAxeViolations(container);
  });

  it("never writes to localStorage or sessionStorage across mount, nav, theme/density changes", async () => {
    const setItemSpy = vi.spyOn(Storage.prototype, "setItem");
    const fetchMock = baseHandlers({
      "PUT /api/control/v1/settings": () => jsonResponse(200, { data: { theme: "venom-hc", density: "compact" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />);
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("link", { name: /providers/i }));
    fireEvent.click(screen.getByRole("radio", { name: THEME_LABELS["venom-hc"] }));
    fireEvent.click(screen.getByRole("radio", { name: DENSITY_LABELS["compact"] }));

    await waitFor(() => {
      const putCalls = fetchMock.mock.calls.filter(
        ([input, init]) => String(input) === "/api/control/v1/settings" && init?.method === "PUT",
      );
      expect(putCalls.length).toBeGreaterThan(0);
    });

    expect(setItemSpy).not.toHaveBeenCalled();
  });
});
