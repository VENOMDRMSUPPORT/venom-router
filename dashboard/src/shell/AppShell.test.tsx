import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import AppShell from "./AppShell";
import { breadcrumbTrail, NAV, NAV_GROUPS } from "./nav";

const SESSION = {
  idleExpiresAt: new Date(Date.now() + 24 * 60_000).toISOString(),
  absoluteExpiresAt: new Date(Date.now() + 9 * 60 * 60_000).toISOString(),
};
const CSRF_TOKEN = "shell-csrf-token";

/** Builds "<n>px" from a number — raw px string literals are banned by the
 * no-raw-values lint gate. */
function px(n: number): string {
  return `${n}px`;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function baseHandlers(overrides: Record<string, () => Response> = {}) {
  return createFetchMock({
    "GET /api/control/v1/settings": () =>
      jsonResponse(200, {
        data: {
          theme: "venom-dark",
          density: "comfortable",
          accent: "mono",
          radius_px: 6,
          spacing_scale: 1,
        },
      }),
    "POST /api/control/v1/auth/logout": () => jsonResponse(200, { data: { logged_out: true } }),
    // The Providers nav destination mounts the real Provider Fleet
    // (P2b-UI-003) — these two are here purely so a test that happens to
    // navigate there doesn't hit an unmapped-route throw; the fleet's own
    // behavior is covered in src/fleet/*.test.tsx.
    "GET /api/control/v1/providers": () => jsonResponse(200, { data: { providers: [] } }),
    "GET /api/control/v1/accounts?limit=200": () => jsonResponse(200, { data: { accounts: [] } }),
    // Same rationale for the Models destination (P6-UI-002) and the
    // review-queue banner it renders (P6-UI-012) — their own behavior is
    // covered in src/models/*.test.tsx.
    "GET /api/control/v1/models?limit=200": () => jsonResponse(200, { data: [] }),
    "GET /api/control/v1/certifications/review": () =>
      jsonResponse(200, {
        data: {
          scanned: 0,
          limit: 50,
          truncated: false,
          evaluated_reasons: ["capability_not_certified"],
          not_evaluated_reasons: [
            "identity_unresolved",
            "context_unverified",
            "funding_unknown",
            "no_healthy_account",
            "quota_exhausted",
            "quota_insufficient",
            "cooling_down",
          ],
          by_reason: [{ reason: "capability_not_certified", count: 0 }],
        },
      }),
    ...overrides,
  });
}

describe("AppShell — navigation", () => {
  it("renders all four nav groups and every item within them", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );

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

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );

    const overviewLink = await screen.findByRole("link", { name: /overview/i });
    expect(overviewLink.getAttribute("aria-current")).toBe("page");

    const providersLink = screen.getByRole("link", { name: /providers/i });
    expect(providersLink.getAttribute("aria-current")).toBeNull();

    fireEvent.click(providersLink);

    expect(screen.getByRole("link", { name: /providers/i }).getAttribute("aria-current")).toBe(
      "page",
    );
    expect(screen.getByRole("link", { name: /overview/i }).getAttribute("aria-current")).toBeNull();
  });
});

describe("AppShell — surface mounting", () => {
  // Each of these proves a real surface is mounted on an EXISTING nav key
  // (nav.ts is never extended by a surface unit), and — just as importantly —
  // that the "coming in a later phase" placeholder is gone for that key. A
  // surface built but never wired is invisible, and the placeholder is exactly
  // what that failure looks like.
  it("mounts the real Models surface on the models nav key", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("link", { name: /^models$/i }));

    // The Models surface's own empty state, not the shell placeholder.
    await screen.findByText(/no models discovered/i);
    expect(screen.queryByText(/coming in a later phase/i)).toBeNull();
  });
});

