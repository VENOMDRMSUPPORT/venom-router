import { describe, expect, it } from "vitest";
import { formatDuration, relativeTime } from "./relativeTime";

const NOW = Date.UTC(2026, 7, 2, 12, 0, 0);
const MIN = 60_000;
const HR = 60 * MIN;

describe("formatDuration", () => {
  it("renders under a minute as the words, not 0", () => {
    expect(formatDuration(0)).toBe("less than a minute");
    expect(formatDuration(59_000)).toBe("less than a minute");
  });

  it("renders minutes with singular/plural", () => {
    expect(formatDuration(1 * MIN)).toBe("1 minute");
    expect(formatDuration(42 * MIN)).toBe("42 minutes");
  });

  it("renders hours + minutes in the target UI's vocabulary", () => {
    expect(formatDuration(2 * HR + 21 * MIN)).toBe("2 hr 21 min");
    expect(formatDuration(3 * HR)).toBe("3 hr");
  });

  it("renders days past 24 hours", () => {
    expect(formatDuration(26 * HR)).toBe("1 day 2 hr");
    expect(formatDuration(48 * HR)).toBe("2 days");
  });

  it("clamps a negative duration to the smallest honest bucket", () => {
    expect(formatDuration(-5)).toBe("less than a minute");
  });
});

describe("relativeTime", () => {
  it("renders the past with 'ago'", () => {
    expect(relativeTime(NOW - 30_000, NOW)).toBe("less than a minute ago");
    expect(relativeTime(NOW - 1 * MIN, NOW)).toBe("1 minute ago");
    expect(relativeTime(NOW - (2 * HR + 21 * MIN), NOW)).toBe("2 hr 21 min ago");
  });

  it("renders the future with 'in' (reset countdowns)", () => {
    expect(relativeTime(NOW + 2 * HR + 21 * MIN, NOW)).toBe("in 2 hr 21 min");
    expect(relativeTime(NOW + 10_000, NOW)).toBe("in less than a minute");
  });

  it("accepts ISO strings", () => {
    expect(relativeTime(new Date(NOW - 5 * MIN).toISOString(), NOW)).toBe("5 minutes ago");
  });

  it("returns an honest dash for an unparseable instant, never a fabricated time", () => {
    expect(relativeTime("not-a-date", NOW)).toBe("—");
  });
});
