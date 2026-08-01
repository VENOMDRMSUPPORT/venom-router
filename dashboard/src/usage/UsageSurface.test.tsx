import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import type { UsageAggregate, UsageGroup, UsageMetric } from "../api/controlClient";
import UsageSurface from "./UsageSurface";
import usageSurfaceSource from "./UsageSurface.tsx?raw";

const USAGE_URL = "GET /api/control/v1/usage";

function metric(sum: number | null, known: number, unknown: number): UsageMetric {
  return {
    sum,
    average: sum !== null && known > 0 ? sum / known : null,
    known_count: known,
    unknown_count: unknown,
  };
}

function group(key: string | null, requests: number, tokensIn: UsageMetric): UsageGroup {
  return {
    key,
    requests,
    tokens_in: tokensIn,
    tokens_out: metric(5, requests, 0),
    latency_ms: metric(100, requests, 0),
  };
}

function aggregate(overrides: Partial<UsageAggregate> = {}): UsageAggregate {
  return {
    window: { from: null, to: null },
    scanned: 3,
    limit: 50,
    truncated: false,
    totals: group(null, 3, metric(40, 2, 1)),
    by_account: [group("acct-1", 2, metric(30, 2, 0)), group("acct-2", 1, metric(null, 0, 1))],
    by_model: [group("model-1", 3, metric(40, 2, 1))],
    by_tier: [group("pro", 2, metric(30, 2, 0)), group("lite", 1, metric(null, 0, 1))],
    ...overrides,
  };
}

function mockUsage(body: UsageAggregate): void {
  vi.stubGlobal("fetch", createFetchMock({ [USAGE_URL]: () => jsonResponse(200, { data: body }) }));
}

