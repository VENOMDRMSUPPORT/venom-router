import * as React from "react";
import { Icon } from "../icons/Icon";
import { Badge, BadgeTone } from "../display/Badge";

/* Routing domain (docs/05). Tier accents come ONLY from tier.* tokens;
   exclusion reasons are the typed codes verbatim; the competitive band and
   distribution policies are made visible, never implied. */

interface StatusMeta {
  tone: BadgeTone;
  icon: string;
  label: string;
}

export type Tier = "lite" | "pro" | "max";

interface TierMeta {
  label: string;
  id: string;
  tone: BadgeTone;
}

const TIER_META: Record<Tier, TierMeta> = {
  lite: { label: "LITE", id: "venom/lite", tone: "tier-lite" },
  pro:  { label: "PRO",  id: "venom/pro",  tone: "tier-pro" },
  max:  { label: "MAX",  id: "venom/max",  tone: "tier-max" },
};

export interface TierBadgeProps {
  tier: Tier;
  showId?: boolean;
}

/** TierBadge — THE single way a tier is labeled anywhere. */
export function TierBadge(props: TierBadgeProps) {
  const { tier, showId = false } = props;
  const m = TIER_META[tier];
  if (!m) return <Badge tone="unknown" icon="circle-help">unknown tier</Badge>;
  return <Badge tone={m.tone} mono title={m.id}>{m.label}{showId ? <> · {m.id}</> : null}</Badge>;
}

interface TierPolicy {
  funding: string;
  ctx: string;
  thinking: string;
  attempts: number;
}

export interface TierPolicySummaryProps {
  tier: Tier;
}

/** TierPolicySummary — the tier's policy facts: funding rule, context ceiling, thinking budget, fallback depth. */
export function TierPolicySummary(props: TierPolicySummaryProps) {
  const { tier } = props;
  const POLICIES: Record<Tier, TierPolicy> = {
    lite: { funding: "Free accounts only — paid is a hard rejection, even under exhaustion (fail closed)", ctx: "256K", thinking: "none", attempts: 3 },
    pro:  { funding: "~25% paid / ~75% free target — deficit controller per workload bucket", ctx: "512K", thinking: "extended", attempts: 4 },
    max:  { funding: "No funding-mix target — quality-first, then quota-fair (DRR + P2C)", ctx: "1M", thinking: "ultra", attempts: 5 },
  };
  const p = POLICIES[tier];
  if (!p) return null;
  return (
    <div className="vn-card vn-card--pad" style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
      <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
        <TierBadge tier={tier} />
        <span className="vn-mono-xs vn-text-muted">{TIER_META[tier].id}</span>
      </div>
      <dl className="vn-kv" style={{ gridTemplateColumns: "max-content 1fr" }}>
        <dt>Funding</dt><dd className="vn-body-compact">{p.funding}</dd>
        <dt>Context ceiling</dt><dd className="vn-data">{p.ctx} <span className="vn-caption">requests above the ceiling are rejected, never auto-promoted</span></dd>
        <dt>Thinking</dt><dd className="vn-data">{p.thinking}</dd>
        <dt>Fallback attempts</dt><dd className="vn-data">{p.attempts}</dd>
      </dl>
    </div>
  );
}

export interface CandidateRejectionReasonProps {
  code: string;
  blocking?: boolean;
  detail?: string;
}

/** CandidateRejectionReason — a typed exclusion code, verbatim, mono. */
export function CandidateRejectionReason(props: CandidateRejectionReasonProps) {
  const { code, blocking = true, detail } = props;
  return (
    <span className={"vn-reason-code" + (blocking ? " vn-reason-code--blocking" : " vn-reason-code--note")} title={detail || (blocking ? "Hard exclusion" : "Non-blocking note")}>
      <Icon name={blocking ? "ban" : "info"} size={11} />{code}
    </span>
  );
}
export function ReasonCode(props: CandidateRejectionReasonProps) { return <CandidateRejectionReason {...props} />; }

export interface ScoreFactor {
  name: string;
  value: number;
  missing?: boolean;
  weight?: number;
}

export interface CandidateScoreBreakdownProps {
  factors?: ScoreFactor[];
  total?: number;
}

const scoreTotalStyle: React.CSSProperties = { fontWeight: "var(--font-weight-semibold)" as React.CSSProperties["fontWeight"] };

