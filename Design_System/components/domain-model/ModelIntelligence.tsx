import * as React from "react";
import { Icon, DOMAIN_ICON_MAP } from "../icons/Icon";
import { Badge, BadgeTone } from "../display/Badge";

/* Certification (docs/04 §5): EXACTLY six states — discovered, observed,
   probing, certified, suspended, expired. There is NO rejected state.
   Capability truth (unknown/supported/unsupported) is an orthogonal dimension. */

export type CertState = "discovered" | "observed" | "probing" | "certified" | "suspended" | "expired";

interface CertMeta {
  tone: BadgeTone;
  icon: string;
  label: string;
}

const CERT: Record<CertState, CertMeta> = {
  discovered: { tone: "inactive", icon: "box",           label: "Discovered" },
  observed:   { tone: "info",     icon: "eye",           label: "Observed" },
  probing:    { tone: "info",     icon: "flask-conical", label: "Probing" },
  certified:  { tone: "accent",   icon: "badge-check",   label: "Certified" },
  suspended:  { tone: "warning",  icon: "pause",         label: "Suspended" },
  expired:    { tone: "warning",  icon: "clock",         label: "Expired" },
};

export interface CertificationStateBadgeProps {
  state: CertState;
  reason?: React.ReactNode;
}

export function CertificationStateBadge(props: CertificationStateBadgeProps) {
  const { state, reason } = props;
  const m: CertMeta = CERT[state] || { tone: "unknown", icon: "circle-help", label: state };
  const title = "certification: " + state + (reason ? " · " + reason : "");
  return <Badge tone={m.tone} icon={m.icon} title={title}>{m.label}{reason ? " *" : ""}</Badge>;
}
/** CertificationState — brief alias (docs/07 inventory name). */
export function CertificationState(props: CertificationStateBadgeProps) { return <CertificationStateBadge {...props} />; }

export type CapabilityTruth = "unknown" | "supported" | "unsupported";

interface TruthMeta {
  tone: BadgeTone;
  icon: string;
  label: string;
}

const TRUTH: Record<CapabilityTruth, TruthMeta> = {
  unknown:     { tone: "unknown",  icon: "circle-help", label: "unknown" },
  supported:   { tone: "healthy",  icon: "check",       label: "supported" },
  unsupported: { tone: "inactive", icon: "x",           label: "unsupported" },
};

export interface CapabilityTruthBadgeProps {
  /** Falls back to "unknown" when omitted. */
  truth?: CapabilityTruth;
}

/** CapabilityTruthBadge — unknown = missing evidence (dashed); unsupported = confirmed absence (quiet, not an alarm). */
export function CapabilityTruthBadge(props: CapabilityTruthBadgeProps) {
  const m = TRUTH[props.truth ?? "unknown"] || TRUTH.unknown;
  return <Badge tone={m.tone} icon={m.icon} mono title={"capability truth: " + m.label}>{m.label}</Badge>;
}

export interface CapabilityIconProps {
  /** A domain capability concept (chat, tools, vision, …) or custom glyph name. */
  capability: string;
  truth?: CapabilityTruth;
  showLabel?: boolean;
}

/** CapabilityIcon — one capability chip: icon + short label + truth treatment. */
export function CapabilityIcon(props: CapabilityIconProps) {
  const { capability, truth = "unknown", showLabel = true } = props;
  const glyph = DOMAIN_ICON_MAP[capability] || "circle-help";
  const nice = capability.replace(/_/g, " ");
  const title = nice + ": " + truth;
  return (
    <span className="vn-cap" data-truth={truth} title={title}>
      <Icon name={glyph} size={12} label={showLabel ? undefined : title} />
      {showLabel ? nice : null}
    </span>
  );
}

const CAP_ORDER = ["chat", "streaming", "tools", "structured_output", "vision", "reasoning", "context_window"];

export type CapabilityTruths = Record<string, CapabilityTruth>;

export interface ModelCapabilitySetProps {
  /** Pass {chat:"supported", tools:"unknown", ...}. */
  truths?: CapabilityTruths;
  capabilities?: string[];
  showLabels?: boolean;
}

