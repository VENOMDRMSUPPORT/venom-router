import * as React from "react";
import { Icon } from "../icons/Icon";
import { Badge, BadgeTone } from "../display/Badge";
import { Mark, MarkProps } from "../display/Mark";

/* Canonical account-state maps — the single axes->rendering mapping
   (states/state-matrix.md). No screen re-derives these. */

interface StatusMeta {
  tone: BadgeTone;
  icon: string;
  label: string;
}

export type ConnectionState = "connecting" | "connected" | "stopped" | "disconnected";

const CONNECTION: Record<ConnectionState, StatusMeta> = {
  connecting:   { tone: "info",     icon: "loader-circle", label: "Connecting" },
  connected:    { tone: "healthy",  icon: "circle-check",  label: "Connected" },
  stopped:      { tone: "inactive", icon: "power",         label: "Stopped" },
  disconnected: { tone: "inactive", icon: "unplug",        label: "Disconnected" },
};

export type HealthState = "unknown" | "healthy" | "degraded" | "unavailable" | "expired";

const HEALTH: Record<HealthState, StatusMeta> = {
  unknown:     { tone: "unknown",  icon: "circle-help",    label: "Unknown" },
  healthy:     { tone: "healthy",  icon: "circle-check",   label: "Healthy" },
  degraded:    { tone: "degraded", icon: "triangle-alert", label: "Degraded" },
  unavailable: { tone: "critical", icon: "circle-x",       label: "Unavailable" },
  expired:     { tone: "warning",  icon: "key-round",      label: "Credential expired" },
};

export type DisplayStatus = HealthState | "connecting" | "stopped" | "disconnected" | "reauthenticating" | "cooling_down";

const DISPLAY: Record<DisplayStatus, StatusMeta> = {
  ...HEALTH,
  connecting:      CONNECTION.connecting,
  stopped:         CONNECTION.stopped,
  disconnected:    CONNECTION.disconnected,
  reauthenticating:{ tone: "info",    icon: "refresh-cw", label: "Reauthenticating" },
  cooling_down:    { tone: "warning", icon: "hourglass",  label: "Cooling down" },
};

export interface ConnectionStateBadgeProps {
  state: ConnectionState;
}

export function ConnectionStateBadge(props: ConnectionStateBadgeProps) {
  const s = CONNECTION[props.state] || DISPLAY.unknown;
  return <Badge tone={s.tone} icon={s.icon} title={"connection_state: " + props.state}>{s.label}</Badge>;
}

export interface HealthStateBadgeProps {
  state: HealthState;
}

export function HealthStateBadge(props: HealthStateBadgeProps) {
  const s = HEALTH[props.state] || HEALTH.unknown;
  return <Badge tone={s.tone} icon={s.icon} title={"health_state: " + props.state}>{s.label}</Badge>;
}

export interface AccountStatusProps {
  status: DisplayStatus;
  retryAfter?: React.ReactNode;
  reason?: string;
}

/** AccountStatus — renders the DERIVED display_status; retryAfter shown for cooling_down. */
export function AccountStatus(props: AccountStatusProps) {
  const { status, retryAfter, reason } = props;
  const s = DISPLAY[status] || DISPLAY.unknown;
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-1)" }}>
      <Badge tone={s.tone} icon={s.icon} title={reason || ("display_status: " + status)}>{s.label}</Badge>
      {status === "cooling_down" && retryAfter ? <span className="vn-caption vn-mono-xs">retry in {retryAfter}</span> : null}
    </span>
  );
}

export type FundingState = "free" | "paid" | "unknown";

const FUNDING: Record<FundingState, StatusMeta> = {
  free:    { tone: "healthy", icon: "hand-coins",  label: "Free" },
  paid:    { tone: "info",    icon: "credit-card", label: "Paid" },
  unknown: { tone: "unknown", icon: "circle-help", label: "Unknown" },
};

/** The canonical 4-value funding evidence source (docs/02 §2). */
export type FundingSource = "provider_policy" | "provider_evidence" | "owner_policy" | "owner_override";

export interface FundingBadgeProps {
  funding?: FundingState;
  plan?: React.ReactNode;
  locked?: boolean;
  source?: FundingSource;
  stale?: boolean;
  conflicting?: boolean;
}

/** FundingBadge — account-scoped funding classification (never provider-level).
    Renders plan string when known; locked / override / stale / conflicting modifiers. */
