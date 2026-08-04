// Presentation helpers for an account's quota windows (shared by the
// QuotaSummary components and AccountRow's balance chip) — kept out of the
// component files so those export only components (react-refresh contract).

import type { QuotaWindow } from "../api/controlClient";

/** The legacy reference's window labels (5H / 7D / 30D) derived from the
 * window's TYPE first, then its rolling:<n>s synthetic key, then the raw
 * key/unit — presentation only, the server vocabulary is never renamed in
 * tooltips. */
const WINDOW_TYPE_LABELS: Record<string, string> = {
  rolling_5h: "5H",
  rolling_7d: "7D",
  rolling_30d: "30D",
  balance: "BALANCE",
};

/** rolling:<seconds>s → 5H / 7D / 30D-style label (whole hours/days only;
 * anything irregular falls through to the verbatim key). */
function labelFromRollingKey(key: string): string | null {
  const m = /^rolling:(\d+)s$/.exec(key);
  if (!m) return null;
  const seconds = Number(m[1]);
  if (seconds % 86400 === 0) return `${seconds / 86400}D`;
  if (seconds % 3600 === 0) return `${seconds / 3600}H`;
  return null;
}

export function compactWindowLabel(w: QuotaWindow): string {
  return (
    WINDOW_TYPE_LABELS[w.window_type] ??
    labelFromRollingKey(w.window_key) ??
    (w.window_key || w.unit).toUpperCase()
  );
}

/** A balance-shaped window (a non-time-boxed remaining amount — e.g.
 * clinepass's credit balance). Rendered as a VALUE, not a 0–100 meter:
 * there is no total to fill against. */
export function isBalanceWindow(w: QuotaWindow): boolean {
  return w.window_type === "balance" && w.remaining != null;
}

/** Formats a balance window's remaining amount: "$4.83" for usd-denominated
 * providers (presentation hint from providerMeta), else the server's unit
 * word verbatim. */
export function formatBalanceValue(w: QuotaWindow, currency?: "usd"): string {
  const value = w.remaining as number;
  if (currency === "usd") {
    return `$${value.toFixed(2)}`;
  }
  return `${Number.isInteger(value) ? value : value.toFixed(2)} ${w.unit}`;
}

/** rolling-window ordering seconds (5H before 7D before 30D — the legacy
 * reference's row order); windows with no readable duration sort last in
 * their original order. */
export function windowSortSeconds(w: QuotaWindow): number {
  const byType: Record<string, number> = {
    rolling_5h: 18000,
    rolling_7d: 604800,
    rolling_30d: 2592000,
  };
  if (byType[w.window_type] != null) return byType[w.window_type];
  const m = /^rolling:(\d+)s$/.exec(w.window_key);
  if (m) return Number(m[1]);
  return Number.MAX_SAFE_INTEGER;
}
