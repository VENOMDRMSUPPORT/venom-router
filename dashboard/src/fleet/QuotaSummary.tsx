import {
  LocalSafetyBudgetIndicator,
  QuotaWindowCard,
  type QuotaEvidenceSource,
  type QuotaFreshness,
  type QuotaWindowState,
} from "@venom/design-system/domain";
import type { QuotaWindow } from "../api/controlClient";

export interface QuotaSummaryProps {
  windows: QuotaWindow[];
}

/** Renders an epoch-seconds reset_at as a locale timestamp for
 * QuotaWindowCard's resetAt slot; `undefined` (never `null`) for an
 * absent reset, matching QuotaResetCountdown's own "no reset (balance)"
 * fallback for a windowless value. */
function formatResetAt(resetAt: number | null): string | undefined {
  if (resetAt == null) return undefined;
  return new Date(resetAt * 1000).toLocaleString();
}

/**
 * One account's quota windows (P3b-UI-001), composed ENTIRELY from the
 * frozen `@venom/design-system` Quota components — no local re-derivation
 * of state/source/freshness, no numeric fabrication. provider_evidence and
 * owner_override windows render through QuotaWindowCard (identity, source,
 * state, meter, reset, freshness all in one, so the source is always
 * visibly distinct); local_safety windows render through
 * LocalSafetyBudgetIndicator instead, so Venom's own routing-safety budget
 * is never presented as provider evidence (docs/02 §3, 07 §5a).
 *
 * Every value the server marked unknown (`null`) is passed through as
 * `undefined`, never coerced to a number — `w.total == null` still trips
 * QuotaWindowMeter's own "total == null" unknown-rendering branch, so this
 * is a type-shape conversion only, not a behavior change.
 */
export default function QuotaSummary(props: QuotaSummaryProps) {
  const { windows } = props;

  if (windows.length === 0) {
    // The same honest "—" idiom FleetOverview's own Models StatCard uses
    // for a not-yet-available count, rather than a zeroed meter.
    return (
      <span className="vn-caption" title="No quota windows tracked for this account yet">
        —
      </span>
    );
  }

  const evidenceWindows = windows.filter((w) => w.source !== "local_safety");
  const localSafetyWindows = windows.filter((w) => w.source === "local_safety");
  const concurrencyWindow = localSafetyWindows.find((w) => w.window_type === "concurrency");
  const consumptionWindow = localSafetyWindows.find(
    (w) => w.window_type === "estimated_consumption",
  );

  return (
    <div className="flex flex-col gap-2">
      {evidenceWindows.map((w) => (
        <QuotaWindowCard
          key={`${w.source}:${w.unit}:${w.window_type}:${w.window_key}`}
          name={w.window_type}
          windowKey={w.window_key}
          source={w.source as QuotaEvidenceSource}
          state={w.state as QuotaWindowState}
          used={w.used ?? undefined}
          total={w.total ?? undefined}
          reserved={w.reserved}
          unit={w.unit}
          resetAt={formatResetAt(w.reset_at)}
          freshness={w.freshness as QuotaFreshness}
        />
      ))}
      {localSafetyWindows.length > 0 ? (
        <LocalSafetyBudgetIndicator
          concurrencyUsed={concurrencyWindow?.reserved}
          concurrencyCap={concurrencyWindow?.limit_value ?? undefined}
          consumptionUsed={consumptionWindow?.used ?? undefined}
          consumptionCap={consumptionWindow?.limit_value ?? undefined}
          consumptionUnit={consumptionWindow?.unit}
        />
      ) : null}
    </div>
  );
}