export function FundingBadge(props: FundingBadgeProps) {
  const { funding = "unknown", plan, locked = false, source, stale = false, conflicting = false } = props;
  const f = FUNDING[funding] || FUNDING.unknown;
  const override = source === "owner_override";
  const title = "funding: " + funding + (source ? " · source: " + source : "") + (locked ? " · locked (override rejected)" : "");
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-1)" }}>
      <Badge tone={conflicting ? "warning" : f.tone} icon={conflicting ? "triangle-alert" : f.icon} outline={override} title={title}>
        {plan || f.label}{conflicting ? " · conflicting" : ""}
      </Badge>
      {locked ? <Icon name="lock" size={12} label="Locked by provider policy" style={{ color: "var(--text-muted)" }} /> : null}
      {override ? <Icon name="user-round" size={12} label="Owner override" style={{ color: "var(--accent-text)" }} /> : null}
      {stale ? <Badge tone="warning" icon="clock" title="Evidence beyond freshness window">stale</Badge> : null}
    </span>
  );
}
/** FundingClassBadge — alias of FundingBadge (inventory name). */
export function FundingClassBadge(props: FundingBadgeProps) { return <FundingBadge {...props} />; }

interface SourceMeta {
  icon: string;
  label: string;
}

const SOURCE_META: Record<FundingSource, SourceMeta> = {
  provider_policy:   { icon: "server",      label: "provider_policy" },
  provider_evidence: { icon: "shield-check",label: "provider_evidence" },
  owner_policy:      { icon: "user-round",  label: "owner_policy" },
  owner_override:    { icon: "user-round",  label: "owner_override" },
};

export interface FundingSourceIndicatorProps {
  source?: FundingSource;
}

/** FundingSourceIndicator — the canonical 4-value evidence source, verbatim mono chip. */
export function FundingSourceIndicator(props: FundingSourceIndicatorProps) {
  const m: SourceMeta = (props.source && SOURCE_META[props.source]) || { icon: "circle-help", label: props.source || "unknown" };
  return <span className="vn-reason-code" title="Funding evidence source"><Icon name={m.icon} size={11} />{m.label}</span>;
}

export interface FundingEvidenceIndicatorProps {
  funding?: FundingState;
  source?: FundingSource;
  confidence?: number;
  observedAt?: React.ReactNode;
  stale?: boolean;
  locked?: boolean;
}

/** FundingEvidenceIndicator — current evidence row: value + source + confidence + freshness. */
export function FundingEvidenceIndicator(props: FundingEvidenceIndicatorProps) {
  const { funding, source, confidence, observedAt, stale = false, locked = false } = props;
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-2)", flexWrap: "wrap" }}>
      <FundingBadge funding={funding} source={source} stale={stale} locked={locked} />
      <FundingSourceIndicator source={source} />
      {confidence != null ? <ConfidenceDots value={confidence} label={"confidence " + confidence} /> : null}
      {observedAt ? <span className="vn-caption vn-mono-xs">{observedAt}</span> : null}
    </span>
  );
}

export interface ConfidenceDotsProps {
  /** 0-1. */
  value?: number;
  label?: string;
}

export function ConfidenceDots(props: ConfidenceDotsProps) {
  const { value = 0, label } = props;
  const n = Math.round(Math.max(0, Math.min(1, value)) * 5);
  return (
    <span className="vn-confidence" role="img" aria-label={label || ("confidence " + value)} title={label || ("confidence " + value)}>
      {[0, 1, 2, 3, 4].map((i) => <i key={i} className={i < n ? "on" : ""}></i>)}
    </span>
  );
}

export type VerificationLevel = "proven" | "partial" | "unknown";

export interface ProviderVerificationConfidenceProps {
  level: VerificationLevel;
}

/** ProviderVerificationConfidence — planning confidence of the wire contract (docs/03): proven | partial | unknown. */
export function ProviderVerificationConfidence(props: ProviderVerificationConfidenceProps) {
  const map: Record<VerificationLevel, StatusMeta> = {
    proven:  { tone: "healthy", icon: "shield-check", label: "proven" },
    partial: { tone: "warning", icon: "shield",       label: "partial" },
    unknown: { tone: "unknown", icon: "circle-help",  label: "unverified" },
  };
  const m = map[props.level] || map.unknown;
  return <Badge tone={m.tone} icon={m.icon} mono title={"Wire-contract confidence: " + m.label + " — re-verify live before implementing"}>{m.label}</Badge>;
}

