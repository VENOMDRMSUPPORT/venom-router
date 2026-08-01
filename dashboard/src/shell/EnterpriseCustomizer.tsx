import { useEffect, useRef, useState } from "react";
import { Button, Card, IconButton, SegmentedControl, Slider } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import { ACCENTS, ACCENT_LABELS, type ThemeName } from "../theme-runtime";
import { CUSTOMIZER_RESET, SLIDER_PERSIST_DEBOUNCE_MS, type CustomizerValue } from "./customizerConfig";

export type { CustomizerValue };

export interface EnterpriseCustomizerProps {
  value: CustomizerValue;
  /** Optimistic apply: the caller applies the appearance to the document
   * (via the DS apply* functions) and updates its own state — called on
   * EVERY change, immediately. */
  onApply: (next: CustomizerValue) => void;
  /** Persist to the settings API. Theme/accent/reset changes persist
   * immediately; slider changes persist on settle — a trailing
   * SLIDER_PERSIST_DEBOUNCE_MS debounce after the last change (covers both
   * drag-release and keyboard-arrow bursts). */
  onPersist: (next: CustomizerValue) => void;
}

/**
 * The floating Enterprise Customizer (2026-08-01 Vercel retheme spec,
 * Batch B): a fixed bottom-right 48 px round trigger expanding into a
 * ~320 px card with Theme Mode, Accent Theme, Border Radius and Layout
 * Spacing controls plus Reset — a 1:1 visual port of the legacy widget
 * onto OUR DS components/tokens. Every change applies optimistically via
 * the caller's onApply and persists server-side via onPersist (never
 * browser storage). Click-outside closes the card.
 */