/** ModelCapabilitySet — the offering-operation truth set. Pass {chat:"supported", tools:"unknown", ...}. */
export function ModelCapabilitySet(props: ModelCapabilitySetProps) {
  const { truths = {}, capabilities, showLabels = true } = props;
  const caps = capabilities || CAP_ORDER.filter((c) => truths[c] !== undefined);
  return (
    <span className="vn-cap-set" role="list" aria-label="Capability truth">
      {caps.map((c) => <span role="listitem" key={c}><CapabilityIcon capability={c} truth={truths[c] || "unknown"} showLabel={showLabels} /></span>)}
    </span>
  );
}
export function CapabilitySet(props: ModelCapabilitySetProps) { return <ModelCapabilitySet {...props} />; }

export interface RoutableIndicatorProps {
  state: CertState;
  truths?: CapabilityTruths;
  required?: string[];
}

/** RoutableIndicator — the conjunction, made visible:
    routable = certification state certified AND every required truth supported. */
export function RoutableIndicator(props: RoutableIndicatorProps) {
  const { state, truths = {}, required = ["chat"] } = props;
  const certified = state === "certified";
  const unsupported = required.filter((c) => truths[c] === "unsupported");
  const unproven = required.filter((c) => !truths[c] || truths[c] === "unknown");
  const routable = certified && unsupported.length === 0 && unproven.length === 0;
  let why = "";
  if (!certified) why = "state is " + state;
  else if (unsupported.length) why = unsupported.join(", ") + " unsupported";
  else if (unproven.length) why = unproven.join(", ") + " not proven yet";
  return (
    <span className="vn-routable" data-routable={String(routable)}>
      <Icon name={routable ? "circle-check" : "ban"} size={13} />
      {routable ? "Routable" : certified ? "Not routable yet" : "Not routable"}
      <span className="vn-routable-eq">certified ∧ supported</span>
      {!routable && why ? <span className="vn-caption">({why})</span> : null}
    </span>
  );
}

const CERT_FLOW: CertState[] = ["discovered", "observed", "probing", "certified"];

export interface CertificationTimelineProps {
  state: CertState;
}

/** CertificationTimeline — lifecycle position; suspended/expired render as a blocked branch off their source state. */
export function CertificationTimeline(props: CertificationTimelineProps) {
  const { state } = props;
  const branch = state === "suspended" || state === "expired";
  const idx = branch ? (state === "expired" ? 3 : 2) : CERT_FLOW.indexOf(state);
  return (
    <span className="vn-cert-timeline" role="img" aria-label={"certification lifecycle: " + state}>
      {CERT_FLOW.map((s, i) => (
        <React.Fragment key={s}>
          {i > 0 ? <span className="vn-cert-arrow" aria-hidden="true">→</span> : null}
          <span className="vn-cert-stage" data-state={i < idx ? "done" : i === idx && !branch ? "current" : i === idx && branch ? "blocked" : "todo"}>
            <span className="vn-cert-dot" aria-hidden="true"></span>{s}
          </span>
        </React.Fragment>
      ))}
      {branch ? (
        <span className="vn-cert-stage" data-state="current" style={{ marginLeft: 4 }}>
          <Icon name={state === "expired" ? "clock" : "pause"} size={11} style={{ color: "var(--status-warning-fg)" }} />
          <span style={{ color: "var(--status-warning-fg)" }}>{state}</span>
        </span>
      ) : null}
    </span>
  );
}

/* Probe execution (docs/04 §2) — separate from capability truth. */
export type ProbeExecutionState = "pending" | "running" | "succeeded" | "inconclusive" | "retryable_failure" | "terminal_failure";

interface ProbeMeta {
  tone: BadgeTone;
  icon: string;
  label: string;
}