export type CredentialKind = "api_key" | "oauth2" | "github_oauth" | "copilot_service";

interface CredentialKindMeta {
  icon: string;
  label: string;
}

const CRED_KIND: Record<CredentialKind, CredentialKindMeta> = {
  api_key:         { icon: "key-round",   label: "api_key" },
  oauth2:          { icon: "fingerprint", label: "oauth2" },
  github_oauth:    { icon: "fingerprint", label: "github_oauth" },
  copilot_service: { icon: "key-round",   label: "copilot_service" },
};

export interface CredentialKindBadgeProps {
  kind: CredentialKind;
  state?: "active" | "staged" | "retired";
  expiresAt?: React.ReactNode;
}

/** CredentialKindBadge — one active credential per (account, kind); kinds coexist. */
export function CredentialKindBadge(props: CredentialKindBadgeProps) {
  const m = CRED_KIND[props.kind] || { icon: "key-round", label: props.kind };
  const state = props.state || "active";
  const tone: BadgeTone = state === "active" ? "inactive" : state === "staged" ? "info" : "inactive";
  return (
    <Badge tone={tone} icon={m.icon} mono title={"credential kind: " + m.label + " · state: " + state}>
      {m.label}{state !== "active" ? " · " + state : ""}{props.expiresAt ? " · exp " : null}{props.expiresAt}
    </Badge>
  );
}

export type ReauthState = "idle" | "staged" | "validating" | "swapping" | "successful" | "failed" | "rollback" | "interrupted";

const REAUTH: Record<ReauthState, StatusMeta> = {
  idle:        { tone: "inactive", icon: "fingerprint",    label: "No reauth in progress" },
  staged:      { tone: "info",     icon: "fingerprint",    label: "Credential staged" },
  validating:  { tone: "info",     icon: "loader-circle",  label: "Validating staged credential" },
  swapping:    { tone: "info",     icon: "refresh-cw",     label: "Swapping (atomic)" },
  successful:  { tone: "healthy",  icon: "circle-check",   label: "Reauthenticated" },
  failed:      { tone: "critical", icon: "circle-x",       label: "Reauth failed — active credential intact" },
  rollback:    { tone: "warning",  icon: "rotate-ccw",     label: "Rolled back — old credential preserved" },
  interrupted: { tone: "warning",  icon: "triangle-alert", label: "Interrupted — stale staged row discarded on startup" },
};

export interface ReauthenticationStatusProps {
  state?: ReauthState;
}

export function ReauthenticationStatus(props: ReauthenticationStatusProps) {
  const m = (props.state && REAUTH[props.state]) || REAUTH.idle;
  return <Badge tone={m.tone} icon={m.icon} title={"reauthentication: " + props.state}>{m.label}</Badge>;
}

export interface AccountCooldownIndicatorProps {
  scope?: React.ReactNode;
  until?: React.ReactNode;
  retryAfter?: React.ReactNode;
}

/** AccountCooldownIndicator — scoped cooldown with retry-after. Never renders as a permanent failure. */
export function AccountCooldownIndicator(props: AccountCooldownIndicatorProps) {
  const { scope = "account", until, retryAfter } = props;
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-1)" }}>
      <Badge tone="warning" icon="hourglass" title={"Cooldown scope: " + scope + (until ? " · until " + until : "")}>Cooling down · {scope}</Badge>
      {retryAfter ? <span className="vn-caption vn-mono-xs">retry in {retryAfter}</span> : null}
    </span>
  );
}

export interface AccountIdentityProps {
  name?: React.ReactNode;
  email?: React.ReactNode;
  externalId?: React.ReactNode;
  plan?: React.ReactNode;
}

const identityNameStyle: React.CSSProperties = { fontWeight: "var(--font-weight-medium)" as React.CSSProperties["fontWeight"] };