/** CandidateScoreBreakdown — Step-5 factor bars (0-1, neutral 0.5 for missing). */
export function CandidateScoreBreakdown(props: CandidateScoreBreakdownProps) {
  const { factors = [], total } = props;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}>
      {factors.map((f) => (
        <div className="vn-score-row" key={f.name}>
          <span className="vn-caption">{f.name}{f.missing ? " (neutral)" : ""}{f.weight != null ? " · w " + f.weight : ""}</span>
          <div className="vn-score-bar" role="img" aria-label={f.name + " " + f.value}><div style={{ width: (f.value * 100) + "%", background: f.missing ? "var(--status-unknown-border)" : undefined }}></div></div>
          <span className="vn-score-val">{f.value.toFixed(2)}</span>
        </div>
      ))}
      {total != null ? (
        <div className="vn-score-row" style={{ borderTop: "var(--border-hairline) solid var(--border-subtle)", paddingTop: "var(--space-1)" }}>
          <span className="vn-label">weighted total</span><span></span><span className="vn-score-val" style={scoreTotalStyle}>{total.toFixed(2)}</span>
        </div>
      ) : null}
    </div>
  );
}

export interface CompetitiveBandCandidate {
  name: string;
  quality: number;
}

export interface CompetitiveBandIndicatorProps {
  tier?: Tier;
  top?: number;
  candidates?: CompetitiveBandCandidate[];
}

/** CompetitiveBandIndicator — quality band below the top candidate (Pro <= 0.08, Max <= 0.03); never auto-widened. */
export function CompetitiveBandIndicator(props: CompetitiveBandIndicatorProps) {
  const { tier = "pro", top = 1, candidates = [] } = props;
  const width = tier === "max" ? 0.03 : 0.08;
  const lo = top - width;
  const pct = (v: number) => Math.max(0, Math.min(100, v * 100));
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}>
      <div className="vn-band" role="img" aria-label={"Competitive band: quality within " + width + " of top (" + top.toFixed(2) + ")"}>
        <div className="vn-band-zone" style={{ left: pct(lo) + "%", width: pct(top) - pct(lo) + "%" }}></div>
        {candidates.map((c) => (
          <span key={c.name} className="vn-band-mark" data-in-band={String(c.quality >= lo)} style={{ left: "calc(" + pct(c.quality) + "% - 1px)" }} title={c.name + " · quality " + c.quality.toFixed(2) + (c.quality >= lo ? " · in band" : " · dropped (outside band)")}></span>
        ))}
      </div>
      <span className="vn-caption">band ≤ {width.toFixed(2)} below top ({tier}) · outside = dropped · never auto-widened · &lt;2 in band ⇒ proceed without widening</span>
    </div>
  );
}

export interface WorkloadProfileBadgeProps {
  properties?: string[];
}

/** WorkloadProfileBadge — deterministic multi-label bucket (normalized → sorted → deduped). */
export function WorkloadProfileBadge(props: WorkloadProfileBadgeProps) {
  const { properties = ["standard"] } = props;
  const key = Array.from(new Set(properties.map((p) => p.toLowerCase()))).sort().join("+");
  return (
    <span style={{ display: "inline-flex", gap: "var(--space-1)", alignItems: "center", flexWrap: "wrap" }} title={"workload_profile_bucket: " + key}>
      {key.split("+").map((p) => <Badge key={p} tone="inactive" mono>{p}</Badge>)}
    </span>
  );
}

export interface CooldownBadgeProps {
  scope?: string;
  retryAfter?: React.ReactNode;
}

/** CooldownBadge — scoped cooldown as an eligibility input (not an error). */
export function CooldownBadge(props: CooldownBadgeProps) {
  const { scope = "account", retryAfter } = props;
  return <Badge tone="warning" icon="hourglass" title={"Cooldown at " + scope + " scope — eligibility input, retried after expiry"}>cooldown · {scope}{retryAfter ? <> · {retryAfter}</> : null}</Badge>;
}

export type BreakerState = "closed" | "open" | "half_open";

const BREAKER: Record<BreakerState, StatusMeta> = {
  closed:    { tone: "healthy", icon: "circle-check",  label: "closed" },
  open:      { tone: "critical", icon: "ban",          label: "open" },
  half_open: { tone: "warning", icon: "flask-conical", label: "half-open" },
};

export interface CircuitBreakerStateProps {
  state?: BreakerState;
  scope?: string;
  reopensIn?: React.ReactNode;
  cycle?: number;
}

/** CircuitBreakerState — scoped breaker with adaptive backoff note. */
export function CircuitBreakerState(props: CircuitBreakerStateProps) {
  const { state, scope, reopensIn, cycle } = props;
  const m = (state && BREAKER[state]) || BREAKER.closed;
  return (
    <span style={{ display: "inline-flex", gap: "var(--space-1)", alignItems: "center" }}>
      <Badge tone={m.tone} icon={m.icon} mono title={"Circuit breaker (" + scope + "): " + m.label + (cycle ? " · backoff cycle " + cycle + " (doubles, cap ~16x)" : "")}>{scope}: {m.label}</Badge>
      {state === "open" && reopensIn ? <span className="vn-caption vn-mono-xs">half-open in {reopensIn}</span> : null}
      {state === "half_open" ? <span className="vn-caption">probe next request</span> : null}
    </span>
  );
}

