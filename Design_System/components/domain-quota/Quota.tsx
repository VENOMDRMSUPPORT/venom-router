import * as React from "react";
import { Icon } from "../icons/Icon";
import { Badge, BadgeTone } from "../display/Badge";
import { Meter } from "../feedback/Meter";

/* Quota (docs/02 §3, 04 §5, 05 §4): multiple concurrent windows per account;
   unknown != unlimited; provider evidence, local safety, and owner override
   are never conflated; reservations have EXACTLY five stored states. */

interface StatusMeta {
  tone: BadgeTone;
  icon: string;
  label: string;
}

export type QuotaFreshness = "fresh" | "stale" | "unknown";

const FRESHNESS: Record<QuotaFreshness, StatusMeta | null> = {
  fresh:   null,
  stale:   { tone: "warning", icon: "clock",       label: "stale" },
  unknown: { tone: "unknown", icon: "circle-help", label: "unknown" },
};

export interface QuotaFreshnessBadgeProps {
  state?: QuotaFreshness;
  age?: React.ReactNode;
}

/** QuotaFreshnessBadge — evidence freshness; fresh renders nothing (no noise). */
export function QuotaFreshnessBadge(props: QuotaFreshnessBadgeProps) {
  const { state, age } = props;
  const m = state ? FRESHNESS[state] : null;
  if (!m) return null;
  return <Badge tone={m.tone} icon={m.icon} title={"freshness: " + state + (age ? " · observed " + age + " ago" : "")}>{m.label}{age ? <> · {age}</> : null}</Badge>;
}

/** The evidence sources a quota window can be sourced from — never conflated. */
export type QuotaEvidenceSource = "provider_evidence" | "local_safety" | "owner_override";

interface SourceMeta {
  icon: string;
  label: string;
}

const SOURCE: Record<QuotaEvidenceSource, SourceMeta> = {
  provider_evidence: { icon: "shield-check", label: "provider evidence" },
  local_safety:      { icon: "shield",       label: "local safety" },
  owner_override:    { icon: "user-round",   label: "owner override" },
};

export interface QuotaResetCountdownProps {
  resetAt?: React.ReactNode;
  remaining?: React.ReactNode;
}

/** QuotaResetCountdown — reset moment + remaining time; absent reset (balance windows) says so. */
export function QuotaResetCountdown(props: QuotaResetCountdownProps) {
  const { resetAt, remaining } = props;
  if (!resetAt) return <span className="vn-quota-reset"><Icon name="clock" size={11} />no reset (balance)</span>;
  return <span className="vn-quota-reset" title={"resets at " + resetAt}><Icon name="clock" size={11} />resets in {remaining || resetAt}</span>;
}

export interface QuotaUnknownStateProps {
  note?: React.ReactNode;
}

/** QuotaUnknownState — the honest no-evidence rendering. Never a number, never a fill. */
export function QuotaUnknownState(props: QuotaUnknownStateProps) {
  const { note } = props;
  return (
    <div className="vn-quota-window" style={{ padding: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
        <Badge tone="unknown" icon="circle-help">No provider quota evidence</Badge>
      </div>
      <Meter unknown label="Provider quota" />
      <span className="vn-caption">{note || "Unknown is not unlimited — execution still reserves against this account's local-safety windows."}</span>
    </div>
  );
}

export type QuotaWindowState = "available" | "insufficient" | "exhausted" | "unknown" | "stale";

export interface QuotaWindowMeterProps {
  used?: number;
  total?: number | null;
  reserved?: number;
  unit?: string;
  state?: QuotaWindowState;
  label?: string;
}

/** QuotaWindowMeter — one window's meter + figures. state: available|insufficient|exhausted|unknown|stale. */
export function QuotaWindowMeter(props: QuotaWindowMeterProps) {
  const { used = 0, total, reserved = 0, unit = "", state = "available", label } = props;
  if (state === "unknown" || state === "stale" || total == null) {
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <Meter unknown label={label} />
        <span className="vn-caption" style={{ display: "flex", gap: 6, alignItems: "center" }}>
          <QuotaFreshnessBadge state={state === "stale" ? "stale" : "unknown"} />
          {state === "stale" ? "treated as unknown · refresh scheduled" : "never rendered as a number"}
        </span>
      </div>
    );
  }
  const pct = Math.round((used / total) * 100);
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <div className={"vn-meter" + (state === "exhausted" ? " is-critical" : state === "insufficient" || pct >= 75 ? " is-warning" : "")}
        role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={total} aria-valuenow={used}
        aria-valuetext={used.toLocaleString() + " of " + total.toLocaleString() + " " + unit}>
        <div style={{ width: Math.min(100, pct) + "%", display: "flex" }}></div>
        {reserved > 0 ? <div className="vn-meter-reserved" style={{ width: Math.min(100 - pct, (reserved / total) * 100) + "%", marginTop: -8 }} title={reserved.toLocaleString() + " " + unit + " reserved by active reservations"}></div> : null}
      </div>
      <span className="vn-quota-figures">
        <span className="vn-quota-used">{used.toLocaleString()}</span>
        <span className="vn-quota-total">/ {total.toLocaleString()} {unit} · {pct}%{reserved ? " · " + reserved.toLocaleString() + " reserved" : ""}</span>
      </span>
    </div>
  );
}
/** QuotaMeter — docs/07 inventory alias. */
export function QuotaMeter(props: QuotaWindowMeterProps) { return <QuotaWindowMeter {...props} />; }

