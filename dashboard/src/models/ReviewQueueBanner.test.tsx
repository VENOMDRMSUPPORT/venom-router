import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { ReviewCensus } from "../api/controlClient";
import ReviewQueueBanner from "./ReviewQueueBanner";

const CENSUS_URL = "GET /api/control/v1/certifications/review";

/** The full closed vocabulary, split exactly as the shipped server splits it. */
const NOT_EVALUATED = [
  "identity_unresolved",
  "context_unverified",
  "funding_unknown",
  "no_healthy_account",
  "quota_exhausted",
  "quota_insufficient",
  "cooling_down",
];

function census(overrides: Partial<ReviewCensus> = {}): ReviewCensus {
  return {
    scanned: 12,
    limit: 50,
    truncated: false,
    evaluated_reasons: ["capability_not_certified"],
    not_evaluated_reasons: NOT_EVALUATED,
    by_reason: [{ reason: "capability_not_certified", count: 3 }],
    ...overrides,
  };
}

function mockCensus(body: ReviewCensus): void {
  vi.stubGlobal("fetch", createFetchMock({ [CENSUS_URL]: () => jsonResponse(200, { data: body }) }));
}

function renderBanner(onReviewBacklog = vi.fn()) {
  return render(<ReviewQueueBanner onSessionExpired={vi.fn()} onReviewBacklog={onReviewBacklog} />);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ReviewQueueBanner — counts by reason", () => {
  it("renders each evaluated reason with its count, using the API's typed code verbatim", async () => {
    mockCensus(census({ by_reason: [{ reason: "capability_not_certified", count: 7 }] }));
    renderBanner();

    const row = await screen.findByTestId("review-reason-capability_not_certified");
    // The typed code appears EXACTLY as the API returned it — renaming or
    // prettifying a reason code breaks the operator's ability to match it
    // against the routing diagnostics and the docs.
    expect(within(row).getByText("capability_not_certified")).toBeTruthy();
    expect(row.textContent ?? "").toMatch(/7/);
  });

  it("renders a human label beside the code without replacing it", async () => {
    mockCensus(census());
    renderBanner();

    const row = await screen.findByTestId("review-reason-capability_not_certified");
    expect(within(row).getByText("capability_not_certified")).toBeTruthy();
    // Some readable gloss is present too, so the banner is usable by someone who
    // has not memorized the vocabulary.
    expect((row.textContent ?? "").replace("capability_not_certified", "").trim().length).toBeGreaterThan(1);
  });
});

describe("ReviewQueueBanner — unevaluated reasons", () => {
  // The central fail-closed assertion of P6-UI-012. A reason the API did not
  // evaluate must NOT render as 0: a zero says "we looked and found none", which
  // reads as an all-clear for a check that never ran.
  it("renders a not-evaluated reason as not evaluated, with no count", async () => {
    mockCensus(census());
    renderBanner();

    for (const reason of NOT_EVALUATED) {
      const row = await screen.findByTestId(`review-not-evaluated-${reason}`);
      expect(within(row).getByText(reason)).toBeTruthy();
      expect(row.textContent ?? "").toMatch(/not evaluated/i);
      // No number at all in that row — not 0, not anything.
      expect(row.textContent ?? "").not.toMatch(/\d/);
    }
  });

  it("never silently omits a not-evaluated reason", async () => {
    // An ABSENT row reads as "none found" just as surely as a 0 does, so every
    // reason the API named must be on screen somewhere.
    mockCensus(census());
    const { container } = renderBanner();
    await screen.findByTestId("review-queue-banner");

    for (const reason of NOT_EVALUATED) {
      expect(container.textContent ?? "", `${reason} must be surfaced`).toContain(reason);
    }
  });

  it("states that quota_insufficient is request-dependent rather than merely missing", async () => {
    mockCensus(census());
    renderBanner();

    const row = await screen.findByTestId("review-not-evaluated-quota_insufficient");
    expect(row.textContent ?? "").toMatch(/request/i);
  });
});

