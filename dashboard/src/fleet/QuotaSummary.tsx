import {
  LocalSafetyBudgetIndicator,
  QuotaWindowCard,
  type QuotaEvidenceSource,
  type QuotaFreshness,
  type QuotaWindowState,
} from "@venom/design-system/domain";
import type { QuotaWindow } from "../api/controlClient";
import {
  compactWindowLabel,
  formatBalanceValue,
  isBalanceWindow,
  windowSortSeconds,
} from "./quotaWindows";
import { formatDuration } from "./relativeTime";

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

// --- Compact variant (the redesigned account row, image 2) -----------------

/** The compact meter's tone from a real percentage: healthy <60, warning
 * 60–90, critical >90. Only ever called with a KNOWN pct — unknown windows
 * never reach a tone. */
function compactTone(pct: number): "healthy" | "warning" | "critical" {
  if (pct > 90) return "critical";
  if (pct >= 60) return "warning";
  return "healthy";
}

/**
 * One evidence window as a compact line: mono window label, a thin meter,
 * the percentage, and the reset countdown — the legacy reference's
 * 5H/7D/30D fill-bar rows. Balance windows render their remaining amount
 * as a value instead of a meter (no total to fill against). The same
 * truthfulness contract as the full card: a window whose used/total is
 * unknown renders its server state word ("unknown", "stale", …) over a
 * hatched bar — NEVER a fabricated 0%.
 */
function CompactQuotaLine(props: { window: QuotaWindow; nowMs: number; balanceCurrency?: "usd" }) {
  const w = props.window;
  const label = compactWindowLabel(w);

  if (isBalanceWindow(w)) {
    return (
      <div
        className="vnd-quota-line"
        title={`${w.remaining} ${w.unit} remaining · state: ${w.state} · freshness: ${w.freshness}`}
      >
        <span className="vnd-quota-label">{label}</span>
        <span className="vnd-quota-balance">{formatBalanceValue(w, props.balanceCurrency)}</span>
        <span className={`vnd-quota-pct vnd-quota-pct--${w.state === "available" ? "healthy" : "unknown"}`}>
          {w.state}
        </span>
      </div>
    );
  }

  const known = w.used != null && w.total != null && w.total > 0;
  const current = known && w.state === "available" && w.freshness === "fresh";

  if (!current) {
    const honestState = w.state === "stale" || w.freshness === "stale" ? "stale" : w.state;
    return (
      <div
        className="vnd-quota-line"
        title={`state: ${w.state} · freshness: ${w.freshness} — used/total not reported; never rendered as a number`}
      >
        <span className="vnd-quota-label">{label}</span>
        <span
          className="vnd-meter vnd-meter--unknown"
          role="img"
          aria-label={`${label} quota: ${honestState}`}
        />
        <span className="vnd-quota-pct vnd-quota-pct--unknown">{honestState}</span>
      </div>
    );
  }

  const used = w.used as number;
  const total = w.total as number;
  const pct = Math.round((used / total) * 100);
  const tone = compactTone(pct);
  return (
    <div className="vnd-quota-line" title={`${used} / ${total} ${w.unit} · state: ${w.state}`}>
      <span className="vnd-quota-label">{label}</span>
      <span
        className="vnd-meter"
        role="meter"
        aria-label={`${label} quota`}
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={used}
        aria-valuetext={`${used} of ${total} ${w.unit}`}
      >
        <span
          className={`vnd-meter-fill${tone === "healthy" ? "" : ` vnd-meter-fill--${tone}`}`}
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      </span>
      <span className={`vnd-quota-pct vnd-quota-pct--${tone}`}>{pct}%</span>
      {w.reset_at != null ? (
        <span className="vnd-quota-reset">
          Resets in {formatDuration(w.reset_at * 1000 - props.nowMs)}
        </span>
      ) : null}
    </div>
  );
}

export interface QuotaSummaryCompactProps {
  windows: QuotaWindow[];
  /** Injected clock for deterministic tests; defaults to Date.now(). */
  nowMs?: number;
  /** Presentation hint: how this provider denominates its balance window
   * (providerMeta.balanceCurrency). */
  balanceCurrency?: "usd";
}

/**
 * The account row's compact quota rendering (P2b-UI-003 redesign):
 * provider_evidence/owner_override windows as thin labelled meters,
 * local_safety windows through the same LocalSafetyBudgetIndicator the
 * full summary uses (Venom's own routing-safety budget is never presented
 * as provider evidence — docs/02 §3, 07 §5a). Zero windows render nothing —
 * the account row's meta line already reports "Quota: —", and a free
 * account's unlimited nature is conveyed by its "Free / ∞" funding badge.
 */
export function QuotaSummaryCompact(props: QuotaSummaryCompactProps) {
  const { windows, nowMs = Date.now(), balanceCurrency } = props;

  if (windows.length === 0) {
    return null;
  }

  // Balance windows are rendered by the account row's own balance chip
  // (next to the identity, like the legacy reference's header balance) —
  // the compact meter list carries the TIME-BOXED windows, shortest first.
  const evidenceWindows = windows
    .filter((w) => w.source !== "local_safety" && !isBalanceWindow(w))
    .sort((a, b) => windowSortSeconds(a) - windowSortSeconds(b));
  const localSafetyWindows = windows.filter((w) => w.source === "local_safety");
  const concurrencyWindow = localSafetyWindows.find((w) => w.window_type === "concurrency");
  const consumptionWindow = localSafetyWindows.find(
    (w) => w.window_type === "estimated_consumption",
  );

  return (
    <div className="vnd-quota-lines">
      {evidenceWindows.map((w) => (
        <CompactQuotaLine
          key={`${w.source}:${w.unit}:${w.window_type}:${w.window_key}`}
          window={w}
          nowMs={nowMs}
          balanceCurrency={balanceCurrency}
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
