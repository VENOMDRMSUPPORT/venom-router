import * as React from "react";
import { Icon } from "../icons/Icon";
import { Badge, BadgeTone } from "../display/Badge";
import { Button } from "../actions/Button";
import { CopyButton } from "../actions/CopyButton";
import { Dialog } from "../overlay/Dialog";
import { Input } from "../forms/Input";
import { FormField } from "../forms/FormField";

/* Security & owner-control set (docs/09 §5). One owner, no roles. Secrets are
   masked by default; reveal requires a fresh (<=5 min) re-verification; nothing
   here ever renders an unredacted stored secret by default. */

interface StatusMeta {
  tone: BadgeTone;
  icon: string;
  label: string;
}

export type SessionState =
  | "active"
  | "idle_warning"
  | "expired"
  | "absolute_expiry"
  | "revoked"
  | "reverification_required"
  | "reverified"
  | "unauthenticated"
  | "locked_out";

const SESSION: Record<SessionState, StatusMeta> = {
  active:                  { tone: "healthy",  icon: "shield-check", label: "Session active" },
  idle_warning:            { tone: "warning",  icon: "clock",        label: "Idle expiry soon" },
  expired:                 { tone: "warning",  icon: "lock",         label: "Session expired" },
  absolute_expiry:         { tone: "warning",  icon: "lock",         label: "Session ended (12h cap)" },
  revoked:                 { tone: "inactive", icon: "log-out",      label: "Session revoked" },
  reverification_required: { tone: "warning",  icon: "shield",       label: "Re-verification required" },
  reverified:              { tone: "healthy",  icon: "shield-check", label: "Re-verified" },
  unauthenticated:         { tone: "inactive", icon: "lock",         label: "Not signed in" },
  locked_out:              { tone: "critical", icon: "ban",          label: "Locked out" },
};

export interface OwnerSessionStatusProps {
  state?: SessionState;
  idleIn?: string;
  absoluteIn?: string;
  reverifiedFor?: React.ReactNode;
  retryAfter?: React.ReactNode;
}

/** OwnerSessionStatus — the topbar session pill: state + countdowns. */
export function OwnerSessionStatus(props: OwnerSessionStatusProps) {
  const { state = "active", idleIn, absoluteIn, reverifiedFor, retryAfter } = props;
  const m = SESSION[state] || SESSION.active;
  const dataState = state === "idle_warning" ? "warning" : state === "expired" || state === "absolute_expiry" || state === "locked_out" ? "expired" : state === "reverified" ? "reverified" : state === "revoked" ? "revoked" : "active";
  return (
    <span className="vn-session-pill" data-state={dataState} title={"Owner session: " + m.label + (idleIn ? " · idle expiry in " + idleIn : "") + (absoluteIn ? " · absolute expiry in " + absoluteIn : "")}>
      <Icon name={m.icon} size={12} />
      {m.label}
      {state === "idle_warning" && idleIn ? <span>· {idleIn}</span> : null}
      {state === "reverified" && reverifiedFor ? <span>· fresh {reverifiedFor}</span> : null}
      {state === "locked_out" && retryAfter ? <span>· retry {retryAfter}</span> : null}
    </span>
  );
}

export interface SessionExpiryWarningProps {
  kind?: "idle" | "absolute";
  inTime?: string;
  onContinue?: () => void;
}

/** SessionExpiryWarning — pre-expiry banner with continue action. */
export function SessionExpiryWarning(props: SessionExpiryWarningProps) {
  const { kind = "idle", inTime = "2m", onContinue } = props;
  return (
    <div className="vn-banner" role="alert">
      <Icon name="clock" size={15} />
      <span style={{ flex: 1 }}>
        {kind === "idle" ? "Your session expires in " + inTime + " due to inactivity." : "Your session reaches its 12-hour absolute limit in " + inTime + ". You will need to sign in again."}
      </span>
      {kind === "idle" && onContinue ? <span className="vn-banner-actions"><Button size="sm" onClick={onContinue}>Stay signed in</Button></span> : null}
    </div>
  );
}

export interface ReverificationPromptProps {
  open: boolean;
  action?: string;
  error?: React.ReactNode;
  locked?: boolean;
  retryAfter?: React.ReactNode;
  onConfirm?: (password: string) => void;
  onCancel?: () => void;
}

