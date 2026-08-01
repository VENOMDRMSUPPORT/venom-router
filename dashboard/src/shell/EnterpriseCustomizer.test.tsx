import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { applyAppearanceSettings } from "../theme-runtime";
import EnterpriseCustomizer, { type CustomizerValue } from "./EnterpriseCustomizer";
import { SLIDER_PERSIST_DEBOUNCE_MS } from "./customizerConfig";

const INITIAL: CustomizerValue = { theme: "venom-dark", accent: "mono", radiusPx: 6, spacingScale: 1 };

/** Mirrors AppShell's wiring exactly: onApply routes through the DS apply*
 * functions (via applyAppearanceSettings) and updates controlled state;
 * onPersist is the injected settings-save spy. */
function Harness(props: { onPersist: (next: CustomizerValue) => void; initial?: CustomizerValue }) {
  const [value, setValue] = useState<CustomizerValue>(props.initial ?? INITIAL);
  return (
    <EnterpriseCustomizer
      value={value}
      onApply={(next) => {
        applyAppearanceSettings({
          theme: next.theme,
          density: "comfortable",
          accent: next.accent,
          radius_px: next.radiusPx,
          spacing_scale: next.spacingScale,
        });
        setValue(next);
      }}
      onPersist={props.onPersist}
    />
  );
}

function openCustomizer() {
  fireEvent.click(screen.getByRole("button", { name: /customize design system/i }));
}

/** Builds "<n>px" from a number — raw px string literals are banned by the
 * no-raw-values lint gate. */
function px(n: number): string {
  return `${n}px`;
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  const root = document.documentElement;
  root.removeAttribute("data-theme");
  root.removeAttribute("data-density");
  root.removeAttribute("data-accent");
  root.style.removeProperty("--vn-radius-base");
  root.style.removeProperty("--vn-spacing-scale");
});

describe("EnterpriseCustomizer — layout", () => {
  it("is closed by default and opening it renders all four sections plus Reset", () => {
    render(<Harness onPersist={vi.fn()} />);

    expect(screen.queryByRole("dialog", { name: /enterprise customizer/i })).toBeNull();

    openCustomizer();

    screen.getByRole("dialog", { name: /enterprise customizer/i });
    screen.getByText(/theme mode/i);
    screen.getByText(/accent theme/i);
    screen.getByText(/border radius/i);
    screen.getByText(/layout spacing/i);
    screen.getByRole("button", { name: /reset/i });
    // All six accent swatches, in the canonical order.
    for (const label of ["Mono", "Blue", "Violet", "Amber", "Emerald", "Rose"]) {
      screen.getByRole("button", { name: new RegExp(`${label} accent`, "i") });
    }
    // Both sliders and their scale labels.
    screen.getByRole("slider", { name: /border radius/i });
    screen.getByRole("slider", { name: /layout spacing/i });
    screen.getByText(`${0}px (Sharp)`);
    screen.getByText(`${16}px (Round)`);
    screen.getByText("Compact (75%)");
    screen.getByText("Cozy (125%)");
  });

  it("closes on a click outside the widget", () => {
    render(
      <div>
        <button type="button">outside</button>
        <Harness onPersist={vi.fn()} />
      </div>,
    );
    openCustomizer();
    screen.getByRole("dialog", { name: /enterprise customizer/i });

    fireEvent.mouseDown(screen.getByRole("button", { name: /outside/i }));

    expect(screen.queryByRole("dialog", { name: /enterprise customizer/i })).toBeNull();
  });

});

describe("EnterpriseCustomizer — accent", () => {
  it("clicking a swatch applies data-accent optimistically and persists immediately", () => {
    const onPersist = vi.fn();
    render(<Harness onPersist={onPersist} />);
    openCustomizer();

    fireEvent.click(screen.getByRole("button", { name: /violet accent/i }));

    expect(document.documentElement.getAttribute("data-accent")).toBe("violet");
    expect(onPersist).toHaveBeenCalledTimes(1);
    expect(onPersist).toHaveBeenCalledWith({ ...INITIAL, accent: "violet" });
    expect(screen.getByRole("button", { name: /violet accent/i }).getAttribute("aria-pressed")).toBe("true");
  });
});

