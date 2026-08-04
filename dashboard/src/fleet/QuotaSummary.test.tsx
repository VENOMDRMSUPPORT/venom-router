import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import type { QuotaWindow } from "../api/controlClient";
import QuotaSummary, { QuotaSummaryCompact } from "./QuotaSummary";
import { compactWindowLabel, formatBalanceValue } from "./quotaWindows";

afterEach(() => {
  cleanup();
});

function window_(overrides: Partial<QuotaWindow> = {}): QuotaWindow {
  return {
    source: "provider_evidence",
    unit: "requests",
    window_type: "rolling",
    window_key: "provider:daily",
    state: "available",
    freshness: "fresh",
    used: 10,
    remaining: 90,
    total: 100,
    limit_value: null,
    reserved: 0,
    reset_at: null,
    observed_at: "2026-07-27T00:00:00Z",
    ...overrides,
  };
}

describe("QuotaSummary — provider-evidence rendering", () => {
  it("renders a provider-evidence window's used/total/unit and its state", () => {
    render(<QuotaSummary windows={[window_()]} />);

    screen.getByText("10");
    screen.getByText(/\/ 100 requests/);
    screen.getByTitle(/Quota source: provider evidence/i);
  });

  it("renders the unknown treatment and no numeric total for a window with total: null — never a fabricated figure", () => {
    // state is deliberately left "available" here: the assertion is that
    // `total: null` ALONE forces the unknown treatment (QuotaWindowMeter's
    // own "total == null" branch), independent of what state says — a
    // stronger claim than a test that also sets state: "unknown" (which
    // would pass even if the component silently coerced a null total to a
    // number, since state alone would still trip the unknown branch).
    const { container } = render(
      <QuotaSummary
        windows={[
          window_({
            total: null,
            used: null,
            remaining: null,
            state: "available",
            freshness: "unknown",
          }),
        ]}
      />,
    );

    screen.getByText(/never rendered as a number/i);
    // No numeric total figure ("/ <number> <unit>") is rendered anywhere.
    expect(container.querySelector(".vn-quota-total")).toBeNull();
    expect(screen.queryByText(/\/ \d+/)).toBeNull();
  });

  it("visually distinguishes a stale window from an available one with identical numbers", () => {
    const { container } = render(
      <QuotaSummary
        windows={[
          window_({
            window_key: "provider:available-window",
            state: "available",
            freshness: "fresh",
          }),
          window_({ window_key: "provider:stale-window", state: "stale", freshness: "stale" }),
        ]}
      />,
    );

    // The available window renders the real numeric meter (role="meter");
    // the stale window — same used/total/unit — renders the unknown
    // treatment instead, per docs/05 §4 ("stale ... treated as unknown").
    const meters = container.querySelectorAll('[role="meter"]');
    expect(meters.length).toBe(1);
    screen.getByText(/treated as unknown/i);
  });
});

describe("QuotaSummary — local_safety separation", () => {
  it("renders local_safety windows through LocalSafetyBudgetIndicator, never as provider evidence", () => {
    render(
      <QuotaSummary
        windows={[
          window_({
            source: "local_safety",
            unit: "concurrency",
            window_type: "concurrency",
            window_key: "local:concurrency",
            total: null,
            used: null,
            remaining: null,
            limit_value: 5,
            reserved: 1,
          }),
        ]}
      />,
    );

    screen.getByTitle(/NOT provider evidence/i);
    // The provider-evidence treatment (QuotaWindowCard's own source badge)
    // must be completely absent for a local_safety window.
    expect(screen.queryByTitle(/Quota source: local safety/i)).toBeNull();
    expect(screen.queryByTitle(/Quota source: provider evidence/i)).toBeNull();
  });
});

describe("QuotaSummary — empty state", () => {
  it("renders the honest empty state for an empty quota array, not a 0% meter", () => {
    const { container } = render(<QuotaSummary windows={[]} />);

    screen.getByText("—");
    expect(container.querySelectorAll('[role="meter"]').length).toBe(0);
  });
});