const PROBE: Record<ProbeExecutionState, ProbeMeta> = {
  pending:           { tone: "inactive", icon: "clock",          label: "Pending" },
  running:           { tone: "info",     icon: "loader-circle",  label: "Running" },
  succeeded:         { tone: "healthy",  icon: "circle-check",   label: "Succeeded" },
  inconclusive:      { tone: "unknown",  icon: "circle-help",    label: "Inconclusive" },
  retryable_failure: { tone: "warning",  icon: "refresh-cw",     label: "Retryable failure" },
  terminal_failure:  { tone: "critical", icon: "circle-x",       label: "Terminal failure" },
};

export interface ProbeStatusProps {
  state: ProbeExecutionState;
  note?: React.ReactNode;
}

/** ProbeStatus — probe EXECUTION state. Infra failures never flip capability truth. */
export function ProbeStatus(props: ProbeStatusProps) {
  const { state, note } = props;
  const m = PROBE[state] || PROBE.pending;
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-1)" }}>
      <Badge tone={m.tone} icon={m.icon} title={"probe execution: " + state}>{m.label}</Badge>
      {note ? <span className="vn-caption">{note}</span> : null}
    </span>
  );
}

export interface ProbeResultSummaryProps {
  operation: string;
  execution: ProbeExecutionState;
  truth?: CapabilityTruth;
  note?: React.ReactNode;
  at?: React.ReactNode;
}

/** ProbeResultSummary — one probe outcome: operation, execution state, truth effect, evidence note. */
export function ProbeResultSummary(props: ProbeResultSummaryProps) {
  const { operation, execution, truth, note, at } = props;
  return (
    <div className="vn-evidence-row">
      <CapabilityIcon capability={operation} truth={truth} />
      <span style={{ display: "flex", flexDirection: "column", gap: 2 }}>
        <span style={{ display: "flex", gap: "var(--space-2)", alignItems: "center" }}>
          <ProbeStatus state={execution} />
          <span aria-hidden="true">→</span>
          <CapabilityTruthBadge truth={truth} />
        </span>
        {note ? <span className="vn-caption">{note}</span> : null}
      </span>
      {at ? <span className="vn-caption vn-mono-xs">{at}</span> : <span></span>}
    </div>
  );
}

export type MetadataSource = "owner_override" | "probe" | "provider_metadata" | "provider_discovery" | "external_registry" | "heuristic" | "unknown";

interface MetaSourceMeta {
  icon: string;
  rank: number;
}

const META_SOURCE: Record<MetadataSource, MetaSourceMeta> = {
  owner_override:    { icon: "user-round",   rank: 1 },
  probe:             { icon: "flask-conical",rank: 2 },
  provider_metadata: { icon: "shield-check", rank: 3 },
  provider_discovery:{ icon: "server",       rank: 4 },
  external_registry: { icon: "external-link",rank: 5 },
  heuristic:         { icon: "zap",          rank: 6 },
  unknown:           { icon: "circle-help",  rank: 7 },
};

export interface MetadataSourceIndicatorProps {
  source: MetadataSource;
}

/** MetadataSourceIndicator — evidence provenance, precedence rank visible (owner override > probe > provider metadata > discovery > registry > heuristic > unknown). */
export function MetadataSourceIndicator(props: MetadataSourceIndicatorProps) {
  const m = META_SOURCE[props.source] || META_SOURCE.unknown;
  return <span className="vn-reason-code" title={"Evidence source · precedence rank " + m.rank}><Icon name={m.icon} size={11} />{props.source}</span>;
}

export interface MetadataConfidenceIndicatorProps {
  confidence?: number;
  exactMatch?: boolean;
  stale?: boolean;
  observedAt?: React.ReactNode;
}

/** MetadataConfidenceIndicator — confidence + exact-identity + freshness of one fact. */
export function MetadataConfidenceIndicator(props: MetadataConfidenceIndicatorProps) {
  const { confidence, exactMatch = true, stale = false, observedAt } = props;
  const n = Math.round(Math.max(0, Math.min(1, confidence ?? 0)) * 5);
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-2)" }} title={"confidence " + confidence + (exactMatch ? " · exact identity match" : " · family match only") + (stale ? " · stale" : "")}>
      <span className="vn-confidence" role="img" aria-label={"confidence " + confidence}>{[0,1,2,3,4].map((i) => <i key={i} className={i < n ? "on" : ""}></i>)}</span>
      {!exactMatch ? <Badge tone="warning" icon="triangle-alert">family match</Badge> : null}
      {stale ? <Badge tone="warning" icon="clock">stale</Badge> : null}
      {observedAt ? <span className="vn-caption vn-mono-xs">{observedAt}</span> : null}
    </span>
  );
}