describe("AppShell — settings restore + persistence", () => {
  it("calls GET /settings on mount and applies ALL FIVE returned fields before rendering content", async () => {
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/settings": () =>
          jsonResponse(200, {
            data: {
              theme: "venom-light",
              density: "compact",
              accent: "amber",
              radius_px: 14,
              spacing_scale: 0.8,
            },
          }),
      }),
    );

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );

    await screen.findByRole("link", { name: /overview/i });

    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-light");
    expect(document.documentElement.getAttribute("data-density")).toBe("compact");
    expect(document.documentElement.getAttribute("data-accent")).toBe("amber");
    expect(document.documentElement.style.getPropertyValue("--vn-radius-base")).toBe(px(14));
    expect(document.documentElement.style.getPropertyValue("--vn-spacing-scale")).toBe("0.8");
  });

  it("falls back to mono / 6 px / 1 when the settings payload omits the customizer fields", async () => {
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/settings": () =>
          jsonResponse(200, { data: { theme: "venom-dark", density: "comfortable" } }),
      }),
    );

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );

    await screen.findByRole("link", { name: /overview/i });

    expect(document.documentElement.getAttribute("data-accent")).toBe("mono");
    expect(document.documentElement.style.getPropertyValue("--vn-radius-base")).toBe(px(6));
    expect(document.documentElement.style.getPropertyValue("--vn-spacing-scale")).toBe("1");
  });

  it("clicking an accent swatch in the customizer applies data-accent and PUTs the full five-field body", async () => {
    const fetchMock = baseHandlers({
      "PUT /api/control/v1/settings": () =>
        jsonResponse(200, {
          data: {
            theme: "venom-dark",
            density: "comfortable",
            accent: "emerald",
            radius_px: 6,
            spacing_scale: 1,
          },
        }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("button", { name: /customize design system/i }));
    fireEvent.click(screen.getByRole("button", { name: /emerald accent/i }));

    // Applied immediately, before the PUT settles.
    expect(document.documentElement.getAttribute("data-accent")).toBe("emerald");

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
    expect(JSON.parse(init.body as string)).toEqual({
      theme: "venom-dark",
      density: "comfortable",
      accent: "emerald",
      radius_px: 6,
      spacing_scale: 1,
    });
  });

  it("falls back to the already-applied defaults and shows a non-blocking notice when GET /settings fails", async () => {
    document.documentElement.setAttribute("data-theme", "venom-dark");
    document.documentElement.setAttribute("data-density", "comfortable");

    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/settings": () =>
          jsonResponse(500, {
            error: {
              code: "internal",
              message: "internal error",
              request_id: "r1",
              retryable: true,
            },
          }),
      }),
    );

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );

    await screen.findByRole("link", { name: /overview/i });
    await screen.findByText(/could not load your saved appearance settings/i);

    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-dark");
    expect(document.documentElement.getAttribute("data-density")).toBe("comfortable");
  });

  it("clicking the header theme toggle flips dark -> light, applies immediately and PUTs /settings with X-CSRF-Token", async () => {
    const fetchMock = baseHandlers({
      "PUT /api/control/v1/settings": () =>
        jsonResponse(200, { data: { theme: "venom-light", density: "comfortable" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    // Current theme is venom-dark, so the toggle offers light.
    fireEvent.click(screen.getByRole("button", { name: /switch to light mode/i }));

    // Applied immediately, before the PUT settles — and the toggle now
    // offers the way back.
    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-light");
    screen.getByRole("button", { name: /switch to dark mode/i });

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
    // PUT /settings is the FULL five-field appearance contract — the
    // untouched customizer fields ride along at their defaults.
    expect(JSON.parse(init.body as string)).toEqual({
      theme: "venom-light",
      density: "comfortable",
      accent: "mono",
      radius_px: 6,
      spacing_scale: 1,
    });
  });

  it("treats venom-hc as a dark appearance (toggle offers light) and toggling back from light lands on venom-dark", async () => {
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/settings": () =>
          jsonResponse(200, { data: { theme: "venom-hc", density: "comfortable" } }),
        "PUT /api/control/v1/settings": () =>
          jsonResponse(200, { data: { theme: "venom-light", density: "comfortable" } }),
      }),
    );

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("button", { name: /switch to light mode/i }));
    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-light");

    fireEvent.click(screen.getByRole("button", { name: /switch to dark mode/i }));
    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-dark");
  });

  it("renders no density control in the header — density is boot-applied from settings only (owner request)", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    const header = screen.getByRole("banner");
    expect(header.querySelector(".vn-density-toggle")).toBeNull();
    expect(screen.queryByRole("radiogroup", { name: /density/i })).toBeNull();
    // The persisted setting still lands on the document root at boot.
    expect(document.documentElement.getAttribute("data-density")).toBe("comfortable");
  });

  it("routes a session_expired PUT /settings response to onSessionExpired", async () => {
    const onSessionExpired = vi.fn();
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "PUT /api/control/v1/settings": () =>
          jsonResponse(401, {
            error: {
              code: "session_expired",
              message: "session expired",
              request_id: "r2",
              retryable: false,
            },
          }),
      }),
    );

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={onSessionExpired}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("button", { name: /switch to light mode/i }));

    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
  });
});

