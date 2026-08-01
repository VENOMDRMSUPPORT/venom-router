import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { TierPolicy } from "../api/controlClient";
import RoutingSurface from "./RoutingSurface";
// Vite's `?raw` import, so the source-level token assertion below needs no
// filesystem access (and therefore no @types/node — this batch adds no deps).
import routingSurfaceSource from "./RoutingSurface.tsx?raw";

const POLICY_URL = "GET /api/control/v1/routing/policy";

/** The three policies EXACTLY as the shipped server serves them
 * (internal/routing/policy.go's shippedPolicies, projected by
 * internal/httpapi/routingpolicy.go). Every value the surface displays must come
 * from this payload — the "response drives the display" test below proves that by
 * changing them. */
const LITE: TierPolicy = {
  tier: "lite",
  funding: "free_only",
  context_ceiling_tokens: 262144,
  thinking_ceiling: "none",
  attempt_budget: 3,
  scored: false,
  weights: null,
  competitive_band: null,
  latency_tie_break_only: true,
};

const PRO: TierPolicy = {
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
};

const MAX: TierPolicy = {
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
};

function mockPolicy(tiers: TierPolicy[]): void {
  vi.stubGlobal(
    "fetch",
    createFetchMock({ [POLICY_URL]: () => jsonResponse(200, { data: { tiers } }) }),
  );
}