describe("ReviewQueueBanner — all-clear and truncation", () => {
  it("renders an explicit all-clear for an empty backlog, not an empty box", async () => {
    mockCensus(census({ by_reason: [{ reason: "capability_not_certified", count: 0 }] }));
    const { container } = renderBanner();

    await screen.findByTestId("review-queue-banner");
    expect(container.textContent ?? "").toMatch(/nothing (is )?waiting|no offering-operation|all clear/i);
    // The all-clear is scoped honestly: it covers only what was evaluated.
    expect(container.textContent ?? "").toMatch(/not evaluated/i);
  });

  it("says when the scan was truncated rather than presenting a partial count as complete", async () => {
    mockCensus(census({ truncated: true, scanned: 50, by_reason: [{ reason: "capability_not_certified", count: 50 }] }));
    const { container } = renderBanner();

    await screen.findByTestId("review-queue-banner");
    expect(container.textContent ?? "").toMatch(/at least|truncated|more than/i);
  });
});

describe("ReviewQueueBanner — unrecognized codes fail closed", () => {
  it("renders an unrecognized reason code rather than dropping it", async () => {
    // A server newer than this console. Dropping the row would hide a real
    // backlog; rendering it verbatim and marking it unrecognized surfaces both
    // the backlog AND the version skew.
    mockCensus(
      census({
        evaluated_reasons: ["capability_not_certified", "brand_new_reason"],
        by_reason: [
          { reason: "capability_not_certified", count: 1 },
          { reason: "brand_new_reason", count: 9 },
        ],
      }),
    );
    const { container } = renderBanner();

    const row = await screen.findByTestId("review-reason-brand_new_reason");
    expect(within(row).getByText("brand_new_reason")).toBeTruthy();
    expect(row.textContent ?? "").toMatch(/9/);
    expect(row.textContent ?? "").toMatch(/unrecognized|not recognized|unknown to this console/i);
    expect(container.textContent ?? "").toContain("brand_new_reason");
  });

  it("renders an unrecognized NOT-evaluated code too, still without a count", async () => {
    mockCensus(census({ not_evaluated_reasons: [...NOT_EVALUATED, "future_reason"] }));
    renderBanner();

    const row = await screen.findByTestId("review-not-evaluated-future_reason");
    expect(row.textContent ?? "").toMatch(/not evaluated/i);
    expect(row.textContent ?? "").not.toMatch(/\d/);
  });
});

describe("ReviewQueueBanner — navigation, states, a11y", () => {
  it("offers a link into the backlog when there is one", async () => {
    const onReviewBacklog = vi.fn();
    mockCensus(census({ by_reason: [{ reason: "capability_not_certified", count: 2 }] }));
    renderBanner(onReviewBacklog);

    fireEvent.click(await screen.findByRole("button", { name: /review the backlog/i }));
    expect(onReviewBacklog).toHaveBeenCalledTimes(1);
  });

  it("offers no backlog link when there is nothing to review", async () => {
    mockCensus(census({ by_reason: [{ reason: "capability_not_certified", count: 0 }] }));
    renderBanner();

    await screen.findByTestId("review-queue-banner");
    expect(screen.queryByRole("button", { name: /review the backlog/i })).toBeNull();
  });

  it("renders nothing but a quiet notice when the census cannot be read", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [CENSUS_URL]: () =>
          jsonResponse(500, {
            error: { code: "internal", message: "internal error", request_id: "r1", retryable: true },
          }),
      }),
    );
    const { container } = renderBanner();

    await waitFor(() => expect(container.textContent ?? "").toMatch(/could not (load|read) the review/i));
    // Above all it must NOT render an all-clear when it simply failed to ask.
    expect(container.textContent ?? "").not.toMatch(/all clear|nothing is waiting/i);
  });

  it("propagates a session expiry", async () => {
    const onSessionExpired = vi.fn();
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [CENSUS_URL]: () =>
          jsonResponse(401, {
            error: { code: "session_expired", message: "session expired", request_id: "r2", retryable: false },
          }),
      }),
    );
    render(<ReviewQueueBanner onSessionExpired={onSessionExpired} onReviewBacklog={vi.fn()} />);
    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
  });

  it("has no axe violations with a backlog", async () => {
    mockCensus(census());
    const { container } = renderBanner();
    await screen.findByTestId("review-queue-banner");
    await assertNoAxeViolations(container);
  });

  it("has no axe violations on the all-clear", async () => {
    mockCensus(census({ by_reason: [{ reason: "capability_not_certified", count: 0 }] }));
    const { container } = renderBanner();
    await screen.findByTestId("review-queue-banner");
    await assertNoAxeViolations(container);
  });
});
