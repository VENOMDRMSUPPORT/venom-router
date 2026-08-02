import { useState, type ChangeEvent } from "react";
import { Badge, Button, Dialog, FormField, IconButton, Select } from "@venom/design-system/primitives";
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

/** A fingerprint-style external id (fingerprint-identity providers store a
 * 64-char hex SHA-256 there). Rendered truncated — the full value stays in
 * the title attribute, never lost. */
const FINGERPRINT_ID = /^[0-9a-f]{32,}$/i;

function shortExternalId(externalId: string): string {
  return FINGERPRINT_ID.test(externalId) ? `${externalId.slice(0, 12)}…` : externalId;
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

  const [fundingOpen, setFundingOpen] = useState(false);
  const [fundingChoice, setFundingChoice] = useState<string>(account.funding?.funding ?? "unknown");
  const [fundingSubmitting, setFundingSubmitting] = useState(false);
  const [fundingError, setFundingError] = useState<AuthApiError | null>(null);

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

  async function handleFundingSubmit() {
    setFundingSubmitting(true);
    setFundingError(null);
    try {
      await updateFunding(
        account.id,
        { funding: fundingChoice as "free" | "paid" | "unknown", expected_version: account.funding?.version },
        csrfToken,
      );
      setFundingOpen(false);
      onChanged();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setFundingError(toApiError(err));
    } finally {
      setFundingSubmitting(false);
    }
  }

  const fundingLocked = account.funding?.locked ?? false;
  // A synthetic plan label that merely repeats the funding classification
  // ("Free" plan + free funding) would render as two identical badges —
  // keep the FundingBadge (the real classification) and drop the echo.
  const plan = account.identity.plan;
  const showPlanBadge = !!plan && plan.toLowerCase() !== (account.funding?.funding ?? "").toLowerCase();
  const isFingerprintId = FINGERPRINT_ID.test(account.external_id);
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
      <div className="vnd-account">
        <span className="vnd-account-index" aria-hidden="true">
          {index}
        </span>

        <div className="vnd-account-body">
          <div className="vnd-account-identity">
            {/* A fingerprint identity renders truncated with the full value
                in the title — never a raw 64-char hex headline. */}
            <span
              className={`vnd-account-email${!account.identity.email && isFingerprintId ? " vn-mono-xs" : ""}`}
              title={!account.identity.email && isFingerprintId ? account.external_id : undefined}
            >
              {account.identity.email || shortExternalId(account.external_id)}
            </span>
            {showPlanBadge && plan ? (
              <Badge tone="info" mono title={`plan: ${plan}`}>
                {plan.toUpperCase()}
              </Badge>
            ) : null}
            {account.identity.email ? (
              <span className="vn-caption vn-mono-xs" title={account.external_id}>
                {shortExternalId(account.external_id)}
              </span>
            ) : null}
            <FundingBadge funding={account.funding?.funding} source={account.funding?.source} locked={fundingLocked} />
            <IconButton
              icon="sliders-horizontal"
              label="Override funding"
              variant="ghost"
              size="sm"
              disabled={fundingLocked}
              onClick={() => {
                setFundingChoice(account.funding?.funding ?? "unknown");
                setFundingError(null);
                setFundingOpen(true);
              }}
            />
          </div>

          {/* self-start: the reveal control is a fit-content chip, never a
              full-width slab stretched by the flex column. */}
          <span className="self-start">
            <SecretRevealControl
              masked="••••••••"
              secret={secret}
              revealed={revealed}
              blocked={!revealed}
              onRevealRequest={handleRevealRequest}
              onHide={handleHide}
              label="credential"
            />
          </span>
          {revealError ? (
            <TypedErrorDisplay code={revealError.code} message={revealError.message} retryable={revealError.retryable} tone="critical" />
          ) : null}

          <QuotaSummaryCompact
            windows={account.quota}
            isUnlimited={isFreeAccount && account.quota.length === 0}
          />

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
              icon="activity"
              label="Sync: health · plan · usage"
              title="Sync: health · plan · usage"
              variant="ghost"
              size="sm"
              disabled={actionPending || account.connection_state === "disconnected"}
              onClick={handleSync}
            />
            <IconButton
              icon="download"
              label="Fetch models from provider"
              title="Fetch models from provider"
              variant="ghost"
              size="sm"
              disabled={actionPending || account.connection_state === "disconnected"}
              onClick={handleFetchModels}
            />
            <IconButton
              icon="box"
              label="Open model test report"
              title={modelCount ? "Open model test report" : "No models discovered yet"}
              variant="ghost"
              size="sm"
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
              size="sm"
              disabled={actionPending || (!canStop && !canResume)}
              onClick={canStop ? handleStop : canResume ? handleResume : undefined}
            />
            <IconButton
              icon="trash-2"
              label="Disconnect account"
              title="Disconnect account"
              variant="ghost"
              size="sm"
              disabled={actionPending || account.connection_state === "disconnected"}
              onClick={() => setDisconnectOpen(true)}
            />
          </div>
          <div className="vnd-account-meta">
            <span className={`vnd-health-dot vnd-health-dot--${dotTone}`} title={`display_status: ${account.display_status}`} />
            <span>
              Quota: {isFreeAccount && quotaObserved == null ? "Unlimited" : quotaObserved == null ? "—" : relativeTime(quotaObserved)} · Checked:{" "}
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
        title={`Disconnect ${account.external_id}?`}
        consequence="Routing through this account stops immediately and its credentials are retired. The row and its sanitized history are retained — restoring it requires a new enrollment, not a resume."
        confirmWord="disconnect"
        confirmLabel="Disconnect account"
        onConfirm={handleDisconnectConfirmed}
        onCancel={() => setDisconnectOpen(false)}
      />

      <Dialog
        open={fundingOpen}
        onClose={() => setFundingOpen(false)}
        title="Override funding classification"
        description="Recorded as an owner override — it supersedes the current evidence row and is never auto-superseded back."
        footer={
          <>
            <Button variant="ghost" onClick={() => setFundingOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" loading={fundingSubmitting} onClick={() => void handleFundingSubmit()}>
              Save
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <FormField label="Funding">
            <Select
              options={FUNDING_OPTIONS}
              value={fundingChoice}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setFundingChoice(e.target.value)}
            />
          </FormField>
          {fundingError ? (
            <TypedErrorDisplay code={fundingError.code} message={fundingError.message} retryable={fundingError.retryable} tone="critical" />
          ) : null}
        </div>
      </Dialog>
    </>
  );
}