export interface QuotaWindowCardProps {
  name: React.ReactNode;
  windowKey: React.ReactNode;
  source?: QuotaEvidenceSource;
  state?: QuotaWindowState;
  used?: number;
  total?: number;
  reserved?: number;
  unit?: string;
  resetAt?: React.ReactNode;
  resetIn?: React.ReactNode;
  freshness?: QuotaFreshness;
  age?: React.ReactNode;
}

/** QuotaWindowCard — one quota window: identity (mono window_key), source, state, meter, reset, freshness. */
export function QuotaWindowCard(props: QuotaWindowCardProps) {
  const { name, windowKey, source = "provider_evidence", state = "available", used, total, reserved, unit, resetAt, resetIn, freshness = "fresh", age } = props;
  const src = SOURCE[source] || SOURCE.provider_evidence;
  const stateBadge: Record<QuotaWindowState, StatusMeta | null> = {
    available:    null,
    insufficient: { tone: "warning",  icon: "triangle-alert", label: "insufficient" },
    exhausted:    { tone: "critical", icon: "ban",            label: "exhausted" },
    unknown:      { tone: "unknown",  icon: "circle-help",    label: "unknown" },
    stale:        { tone: "warning",  icon: "clock",          label: "stale" },
  };
  const sb = stateBadge[state];
  return (
    <div className="vn-card vn-quota-window">
      <div className="vn-quota-head">
        <span className="vn-title-sub" style={{ flex: 1 }}>{name}</span>
        {sb ? <Badge tone={sb.tone} icon={sb.icon}>{sb.label}</Badge> : null}
      </div>
      <div style={{ display: "flex", gap: "var(--space-2)", alignItems: "center", flexWrap: "wrap" }}>
        <span className="vn-quota-key">{windowKey}</span>
        <span className="vn-reason-code" title={"Quota source: " + src.label + " — sources are never conflated"}><Icon name={src.icon} size={11} />{source}</span>
        <QuotaFreshnessBadge state={freshness} age={age} />
      </div>
      <QuotaWindowMeter used={used} total={total} reserved={reserved} unit={unit} state={state} label={typeof name === "string" ? name : undefined} />
      <QuotaResetCountdown resetAt={resetAt} remaining={resetIn} />
    </div>
  );
}

export interface LocalSafetyBudgetIndicatorProps {
  concurrencyUsed?: number;
  concurrencyCap?: number;
  consumptionUsed?: number;
  consumptionCap?: number;
  consumptionUnit?: string;
}

/** LocalSafetyBudgetIndicator — Venom's own owner-policy budget; explicitly labeled, never presented as provider evidence. */
export function LocalSafetyBudgetIndicator(props: LocalSafetyBudgetIndicatorProps) {
  const { concurrencyUsed = 0, concurrencyCap = 1, consumptionUsed, consumptionCap, consumptionUnit = "requests" } = props;
  return (
    <div className="vn-card vn-quota-window" style={{ borderStyle: "dashed" }}>
      <div className="vn-quota-head">
        <span className="vn-title-sub" style={{ flex: 1 }}>Local safety budget</span>
        <span className="vn-reason-code" title="Owner-policy routing-safety budget — authoritative local policy, NOT provider evidence"><Icon name="shield" size={11} />local_safety</span>
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "max-content 1fr", gap: "var(--space-2) var(--space-3)", alignItems: "center", fontSize: "var(--font-size-xs)" }}>
        <span className="vn-quota-key">local:concurrency</span>
        <span className="vn-data">{concurrencyUsed} / {concurrencyCap} in-flight</span>
        {consumptionCap != null ? <><span className="vn-quota-key">local:{consumptionUnit}</span><QuotaWindowMeter used={consumptionUsed} total={consumptionCap} unit={consumptionUnit} label="Estimated local consumption" /></> : null}
      </div>
      <span className="vn-caption">Every account carries this budget; every execution reserves against it — including accounts with no provider quota endpoint.</span>
    </div>
  );
}

/* Reservation machine — EXACTLY five stored states; expires_at is a deadline, not a state. */
export type ReservationState = "reserved" | "reconciliation_pending" | "settled" | "released" | "unknown_consumption";

interface ReservationMeta {
  tone: BadgeTone;
  icon: string;
  label: string;
}

const RESERVATION: Record<ReservationState, ReservationMeta> = {
  reserved:               { tone: "info",     icon: "clock",          label: "Reserved" },
  reconciliation_pending: { tone: "warning",  icon: "refresh-cw",     label: "Reconciliation pending" },
  settled:                { tone: "healthy",  icon: "circle-check",   label: "Settled" },
  released:               { tone: "inactive", icon: "check",          label: "Released" },
  unknown_consumption:    { tone: "critical", icon: "triangle-alert", label: "Unknown consumption" },
};

