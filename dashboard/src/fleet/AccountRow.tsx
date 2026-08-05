import { useEffect, useRef, useState, type ChangeEvent, type KeyboardEvent } from "react";
import { toast } from "@venom/design-system";
import {
  Badge,
  Button,
  Dialog,
  FormField,
  IconButton,
  Input,
  Select,
} from "@venom/design-system/primitives";
import {
  DestructiveActionConfirmation,
  FundingBadge,
  SecretRevealControl,
  TypedErrorDisplay,
} from "@venom/design-system/domain";
import ReverifyModal from "../auth/ReverifyModal";
import CertificationSummary from "./CertificationSummary";
import { QuotaSummaryCompact } from "./QuotaSummary";
import { formatBalanceValue, isBalanceWindow } from "./quotaWindows";
import { pollJobToTerminal } from "./jobs";
import { providerMeta } from "./providerMeta";
import { reauthErrorGuidance } from "./reauthErrorGuidance";
import { relativeTime } from "./relativeTime";
import { useOAuthRelayCompletion } from "./useOAuthRelay";
import {
  AuthApiError,
  disconnectAccount,
  isSessionExpired,
  oauthCompleteCode,
  oauthReauthBegin,
  patchAccountLabel,
  pollOAuthStatus,
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
  type QuotaWindow,
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
function latestQuotaObservedAt(windows: QuotaWindow[]): number | null {
  let latest: number | null = null;
  for (const w of windows) {
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
 * secret by default. Reveal is reverify-FIRST — onRevealRequest opens the
 * same ReverifyModal auth/AuthGate's own sensitive actions use, and only a
 * successful reverify runs POST /accounts/{id}/reveal. (attemptReveal keeps
 * its 401 handler as defense in depth, but the up-front challenge means the
 * reveal call is not fired speculatively, so it never 401s into the console.)
 * Both onHide AND losing focus clear the plaintext from this component's
 * state immediately; it is never logged, never stored, and the DOM shows
 * only the masked placeholder once hidden.
 */
export default function AccountRow(props: AccountRowProps) {
  const { account, index, modelCount, csrfToken, onSessionExpired, onChanged, onOpenModelReport } =
    props;

  const [revealed, setRevealed] = useState(false);
  const [secret, setSecret] = useState("");
  const [revealPending, setRevealPending] = useState(false);
  const [revealError, setRevealError] = useState<AuthApiError | null>(null);
  const [reverifyOpen, setReverifyOpen] = useState(false);

  const [syncState, setSyncState] = useState<"idle" | "loading" | "success" | "failure">("idle");
  const [fetchState, setFetchState] = useState<"idle" | "loading" | "success" | "failure">("idle");

  const [actionPending, setActionPending] = useState(false);
  const [actionError, setActionError] = useState<AuthApiError | null>(null);
  const [actionNote, setActionNote] = useState<string | null>(null);
  const [reauthAuthorizeUrl, setReauthAuthorizeUrl] = useState<string | null>(null);
  // The in-flight reauth transaction. It is state, not a ref, because the
  // relay hook subscribes on it: a provider that omits `state` can only be
  // completed by US, with this id (see useOAuthRelay).
  const [reauthTransactionId, setReauthTransactionId] = useState<string | null>(null);
  const reauthPollRef = useRef<number | null>(null);
  const reauthPollBusyRef = useRef(false);
  const [disconnectOpen, setDisconnectOpen] = useState(false);

  // One "Edit account" dialog drives both settings: the display label and
  // the funding override. Its two writes are independent — each fires only
  // when its own field actually changed.
  const [editOpen, setEditOpen] = useState(false);
  const [labelInput, setLabelInput] = useState("");
  const [fundingChoice, setFundingChoice] = useState<string>(account.funding?.funding ?? "unknown");
  const [editSubmitting, setEditSubmitting] = useState(false);
  const [editError, setEditError] = useState<AuthApiError | null>(null);

  // Called at every terminal point of a reauth (and once before starting a
  // fresh one), so it also drops the transaction id — that unsubscribes the
  // relay listener, and a stale id must never be spent by a later message.
  function stopReauthPolling() {
    if (reauthPollRef.current != null) {
      window.clearInterval(reauthPollRef.current);
      reauthPollRef.current = null;
    }
    setReauthTransactionId(null);
  }

  useEffect(
    () => () => {
      if (reauthPollRef.current != null) window.clearInterval(reauthPollRef.current);
    },
    [],
  );

  // The state-less completion leg. ClinePass never echoes `state`, so its
  // callback arrives at the backend with a bare `code` and is relayed to this
  // window; without this the code is dropped and the reauth silently expires.
  useOAuthRelayCompletion({
    transactionId: reauthTransactionId,
    onCode: (transactionId, code) => {
      void (async () => {
        try {
          const status = await oauthCompleteCode(transactionId, code, csrfToken);
          if (status.status === "completed") {
            stopReauthPolling();
            setActionPending(false);
            toast.success("Account reauthenticated");
            setReauthAuthorizeUrl(null);
            onChanged();
            return;
          }
          stopReauthPolling();
          setActionPending(false);
          setActionError(
            new AuthApiError(0, {
              code: status.error ?? "oauth_failed",
              message: "The reauthentication did not complete.",
              request_id: "",
              retryable: true,
            }),
          );
        } catch (err) {
          if (isSessionExpired(err)) {
            stopReauthPolling();
            setActionPending(false);
            onSessionExpired();
            return;
          }
          // Leave the poll running: the transaction may still be completed
          // server-side, and it has its own server-issued expiry.
          setActionError(toApiError(err));
        }
      })();
    },
    onDenied: (error) => {
      stopReauthPolling();
      setActionPending(false);
      setActionError(
        new AuthApiError(0, {
          code: error || "oauth_denied",
          message: "The reauthentication was denied.",
          request_id: "",
          retryable: true,
        }),
      );
    },
  });

  async function handleReauthenticate() {
    // reserve the popup synchronously inside the click gesture; navigating a
    // popup only after the POST resolves is routinely blocked by browsers.
    const popup = window.open("", `venom-reauth-${account.id}`, "popup,width=760,height=820");
    setActionPending(true);
    setActionError(null);
    setReauthAuthorizeUrl(null);
    stopReauthPolling();
    try {
      const begin = await oauthReauthBegin(account.id, csrfToken);
      if (popup) {
        popup.location.assign(begin.authorize_url);
      } else {
        setReauthAuthorizeUrl(begin.authorize_url);
      }
      // Arm the relay leg: for a provider that omits `state` the backend
      // cannot finish the callback itself, so this id is the only way the
      // relayed code can be spent (see useOAuthRelay).
      setReauthTransactionId(begin.transaction_id);
      setActionNote("Complete sign-in to restore this account.");
      const expiresAt = Date.parse(begin.expires_at);
      if (Number.isNaN(expiresAt)) throw new Error("OAuth transaction returned an invalid expiry");
      reauthPollRef.current = window.setInterval(() => {
        void (async () => {
          if (reauthPollBusyRef.current) return;
          if (Date.now() >= expiresAt) {
            stopReauthPolling();
            setActionPending(false);
            setActionError(
              new AuthApiError(0, {
                code: "oauth_expired",
                message: "The reauthentication window expired. Start sign-in again.",
                request_id: "",
                retryable: true,
              }),
            );
            return;
          }
          reauthPollBusyRef.current = true;
          try {
            const status = await pollOAuthStatus(begin.transaction_id);
            if (status.status === "completed") {
              stopReauthPolling();
              setActionPending(false);
              toast.success("Account reauthenticated");
              setReauthAuthorizeUrl(null);
              onChanged();
            } else if (status.status === "failed" || status.status === "expired") {
              stopReauthPolling();
              setActionPending(false);
              setActionError(
                new AuthApiError(0, {
                  code: status.error ?? `oauth_${status.status}`,
                  message: "The reauthentication did not complete.",
                  request_id: "",
                  retryable: true,
                }),
              );
            }
          } catch (err) {
            if (isSessionExpired(err)) {
              stopReauthPolling();
              setActionPending(false);
              onSessionExpired();
            }
            // Other poll failures are transient; the bounded transaction
            // continues until its server-issued expiry.
          } finally {
            reauthPollBusyRef.current = false;
          }
        })();
      }, 1_500);
    } catch (err) {
      popup?.close();
      setActionPending(false);
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setActionError(toApiError(err));
    }
  }

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
      toast.danger("Failed to reveal credential", {
        detail: err instanceof Error ? err.message : String(err),
      });
    } finally {
      setRevealPending(false);
    }
  }

  function handleRevealRequest() {
    if (revealPending) return;
    // Reveal-first: revealing a secret ALWAYS challenges with a fresh
    // re-verification, so we open the prompt up front rather than firing an
    // optimistic reveal that would come back 401 (a red console entry) whenever
    // the session is not already reverify-fresh. attemptReveal then runs from
    // handleReverifySuccess.
    setRevealError(null);
    setReverifyOpen(true);
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

  async function runLifecycleAction(action: () => Promise<unknown>): Promise<boolean> {
    setActionPending(true);
    setActionError(null);
    try {
      await action();
      onChanged();
      return true;
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return false;
      }
      const apiErr = toApiError(err);
      setActionError(apiErr);
      toast.danger("Account action failed", {
        detail: apiErr.message || (err instanceof Error ? err.message : String(err)),
      });
      return false;
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
    setSyncState("loading");
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
          const note = "Quota sync skipped — this provider has no quota capability. Health was refreshed.";
          setActionNote(note);
          return;
        }
        throw err;
      }
    }).then((success) => {
      setSyncState(success ? "success" : "failure");
      setTimeout(() => {
        try {
          setSyncState("idle");
        } catch {
          // The component may have unmounted before the 2s reset fired;
          // a late setState throw is expected noise, not a failure.
        }
      }, 2000);
      if (success) {
        toast.success("Health and quota refreshed", {
          detail: `Account: ${account.label || defaultName}`,
        });
        if (modelCount === 0 || modelCount == null) {
          handleFetchModels();
        }
      }
    });
  }

  /** "Fetch models from provider" — discovery for THIS account, polled to
   * terminal, then a refetch (the parent reloads offerings too). */
  function handleFetchModels() {
    setFetchState("loading");
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
    }).then((success) => {
      setFetchState(success ? "success" : "failure");
      setTimeout(() => setFetchState("idle"), 2000);
      if (success) {
        toast.success("Models discovered successfully", {
          detail: `Discovered models for ${account.label || defaultName}`,
        });
      }
    });
  }

  function handleStop() {
    void runLifecycleAction(() => stopAccount(account.id, csrfToken)).then((success) => {
      if (success) {
        toast.success("Account paused", {
          detail: `Paused account "${account.label || defaultName}"`,
        });
      }
    });
  }

  function handleResume() {
    void runLifecycleAction(() => resumeAccount(account.id, csrfToken)).then((success) => {
      if (success) {
        toast.success("Account resumed", {
          detail: `Resumed account "${account.label || defaultName}"`,
        });
      }
    });
  }

  async function handleDisconnectConfirmed() {
    setDisconnectOpen(false);
    setActionPending(true);
    setActionError(null);
    try {
      await disconnectAccount(account.id, csrfToken);
      toast.success("Account disconnected", {
        detail: `Removed account "${account.label || account.id}"`,
      });
      onChanged();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      const apiErr = toApiError(err);
      setActionError(apiErr);
      toast.danger("Failed to disconnect account", {
        detail: err instanceof Error ? err.message : String(err),
      });
    } finally {
      setActionPending(false);
    }
  }

  const fundingLocked = account.funding?.locked ?? false;

  /** Saves the Edit-account dialog. The label and the funding override are
   * two separate server writes; each runs ONLY when its field changed, so an
   * untouched (or locked) funding value is never re-submitted. */
  async function handleEditSave() {
    setEditSubmitting(true);
    setEditError(null);
    const nextLabel = labelInput.trim();
    const labelChanged = nextLabel !== (account.label ?? "");
    const fundingChanged = !fundingLocked && fundingChoice !== (account.funding?.funding ?? "unknown");

    let hasError = false;

    if (labelChanged) {
      try {
        await patchAccountLabel(account.id, nextLabel, csrfToken);
        toast.success("Account label updated", { detail: `Renamed to "${nextLabel}"` });
      } catch (err) {
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setEditError(toApiError(err));
        toast.danger("Failed to update account label", {
          detail: err instanceof Error ? err.message : String(err),
        });
        hasError = true;
      }
    }

    if (!hasError && fundingChanged) {
      try {
        await updateFunding(
          account.id,
          {
            funding: fundingChoice as "free" | "paid" | "unknown",
            expected_version: account.funding?.version,
          },
          csrfToken,
        );
        toast.success("Funding details updated");
      } catch (err) {
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setEditError(toApiError(err));
        toast.danger("Failed to update funding details", {
          detail: err instanceof Error ? err.message : String(err),
        });
        hasError = true;
      }
    }

    if (!hasError) {
      setEditOpen(false);
      onChanged();
    }
    setEditSubmitting(false);
  }

  function openEdit() {
    setLabelInput(account.label ?? "");
    setFundingChoice(account.funding?.funding ?? "unknown");
    setEditError(null);
    setEditOpen(true);
  }
  const isApiKeyAccount = account.auth_type === "api_key";
  const isClinePassOAuth = account.provider === "clinepass" && account.auth_type === "oauth2";
  const subscriptionRequired =
    isClinePassOAuth &&
    account.display_status === "degraded" &&
    (account.last_health_error ?? "").toLowerCase().includes("no active clinepass subscription");
  const needsOAuthReauth = !isApiKeyAccount && (account.display_status === "expired" || subscriptionRequired);
  // A synthetic plan label that merely repeats the funding classification
  // ("Free" plan + free funding) would render as two identical badges —
  // keep the FundingBadge (the real classification) and drop the echo.
  const plan = account.identity.plan;
  const showPlanBadge =
    !isClinePassOAuth &&
    !!plan &&
    plan.toLowerCase() !== (account.funding?.funding ?? "").toLowerCase();
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
  const canSync = account.connection_state === "connected" && account.display_status !== "expired" && !subscriptionRequired;
  const canFetchModels = account.connection_state === "connected" && account.display_status !== "expired" && !subscriptionRequired;
  const canTogglePower = canStop || canResume;
  const powerTitle = canStop
    ? "Disable account"
    : canResume
      ? "Enable account"
      : `Account is ${account.connection_state}`;
  const isFreeAccount = account.funding?.funding?.toLowerCase() === "free";
  // A live health problem with a provider-observed reason (e.g. clinepass's
  // "no active subscription") renders inline so the owner sees WHY the dot
  // is not green — and can fix or remove the account.
  const dotTone = subscriptionRequired
    ? "warning"
    : (DOT_TONE[account.display_status] ?? "unknown");
  // Provider evidence is live UI data, not account history. Never render a
  // balance or percentage while the account is unhealthy, and never render
  // an evidence window the backend has marked stale/non-available.
  const liveQuotaWindows =
    account.display_status === "healthy"
      ? account.quota.filter(
          (w) =>
            w.source === "local_safety" || (w.state === "available" && w.freshness === "fresh"),
        )
      : [];
  const quotaObserved = latestQuotaObservedAt(liveQuotaWindows);
  const checkedAt = account.last_health_check_at ? Date.parse(account.last_health_check_at) : NaN;
  // The credential reveal control is an API-KEY affordance: an OAuth
  // account's stored credential is a rotating token envelope the owner
  // never pastes or copies, so the masked-key row is pure noise there
  // (and the legacy reference shows nothing key-like for OAuth accounts).
  // The account's provider-reported balance (e.g. clinepass credits),
  // shown as an identity chip like the legacy header balance. Currency
  // formatting is a per-provider presentation hint (providerMeta).
  const balanceWindow = liveQuotaWindows.find(
    (w) => w.source !== "local_safety" && isBalanceWindow(w),
  );
  const balanceCurrency = providerMeta(account.provider)?.balanceCurrency;
  const checkedLabel = Number.isNaN(checkedAt) ? "not yet" : relativeTime(checkedAt);
  const metaLabel = subscriptionRequired
    ? `Subscription checked ${checkedLabel} · Usage unavailable`
    : needsOAuthReauth
      ? "Live updates paused · Sign in again"
      : isFreeAccount && quotaObserved == null
        ? `Free · Health checked ${checkedLabel}`
        : quotaObserved == null
          ? `Usage unavailable · Health checked ${checkedLabel}`
          : `Usage updated ${relativeTime(quotaObserved)} · Health checked ${checkedLabel}`;

  // A failed action replaces the state description IN PLACE — same box, same
  // inline CTA, no second error panel stacked underneath. The row's structure
  // must never change because a call failed: that divergence is exactly what
  // made two non-operational rows read as two different components, and a raw
  // code plus a "not retryable" badge told the owner nothing about the cause.
  const actionGuidance = actionError
    ? reauthErrorGuidance(
        actionError.code,
        actionError.message,
        account.identity.email || account.label || defaultName,
      )
    : null;

  const statusConfig = (() => {
    if (subscriptionRequired) {
      return {
        tone: "critical",
        symbol: "!",
        title: "Subscription required",
        badgeText: "Subscription required",
        badgeTone: "critical" as const,
        badgeIcon: "triangle-alert",
        description: "No active ClinePass subscription found for this account.",
        meta: "Live updates paused · Usage unavailable"
      };
    }
    if (account.connection_state === "stopped") {
      return {
        tone: "inactive",
        symbol: "—",
        title: "Disabled by user",
        badgeText: "Disabled",
        badgeTone: "unknown" as const,
        badgeIcon: "ban",
        description: "This account is currently turned off.",
        meta: "Live updates paused · Usage unavailable"
      };
    }
    if (account.display_status === "expired" || needsOAuthReauth) {
      return {
        tone: "critical",
        symbol: "!",
        title: "Sign-in required",
        badgeText: "Sign-in required",
        badgeTone: "critical" as const,
        badgeIcon: "triangle-alert",
        description: "OAuth session expired. Sign in again to resume live updates.",
        meta: "Live updates paused · Usage unavailable"
      };
    }
    if (account.display_status === "healthy") {
      return {
        tone: "healthy",
        symbol: "✓",
        title: "Healthy",
        badgeText: isClinePassOAuth ? "Pass active" : "Active",
        badgeTone: "healthy" as const,
        badgeIcon: "circle-check",
        description: null,
        meta: "Live updates active · Usage available"
      };
    }
    if (account.display_status === "degraded" || account.display_status === "unavailable") {
      return {
        tone: "critical",
        symbol: "!",
        title: "Attention required",
        badgeText: "Degraded",
        badgeTone: "critical" as const,
        badgeIcon: "triangle-alert",
        description: account.last_health_error || "Account access is degraded or unavailable.",
        meta: "Live updates paused · Usage unavailable"
      };
    }
    return {
      tone: "critical",
      symbol: "!",
      title: "Inactive",
      badgeText: "Inactive",
      badgeTone: "critical" as const,
      badgeIcon: "triangle-alert",
      description: null,
      meta: "Live updates paused · Usage unavailable"
    };
  })();

  return (
    <>
      <div
        className={`vnd-account vnd-account--status-${statusConfig.tone}${subscriptionRequired ? " vnd-account--subscription-required" : ""}${needsOAuthReauth ? " vnd-account--expired" : ""}`}
        data-account-id={account.id}
        data-account-state={subscriptionRequired ? "subscription-required" : account.display_status}
      >
        <div className={`vnd-account-status-bar vnd-account-status-bar--${statusConfig.tone}`}>
          <div className="vnd-account-status-circle">
            <span className="vnd-account-status-symbol">{statusConfig.symbol}</span>
          </div>
          <span className="vnd-account-index" aria-hidden="true">
            #{String(index).padStart(2, "0")}
          </span>
        </div>

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
            {statusConfig.badgeText && statusConfig.badgeText !== "Degraded" ? (
              <Badge tone={statusConfig.badgeTone} icon={statusConfig.badgeIcon}>
                {statusConfig.badgeText}
              </Badge>
            ) : null}
            {secondaryEmail ? (
              <span className="vn-caption" title={secondaryEmail}>
                {secondaryEmail}
              </span>
            ) : null}
            {!isClinePassOAuth ? (
              <FundingBadge
                funding={account.funding?.funding}
                source={account.funding?.source}
                locked={fundingLocked}
                plan={isFreeAccount ? "Free / ∞" : undefined}
              />
            ) : null}
            {balanceWindow ? (
              <Badge
                tone={
                  balanceWindow.state === "available" && balanceWindow.freshness === "fresh"
                    ? "healthy"
                    : "unknown"
                }
                mono
                title={`balance: ${balanceWindow.remaining} ${balanceWindow.unit} · state: ${balanceWindow.state}`}
              >
                {formatBalanceValue(balanceWindow, balanceCurrency)}
              </Badge>
            ) : null}
          </div>

          <div className="vnd-account-details">
            {isApiKeyAccount ? (
              <SecretRevealControl
                masked={secret ? "•".repeat(secret.length) : "•".repeat(64)}
                secret={secret}
                revealed={revealed}
                blocked={!revealed}
                onRevealRequest={handleRevealRequest}
                onHide={handleHide}
                onCopy={() => toast.success("Credential copied to clipboard")}
                label="credential"
              />
            ) : null}
            <QuotaSummaryCompact windows={liveQuotaWindows} balanceCurrency={balanceCurrency} />
          </div>
          {actionGuidance?.message || statusConfig.description ? (
            <div className={`vnd-account-issue-box vnd-account-issue-box--${statusConfig.tone}`}>
              <p className="vnd-account-issue-desc">
                {statusConfig.badgeIcon ? (
                  <span
                    className={`vn-icon vn-icon--${statusConfig.badgeIcon} vnd-account-issue-icon`}
                    style={{ width: 14, height: 14 }}
                    aria-hidden
                  />
                ) : null}
                <span>{actionGuidance?.message || statusConfig.description}</span>
                {needsOAuthReauth ? (
                  reauthAuthorizeUrl ? (
                    <a
                      className="vnd-account-inline-cta"
                      href={reauthAuthorizeUrl}
                      target="_blank"
                      rel="noreferrer"
                    >
                      Reauthenticate now <span className="vnd-cta-arrow">→</span>
                    </a>
                  ) : (
                    <button
                      className="vnd-account-inline-cta-btn"
                      disabled={actionPending}
                      onClick={() => void handleReauthenticate()}
                    >
                      Reauthenticate now <span className="vnd-cta-arrow">→</span>
                    </button>
                  )
                ) : subscriptionRequired ? (
                  <button
                    className="vnd-account-inline-cta-btn"
                    onClick={handleSync}
                    disabled={actionPending}
                  >
                    Check subscription <span className="vnd-cta-arrow">→</span>
                  </button>
                ) : account.connection_state === "stopped" ? (
                  <button
                    className="vnd-account-inline-cta-btn"
                    onClick={handleResume}
                    disabled={actionPending}
                  >
                    Enable account <span className="vnd-cta-arrow">→</span>
                  </button>
                ) : (
                  <button
                    className="vnd-account-inline-cta-btn"
                    onClick={handleSync}
                    disabled={actionPending}
                  >
                    Sync again <span className="vnd-cta-arrow">→</span>
                  </button>
                )}
              </p>
            </div>
          ) : null}
          {revealError ? (
            <TypedErrorDisplay
              code={revealError.code}
              message={revealError.message}
              retryable={revealError.retryable}
              tone="critical"
            />
          ) : null}

          {/* P3c-UI-001: retained mount (no-removal rule). The per-account
           * LIVE certification surface is the Model Test Report modal
           * (per-model status derived from the same offering capability
           * truths); with zero operations this renders nothing. */}
          <CertificationSummary operations={[]} />

          {reauthAuthorizeUrl ? (
            <a className="vn-link" href={reauthAuthorizeUrl} target="_blank" rel="noreferrer">
              Continue secure sign-in
            </a>
          ) : null}
          {/* No TypedErrorDisplay for an action error: that component is the
              envelope renderer for an API CALL (code chip + "not retryable"),
              and an account-lifecycle failure is not that. Its text now lives
              in the row's own issue box above (actionGuidance), so the row
              keeps ONE shape whether or not the last action failed. */}
        </div>

        <div className="vnd-account-right">
          <div className="vnd-account-actions-box">
            <div className="vnd-account-actions">
              {needsOAuthReauth ? (
                <IconButton
                  icon="refresh-cw"
                  label="Reauthenticate account"
                  title="Sign in again"
                  variant="ghost"
                  size="md"
                  disabled={actionPending}
                  onClick={() => void handleReauthenticate()}
                />
              ) : null}
              {/* API-key accounts only. For an OAuth account this dialog holds
                  exactly ONE optional cosmetic field — the label — because
                  funding is detected from the provider and is api-key-only; and
                  the provider already returns the identity that names the row,
                  so the label is redundant there. A control that opens a dialog
                  with nothing worth deciding is noise in the cluster. */}
              {isApiKeyAccount ? (
                <IconButton
                  icon="settings"
                  label="Edit account"
                  title="Edit label & funding"
                  variant="ghost"
                  size="md"
                  disabled={actionPending}
                  onClick={openEdit}
                />
              ) : null}
              <IconButton
                icon={
                  syncState === "loading"
                    ? "loader-circle"
                    : syncState === "success"
                      ? "check"
                      : syncState === "failure"
                        ? "x"
                        : "heart-pulse"
                }
                className={
                  syncState === "loading"
                    ? "vnd-spinner"
                    : syncState === "success"
                      ? "vnd-btn--success"
                      : syncState === "failure"
                        ? "vnd-btn--failure"
                        : ""
                }
                label="Sync: health · plan · usage"
                title="Sync: health · plan · usage"
                variant="ghost"
                size="md"
                disabled={actionPending || !canSync}
                onClick={handleSync}
              />
              <IconButton
                icon={
                  fetchState === "loading"
                    ? "loader-circle"
                    : fetchState === "success"
                      ? "check"
                      : fetchState === "failure"
                        ? "x"
                        : "download"
                }
                className={
                  fetchState === "loading"
                    ? "vnd-spinner"
                    : fetchState === "success"
                      ? "vnd-btn--success"
                      : fetchState === "failure"
                        ? "vnd-btn--failure"
                        : ""
                }
                label="Fetch models from provider"
                title="Fetch models from provider"
                variant="ghost"
                size="md"
                disabled={actionPending || !canFetchModels}
                onClick={handleFetchModels}
              />
              <IconButton
                icon="flask-conical"
                label="Open model test report"
                title={modelCount ? "Open model test report" : "No live models"}
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
                icon={canStop ? "pause" : "play"}
                label={powerTitle}
                title={powerTitle}
                variant="ghost"
                size="md"
                disabled={actionPending || !canTogglePower}
                onClick={canStop ? handleStop : canResume ? handleResume : undefined}
              />
              <IconButton
                icon="trash-2"
                label="Delete account"
                title="Delete account"
                variant="ghost"
                size="md"
                disabled={actionPending || account.connection_state === "disconnected"}
                onClick={() => setDisconnectOpen(true)}
              />
            </div>
            <div className="vnd-account-meta">
              <span
                className={`vnd-health-dot vnd-health-dot--${dotTone}`}
                title={`display_status: ${account.display_status}`}
              />
              <span title={actionNote || undefined}>{actionNote ? actionNote : (isClinePassOAuth ? statusConfig.meta : metaLabel)}</span>
            </div>
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
        title={`Delete ${displayName}?`}
        consequence="This permanently removes the account and everything derived from it — its discovered models, quota, health, and credentials — and returns the provider to Available (awaiting connection). Audit history is retained. Reconnecting requires a new enrollment."
        confirmWord="delete"
        confirmLabel="Delete account"
        onConfirm={handleDisconnectConfirmed}
        onCancel={() => setDisconnectOpen(false)}
      />

      <Dialog
        open={editOpen}
        onClose={() => setEditOpen(false)}
        title="Edit account"
        description={
          isApiKeyAccount
            ? "Set a display label and the funding classification for this account."
            : "Set a display label for this account. Funding is detected automatically for OAuth providers."
        }
        footer={
          <>
            <Button variant="ghost" onClick={() => setEditOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              loading={editSubmitting}
              onClick={() => void handleEditSave()}
            >
              Save
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <FormField
            label="Label"
            description="Optional. Shown instead of the auto-generated account number."
          >
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
          {/* Funding is an owner CHOICE only for API-key providers. OAuth
              accounts detect their classification automatically after login
              (provider policy or plan evidence — e.g. clinepass is paid by
              policy; antigravity reads the Google plan), so offering a
              free/paid selector there would only invite contradicting the
              provider's own answer. */}
          {isApiKeyAccount ? (
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
          ) : null}
          {editError ? (
            <TypedErrorDisplay
              code={editError.code}
              message={editError.message}
              retryable={editError.retryable}
              tone="critical"
            />
          ) : null}
        </div>
      </Dialog>
    </>
  );
}