/** ReverificationPrompt — modal password proof gating a sensitive action (5-minute freshness). */
export function ReverificationPrompt(props: ReverificationPromptProps) {
  const { open, action = "reveal this credential", error, locked = false, retryAfter, onConfirm, onCancel } = props;
  const [pw, setPw] = React.useState("");
  return (
    <Dialog open={open} onClose={onCancel} title="Confirm your password"
      footer={<>
        <Button onClick={onCancel}>Cancel</Button>
        <Button variant="primary" icon="shield-check" disabled={locked || !pw} onClick={() => { onConfirm && onConfirm(pw); setPw(""); }}>Verify</Button>
      </>}>
      <p style={{ marginTop: 0 }}>To {action}, re-enter the owner password. Verification stays fresh for 5 minutes.</p>
      <FormField label="Owner password" error={error}>
        <Input type="password" autoComplete="current-password" value={pw} disabled={locked} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setPw(e.target.value)} />
      </FormField>
      {locked ? <div className="vn-alert vn-alert--critical" role="alert" style={{ marginTop: "var(--space-3)" }}><Icon name="ban" size={15} /><div>Too many failed attempts. Try again in {retryAfter || "a few minutes"}.<div className="vn-alert-code">locked_out</div></div></div> : null}
    </Dialog>
  );
}

export interface SecretRevealControlProps {
  masked: React.ReactNode;
  secret: string;
  revealed?: boolean;
  blocked?: boolean;
  onRevealRequest?: () => void;
  onHide?: () => void;
  label?: string;
}

/** SecretRevealControl — masked by default; reveal gated on fresh re-verification; cleared on hide/blur; never persisted in the DOM after hide. */
export function SecretRevealControl(props: SecretRevealControlProps) {
  const { masked, secret, revealed = false, blocked = false, onRevealRequest, onHide, label = "credential" } = props;
  React.useEffect(() => {
    if (!revealed) return;
    const hide = () => onHide && onHide();
    window.addEventListener("blur", hide);
    return () => window.removeEventListener("blur", hide);
  }, [revealed]);
  const state = blocked ? "blocked" : revealed ? "revealed" : "hidden";
  return (
    <span className="vn-secret" data-state={state}>
      <Icon name="key-round" size={13} />
      <span className="vn-secret-value">{revealed && secret ? secret : masked}</span>
      {revealed ? <CopyButton value={secret} label={"Copy " + label} /> : null}
      <button type="button" className="vn-btn vn-btn--icon vn-btn--ghost vn-btn--sm"
        aria-label={revealed ? "Hide " + label : blocked ? "Reveal " + label + " (re-verification required)" : "Reveal " + label}
        title={blocked ? "Requires fresh owner re-verification (≤ 5 min)" : undefined}
        onClick={() => (revealed ? onHide && onHide() : onRevealRequest && onRevealRequest())}>
        <Icon name={revealed ? "eye-off" : "eye"} size={13} />
      </button>
      {blocked ? <Icon name="shield" size={12} label="Re-verification required" style={{ color: "var(--status-warning-fg)" }} /> : null}
      {revealed ? <span className="vn-caption" style={{ color: "var(--status-warning-fg)" }}>clears on blur</span> : null}
    </span>
  );
}

export interface APIKeyPrefixProps {
  prefix: React.ReactNode;
  label?: string;
}

/** APIKeyPrefix — the only persistent representation of a Venom key: prefix + fingerprint, mono. */
export function APIKeyPrefix(props: APIKeyPrefixProps) {
  const { prefix, label } = props;
  return <span className="vn-code-inline" title={label || "Key prefix — the full key is hash-only and never retrievable"}>{prefix}…</span>;
}

export interface APIKeyCreationResultProps {
  rawKey: string;
  keyLabel?: string;
  onDone?: () => void;
}

/** APIKeyCreationResult — the ONE-TIME raw key reveal after POST /keys. */
export function APIKeyCreationResult(props: APIKeyCreationResultProps) {
  const { rawKey, keyLabel, onDone } = props;
  return (
    <div className="vn-card vn-card--pad" style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)", borderColor: "var(--status-warning-border)" }}>
      <div style={{ display: "flex", gap: "var(--space-2)", alignItems: "center" }}>
        <Icon name="key-round" size={16} style={{ color: "var(--status-warning-fg)" }} />
        <span className="vn-title-sub">{keyLabel ? keyLabel + " created" : "API key created"}</span>
      </div>
      <div className="vn-alert vn-alert--warning" role="alert">
        <Icon name="triangle-alert" size={15} />
        <div>This key is shown <strong>once</strong>. Only a hash is stored — it cannot be retrieved again. Copy it now.</div>
      </div>
      <span className="vn-secret" data-state="revealed" style={{ justifyContent: "space-between" }}>
        <span className="vn-secret-value">{rawKey}</span>
        <CopyButton value={rawKey} label="Copy API key" size="md" />
      </span>
      {onDone ? <div style={{ display: "flex", justifyContent: "flex-end" }}><Button variant="primary" onClick={onDone}>I stored the key</Button></div> : null}
    </div>
  );
}