export interface ReservationStateBadgeProps {
  state: ReservationState;
  confidence?: "low";
}

/** ReservationStateBadge — unresolved possible consumption is NEVER neutral/success. */
export function ReservationStateBadge(props: ReservationStateBadgeProps) {
  const { state, confidence } = props;
  const m = RESERVATION[state] || RESERVATION.reserved;
  const titles: Record<ReservationState, string> = {
    reserved: "Headroom debited; awaiting dispatch/settle",
    reconciliation_pending: "Dispatched with ambiguous outcome — headroom stays debited; never auto-released on deadline",
    settled: "Actual cost confirmed" + (confidence === "low" ? " (low-confidence estimate)" : ""),
    released: "Never dispatched, or provider proved no consumption",
    unknown_consumption: "Terminal evidence gap — usage_gap audited; account re-baselined at next quota sync",
  };
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-1)" }}>
      <Badge tone={m.tone} icon={m.icon} mono title={titles[state]}>{state}</Badge>
      {confidence === "low" ? <Badge tone="warning" icon="triangle-alert">confidence low</Badge> : null}
    </span>
  );
}

export interface ReconciliationStatusProps {
  state: ReservationState;
  attemptId?: React.ReactNode;
  windows?: string[];
  nextRetry?: React.ReactNode;
  attempts?: React.ReactNode;
  onResync?: () => void;
  onAcceptEstimate?: () => void;
}

/** ReconciliationStatus — a pending/terminal reconciliation item with owner actions. */
export function ReconciliationStatus(props: ReconciliationStatusProps) {
  const { state, attemptId, windows = [], nextRetry, attempts, onResync, onAcceptEstimate } = props;
  return (
    <div className="vn-evidence-row" style={{ gridTemplateColumns: "max-content 1fr max-content" }}>
      <ReservationStateBadge state={state} />
      <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
        <span className="vn-mono-xs">{attemptId}</span>
        <span className="vn-caption">
          {windows.length ? "holds " + windows.join(", ") : ""}
          {state === "reconciliation_pending" && nextRetry ? <> · retry {attempts} in {nextRetry}</> : null}
          {state === "unknown_consumption" ? " · usage_gap recorded · re-baseline at next sync" : ""}
        </span>
      </span>
      <span style={{ display: "flex", gap: "var(--space-1)" }}>
        {onResync ? <button type="button" className="vn-btn vn-btn--secondary vn-btn--sm" onClick={onResync}>Re-sync</button> : null}
        {onAcceptEstimate ? <button type="button" className="vn-btn vn-btn--ghost vn-btn--sm" onClick={onAcceptEstimate}>Accept estimate</button> : null}
      </span>
    </div>
  );
}

export interface QuotaSummaryWindow {
  windowKey: string;
  used?: number;
  total?: number;
  unit?: string;
  state?: QuotaWindowState;
  freshness?: QuotaFreshness;
  age?: React.ReactNode;
  reserved?: number;
}

export interface MultiWindowQuotaSummaryProps {
  windows?: QuotaSummaryWindow[];
}

/** MultiWindowQuotaSummary — the most-restrictive-wins account rollup + per-window mini list. */
export function MultiWindowQuotaSummary(props: MultiWindowQuotaSummaryProps) {
  const { windows = [] } = props;
  const rank: Record<QuotaWindowState, number> = { exhausted: 0, insufficient: 1, stale: 2, unknown: 3, available: 4 };
  const worst = windows.slice().sort((a, b) => (rank[a.state ?? "available"] ?? 4) - (rank[b.state ?? "available"] ?? 4))[0];
  const worstBadge: Record<QuotaWindowState, StatusMeta> = {
    exhausted:    { tone: "critical", icon: "ban",            label: "Exhausted" },
    insufficient: { tone: "warning",  icon: "triangle-alert", label: "Insufficient" },
    stale:        { tone: "warning",  icon: "clock",          label: "Stale" },
    unknown:      { tone: "unknown",  icon: "circle-help",    label: "Unknown" },
    available:    { tone: "healthy",  icon: "circle-check",   label: "Available" },
  };
  const w = worst ? worstBadge[worst.state ?? "available"] : worstBadge.unknown;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
      <span style={{ display: "inline-flex", gap: "var(--space-2)", alignItems: "center" }}>
        <Badge tone={w.tone} icon={w.icon} title="Most restrictive window governs the attempt">{w.label}</Badge>
        <span className="vn-caption">{windows.length} concurrent windows · most restrictive governs</span>
      </span>
      <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}>
        {windows.map((win) => (
          <div key={win.windowKey} style={{ display: "grid", gridTemplateColumns: "150px 1fr max-content", gap: "var(--space-2)", alignItems: "center", fontSize: "var(--font-size-xs)" }}>
            <span className="vn-quota-key vn-truncate" title={win.windowKey}>{win.windowKey}</span>
            <QuotaWindowMeter {...win} label={win.windowKey} />
            <QuotaFreshnessBadge state={win.freshness || "fresh"} age={win.age} />
          </div>
        ))}
      </div>
    </div>
  );
}