/** AccountIdentity — display name/email + immutable external id (mono) + plan. */
export function AccountIdentity(props: AccountIdentityProps) {
  const { name, email, externalId, plan } = props;
  return (
    <span style={{ display: "inline-flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
      <span className="vn-body-compact vn-truncate" style={identityNameStyle}>{name || email}</span>
      <span className="vn-caption vn-truncate">
        {email && name ? <>{email} · </> : null}
        {externalId ? <span className="vn-mono-xs">{externalId}</span> : null}
        {plan ? <> · {plan}</> : null}
      </span>
    </span>
  );
}

export type AuthMode = "oauth2" | "api_key" | "custom_openai";

interface AuthModeMeta {
  icon: string;
  label: string;
}

const AUTH_MODE: Record<AuthMode, AuthModeMeta> = {
  oauth2:        { icon: "fingerprint", label: "OAuth" },
  api_key:       { icon: "key-round",   label: "API key" },
  custom_openai: { icon: "plug",        label: "OpenAI-compatible" },
};

export interface ProviderBadgeProps {
  authMode?: AuthMode;
}

/** ProviderBadge — the integration's auth mode (a provider is never "free" or "paid"). */
export function ProviderBadge(props: ProviderBadgeProps) {
  const m = (props.authMode && AUTH_MODE[props.authMode]) || AUTH_MODE.api_key;
  return <Badge tone="inactive" icon={m.icon} title={"auth_mode: " + props.authMode}>{m.label}</Badge>;
}

export function ProviderMark(props: MarkProps) { return <Mark {...props} />; }

export interface ProviderSummaryCardProps {
  name: React.ReactNode;
  slug?: string;
  authMode?: AuthMode;
  accountCount?: number;
  healthyCount?: number;
  verification?: VerificationLevel;
  setupRequired?: boolean;
  /** Missing environment variable NAMES only — values are never shown. */
  missingEnv?: string[];
  children?: React.ReactNode;
  actions?: React.ReactNode;
}

/** ProviderSummaryCard — the fleet's provider row header: integration facts +
    aggregate account health. setupRequired lists missing env var NAMES only. */
export function ProviderSummaryCard(props: ProviderSummaryCardProps) {
  const { name, slug, authMode, accountCount = 0, healthyCount = 0, verification, setupRequired = false, missingEnv = [], children, actions } = props;
  return (
    <div className="vn-panel">
      <div className="vn-fleet-provider">
        <ProviderMark name={slug || String(name)} size="lg" />
        <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0, flex: 1 }}>
          <span style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
            <span className="vn-fleet-name">{name}</span>
            <span className="vn-fleet-slug">{slug}</span>
            <ProviderBadge authMode={authMode} />
            {verification ? <ProviderVerificationConfidence level={verification} /> : null}
          </span>
          <span className="vn-caption">
            {accountCount === 0 ? "No accounts connected" : healthyCount + " healthy of " + accountCount + " account" + (accountCount === 1 ? "" : "s")}
          </span>
        </span>
        {actions}
      </div>
      {setupRequired ? (
        <div className="vn-banner" role="status" style={{ borderLeft: 0, borderRight: 0 }}>
          <Icon name="triangle-alert" size={15} />
          <span style={{ flex: 1 }}>Setup required — missing environment {missingEnv.length === 1 ? "variable" : "variables"}: {missingEnv.map((v, i) => <span key={v} className="vn-code-inline">{v}</span>).reduce<React.ReactNode[]>((acc, el, i) => (i ? [...acc, " ", el] : [el]), [])} (names only, values are never shown).</span>
        </div>
      ) : null}
      {children ? <div className="vn-fleet-accounts">{children}</div> : null}
    </div>
  );
}

export interface ProviderAccountRowProps {
  identity?: React.ReactNode;
  status?: React.ReactNode;
  retryAfter?: React.ReactNode;
  funding?: React.ReactNode;
  quota?: React.ReactNode;
  actions?: React.ReactNode;
}

/** ProviderAccountRow — one connected account under a provider: identity ·
    status · funding · quota summary · actions. Funding lives HERE, never on the provider. */
export function ProviderAccountRow(props: ProviderAccountRowProps) {
  const { identity, status, retryAfter, funding, quota, actions } = props;
  return (
    <div className="vn-fleet-account">
      <Icon name="user-round" size={16} style={{ color: "var(--text-muted)" }} />
      <span style={{ minWidth: 0 }}>{identity}</span>
      <span>{status}{retryAfter ? <span className="vn-caption vn-mono-xs" style={{ marginLeft: 6 }}>retry {retryAfter}</span> : null}</span>
      <span>{funding}</span>
      <span>{quota}</span>
      <span style={{ display: "flex", gap: "var(--space-1)", justifySelf: "end" }}>{actions}</span>
    </div>
  );
}
