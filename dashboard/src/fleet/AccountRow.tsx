import { useState, type ChangeEvent, type KeyboardEvent } from "react";
import { Badge, Button, Dialog, FormField, IconButton, Input, Select } from "@venom/design-system/primitives";
import {
  DestructiveActionConfirmation,
  FundingBadge,
  SecretRevealControl,
  TypedErrorDisplay,
} from "@venom/design-system/domain";
import ReverifyModal from "../auth/ReverifyModal";
import CertificationSummary from "./CertificationSummary";
import { QuotaSummaryCompact } from "./QuotaSummary";
import { pollJobToTerminal } from "./jobs";
import { relativeTime } from "./relativeTime";
import {
  AuthApiError,
  disconnectAccount,
  isSessionExpired,
  patchAccountLabel,
  refreshHealth,
  refreshQuota,
  resumeAccount,
  revealCredential,
  startDiscovery,
  stopAccount,
  toApiError,
  updateFunding,
  type AccountProjection,
  type DisplayStatus,
} from "../api/controlClient";

export interface AccountRowProps {
  account: AccountProjection;
  /** 1-based position within its provider — the "#1 / #2" index chip. */
  index: number;
  /** Distinct models discovered for this account; null while the offerings
   * read is loading or failed (rendered as an honest "—", never 0). */
  modelCount: number | null;
  csrfToken: string;
  onSessionExpired: () => void;
  /** Called after any successful mutation — the parent re-fetches the
   * fleet (providers + accounts + offerings) so this row's own props are
   * refreshed from the server. */
  onChanged: () => void;
  /** Opens this account's Model Test Report modal (owned by the page so
   * the dialog outlives row re-renders). */
  onOpenModelReport: () => void;
}

const FUNDING_OPTIONS = [
  { value: "free", label: "Free" },
  { value: "paid", label: "Paid" },
  { value: "unknown", label: "Unknown" },
];

/** display_status -> dot tone. PRESENTATION mapping only (mirrors the DS
 * state matrix's tones); the verbatim server value always travels in the
 * dot's title, never renamed. */
const DOT_TONE: Record<DisplayStatus, string> = {
  connecting: "info",
  healthy: "healthy",
  degraded: "degraded",
  unavailable: "critical",
  expired: "warning",
  unknown: "unknown",
  reauthenticating: "info",
  cooling_down: "warning",
  stopped: "inactive",
  disconnected: "inactive",
};

/** The most recent observed_at across the account's evidence windows —
 * the "Quota: … ago" instant. Null when no evidence window reports one. */
function latestQuotaObservedAt(account: AccountProjection): number | null {
  let latest: number | null = null;
  for (const w of account.quota) {
    if (w.source === "local_safety") continue;
    const t = Date.parse(w.observed_at);
    if (!Number.isNaN(t) && (latest == null || t > latest)) latest = t;
  }
  return latest;
}

/**
 * One connected account's row (image 2 layout): numbered index chip,
 * identity (email, uppercase plan badge, immutable external id), the
 * credential reveal + reverify flow, the funding badge + owner override,
 * compact quota meters, and the action cluster — sync (health · plan ·
 * usage), fetch models, the model-count chip opening the Model Test
 * Report, the disable/enable power toggle, and disconnect.
 *
 * Credential reveal (security-critical): the masked control shows no
 * secret by default. onRevealRequest calls POST /accounts/{id}/reveal;
 * a reverification_required (401) opens the same ReverifyModal
 * auth/AuthGate's own sensitive actions use — on a successful reverify,
 * reveal is retried once. Both onHide AND losing focus clear the plaintext
 * from this component's state immediately; it is never logged, never
 * stored, and the DOM shows only the masked placeholder once hidden.
 */