export type FallbackOutcome = "succeeded" | "failed" | "skipped";

export interface FallbackAttempt {
  route: React.ReactNode;
  outcome?: FallbackOutcome;
  code?: string;
  detail?: string;
}

export interface FallbackChainProps {
  attempts?: FallbackAttempt[];
  budget?: React.ReactNode;
}

/** FallbackChain — the attempt sequence; funding/capability boundaries are never crossed. */
export function FallbackChain(props: FallbackChainProps) {
  const { attempts = [], budget } = props;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}>
      <div className="vn-fallback-chain">
        {attempts.map((a, i) => (
          <React.Fragment key={i}>
            {i > 0 ? <Icon name="corner-down-right" size={12} className="vn-fallback-arrow" /> : null}
            <span className="vn-fallback-step" data-outcome={a.outcome} title={(a.detail || "") + (a.code ? " · " + a.code : "")}>
              <Icon name={a.outcome === "succeeded" ? "circle-check" : a.outcome === "failed" ? "circle-x" : "chevron-right"} size={11} />
              <span className="vn-mono-xs">#{i + 1}</span> {a.route}
              {a.code ? <span className="vn-mono-xs vn-text-muted">{a.code}</span> : null}
            </span>
          </React.Fragment>
        ))}
      </div>
      {budget ? <span className="vn-caption">attempt budget {budget} · fallback never crosses funding or capability boundaries · streaming falls back only before first byte</span> : null}
    </div>
  );
}

export type RoutingReservationOutcome = "reserved" | "reconciliation_pending" | "settled" | "released" | "unknown_consumption";

export interface RoutingAttempt {
  route: React.ReactNode;
  outcome?: "succeeded" | "failed";
  reservation?: RoutingReservationOutcome;
  code?: React.ReactNode;
  detail?: React.ReactNode;
  latency?: React.ReactNode;
}

export interface RoutingAttemptTimelineProps {
  attempts?: RoutingAttempt[];
}

/** RoutingAttemptTimeline — reserve → execute → settle/release per attempt. */
export function RoutingAttemptTimeline(props: RoutingAttemptTimelineProps) {
  const { attempts = [] } = props;
  return (
    <ol className="vn-timeline">
      {attempts.map((a, i) => (
        <li key={i}>
          <span className={"vn-timeline-dot vn-timeline-dot--" + (a.outcome === "succeeded" ? "healthy" : a.outcome === "failed" ? "critical" : "warning")} aria-hidden="true"></span>
          <div className="vn-body-compact" style={{ display: "flex", gap: "var(--space-2)", alignItems: "center", flexWrap: "wrap" }}>
            <span className="vn-mono-xs">attempt {i + 1}</span> {a.route}
            {a.reservation ? <Badge tone={a.reservation === "settled" ? "healthy" : a.reservation === "released" ? "inactive" : a.reservation === "reconciliation_pending" ? "warning" : "info"} mono>{a.reservation}</Badge> : null}
            {a.code ? <span className="vn-reason-code">{a.code}</span> : null}
          </div>
          <div className="vn-caption">{a.detail}{a.latency ? <> · {a.latency}</> : null}</div>
        </li>
      ))}
    </ol>
  );
}

export interface RouteCandidate {
  route: React.ReactNode;
  funding?: "free" | "paid" | "unknown";
  quality?: number;
  score?: number;
  outcome?: "chosen" | "excluded" | "in_band";
  reasons?: string[];
  clamp?: React.ReactNode;
}

export interface RouteDecisionTraceProps {
  candidates?: RouteCandidate[];
  label?: string;
}