function renderSurface() {
  return render(<UsageSurface onSessionExpired={vi.fn()} />);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("UsageSurface — the three tables", () => {
  it("renders per-account, per-model and per-tier tables", async () => {
    mockUsage(aggregate());
    renderSurface();

    await screen.findByTestId("usage-by-account");
    screen.getByTestId("usage-by-model");
    screen.getByTestId("usage-by-tier");

    expect(screen.getByTestId("usage-account-acct-1").textContent ?? "").toContain("acct-1");
    expect(screen.getByTestId("usage-model-model-1").textContent ?? "").toContain("model-1");
    expect(screen.getByTestId("usage-tier-pro")).toBeTruthy();
  });

  it("renders the unattributed bucket as unattributed, not as a blank row", async () => {
    mockUsage(
      aggregate({
        by_account: [group(null, 1, metric(7, 1, 0))],
      }),
    );
    renderSurface();

    const row = await screen.findByTestId("usage-account-__unattributed");
    expect(row.textContent ?? "").toMatch(/unattributed/i);
    expect(row.textContent ?? "").toContain("7");
  });
});

describe("UsageSurface — unknowns are never zero", () => {
  // THE central assertion. An all-unknown group means requests happened and none
  // reported a token count. Showing 0 would say the traffic consumed nothing.
  it("renders an all-unknown group as unknown, never as 0", async () => {
    mockUsage(
      aggregate({
        totals: group(null, 2, metric(null, 0, 2)),
        by_tier: [group("lite", 2, metric(null, 0, 2))],
        by_account: [],
        by_model: [],
      }),
    );
    renderSurface();

    const cell = await screen.findByTestId("usage-total-tokens_in");
    expect(cell.textContent ?? "").toMatch(/unknown/i);
    expect(cell.textContent ?? "").not.toMatch(/\b0\b/);

    const tierCell = screen.getByTestId("usage-tier-lite-tokens_in");
    expect(tierCell.textContent ?? "").toMatch(/unknown/i);
    expect(tierCell.textContent ?? "").not.toMatch(/\b0\b/);
  });

  it("labels a partially-unknown number as a FLOOR, not a total", async () => {
    mockUsage(aggregate({ totals: group(null, 3, metric(40, 2, 1)) }));
    renderSurface();

    const cell = await screen.findByTestId("usage-total-tokens_in");
    const text = cell.textContent ?? "";
    expect(text).toContain("40");
    expect(text).toMatch(/at least/i);
    expect(text).toMatch(/floor, not a total/i);
    // And it says how many rows were unmeasured, so "at least" is quantified.
    expect(text).toMatch(/1 of 3/);
  });

  it("shows a fully-measured number plainly, with no floor language", async () => {
    // The other direction, so the test above cannot pass by labelling everything a
    // floor.
    mockUsage(aggregate({ totals: group(null, 2, metric(30, 2, 0)) }));
    renderSurface();

    const cell = await screen.findByTestId("usage-total-tokens_in");
    const text = cell.textContent ?? "";
    expect(text).toContain("30");
    expect(text).not.toMatch(/at least/i);
    expect(text).not.toMatch(/floor/i);
    // An average IS stated, over the known denominator.
    expect(text).toMatch(/avg 15/);
  });

  it("renders the request count plainly — a row count is always known", async () => {
    mockUsage(aggregate({ totals: group(null, 3, metric(null, 0, 3)) }));
    renderSurface();

    const totals = await screen.findByTestId("usage-totals");
    // 3 requests, even though nothing about them was measured.
    expect(totals.textContent ?? "").toContain("3");
  });
});

describe("UsageSurface — empty is not all-unknown", () => {
  it("renders a distinct empty state when no request has been routed", async () => {
    mockUsage(
      aggregate({
        scanned: 0,
        totals: group(null, 0, metric(null, 0, 0)),
        by_account: [],
        by_model: [],
        by_tier: [],
      }),
    );
    const { container } = renderSurface();

    await waitFor(() => expect(container.textContent ?? "").toMatch(/no usage recorded/i));
    // Crucially NOT the all-unknown wording — nothing happened, as opposed to
    // things happening unmeasured.
    expect(container.textContent ?? "").not.toMatch(/unknown/i);
    expect(screen.queryByTestId("usage-totals")).toBeNull();
  });

  it("renders the totals card (not the empty state) when traffic exists but is unmeasured", async () => {
    mockUsage(
      aggregate({
        totals: group(null, 2, metric(null, 0, 2)),
        by_account: [],
        by_model: [],
        by_tier: [group("lite", 2, metric(null, 0, 2))],
      }),
    );
    const { container } = renderSurface();

    await screen.findByTestId("usage-totals");
    expect(container.textContent ?? "").not.toMatch(/no usage recorded/i);
    expect(container.textContent ?? "").toMatch(/unknown/i);
  });
});

describe("UsageSurface — truncation", () => {
  it("says the scan was capped, so the numbers are a floor", async () => {
    mockUsage(aggregate({ truncated: true, scanned: 50, limit: 50 }));
    renderSurface();

    const totals = await screen.findByTestId("usage-totals");
    expect(totals.textContent ?? "").toMatch(/capped/i);
    expect(totals.textContent ?? "").toMatch(/floor/i);
  });
});

describe("UsageSurface — no invented cost", () => {
  it("states no cost or price anywhere", async () => {
    // The API states no cost, and deriving one from token counts would invent a
    // price the router never charged.
    mockUsage(aggregate());
    const { container } = renderSurface();
    await screen.findByTestId("usage-totals");

    expect(container.textContent ?? "").not.toMatch(/\$|\bcost\b|\bprice\b|\bspend\b|\bbill/i);
  });
});

describe("UsageSurface — tokens only, no raw values", () => {
  it("carries no raw colour or px literal in its own source", () => {
    expect(usageSurfaceSource).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(usageSurfaceSource).not.toMatch(/\b(rgba?|hsla?)\(/);
    expect(usageSurfaceSource).not.toMatch(/\b\d+px\b/);
    expect(usageSurfaceSource).not.toMatch(/-\[[^\]]+\]/);
  });
});

describe("UsageSurface — loading, error, a11y", () => {
  it("renders a loading state before the aggregate arrives", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    renderSurface();
    expect(screen.getByRole("status").getAttribute("aria-label") ?? "").toMatch(/loading/i);
  });

  it("renders an error state rather than an empty report when the API fails", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [USAGE_URL]: () =>
          jsonResponse(500, {
            error: { code: "internal", message: "internal error", request_id: "r1", retryable: true },
          }),
      }),
    );
    const { container } = renderSurface();

    await waitFor(() => expect(container.textContent ?? "").toMatch(/could not load usage/i));
    // "We could not ask" must not read as "you have no usage".
    expect(container.textContent ?? "").not.toMatch(/no usage recorded/i);
  });

  it("propagates a session expiry", async () => {
    const onSessionExpired = vi.fn();
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        [USAGE_URL]: () =>
          jsonResponse(401, {
            error: { code: "session_expired", message: "session expired", request_id: "r2", retryable: false },
          }),
      }),
    );
    render(<UsageSurface onSessionExpired={onSessionExpired} />);
    await waitFor(() => expect(onSessionExpired).toHaveBeenCalledTimes(1));
  });

  it("has no axe violations", async () => {
    mockUsage(aggregate());
    const { container } = renderSurface();
    await screen.findByTestId("usage-totals");
    await assertNoAxeViolations(container);
  });
});
