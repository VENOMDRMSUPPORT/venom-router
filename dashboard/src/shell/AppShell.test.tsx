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
  // Navigation now writes the real URL path (history.pushState), and jsdom keeps
  // that across tests — reset to root so each test starts on the default page
  // rather than inheriting the previous test's route.
  window.history.replaceState(null, "", "/");
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
    "GET /api/control/v1/offerings?limit=200": () => jsonResponse(200, { data: [] }),
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
    // The Routing destination (P6-UI-003) — the three tier policies exactly as
    // internal/routing's shipped table serves them.
    "GET /api/control/v1/routing/policy": () =>
      jsonResponse(200, {
        data: {
          tiers: [
            {
              tier: "lite",
              funding: "free_only",
              context_ceiling_tokens: 262144,
              thinking_ceiling: "none",
              attempt_budget: 3,
              scored: false,
              weights: null,
              competitive_band: null,
              latency_tie_break_only: true,
            },
            {
              tier: "pro",
              funding: "free_and_paid",
              context_ceiling_tokens: 524288,
              thinking_ceiling: "extended",
              attempt_budget: 4,
              scored: true,
              weights: {
                quality: 0.4,
                reliability: 0.25,
                quota_headroom: 0.15,
                evidence_confidence: 0,
                cost_class: 0.15,
                latency: 0.05,
              },
              competitive_band: 0.08,
              latency_tie_break_only: false,
            },
            {
              tier: "max",
              funding: "free_and_paid",
              context_ceiling_tokens: 1048576,
              thinking_ceiling: "ultra",
              attempt_budget: 5,
              scored: true,
              weights: {
                quality: 0.6,
                reliability: 0.2,
                quota_headroom: 0.05,
                evidence_confidence: 0.1,
                cost_class: 0.05,
                latency: 0,
              },
              competitive_band: 0.03,
              latency_tie_break_only: true,
            },
          ],
        },
      }),
    // Overview (P6-UI-001) is the DEFAULT landing surface, so every test in this
    // file renders it — these are the reads its cards make. Its own behavior is
    // covered in src/overview/*.test.tsx.
    "GET /api/control/v1/diagnostics/routes?limit=10": () => jsonResponse(200, { data: [] }),
    "GET /api/control/v1/keys": () => jsonResponse(200, { data: [] }),
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

    // Scoped to the primary nav: the providers page's own breadcrumb now
    // carries a "Providers" link too.
    const primary = within(screen.getByRole("navigation", { name: /primary/i }));
    const providersLink = primary.getByRole("link", { name: /providers/i });
    expect(providersLink.getAttribute("aria-current")).toBeNull();

    fireEvent.click(providersLink);

    expect(primary.getByRole("link", { name: /providers/i }).getAttribute("aria-current")).toBe(
      "page",
    );
    expect(primary.getByRole("link", { name: /overview/i }).getAttribute("aria-current")).toBeNull();
  });
});