/** RouteDecisionTrace — "why this route?": candidates, scores, typed exclusions, chosen row emphasized. */
export function RouteDecisionTrace(props: RouteDecisionTraceProps) {
  const { candidates = [], label = "Route decision" } = props;
  return (
    <div className="vn-table-wrap">
      <table className="vn-table" aria-label={label}>
        <thead><tr><th>Candidate</th><th>Funding</th><th className="vn-numeric">Quality</th><th className="vn-numeric">Score</th><th>Outcome</th></tr></thead>
        <tbody>
          {candidates.map((c, i) => (
            <tr key={i} className="vn-trace-row" data-outcome={c.outcome}>
              <td className="vn-mono">{c.route}</td>
              <td>{c.funding ? <Badge tone={c.funding === "free" ? "healthy" : c.funding === "paid" ? "info" : "unknown"} icon={c.funding === "free" ? "hand-coins" : c.funding === "paid" ? "credit-card" : "circle-help"}>{c.funding}</Badge> : null}</td>
              <td className="vn-numeric">{c.quality != null ? c.quality.toFixed(2) : "—"}</td>
              <td className="vn-numeric">{c.score != null ? c.score.toFixed(2) : "—"}</td>
              <td>
                {c.outcome === "chosen" ? <Badge tone="accent" icon="circle-check">chosen</Badge>
                  : c.outcome === "excluded" ? <span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>{(c.reasons || []).map((r) => <CandidateRejectionReason key={r} code={r} />)}</span>
                  : <span className="vn-caption">in band, not selected</span>}
                {c.clamp ? <span className="vn-reason-code vn-reason-code--note" style={{ marginLeft: 4 }} title="Thinking budget clamped — reported, non-blocking"><Icon name="brain" size={11} />{c.clamp}</span> : null}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
/** RouteExplain — docs/07 inventory alias. */
export function RouteExplain(props: RouteDecisionTraceProps) { return <RouteDecisionTrace {...props} />; }

export interface FundingMixIndicatorProps {
  /** 0-1. */
  paidShare: number;
  /** 0-1. */
  target?: number;
  bucket?: React.ReactNode;
  sample?: React.ReactNode;
}

/** FundingMixIndicator — PRO ONLY: realized paid share vs the ~25% target, per workload bucket cell. */
export function FundingMixIndicator(props: FundingMixIndicatorProps) {
  const { paidShare, target = 0.25, bucket, sample } = props;
  const dev = Math.abs(paidShare - target);
  const within = dev <= 0.05;
  return (
    <div className="vn-mix" title={"Pro funding mix — deficit cell per (tier, workload bucket, funding class)"}>
      <div style={{ display: "flex", gap: "var(--space-2)", alignItems: "center", fontSize: "var(--font-size-xs)" }}>
        <TierBadge tier="pro" />
        {bucket ? <span className="vn-reason-code">{bucket}</span> : null}
        <span className="vn-data">{Math.round(paidShare * 100)}% paid</span>
        <Badge tone={within ? "healthy" : "warning"} icon={within ? "circle-check" : "triangle-alert"}>{within ? "within ±5pp of target" : "deficit " + (paidShare < target ? "paid" : "free")}</Badge>
        {sample ? <span className="vn-caption">n={sample}</span> : null}
      </div>
      <div className="vn-mix-bar" role="img" aria-label={"Paid " + Math.round(paidShare * 100) + "%, free " + Math.round((1 - paidShare) * 100) + "%, target " + Math.round(target * 100) + "% paid"}>
        <span className="vn-mix-paid" style={{ width: paidShare * 100 + "%" }}></span>
        <span className="vn-mix-free" style={{ flex: 1 }}></span>
      </div>
      <div className="vn-mix-target"><span style={{ position: "absolute", left: target * 100 + "%" }} title={"target " + Math.round(target * 100) + "% paid"}><span style={{ position: "absolute", top: -14, width: 2, height: 18, background: "var(--text-primary)", display: "block" }}></span></span></div>
    </div>
  );
}

export interface QuotaFairnessAccount {
  name: string;
  /** Capacity weight, 0-1. */
  weight: number;
  /** Realized share, 0-1. */
  realized: number;
  saturated?: boolean;
}

export interface QuotaFairnessIndicatorProps {
  accounts?: QuotaFairnessAccount[];
}

/** QuotaFairnessIndicator — MAX ONLY: DRR capacity weight vs realized share; saturated accounts marked ineligible. No funding target exists. */
export function QuotaFairnessIndicator(props: QuotaFairnessIndicatorProps) {
  const { accounts = [] } = props;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}>
      <div style={{ display: "flex", gap: "var(--space-2)", alignItems: "center" }}>
        <TierBadge tier="max" />
        <span className="vn-caption">quota-fair DRR + P2C · no funding-mix target · funding observable, never an objective</span>
      </div>
      {accounts.map((a) => (
        <div key={a.name} className="vn-score-row" style={{ gridTemplateColumns: "160px 1fr 110px" }}>
          <span className="vn-caption vn-truncate" style={{ display: "flex", gap: 4, alignItems: "center" }}>
            {a.saturated ? <Icon name="ban" size={11} label="Saturated on a required window — ineligible this attempt" style={{ color: "var(--status-critical-fg)" }} /> : null}
            {a.name}
          </span>
          <div className="vn-score-bar" role="img" aria-label={a.name + " realized " + Math.round(a.realized * 100) + "% vs capacity " + Math.round(a.weight * 100) + "%"}>
            <div style={{ width: a.realized * 100 + "%", background: a.saturated ? "var(--status-unknown-border)" : "var(--viz-cat-4)" }}></div>
          </div>
          <span className="vn-score-val">{Math.round(a.realized * 100)}% / cap {Math.round(a.weight * 100)}%</span>
        </div>
      ))}
    </div>
  );
}