describe("AppShell — sign out", () => {
  it("calls logout then onLoggedOut", async () => {
    const onLoggedOut = vi.fn();
    const fetchMock = baseHandlers();
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={onLoggedOut}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("button", { name: /owner menu/i }));
    fireEvent.click(await screen.findByText(/sign out/i));

    await waitFor(() => expect(onLoggedOut).toHaveBeenCalledTimes(1));

    const logoutCall = fetchMock.mock.calls.find(([input]) =>
      String(input).endsWith("/auth/logout"),
    );
    expect(logoutCall).toBeTruthy();
  });
});

describe("AppShell — shared chrome header (per-page metadata)", () => {
  it("renders the page title, one-line description, and accent icon tile from the nav metadata", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    const header = within(screen.getByRole("banner"));
    expect(header.getByRole("heading", { level: 1 }).textContent).toBe("Overview");
    header.getByText("Local runtime health and delivery status.");
    // The icon tile carries the page's own glyph, accent-tinted.
    expect(screen.getByRole("banner").querySelector(".vn-icon--layout-dashboard")).toBeTruthy();
  });

  it("updates title/description/icon when navigating to another page", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("link", { name: /quota & limits/i }));

    const header = within(screen.getByRole("banner"));
    expect(header.getByRole("heading", { level: 1 }).textContent).toBe("Quota & Limits");
    header.getByText("Quota windows, reservations, and cooldowns per account.");
    expect(screen.getByRole("banner").querySelector(".vn-icon--gauge")).toBeTruthy();
  });

  it("every nav item ships the header metadata (non-empty description + icon)", () => {
    for (const item of NAV) {
      expect(item.description.trim().length, `description for ${item.key}`).toBeGreaterThan(0);
      expect(item.icon.trim().length, `icon for ${item.key}`).toBeGreaterThan(0);
    }
  });

  it("the notification bell opens an honest empty state — one disabled entry, no feed", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("button", { name: /notifications/i }));

    const menu = screen.getByRole("menu");
    const items = within(menu).getAllByRole("menuitem");
    expect(items).toHaveLength(1);
    expect(items[0].textContent).toMatch(/no notifications yet/i);
    expect(items[0]).toHaveProperty("disabled", true);
  });
});