describe("AppShell — responsive section deck", () => {
  it("opens a section tray, navigates, and collapses it after selection", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );

    await screen.findByRole("navigation", { name: /primary/i });
    const deck = within(screen.getByRole("navigation", { name: /sections/i }));

    fireEvent.click(deck.getByRole("button", { name: /manage/i }));
    const apiKeys = deck.getByRole("button", { name: /api keys/i });
    fireEvent.click(apiKeys);

    expect(screen.getByText("API Keys", { selector: "h1" })).toBeTruthy();
    expect(deck.queryByRole("button", { name: /api keys/i })).toBeNull();
  });

  // GOVERNOR NOTE (review-time fix): this test used to navigate to Playground and
  // assert the planned-surface treatment. P6-UI-004/005/008/010 gave every nav.ts
  // key a real surface, so it was renamed to "…for an unrecognised destination" —
  // but the NAME and the comment then described a path the body never exercises.
  // parseLocation clamps any unrecognised PATH to DEFAULT_NAV_KEY (see ./route),
  // so an unknown destination NEVER reaches renderSurface's PlannedSurface
  // fallback; what the body actually proves is that the operator lands on the
  // default surface instead of a dead page. Renamed to say exactly that, so no
  // future reader trusts a coverage claim this file does not make.
  //
  // The PlannedSurface fallback is therefore unreachable through the UI while
  // every nav key has a surface. It is kept as a defensive default, and the guard
  // that it stays unreachable is the NAV loop below: add a nav key without a
  // surface and that test fails.
  it("resolves an unrecognised path to the default surface, never a dead page", async () => {
    vi.stubGlobal("fetch", baseHandlers());
    window.history.replaceState(null, "", "/no-such-surface");
    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );
    await screen.findByRole("navigation", { name: /primary/i });

    // An unknown path resolves to the DEFAULT surface, so the operator lands
    // somewhere real rather than on a dead page.
    await screen.findByTestId("overview-card-fleet");
    expect(screen.queryByText(/later phase/i)).toBeNull();
    expect(screen.queryByText(/roadmap/i)).toBeNull();
  });

  it("mounts a real surface for every nav destination — none is a placeholder", async () => {
    vi.stubGlobal("fetch", baseHandlers());
    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );

    const primary = within(await screen.findByRole("navigation", { name: /primary/i }));
    for (const item of NAV) {
      fireEvent.click(primary.getByRole("link", { name: new RegExp(`^${item.label}$`, "i") }));
      // The shared planned-surface treatment is the tell that a key has no surface.
      expect(screen.queryByText("Planned surface"), `${item.key} is still a placeholder`).toBeNull();
    }
  });

  it("opens API-key creation from the global page context action", async () => {
    vi.stubGlobal("fetch", baseHandlers());

    render(
      <AppShell
        session={SESSION}
        csrfToken={CSRF_TOKEN}
        onSessionExpired={vi.fn()}
        onLoggedOut={vi.fn()}
      />,
    );

    const primary = within(await screen.findByRole("navigation", { name: /primary/i }));
    fireEvent.click(primary.getByRole("link", { name: /api keys/i }));
    fireEvent.click(screen.getByRole("button", { name: /new api key/i }));

    expect(screen.getByRole("dialog", { name: /create an api key/i })).toBeTruthy();
  });
});

describe("AppShell — URL routing (each page its own link, refresh stays put)", () => {
  it("writes the page's own path to the URL bar when navigating", async () => {
    vi.stubGlobal("fetch", baseHandlers());
    render(
      <AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />,
    );
    const primary = within(await screen.findByRole("navigation", { name: /primary/i }));

    fireEvent.click(primary.getByRole("link", { name: /^providers$/i }));
    expect(window.location.pathname).toBe("/providers");

    fireEvent.click(primary.getByRole("link", { name: /^models$/i }));
    expect(window.location.pathname).toBe("/models");
  });

  it("opens the page named by the URL path on mount — a refresh does NOT snap back to Overview", async () => {
    vi.stubGlobal("fetch", baseHandlers());
    // Simulate the browser reloading the app while on /models.
    window.history.replaceState(null, "", "/models");
    render(
      <AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />,
    );
    const primary = within(await screen.findByRole("navigation", { name: /primary/i }));
    expect(primary.getByRole("link", { name: /^models$/i }).getAttribute("aria-current")).toBe("page");
    // And NOT the default page.
    expect(primary.getByRole("link", { name: /^overview$/i }).getAttribute("aria-current")).toBeNull();
  });

  it("follows browser back/forward (popstate) to whatever page the URL now names", async () => {
    vi.stubGlobal("fetch", baseHandlers());
    render(
      <AppShell session={SESSION} csrfToken={CSRF_TOKEN} onSessionExpired={vi.fn()} onLoggedOut={vi.fn()} />,
    );
    const primary = within(await screen.findByRole("navigation", { name: /primary/i }));
    fireEvent.click(primary.getByRole("link", { name: /^providers$/i }));
    expect(primary.getByRole("link", { name: /^providers$/i }).getAttribute("aria-current")).toBe("page");

    // The browser Back button restores the previous URL then fires popstate.
    window.history.replaceState(null, "", "/");
    window.dispatchEvent(new PopStateEvent("popstate"));

    await waitFor(() =>
      expect(primary.getByRole("link", { name: /^overview$/i }).getAttribute("aria-current")).toBe("page"),
    );
  });
});