export default function EnterpriseCustomizer(props: EnterpriseCustomizerProps) {
  const { value, onApply, onPersist } = props;
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const persistTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!isOpen) return;

    function handleClickOutside(event: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    }

    document.addEventListener("mousedown", handleClickOutside);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
    };
  }, [isOpen]);

  // Clear (not flush) any pending debounced persist on unmount — the
  // optimistic apply already happened, and firing a PUT during teardown
  // (e.g. logout) would race the session.
  useEffect(() => {
    return () => {
      if (persistTimerRef.current !== null) clearTimeout(persistTimerRef.current);
    };
  }, []);

  function applyAndPersistNow(next: CustomizerValue) {
    if (persistTimerRef.current !== null) {
      clearTimeout(persistTimerRef.current);
      persistTimerRef.current = null;
    }
    onApply(next);
    onPersist(next);
  }

  function applyAndPersistDebounced(next: CustomizerValue) {
    onApply(next);
    if (persistTimerRef.current !== null) clearTimeout(persistTimerRef.current);
    persistTimerRef.current = setTimeout(() => {
      persistTimerRef.current = null;
      onPersist(next);
    }, SLIDER_PERSIST_DEBOUNCE_MS);
  }

  return (
    <div ref={containerRef} className="vn-enterprise-customizer">
      <IconButton
        icon="sliders-horizontal"
        label="Customize design system"
        variant="primary"
        className="vn-enterprise-customizer-trigger"
        aria-expanded={isOpen}
        onClick={() => setIsOpen((open) => !open)}
      />

      {isOpen ? (
        <Card className="vn-enterprise-customizer-panel" role="dialog" aria-label="Enterprise customizer">
          <div className="mb-4 flex items-center justify-between border-b border-border-default pb-3">
            <div className="flex items-center gap-2">
              <Icon name="sliders-horizontal" size={14} />
              <h3 className="vn-overline">Enterprise Customizer</h3>
            </div>
            <Button
              variant="ghost"
              size="sm"
              icon="rotate-ccw"
              onClick={() => applyAndPersistNow({ ...CUSTOMIZER_RESET })}
            >
              Reset
            </Button>
          </div>

          <div className="flex flex-col gap-4">
            {/* Theme Mode (Light/Dark only — HC stays on the Settings page) */}
            <div className="flex flex-col gap-1.5">
              <span className="vn-overline" id="customizer-theme-label">
                Theme Mode
              </span>
              <SegmentedControl
                label="Theme mode"
                value={value.theme}
                onChange={(theme) => applyAndPersistNow({ ...value, theme: theme as ThemeName })}
                options={[
                  {
                    value: "venom-light",
                    label: (
                      <span className="flex items-center gap-2">
                        <Icon name="sun" size={12} />
                        Light
                      </span>
                    ),
                  },
                  {
                    value: "venom-dark",
                    label: (
                      <span className="flex items-center gap-2">
                        <Icon name="moon" size={12} />
                        Dark
                      </span>
                    ),
                  },
                ]}
              />
            </div>

            {/* Accent Theme */}
            <div className="flex flex-col gap-1.5">
              <span className="vn-overline">Accent Theme</span>
              <div className="grid grid-cols-6 gap-2">
                {ACCENTS.map((accent) => {
                  const isActive = value.accent === accent;
                  return (
                    <button
                      key={accent}
                      type="button"
                      aria-pressed={isActive}
                      aria-label={`${ACCENT_LABELS[accent]} accent`}
                      title={`${ACCENT_LABELS[accent]} accent`}
                      className={
                        "relative flex h-10 flex-col items-center justify-center rounded border " +
                        (isActive
                          ? "border-accent-default bg-accent-subtle-bg"
                          : "border-border-default bg-surface-secondary hover:border-border-strong")
                      }
                      onClick={() => applyAndPersistNow({ ...value, accent })}
                    >
                      {/* The swatch dot carries its OWN data-theme +
                          data-accent, so the DS's [data-accent] override
                          blocks (and their per-theme variants, e.g. light
                          emerald/rose) resolve var(--accent-default) to
                          THAT accent's color locally — swatch colors stay
                          100% token-driven, no raw hex anywhere. Mono has
                          no override block, so the dot shows the base
                          theme's own mono accent (black in light, white in
                          dark). */}
                      <span
                        className="h-4 w-4 rounded-full border border-border-default bg-accent-default"
                        data-theme={value.theme}
                        data-accent={accent}
                        aria-hidden="true"
                      />
                      <span className="vn-caption mt-1 max-w-full truncate px-0.5">{ACCENT_LABELS[accent]}</span>
                      {isActive ? (
                        <span
                          className="absolute right-0.5 top-0.5 flex h-3 w-3 items-center justify-center rounded-full bg-accent-default text-text-on-accent"
                          aria-hidden="true"
                        >
                          <Icon name="check" size={8} />
                        </span>
                      ) : null}
                    </button>
                  );
                })}
              </div>
            </div>

            {/* Border Radius */}
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between">
                <span className="vn-overline">Border Radius</span>
                <span className="vn-caption vn-mono-xs">{value.radiusPx}px</span>
              </div>
              <Slider
                label="Border radius"
                min={0}
                max={16}
                step={1}
                value={value.radiusPx}
                showValue={false}
                onChange={(e) => applyAndPersistDebounced({ ...value, radiusPx: Number(e.target.value) })}
              />
              <div className="flex justify-between">
                <span className="vn-caption vn-mono-xs">0px (Sharp)</span>
                <span className="vn-caption vn-mono-xs">8px</span>
                <span className="vn-caption vn-mono-xs">16px (Round)</span>
              </div>
            </div>

            {/* Layout Spacing */}
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between">
                <span className="vn-overline">Layout Spacing</span>
                <span className="vn-caption vn-mono-xs">{Math.round(value.spacingScale * 100)}%</span>
              </div>
              <Slider
                label="Layout spacing"
                min={0.75}
                max={1.25}
                step={0.05}
                value={value.spacingScale}
                showValue={false}
                onChange={(e) => applyAndPersistDebounced({ ...value, spacingScale: Number(e.target.value) })}
              />
              <div className="flex justify-between">
                <span className="vn-caption vn-mono-xs">Compact (75%)</span>
                <span className="vn-caption vn-mono-xs">100%</span>
                <span className="vn-caption vn-mono-xs">Cozy (125%)</span>
              </div>
            </div>
          </div>
        </Card>
      ) : null}
    </div>
  );
}