function renderSurface() {
  return render(<RoutingSurface onSessionExpired={vi.fn()} />);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("RoutingSurface — the three tier policies", () => {
  it("renders every policy field for all three tiers", async () => {
    mockPolicy([LITE, PRO, MAX]);
    renderSurface();

    for (const tier of [LITE, PRO, MAX]) {
      const card = await screen.findByTestId(`tier-policy-${tier.tier}`);
      const text = card.textContent ?? "";
      expect(text, `${tier.tier} funding`).toContain(tier.funding!);
      expect(text, `${tier.tier} context ceiling`).toContain(tier.context_ceiling_tokens!.toLocaleString());
      expect(text, `${tier.tier} thinking ceiling`).toContain(tier.thinking_ceiling!);
      expect(text, `${tier.tier} attempt budget`).toContain(String(tier.attempt_budget));
    }
  });

  it("renders each scored tier's weights and competitive band", async () => {
    mockPolicy([LITE, PRO, MAX]);
    renderSurface();

    const pro = await screen.findByTestId("tier-weights-pro");
    for (const [key, value] of Object.entries(PRO.weights!)) {
      const row = within(pro).getByTestId(`tier-weight-pro-${key}`);
      expect(row.textContent ?? "", `pro ${key}`).toContain(String(value));
    }
    expect((await screen.findByTestId("tier-band-pro")).textContent ?? "").toContain("0.08");
    expect((await screen.findByTestId("tier-band-max")).textContent ?? "").toContain("0.03");
  });

  it("renders an unscored tier's weights and band as not applicable, never as 0", async () => {
    // routing.TierPolicy REQUIRES an unscored tier's weights and band to be
    // zero, precisely because they carry no meaning there. Rendering those zeros
    // would read as "quality is weighted at zero" and "the band is zero wide" —
    // two scoring claims Lite never makes.
    mockPolicy([LITE, PRO, MAX]);
    renderSurface();

    const scoring = await screen.findByTestId("tier-scoring-lite");
    const text = scoring.textContent ?? "";
    expect(text).toMatch(/not scored|unscored/i);
    expect(text).toMatch(/not applicable|n\/a/i);
    expect(text).not.toMatch(/\b0(\.0+)?\b/);
    expect(screen.queryByTestId("tier-weights-lite")).toBeNull();
    expect(screen.queryByTestId("tier-band-lite")).toBeNull();
  });

  it("derives the fallback pool from the served funding rule", async () => {
    mockPolicy([LITE, PRO, MAX]);
    renderSurface();

    // free_only is what makes Lite's fallback fail closed rather than reach for
    // a paid offering (05 §1's "fallback on exhaustion" row).
    const lite = await screen.findByTestId("tier-fallback-lite");
    expect(lite.textContent ?? "").toMatch(/fail closed|never paid|free/i);
    const pro = await screen.findByTestId("tier-fallback-pro");
    expect(pro.textContent ?? "").toMatch(/free \+ paid|free and paid/i);
  });
});

describe("RoutingSurface — nothing is hardcoded", () => {
  // THE anti-hardcoding proof. The served payload is deliberately given values
  // that are NOT the shipped policy; the display must follow the response. A
  // component carrying its own copy of docs/05 §1 would keep showing 262144/3
  // here and fail.
  it("displays whatever the API returns, not the shipped V1 numbers", async () => {
    mockPolicy([
      { ...LITE, context_ceiling_tokens: 111, attempt_budget: 9, thinking_ceiling: "standard", funding: "free_and_paid" },
      { ...PRO, context_ceiling_tokens: 222, attempt_budget: 8, competitive_band: 0.42 },
      { ...MAX, context_ceiling_tokens: 333, attempt_budget: 7 },
    ]);
    renderSurface();

    const lite = await screen.findByTestId("tier-policy-lite");
    expect(lite.textContent ?? "").toContain("111");
    expect(lite.textContent ?? "").toContain("9");
    expect(lite.textContent ?? "").toContain("standard");
    // The shipped values must be nowhere in sight.
    expect(lite.textContent ?? "").not.toContain("262,144");
    expect(lite.textContent ?? "").not.toContain("262144");

    expect((await screen.findByTestId("tier-policy-pro")).textContent ?? "").toContain("222");
    expect((await screen.findByTestId("tier-band-pro")).textContent ?? "").toContain("0.42");
    expect((await screen.findByTestId("tier-policy-max")).textContent ?? "").toContain("333");
  });

  it("renders a field the API omits as unknown, never as a client-side default", async () => {
    mockPolicy([
      { tier: "lite" },
      PRO,
      MAX,
    ]);
    renderSurface();

    const card = await screen.findByTestId("tier-policy-lite");
    const text = card.textContent ?? "";
    // Every absent field says unknown, and no invented number appears.
    expect(text).toMatch(/unknown/i);
    expect(text).not.toContain("262,144");
    expect(text).not.toMatch(/\b3\b/);
  });

  it("renders only the tiers the API returned", async () => {
    // A surface that laid out three fixed tier cards and filled them in would
    // show an empty "max" here. The list comes from the response.
    mockPolicy([LITE, PRO]);
    renderSurface();

    await screen.findByTestId("tier-policy-lite");
    screen.getByTestId("tier-policy-pro");
    expect(screen.queryByTestId("tier-policy-max")).toBeNull();
  });
});

describe("RoutingSurface — V1 policy is read-only", () => {
  it("offers no editing control for the weights", async () => {
    // 05 §8.4 defers dashboard weight tuning past V1, and there is no PUT
    // server-side. An input here would promise something nothing can deliver.
    mockPolicy([LITE, PRO, MAX]);
    const { container } = renderSurface();
    await screen.findByTestId("tier-policy-pro");

    expect(container.querySelectorAll("input").length).toBe(0);
    expect(container.querySelectorAll("select").length).toBe(0);
    expect(container.querySelectorAll("textarea").length).toBe(0);
    expect(screen.queryByRole("button", { name: /save|apply|edit|change/i })).toBeNull();
    expect(container.textContent ?? "").toMatch(/fixed in v1|not tunable|read-only/i);
  });
});

describe("RoutingSurface — tokens only, no raw values", () => {
  it("colours the tier badges through tier.* tokens and contains no raw colour literal", async () => {
    mockPolicy([LITE, PRO, MAX]);
    const { container } = renderSurface();
    await screen.findByTestId("tier-policy-lite");

    // The DS TierBadge is the single way a tier is labelled, and it carries the
    // token-mapped `vn-badge--tier-*` class.
    for (const tier of ["lite", "pro", "max"]) {
      const card = screen.getByTestId(`tier-policy-${tier}`);
      expect(card.querySelector(`.vn-badge--tier-${tier}`), `${tier} badge`).toBeTruthy();
    }
    // No inline colour anywhere in the rendered markup.
    expect(container.innerHTML).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(container.innerHTML).not.toMatch(/\b(rgba?|hsla?)\(/);
  });

  it("carries no raw colour or px literal in its own source", () => {
    // The eslint no-raw-values rule (07 §8) is the primary, CI-blocking gate;
    // this asserts the same property inside this component's own suite so the
    // requirement is visible where the component is reviewed.
    expect(routingSurfaceSource).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(routingSurfaceSource).not.toMatch(/\b(rgba?|hsla?)\(/);
    expect(routingSurfaceSource).not.toMatch(/\b\d+px\b/);
    expect(routingSurfaceSource).not.toMatch(/-\[[^\]]+\]/);
  });
});

describe("RoutingSurface — loading, error, a11y", () => {
  it("renders a loading state before the policy arrives", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    renderSurface();
    expect(screen.getByRole("status").getAttribute("aria-label") ?? "").toMatch(/loading/i);
  });

  it("renders an error state rather than an empty policy when the API fails", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [POLICY_URL]: () =>
          jsonResponse(500, {
            error: { code: "internal", message: "internal error", request_id: "r1", retryable: true },
          }),
      }),
    );
    const { container } = renderSurface();

    await waitFor(() => expect(container.textContent ?? "").toMatch(/could not load/i));
    // An empty policy would read as "the router has no tiers".
    expect(screen.queryByTestId("tier-policy-lite")).toBeNull();
    expect(container.textContent ?? "").not.toMatch(/no tiers/i);
  });

  it("propagates a session expiry", async () => {
    const onSessionExpired = vi.fn();
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [POLICY_URL]: () =>
          jsonResponse(401, {
            error: { code: "session_expired", message: "session expired", request_id: "r2", retryable: false },
          }),
      }),
    );
    render(<RoutingSurface onSessionExpired={onSessionExpired} />);
    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
  });

  it("has no axe violations", async () => {
    mockPolicy([LITE, PRO, MAX]);
    const { container } = renderSurface();
    await screen.findByTestId("tier-policy-max");
    await assertNoAxeViolations(container);
  });
});