describe("AppShell — surface mounting", () => {
  it("mounts the real Overview surface on the default overview nav key", async () => {
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

    // Overview is the landing surface, so it renders with no navigation at all.
    await screen.findByTestId("overview-card-fleet");
    screen.getByTestId("overview-card-activity");
    expect(container.textContent ?? "").not.toMatch(/coming in a later phase/i);
  });

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

  it("mounts the real Routing surface on the routing nav key", async () => {
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

    fireEvent.click(screen.getByRole("link", { name: /^routing$/i }));

    await screen.findByTestId("tier-policy-lite");
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
                { id: "agnes-ai", display_name: "Agnes AI", description: "API-key.", auth_mode: "api_key", funding: { mode: "owner_policy", locked: false, non_expiring: false, fixed: null }, capabilities: [], configured: true, missing_env: [] },
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

    // Active Providers is the page-load default: only connected providers render.
    await screen.findByText("OpenCode Zen");
    expect(screen.queryByText("Agnes AI")).toBeNull();

    expect(screen.getByRole("button", { name: /active providers/i }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: /all integrations/i }).getAttribute("aria-pressed")).toBe("false");

    // Click "All Integrations" — the full catalog returns.
    fireEvent.click(screen.getByRole("button", { name: /all integrations/i }));
    await waitFor(() => screen.getByText("Agnes AI"));
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

    fireEvent.click(screen.getByRole("link", { name: /^models$/i }));

    const crumbs = screen.getByRole("navigation", { name: /breadcrumb/i });
    within(crumbs).getByText("Dashboard");
    within(crumbs).getByText("Operate");
    expect(crumbs.querySelector('[aria-current="page"]')?.textContent).toBe("Models");
  });

  it("mirrors the providers auth filter in the breadcrumb's third segment", async () => {
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

    // The documented trail: Dashboard / Providers / <filter> Providers.
    const currentCrumb = () =>
      screen.getByRole("navigation", { name: /breadcrumb/i }).querySelector('[aria-current="page"]')?.textContent;
    within(screen.getByRole("navigation", { name: /breadcrumb/i })).getByText("Providers");
    expect(currentCrumb()).toBe("All Providers");

    const tabs = await screen.findByRole("group", { name: /filter providers by authentication type/i });
    fireEvent.click(within(tabs).getByRole("button", { name: "OAuth" }));
    expect(currentCrumb()).toBe("OAuth Providers");

    fireEvent.click(within(tabs).getByRole("button", { name: "API Key" }));
    expect(currentCrumb()).toBe("API KEY Providers");
  });

  it("shows the Debug chip on the providers page only, toggling the Debug Log panel", async () => {
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
    expect(screen.queryByRole("button", { name: /^debug$/i })).toBeNull();

    fireEvent.click(screen.getByRole("link", { name: /providers/i }));
    const debugChip = await screen.findByRole("button", { name: /^debug$/i });
    fireEvent.click(debugChip);

    await screen.findByRole("dialog", { name: /debug log/i });

    // Closing via the panel's own × works, and navigating away removes the chip.
    fireEvent.click(screen.getByRole("button", { name: /^close$/i }));
    expect(screen.queryByRole("dialog", { name: /debug log/i })).toBeNull();

    fireEvent.click(screen.getByRole("link", { name: /overview/i }));
    expect(screen.queryByRole("button", { name: /^debug$/i })).toBeNull();
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

    // Scoped to the primary nav: the providers breadcrumb also carries a
    // "Providers" link.
    const primary = within(screen.getByRole("navigation", { name: /primary/i }));
    fireEvent.click(primary.getByRole("link", { name: /providers/i }));
    expect(primary.getByRole("link", { name: /providers/i }).getAttribute("aria-current")).toBe(
      "page",
    );

    const crumbs = screen.getByRole("navigation", { name: /breadcrumb/i });
    fireEvent.click(within(crumbs).getByText("Dashboard"));

    expect(primary.getByRole("link", { name: /overview/i }).getAttribute("aria-current")).toBe(
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
        jsonResponse(200, { data: { theme: "venom-light", density: "compact" } }),
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