export type BackupState = "idle" | "running" | "completed" | "failed";

const BACKUP: Record<BackupState, StatusMeta> = {
  idle:      { tone: "inactive", icon: "archive",       label: "No backup running" },
  running:   { tone: "info",     icon: "loader-circle", label: "Creating backup" },
  completed: { tone: "healthy",  icon: "circle-check",  label: "Backup created" },
  failed:    { tone: "critical", icon: "circle-x",      label: "Backup failed" },
};

export interface BackupStatusProps {
  state?: BackupState;
  artifact?: React.ReactNode;
  at?: React.ReactNode;
  code?: React.ReactNode;
}

export function BackupStatus(props: BackupStatusProps) {
  const { state = "idle", artifact, at, code } = props;
  const m = BACKUP[state];
  return (
    <span style={{ display: "inline-flex", gap: "var(--space-2)", alignItems: "center", flexWrap: "wrap" }}>
      <Badge tone={m.tone} icon={m.icon}>{m.label}</Badge>
      {artifact ? <span className="vn-code-inline">{artifact}</span> : null}
      {at ? <span className="vn-caption vn-mono-xs">{at}</span> : null}
      {code ? <span className="vn-reason-code vn-reason-code--blocking">{code}</span> : null}
    </span>
  );
}

export type RestoreState = "idle" | "validating" | "decrypting" | "verifying" | "swapped" | "failed";

const RESTORE: Record<RestoreState, StatusMeta> = {
  idle:       { tone: "inactive", icon: "archive-restore", label: "No restore running" },
  validating: { tone: "info",     icon: "loader-circle",   label: "Validating container" },
  decrypting: { tone: "info",     icon: "loader-circle",   label: "Decrypting" },
  verifying:  { tone: "info",     icon: "database",        label: "Verifying integrity" },
  swapped:    { tone: "healthy",  icon: "circle-check",    label: "Restore complete" },
  failed:     { tone: "critical", icon: "circle-x",        label: "Restore failed — live state untouched" },
};

export interface RestoreStatusProps {
  state?: RestoreState;
  code?: React.ReactNode;
}

export function RestoreStatus(props: RestoreStatusProps) {
  const { state = "idle", code } = props;
  const m = RESTORE[state];
  return (
    <span style={{ display: "inline-flex", gap: "var(--space-2)", alignItems: "center" }}>
      <Badge tone={m.tone} icon={m.icon}>{m.label}</Badge>
      {code ? <span className="vn-reason-code vn-reason-code--blocking" title="Typed, user-safe error code">{code}</span> : null}
    </span>
  );
}

export interface DestructiveActionConfirmationProps {
  open: boolean;
  title?: React.ReactNode;
  consequence?: React.ReactNode;
  /** When set, requires typing this exact word before the confirm button arms. */
  confirmWord?: string;
  confirmLabel?: React.ReactNode;
  onConfirm?: () => void;
  onCancel?: () => void;
}

/** DestructiveActionConfirmation — blocking dialog for irreversible operations; optional type-to-confirm. */
export function DestructiveActionConfirmation(props: DestructiveActionConfirmationProps) {
  const { open, title, consequence, confirmWord, confirmLabel = "Confirm", onConfirm, onCancel } = props;
  const [typed, setTyped] = React.useState("");
  const armed = !confirmWord || typed === confirmWord;
  return (
    <Dialog open={open} onClose={onCancel} title={title}
      footer={<>
        <Button onClick={onCancel}>Cancel</Button>
        <Button variant="danger" disabled={!armed} onClick={() => { onConfirm && onConfirm(); setTyped(""); }}>{confirmLabel}</Button>
      </>}>
      <div className="vn-alert vn-alert--critical" role="alert"><Icon name="triangle-alert" size={15} /><div>{consequence}</div></div>
      {confirmWord ? (
        <div style={{ marginTop: "var(--space-3)" }}>
          <FormField label={<span>Type <span className="vn-code-inline">{confirmWord}</span> to confirm</span>}>
            <Input mono value={typed} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setTyped(e.target.value)} />
          </FormField>
        </div>
      ) : null}
    </Dialog>
  );
}
