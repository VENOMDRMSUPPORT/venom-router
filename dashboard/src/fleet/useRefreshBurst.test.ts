import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  REFRESH_BURST_INTERVAL_MS,
  REFRESH_BURST_TICKS,
  useRefreshBurst,
} from "./useRefreshBurst";

describe("useRefreshBurst", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("does nothing until start() is called", () => {
    const onRefresh = vi.fn();
    renderHook(() => useRefreshBurst(onRefresh));
    act(() => vi.advanceTimersByTime(REFRESH_BURST_INTERVAL_MS * 5));
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it("fires onRefresh once per interval, exactly REFRESH_BURST_TICKS times, then stops", () => {
    const onRefresh = vi.fn();
    const { result } = renderHook(() => useRefreshBurst(onRefresh));

    act(() => result.current());

    // Drive well past the whole burst window.
    for (let i = 0; i < REFRESH_BURST_TICKS + 5; i++) {
      act(() => vi.advanceTimersByTime(REFRESH_BURST_INTERVAL_MS));
    }
    expect(onRefresh).toHaveBeenCalledTimes(REFRESH_BURST_TICKS);
  });

  it("restarts a full burst when start() is called again mid-flight", () => {
    const onRefresh = vi.fn();
    const { result } = renderHook(() => useRefreshBurst(onRefresh));

    act(() => result.current());
    // One interval per act so the effect flushes and reschedules between ticks
    // (how real time behaves — a single fake jump only fires the one already-
    // scheduled timeout).
    for (let i = 0; i < 3; i++) {
      act(() => vi.advanceTimersByTime(REFRESH_BURST_INTERVAL_MS));
    }
    expect(onRefresh).toHaveBeenCalledTimes(3);

    // A second account connected — restart the countdown from the top.
    act(() => result.current());
    for (let i = 0; i < REFRESH_BURST_TICKS + 2; i++) {
      act(() => vi.advanceTimersByTime(REFRESH_BURST_INTERVAL_MS));
    }
    expect(onRefresh).toHaveBeenCalledTimes(3 + REFRESH_BURST_TICKS);
  });
});