export interface ContextWindowDisplayProps {
  tokens?: number | null;
  verified?: boolean;
  source?: React.ReactNode;
}

/** ContextWindowDisplay — verified context tokens. Unknown renders as the word, never 0; unverified is ineligible. */
export function ContextWindowDisplay(props: ContextWindowDisplayProps) {
  const { tokens, verified = false, source } = props;
  if (tokens == null) {
    return <Badge tone="unknown" icon="circle-help" title="Context unknown — ineligible for every tier (fail closed)">ctx unknown</Badge>;
  }
  const fmt = tokens >= 1000000 ? (tokens / 1000000) + "M" : tokens >= 1000 ? Math.round(tokens / 1000) + "K" : String(tokens);
  return (
    <span className="vn-badge vn-badge--mono" title={tokens.toLocaleString() + " tokens" + (source ? " · " + source : "") + (verified ? " · verified" : " · declared, unverified — not routable")} style={!verified ? { borderStyle: "dashed" } : undefined}>
      <Icon name="scan-text" size={11} />{fmt}{verified ? "" : " ?"}
    </span>
  );
}

export type ModelAvailability = "available" | "withdrawn" | "catalog_only";

export interface ModelIdentityProps {
  name: React.ReactNode;
  providerModelId?: React.ReactNode;
  availability?: ModelAvailability;
}

/** ModelIdentity — display name + provider-scoped external model id (mono) + availability. */
export function ModelIdentity(props: ModelIdentityProps) {
  const { name, providerModelId, availability = "available" } = props;
  const nameStyle: React.CSSProperties = {
    fontWeight: "var(--font-weight-medium)" as React.CSSProperties["fontWeight"],
    textDecoration: availability === "withdrawn" ? "line-through" : undefined,
  };
  return (
    <span style={{ display: "inline-flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
      <span className="vn-body-compact vn-truncate" style={nameStyle}>
        {name}
        {availability === "withdrawn" ? <Badge tone="inactive" icon="x" className="vn-badge--outline" title="Withdrawn by discovery snapshot"> withdrawn</Badge> : null}
        {availability === "catalog_only" ? <Badge tone="inactive" icon="ban" title="Catalog only — never enters a tier (media/image-only; future scope)"> not in any tier</Badge> : null}
      </span>
      <span className="vn-mono-xs vn-text-muted vn-truncate">{providerModelId}</span>
    </span>
  );
}

export interface ModelOfferingRowProps {
  identity?: React.ReactNode;
  context?: React.ReactNode;
  capabilities?: React.ReactNode;
  certification?: React.ReactNode;
  routable?: React.ReactNode;
  actions?: React.ReactNode;
}

/** ModelOfferingRow — one offering line: identity · context · capability truths · certification · routable. */
export function ModelOfferingRow(props: ModelOfferingRowProps) {
  const { identity, context, capabilities, certification, routable, actions } = props;
  return (
    <div style={{ display: "grid", gridTemplateColumns: "minmax(200px,1.4fr) max-content 1.6fr max-content max-content max-content", gap: "var(--space-3)", alignItems: "center", padding: "var(--space-2) var(--space-4)", borderBottom: "var(--border-hairline) solid var(--border-subtle)", fontSize: "var(--font-size-sm)" }}>
      <span style={{ minWidth: 0 }}>{identity}</span>
      <span>{context}</span>
      <span>{capabilities}</span>
      <span>{certification}</span>
      <span>{routable}</span>
      <span style={{ display: "flex", gap: "var(--space-1)", justifySelf: "end" }}>{actions}</span>
    </div>
  );
}