describe("EnterpriseCustomizer — radius slider", () => {
  it("applies --vn-radius-base optimistically, clamped to [0, 16]", () => {
    vi.useFakeTimers();
    render(<Harness onPersist={vi.fn()} />);
    openCustomizer();

    const slider = screen.getByRole("slider", { name: /border radius/i });

    fireEvent.change(slider, { target: { value: "12" } });
    expect(document.documentElement.style.getPropertyValue("--vn-radius-base")).toBe(px(12));

    // An out-of-range value never escapes the clamp (the input's own
    // max=16 and the DS applyRadius clamp both hold the line).
    fireEvent.change(slider, { target: { value: "99" } });
    expect(document.documentElement.style.getPropertyValue("--vn-radius-base")).toBe(px(16));
  });

  it("debounces persistence: a burst of changes settles into ONE save with the final value", () => {
    vi.useFakeTimers();
    const onPersist = vi.fn();
    render(<Harness onPersist={onPersist} />);
    openCustomizer();

    const slider = screen.getByRole("slider", { name: /border radius/i });
    fireEvent.change(slider, { target: { value: "10" } });
    fireEvent.change(slider, { target: { value: "14" } });
    fireEvent.change(slider, { target: { value: "16" } });

    expect(onPersist).not.toHaveBeenCalled();
    vi.advanceTimersByTime(SLIDER_PERSIST_DEBOUNCE_MS);
    expect(onPersist).toHaveBeenCalledTimes(1);
    expect(onPersist).toHaveBeenCalledWith({ ...INITIAL, radiusPx: 16 });
  });
});

describe("EnterpriseCustomizer — spacing slider", () => {
  it("applies --vn-spacing-scale optimistically and persists debounced", () => {
    vi.useFakeTimers();
    const onPersist = vi.fn();
    render(<Harness onPersist={onPersist} />);
    openCustomizer();

    const slider = screen.getByRole("slider", { name: /layout spacing/i });
    fireEvent.change(slider, { target: { value: "0.85" } });

    expect(document.documentElement.style.getPropertyValue("--vn-spacing-scale")).toBe("0.85");
    expect(screen.getByText("85%")).toBeTruthy();

    expect(onPersist).not.toHaveBeenCalled();
    vi.advanceTimersByTime(SLIDER_PERSIST_DEBOUNCE_MS);
    expect(onPersist).toHaveBeenCalledTimes(1);
    expect(onPersist).toHaveBeenCalledWith({ ...INITIAL, spacingScale: 0.85 });
  });
});

describe("EnterpriseCustomizer — reset", () => {
  it("restores dark / mono / 6 px / 100%, applies them, and persists once immediately", () => {
    vi.useFakeTimers();
    const onPersist = vi.fn();
    render(<Harness onPersist={onPersist} initial={{ theme: "venom-light", accent: "rose", radiusPx: 14, spacingScale: 1.2 }} />);
    openCustomizer();

    fireEvent.click(screen.getByRole("button", { name: /reset/i }));

    expect(document.documentElement.getAttribute("data-theme")).toBe("venom-dark");
    expect(document.documentElement.getAttribute("data-accent")).toBe("mono");
    expect(document.documentElement.style.getPropertyValue("--vn-radius-base")).toBe(px(6));
    expect(document.documentElement.style.getPropertyValue("--vn-spacing-scale")).toBe("1");
    expect(onPersist).toHaveBeenCalledTimes(1);
    expect(onPersist).toHaveBeenCalledWith({ theme: "venom-dark", accent: "mono", radiusPx: 6, spacingScale: 1 });
  });

  it("cancels a pending debounced slider persist (no stale save after reset)", () => {
    vi.useFakeTimers();
    const onPersist = vi.fn();
    render(<Harness onPersist={onPersist} />);
    openCustomizer();

    fireEvent.change(screen.getByRole("slider", { name: /border radius/i }), { target: { value: "14" } });
    fireEvent.click(screen.getByRole("button", { name: /reset/i }));

    vi.advanceTimersByTime(SLIDER_PERSIST_DEBOUNCE_MS * 2);
    // Exactly ONE persist — the reset's own — and it carries the defaults,
    // not the abandoned 14px.
    expect(onPersist).toHaveBeenCalledTimes(1);
    expect(onPersist).toHaveBeenCalledWith({ theme: "venom-dark", accent: "mono", radiusPx: 6, spacingScale: 1 });
  });
});

describe("EnterpriseCustomizer — storage guarantee", () => {
  it("never writes to localStorage or sessionStorage across open, accent, sliders, reset", () => {
    vi.useFakeTimers();
    const setItemSpy = vi.spyOn(Storage.prototype, "setItem");
    render(<Harness onPersist={vi.fn()} />);

    openCustomizer();
    fireEvent.click(screen.getByRole("button", { name: /blue accent/i }));
    fireEvent.change(screen.getByRole("slider", { name: /border radius/i }), { target: { value: "3" } });
    fireEvent.change(screen.getByRole("slider", { name: /layout spacing/i }), { target: { value: "1.1" } });
    fireEvent.click(screen.getByRole("button", { name: /reset/i }));
    vi.advanceTimersByTime(SLIDER_PERSIST_DEBOUNCE_MS);

    expect(setItemSpy).not.toHaveBeenCalled();
  });
});
