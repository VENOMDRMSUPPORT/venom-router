import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import type { QuotaWindow } from "../api/controlClient";
import QuotaSummary, { QuotaSummaryCompact } from "./QuotaSummary";

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
          window_({ window_key: "opt", used: null, remaining: null, total: null, state: "unknown", freshness: "unknown" }),
        ]}
      />,
    );

    screen.getByText("GEM");
    screen.getByText("10%");
    screen.getByText("OPT");
    screen.getByText("unknown");
    expect(container.querySelectorAll('[role="meter"]').length).toBe(1);
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
