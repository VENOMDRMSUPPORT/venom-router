import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { FLEET_POLL_INTERVAL_MS, usePollingRefresh } from "./usePollingRefresh";

function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => state,
  });
  document.dispatchEvent(new Event("visibilitychange"));
}

describe("usePollingRefresh", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    setVisibility("visible");
  });

  it("fires onRefresh once per interval while mounted", () => {
    const onRefresh = vi.fn();
    renderHook(() => usePollingRefresh(onRefresh));

    act(() => vi.advanceTimersByTime(FLEET_POLL_INTERVAL_MS * 3));
    expect(onRefresh).toHaveBeenCalledTimes(3);
  });

  it("stops on unmount", () => {
    const onRefresh = vi.fn();
    const { unmount } = renderHook(() => usePollingRefresh(onRefresh));
    unmount();
    act(() => vi.advanceTimersByTime(FLEET_POLL_INTERVAL_MS * 3));
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it("pauses while the tab is hidden and refreshes immediately on return", () => {
    const onRefresh = vi.fn();
    renderHook(() => usePollingRefresh(onRefresh));

    act(() => setVisibility("hidden"));
    act(() => vi.advanceTimersByTime(FLEET_POLL_INTERVAL_MS * 5));
    expect(onRefresh).not.toHaveBeenCalled();

    act(() => setVisibility("visible"));
    // The return-to-tab refresh fires without waiting a full interval…
    expect(onRefresh).toHaveBeenCalledTimes(1);
    // …and the steady loop resumes.
    act(() => vi.advanceTimersByTime(FLEET_POLL_INTERVAL_MS));
    expect(onRefresh).toHaveBeenCalledTimes(2);
  });

  it("never restarts the interval just because the callback identity changed", () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(({ cb }) => usePollingRefresh(cb), {
      initialProps: { cb: first },
    });

    act(() => vi.advanceTimersByTime(FLEET_POLL_INTERVAL_MS / 2));
    rerender({ cb: second });
    act(() => vi.advanceTimersByTime(FLEET_POLL_INTERVAL_MS / 2));

    // The half-elapsed interval completed on schedule, calling the LATEST cb.
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });
});