describe("QuotaSummaryCompact — empty state", () => {
  it("renders NOTHING for zero windows — the account meta line already reports the absence, and a free account's unlimited nature is shown by its funding badge", () => {
    const { container } = render(<QuotaSummaryCompact windows={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it("still renders a real known window's meter and an unknown window's state word", () => {
    const { container } = render(
      <QuotaSummaryCompact
        nowMs={Date.parse("2026-07-27T01:00:00Z")}
        windows={[
          window_({ window_key: "gem" }),
          window_({
            window_key: "opt",
            used: null,
            remaining: null,
            total: null,
            state: "unknown",
            freshness: "unknown",
          }),
        ]}
      />,
    );

    screen.getByText("GEM");
    screen.getByText("10%");
    screen.getByText("OPT");
    screen.getByText("unknown");
    expect(container.querySelectorAll('[role="meter"]').length).toBe(1);
  });

  it("renders stale numeric evidence as stale, never as a live 0%", () => {
    const { container } = render(
      <QuotaSummaryCompact
        windows={[
          window_({
            unit: "percent",
            window_type: "rolling_5h",
            window_key: "rolling:18000s",
            used: 0,
            remaining: 100,
            total: 100,
            state: "stale",
            freshness: "stale",
          }),
        ]}
      />,
    );

    screen.getByText("stale");
    expect(screen.queryByText("0%")).toBeNull();
    expect(container.querySelector('[role="meter"]')).toBeNull();
    expect(container.querySelector('[role="img"]')).toBeTruthy();
  });
});

describe("QuotaSummaryCompact — legacy-style rolling windows + balance", () => {
  it("labels rolling windows 5H/7D/30D from the window type and orders shortest first", () => {
    render(
      <QuotaSummaryCompact
        nowMs={Date.parse("2026-08-04T01:00:00Z")}
        windows={[
          window_({ unit: "percent", window_type: "rolling_30d", window_key: "rolling:2592000s", used: 5, remaining: 95, total: 100 }),
          window_({ unit: "percent", window_type: "rolling_5h", window_key: "rolling:18000s", used: 32, remaining: 68, total: 100 }),
          window_({ unit: "percent", window_type: "rolling_7d", window_key: "rolling:604800s", used: 12, remaining: 88, total: 100 }),
        ]}
      />,
    );

    const labels = Array.from(document.querySelectorAll(".vnd-quota-label")).map(
      (el) => el.textContent,
    );
    expect(labels).toEqual(["5H", "7D", "30D"]);
    screen.getByText("32%");
    screen.getByText("12%");
    screen.getByText("5%");
  });

  it("derives an H/D label from a rolling:<n>s key when the type is unrecognized", () => {
    expect(
      compactWindowLabel(window_({ window_type: "rolling", window_key: "rolling:18000s" })),
    ).toBe("5H");
    expect(
      compactWindowLabel(window_({ window_type: "rolling", window_key: "rolling:2592000s" })),
    ).toBe("30D");
    expect(compactWindowLabel(window_({ window_type: "rolling", window_key: "gem" }))).toBe("GEM");
  });

  it("excludes the balance window from the compact meter list — the account row's balance chip owns it", () => {
    const { container } = render(
      <QuotaSummaryCompact
        windows={[
          window_({
            unit: "credits",
            window_type: "balance",
            window_key: "local:credits",
            used: null,
            total: null,
            remaining: 4.83,
          }),
          window_({ unit: "percent", window_type: "rolling_5h", window_key: "rolling:18000s", used: 32, remaining: 68, total: 100 }),
        ]}
      />,
    );

    expect(screen.queryByText("BALANCE")).toBeNull();
    expect(container.querySelectorAll(".vnd-quota-line").length).toBe(1);
    screen.getByText("5H");
  });

  it("formats a usd-denominated balance as dollars and others with the unit word", () => {
    const balance = window_({
      unit: "credits",
      window_type: "balance",
      used: null,
      total: null,
      remaining: 4.83,
    });
    expect(formatBalanceValue(balance, "usd")).toBe("$4.83");
    expect(formatBalanceValue(balance)).toBe("4.83 credits");
    expect(formatBalanceValue({ ...balance, remaining: 5 })).toBe("5 credits");
  });
});

describe("QuotaSummary — accessibility", () => {
  it("has zero axe violations for a mixed provider-evidence + local_safety summary", async () => {
    const { container } = render(
      <QuotaSummary
        windows={[
          window_(),
          window_({
            source: "local_safety",
            unit: "concurrency",
            window_type: "concurrency",
            window_key: "local:concurrency",
            total: null,
            used: null,
            remaining: null,
            limit_value: 5,
            reserved: 1,
          }),
        ]}
      />,
    );

    await assertNoAxeViolations(container);
  });
});