export default function AccountRow(props: AccountRowProps) {
  const { account, index, modelCount, csrfToken, onSessionExpired, onChanged, onOpenModelReport } = props;

  const [revealed, setRevealed] = useState(false);
  const [secret, setSecret] = useState("");
  const [revealPending, setRevealPending] = useState(false);
  const [revealError, setRevealError] = useState<AuthApiError | null>(null);
  const [reverifyOpen, setReverifyOpen] = useState(false);

  const [actionPending, setActionPending] = useState(false);
  const [actionError, setActionError] = useState<AuthApiError | null>(null);
  const [actionNote, setActionNote] = useState<string | null>(null);
  const [disconnectOpen, setDisconnectOpen] = useState(false);

  // One "Edit account" dialog drives both settings: the display label and
  // the funding override. Its two writes are independent — each fires only
  // when its own field actually changed.
  const [editOpen, setEditOpen] = useState(false);
  const [labelInput, setLabelInput] = useState("");
  const [fundingChoice, setFundingChoice] = useState<string>(account.funding?.funding ?? "unknown");
  const [editSubmitting, setEditSubmitting] = useState(false);
  const [editError, setEditError] = useState<AuthApiError | null>(null);

  async function attemptReveal() {
    setRevealPending(true);
    setRevealError(null);
    try {
      const plaintext = await revealCredential(account.id, csrfToken);
      setSecret(plaintext);
      setRevealed(true);
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      if (err instanceof AuthApiError && err.code === "reverification_required") {
        setReverifyOpen(true);
        return;
      }
      setRevealError(toApiError(err));
    } finally {
      setRevealPending(false);
    }
  }

  function handleRevealRequest() {
    if (revealPending) return;
    void attemptReveal();
  }

  function handleHide() {
    // The plaintext must never outlive this call, in state or otherwise —
    // cleared on the explicit Hide click AND on window blur (both routes
    // call this same handler; see SecretRevealControl's own effect).
    setSecret("");
    setRevealed(false);
  }

  function handleReverifySuccess() {
    setReverifyOpen(false);
    void attemptReveal();
  }

  async function runLifecycleAction(action: () => Promise<unknown>) {
    setActionPending(true);
    setActionError(null);
    setActionNote(null);
    try {
      await action();
      onChanged();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setActionError(toApiError(err));
    } finally {
      setActionPending(false);
    }
  }

  /** "Sync: health · plan · usage" — refreshHealth (the live probe), then
   * the async quota refresh polled to its terminal job status, then a
   * refetch. A provider with NO quota capability answers the quota trigger
   * with a typed 409 `quota_unsupported` — that is a benign fact about the
   * provider, not a failure of this sync: it gets a muted caption, never
   * an error banner. Every OTHER quota failure still surfaces verbatim. */
  function handleSync() {
    void runLifecycleAction(async () => {
      await refreshHealth(account.id, csrfToken);
      try {
        const handle = await refreshQuota(account.id, csrfToken);
        const job = await pollJobToTerminal(handle.job_id);
        if (job.status !== "completed") {
          throw new AuthApiError(0, {
            code: job.error?.code ?? `job_${job.status}`,
            message: job.error?.message ?? `The quota refresh job is ${job.status}.`,
            request_id: "",
            retryable: true,
          });
        }
      } catch (err) {
        if (err instanceof AuthApiError && err.code === "quota_unsupported") {
          setActionNote("Quota sync skipped — this provider has no quota capability. Health was refreshed.");
          return;
        }
        throw err;
      }
    });
  }

  /** "Fetch models from provider" — discovery for THIS account, polled to
   * terminal, then a refetch (the parent reloads offerings too). */
  function handleFetchModels() {
    void runLifecycleAction(async () => {
      const handle = await startDiscovery(account.id, csrfToken);
      const job = await pollJobToTerminal(handle.job_id);
      if (job.status !== "completed") {
        throw new AuthApiError(0, {
          code: job.error?.code ?? `job_${job.status}`,
          message: job.error?.message ?? `The discovery job is ${job.status}.`,
          request_id: "",
          retryable: true,
        });
      }
    });
  }

  function handleStop() {
    void runLifecycleAction(() => stopAccount(account.id, csrfToken));
  }

  function handleResume() {
    void runLifecycleAction(() => resumeAccount(account.id, csrfToken));
  }

  function handleDisconnectConfirmed() {
    setDisconnectOpen(false);
    void runLifecycleAction(() => disconnectAccount(account.id, csrfToken));
  }

  const fundingLocked = account.funding?.locked ?? false;

  /** Saves the Edit-account dialog. The label and the funding override are
   * two separate server writes; each runs ONLY when its field changed, so an
   * untouched (or locked) funding value is never re-submitted. */
  async function handleEditSave() {
    setEditSubmitting(true);
    setEditError(null);
    try {
      const nextLabel = labelInput.trim();
      if (nextLabel !== (account.label ?? "")) {
        await patchAccountLabel(account.id, nextLabel, csrfToken);
      }
      if (!fundingLocked && fundingChoice !== (account.funding?.funding ?? "unknown")) {
        await updateFunding(
          account.id,
          { funding: fundingChoice as "free" | "paid" | "unknown", expected_version: account.funding?.version },
          csrfToken,
        );
      }
      setEditOpen(false);
      onChanged();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setEditError(toApiError(err));
    } finally {
      setEditSubmitting(false);
    }
  }

  function openEdit() {
    setLabelInput(account.label ?? "");
    setFundingChoice(account.funding?.funding ?? "unknown");
    setEditError(null);
    setEditOpen(true);
  }
  // A synthetic plan label that merely repeats the funding classification
  // ("Free" plan + free funding) would render as two identical badges —
  // keep the FundingBadge (the real classification) and drop the echo.
  const plan = account.identity.plan;
  const showPlanBadge = !!plan && plan.toLowerCase() !== (account.funding?.funding ?? "").toLowerCase();
  // The row's headline: the owner's label when set, otherwise the real
  // identity email, otherwise the numbered default. The opaque external_id
  // (a 64-char SHA-256 fingerprint for API-key accounts) is never shown.
  const defaultName = `#account ${String(index).padStart(2, "0")}`;
  const displayName = account.label || account.identity.email || defaultName;
  // When a custom label overrides a real email, keep the email as a muted
  // caption so that identity isn't lost — but never the fingerprint hex.
  const secondaryEmail = account.label && account.identity.email ? account.identity.email : null;
  const canStop = account.connection_state === "connected";
  const canResume = account.connection_state === "stopped";
  const powerTitle = canStop
    ? "Disable account"
    : canResume
      ? "Enable account"
      : `Account is ${account.connection_state}`;
  const isFreeAccount = account.funding?.funding?.toLowerCase() === "free";
  const dotTone = DOT_TONE[account.display_status] ?? "unknown";
  const quotaObserved = latestQuotaObservedAt(account);
  const checkedAt = account.last_health_check_at ? Date.parse(account.last_health_check_at) : NaN;

  return (
    <>
      <div className="vnd-account" data-account-id={account.id}>
        <span className="vnd-account-index" aria-hidden="true">
          #{String(index).padStart(2, "0")}
        </span>

        <div className="vnd-account-body">
          <div className="vnd-account-identity">
            {/* Headline: label > email > numbered default. The opaque
                fingerprint external_id is deliberately never rendered. */}
            <span className="vnd-account-email" title={displayName}>
              {displayName}
            </span>
            {showPlanBadge && plan ? (
              <Badge tone="info" mono title={`plan: ${plan}`}>
                {plan.toUpperCase()}
              </Badge>
            ) : null}
            {secondaryEmail ? (
              <span className="vn-caption" title={secondaryEmail}>
                {secondaryEmail}
              </span>
            ) : null}
            <FundingBadge
              funding={account.funding?.funding}
              source={account.funding?.source}
              locked={fundingLocked}
              plan={isFreeAccount ? "Free / ∞" : undefined}
            />
          </div>

          <div className="vnd-account-details">
            <SecretRevealControl
              masked={secret ? "•".repeat(secret.length) : "•".repeat(64)}
              secret={secret}
              revealed={revealed}
              blocked={!revealed}
              onRevealRequest={handleRevealRequest}
              onHide={handleHide}
              label="credential"
            />
            <QuotaSummaryCompact windows={account.quota} />
          </div>
          {revealError ? (
            <TypedErrorDisplay code={revealError.code} message={revealError.message} retryable={revealError.retryable} tone="critical" />
          ) : null}

          {/* P3c-UI-001: retained mount (no-removal rule). The per-account
           * LIVE certification surface is the Model Test Report modal
           * (per-model status derived from the same offering capability
           * truths); with zero operations this renders nothing. */}
          <CertificationSummary operations={[]} />

          {actionNote ? <span className="vn-caption">{actionNote}</span> : null}
          {actionError ? (
            <TypedErrorDisplay code={actionError.code} message={actionError.message} retryable={actionError.retryable} tone="critical" />
          ) : null}
        </div>

        <div className="vnd-account-right">
          <div className="vnd-account-actions">
            <IconButton
              icon="settings"
              label="Edit account"
              title="Edit label & funding"
              variant="ghost"
              size="md"
              disabled={actionPending}
              onClick={openEdit}
            />
            <IconButton
              icon="heart-pulse"
              label="Sync: health · plan · usage"
              title="Sync: health · plan · usage"
              variant="ghost"
              size="md"
              disabled={actionPending || account.connection_state === "disconnected"}
              onClick={handleSync}
            />
            <IconButton
              icon="download"
              label="Fetch models from provider"
              title="Fetch models from provider"
              variant="ghost"
              size="md"
              disabled={actionPending || account.connection_state === "disconnected"}
              onClick={handleFetchModels}
            />
            <IconButton
              icon="flask-conical"
              label="Open model test report"
              title={modelCount ? "Open model test report" : "No models discovered yet"}
              variant="ghost"
              size="md"
              className="vnd-count-btn"
              disabled={!modelCount}
              onClick={onOpenModelReport}
            >
              {/* An unknown count is "—", never a fabricated 0. */}
              {modelCount == null ? "—" : modelCount}
            </IconButton>
            <IconButton
              icon="power"
              label={powerTitle}
              title={powerTitle}
              variant="ghost"
              size="md"
              disabled={actionPending || (!canStop && !canResume)}
              onClick={canStop ? handleStop : canResume ? handleResume : undefined}
            />
            <IconButton
              icon="unplug"
              label="Disconnect account"
              title="Disconnect account"
              variant="ghost"
              size="md"
              disabled={actionPending || account.connection_state === "disconnected"}
              onClick={() => setDisconnectOpen(true)}
            />
          </div>
          <div className="vnd-account-meta">
            <span className={`vnd-health-dot vnd-health-dot--${dotTone}`} title={`display_status: ${account.display_status}`} />
            <span>
              {isFreeAccount && quotaObserved == null ? "Free" : `Quota: ${quotaObserved == null ? "—" : relativeTime(quotaObserved)}`} · Checked:{" "}
              {Number.isNaN(checkedAt) ? "—" : relativeTime(checkedAt)}
            </span>
          </div>
        </div>
      </div>

      <ReverifyModal
        open={reverifyOpen}
        action="reveal this credential"
        csrfToken={csrfToken}
        onSuccess={handleReverifySuccess}
        onCancel={() => setReverifyOpen(false)}
        onSessionExpired={onSessionExpired}
      />

      <DestructiveActionConfirmation
        open={disconnectOpen}
        title={`Disconnect ${displayName}?`}
        consequence="This permanently removes the account and everything derived from it — its discovered models, quota, health, and credentials — and returns the provider to Available (awaiting connection). Audit history is retained. Reconnecting requires a new enrollment."
        confirmWord="disconnect"
        confirmLabel="Disconnect account"
        onConfirm={handleDisconnectConfirmed}
        onCancel={() => setDisconnectOpen(false)}
      />

      <Dialog
        open={editOpen}
        onClose={() => setEditOpen(false)}
        title="Edit account"
        description="Set a display label and the funding classification for this account."
        footer={
          <>
            <Button variant="ghost" onClick={() => setEditOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" loading={editSubmitting} onClick={() => void handleEditSave()}>
              Save
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <FormField label="Label" description="Optional. Shown instead of the auto-generated account number.">
            <Input
              placeholder={defaultName}
              maxLength={100}
              value={labelInput}
              disabled={editSubmitting}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setLabelInput(e.target.value)}
              onKeyDown={(e: KeyboardEvent<HTMLInputElement>) => {
                if (e.key === "Enter") void handleEditSave();
              }}
            />
          </FormField>
          <FormField
            label="Funding"
            description={
              fundingLocked
                ? "Locked by provider policy — this account's funding can't be overridden."
                : "Recorded as an owner override; it supersedes the current evidence and is never auto-reverted."
            }
          >
            <Select
              options={FUNDING_OPTIONS}
              value={fundingChoice}
              disabled={editSubmitting || fundingLocked}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setFundingChoice(e.target.value)}
            />
          </FormField>
          {editError ? (
            <TypedErrorDisplay code={editError.code} message={editError.message} retryable={editError.retryable} tone="critical" />
          ) : null}
        </div>
      </Dialog>
    </>
  );
}