describe("AppShell — global breadcrumb", () => {
  it("shows the fleet chips beside the breadcrumb on the Providers page only", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    // Not on Overview.
    expect(screen.queryByText("All Integrations")).toBeNull();

    fireEvent.click(screen.getByRole("link", { name: /providers/i }));

    // Rendered once the fleet's live counts load (empty fixtures -> 0/0).
    await screen.findByText("Active Providers");
    screen.getByText("All Integrations");

    // Gone again after navigating away.
    fireEvent.click(screen.getByRole("link", { name: /overview/i }));
    expect(screen.queryByText("All Integrations")).toBeNull();
  });

  it("toggles the fleet view between Active and All via the breadcrumb chips", async () => {
    vi.stubGlobal(
      "fetch",
      baseHandlers({
        "GET /api/control/v1/providers": () =>
          jsonResponse(200, {
            data: {
              providers: [
                { id: "opencode-zen", display_name: "OpenCode Zen", description: "API-key.", auth_mode: "api_key", funding: { mode: "owner_policy", locked: false, non_expiring: false, fixed: null }, capabilities: [], configured: true, missing_env: [] },
                { id: "antigravity", display_name: "Antigravity", description: "OAuth.", auth_mode: "oauth2", funding: { mode: "owner_policy", locked: false, non_expiring: false, fixed: null }, capabilities: [], configured: true, missing_env: [] },
              ],
            },
          }),
        "GET /api/control/v1/accounts?limit=200": () =>
          jsonResponse(200, {
            data: {
              accounts: [
                { id: "acct-1", provider: "opencode-zen", external_id: "ext-1", auth_type: "api_key", connection_state: "connected", health_state: "healthy", reauth_in_progress: false, identity: { email: undefined, plan: "Free" }, funding: { funding: "free", source: "owner_policy", locked: false, version: "v1" }, display_status: "healthy", eligibility: { eligible: true }, quota: [], created_at: "2026-07-01T00:00:00Z", updated_at: "2026-07-01T00:00:00Z" },
              ],
            },
          }),
      }),
    );

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    // Wait for the shell to finish loading settings before interacting.
    await screen.findByRole("link", { name: /overview/i });
    fireEvent.click(screen.getByRole("link", { name: /providers/i }));

    // Default view is "all": both providers render.
    await screen.findByText("OpenCode Zen");
    screen.getByText("Antigravity");

    // "All Integrations" is selected by default; "Active Providers" is not.
    expect(screen.getByRole("button", { name: /all integrations/i }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: /active providers/i }).getAttribute("aria-pressed")).toBe("false");

    // Click "Active Providers" — the grid narrows to connected providers only.
    fireEvent.click(screen.getByRole("button", { name: /active providers/i }));
    await waitFor(() => expect(screen.queryByText("Antigravity")).toBeNull());
    screen.getByText("OpenCode Zen");

    // Selection followed the click.
    expect(screen.getByRole("button", { name: /active providers/i }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: /all integrations/i }).getAttribute("aria-pressed")).toBe("false");

    // Click "All Integrations" — the full catalog returns.
    fireEvent.click(screen.getByRole("button", { name: /all integrations/i }));
    await waitFor(() => screen.getByText("Antigravity"));
    screen.getByText("OpenCode Zen");
  });

  it("renders Dashboard / <leaf> on Overview with the leaf marked aria-current=page", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    const crumbs = screen.getByRole("navigation", { name: /breadcrumb/i });
    within(crumbs).getByText("Dashboard");
    const leaf = crumbs.querySelector('[aria-current="page"]');
    expect(leaf?.textContent).toBe("Overview");
  });

  it("includes the nav group as the middle crumb on grouped pages", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("link", { name: /providers/i }));

    const crumbs = screen.getByRole("navigation", { name: /breadcrumb/i });
    within(crumbs).getByText("Dashboard");
    within(crumbs).getByText("Operate");
    expect(crumbs.querySelector('[aria-current="page"]')?.textContent).toBe("Providers");
  });

  it("derives the trail from the same nav metadata for every page", () => {
    for (const item of NAV) {
      const trail = breadcrumbTrail(item);
      expect(trail[0]).toBe("Dashboard");
      expect(trail[trail.length - 1]).toBe(item.label);
      if (item.group !== item.label) expect(trail).toContain(item.group);
    }
  });

  it("clicking the Dashboard crumb navigates back to Overview", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("link", { name: /providers/i }));
    expect(screen.getByRole("link", { name: /providers/i }).getAttribute("aria-current")).toBe(
      "page",
    );

    const crumbs = screen.getByRole("navigation", { name: /breadcrumb/i });
    fireEvent.click(within(crumbs).getByText("Dashboard"));

    expect(screen.getByRole("link", { name: /overview/i }).getAttribute("aria-current")).toBe(
      "page",
    );
  });
});

describe("AppShell — sidebar brand", () => {
  it("renders the wordmark with the AI Control Center slogan and an accent-tinted mark", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    const nav = screen.getByRole("navigation", { name: /primary/i });
    const brand = nav.querySelector(".vn-nav-brand") as HTMLElement;
    expect(brand.textContent).toContain("Venom Router");
    expect(brand.textContent).toContain("AI Control Center");
    // The mark inherits the live accent via the text-accent-text token class.
    const mark = brand.querySelector(".vn-icon--route") as HTMLElement;
    expect(mark.className).toContain("text-accent-text");
  });
});

describe("AppShell — accessibility and storage guarantees", () => {
  it("has zero axe violations once settings have resolved", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    const { container } = render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    await assertNoAxeViolations(container);
  });

  it("never writes to localStorage or sessionStorage across mount, nav, and theme changes", async () => {
    const setItemSpy = vi.spyOn(Storage.prototype, "setItem");
    const fetchMock = baseHandlers({
      "PUT /api/control/v1/settings": () =>
        jsonResponse(200, { data: { theme: "venom-hc", density: "compact" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("link", { name: /overview/i });

    fireEvent.click(screen.getByRole("link", { name: /providers/i }));
    fireEvent.click(screen.getByRole("button", { name: /switch to light mode/i }));

    await waitFor(() => {
      const putCalls = fetchMock.mock.calls.filter(
        ([input, init]) => String(input) === "/api/control/v1/settings" && init?.method === "PUT",
      );
      expect(putCalls.length).toBeGreaterThan(0);
    });

    expect(setItemSpy).not.toHaveBeenCalled();
  });
});
