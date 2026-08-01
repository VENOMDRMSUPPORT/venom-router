// P6-TEST-001's shared jsdom flow helpers.
//
// The existing per-surface suites each render ONE component with a handful of
// hand-listed fetch routes. That proves a component works; it cannot prove a
// JOURNEY works, and a11y regressions live disproportionately in the seams
// between steps — focus that lands nowhere after a dialog closes, a live
// region that never announces, a table that loses its caption once populated.
//
// These helpers drive the REAL <AuthGate /> (login -> AppShell -> surfaces)
// against the shared deterministic fixture table, so a journey test reads as
// the sequence of things an owner actually does.
//
// WHAT THIS FILE DOES NOT DO: it does not assert colour contrast. jsdom has no
// layout or paint engine, so axe-core's `color-contrast` rule cannot run here
// and src/test/axe.ts disables it. Contrast is covered in a REAL browser by
// tests/e2e/a11y.spec.ts, and nothing in this layer should be described
// as covering it.

import { fireEvent, render, screen, waitFor, within, type RenderResult } from "@testing-library/react";
import { createElement } from "react";
import { vi } from "vitest";
import { CONTROL_ROUTES, matchRoute, apiError, type RouteStub } from "../../tests/e2e/fixtures";
import AuthGate from "../auth/AuthGate";
import { SENTINELS } from "./noSecrets";

/** Every request the stub served, in order — so a journey can assert an
 * endpoint was actually reached rather than trusting that a render implies a
 * fetch. */
export interface RecordedRequest {
  readonly method: string;
  readonly pathname: string;
}

export interface ControlFetchStub {
  /** Requests served so far, oldest first. */
  readonly requests: RecordedRequest[];
  /** True if any request matched this method + exact pathname. */
  sawRequest(method: string, pathname: string): boolean;
}

/**
 * Stubs `globalThis.fetch` with the shared deterministic route table.
 *
 * `overrides` are consulted FIRST (as their own table, so an override is never
 * out-scored by a more literal default), letting one test make a single
 * endpoint fail — the error-state a11y step — without restating the other
 * thirty.
 *
 * An unmatched route THROWS rather than returning an empty 200. A surface that
 * silently receives nothing renders its empty state, axe passes over an empty
 * page, and the test proves nothing — that is the exact failure mode this
 * throw exists to prevent.
 */
export function installControlFetch(overrides: readonly RouteStub[] = []): ControlFetchStub {
  const requests: RecordedRequest[] = [];

  const stub = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const method = (init?.method ?? "GET").toUpperCase();
    // Surfaces call relative paths; a base is required to parse them.
    const { pathname } = new URL(String(input), "http://127.0.0.1");
    requests.push({ method, pathname });

    const route = matchRoute(method, pathname, overrides) ?? matchRoute(method, pathname, CONTROL_ROUTES);
    if (route === undefined) {
      throw new Error(
        `installControlFetch: no fixture for "${method} ${pathname}". Add it to CONTROL_ROUTES ` +
          `in tests/e2e/fixtures.ts, or pass an override — never let a surface render against a ` +
          `missing endpoint, which turns the assertion that follows into a no-op.`,
      );
    }

    return Promise.resolve(
      new Response(JSON.stringify(route.body), {
        status: route.status,
        headers: { "Content-Type": "application/json" },
      }),
    );
  });

  vi.stubGlobal("fetch", stub);

  return {
    requests,
    sawRequest(method, pathname) {
      return requests.some((r) => r.method === method.toUpperCase() && r.pathname === pathname);
    },
  };
}

/** The override that makes AuthGate show the LOGIN screen: no live session.
 * This is the server's real answer when the session cookie is absent or
 * expired, not a test-only contrivance. */
export const NO_LIVE_SESSION: RouteStub = {
  method: "GET",
  path: "/api/control/v1/auth/session",
  status: 401,
  body: apiError("session_expired", "no live session"),
};

/**
 * Renders the real app and completes a genuine owner login with the sentinel
 * password, leaving the caller on the authenticated shell.
 *
 * The login is driven through the FORM rather than by stubbing the gate into
 * its authenticated state, because that is the step which puts the owner
 * password into the DOM — and the whole point of asserting the canary
 * afterwards is to prove the password does not survive into the shell.
 */
export async function loginToShell(
  overrides: readonly RouteStub[] = [],
): Promise<{ view: RenderResult; fetch: ControlFetchStub }> {
  const fetchStub = installControlFetch([NO_LIVE_SESSION, ...overrides]);
  const view = render(createElement(AuthGate));

  const passwordField = await screen.findByLabelText(/owner password/i);
  fireEvent.change(passwordField, { target: { value: SENTINELS.ownerPassword } });
  fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

  await waitForShell();
  return { view, fetch: fetchStub };
}

/**
 * Renders straight into the authenticated shell, skipping the login form.
 *
 * `GET /auth/session` answering with a live session is exactly what the real
 * app sees on a reload with a valid cookie, so this is a real path rather than
 * a test-only shortcut.
 */
export async function renderShell(
  overrides: readonly RouteStub[] = [],
): Promise<{ view: RenderResult; fetch: ControlFetchStub }> {
  const fetchStub = installControlFetch(overrides);
  const view = render(createElement(AuthGate));
  await waitForShell();
  return { view, fetch: fetchStub };
}

/** The shell is up once the primary nav exists — present on every
 * authenticated surface and on none of the unauthenticated ones. */
async function waitForShell(): Promise<void> {
  await screen.findByRole("navigation", { name: /primary/i });
}

/**
 * Navigates the shell to `label`'s surface by clicking its primary-nav link,
 * then waits for that link to become the current page.
 *
 * Waiting on rendered STATE (aria-current), never on a timer, is what keeps
 * these journeys deterministic — there is no `waitForTimeout` anywhere in this
 * file, by policy.
 */
export async function gotoNav(label: string | RegExp): Promise<void> {
  const nav = screen.getByRole("navigation", { name: /primary/i });
  const links = within(nav).getAllByRole("link");
  const target = links.find((link) => matchesLabel(link.textContent ?? "", label));
  if (target === undefined) {
    throw new Error(
      `gotoNav: no primary-nav link matching ${String(label)}; available: ` +
        links.map((l) => JSON.stringify(l.textContent)).join(", "),
    );
  }

  fireEvent.click(target);
  await waitFor(() => {
    if (target.getAttribute("aria-current") !== "page") {
      throw new Error(`gotoNav: ${String(label)} did not become the current page`);
    }
  });
}

function matchesLabel(text: string, label: string | RegExp): boolean {
  return typeof label === "string" ? text.trim() === label : label.test(text);
}
