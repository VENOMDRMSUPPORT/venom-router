import { useState, type ChangeEvent } from "react";
import { Button, Dialog, DropdownMenu, FormField, IconButton, Select } from "@venom/design-system/primitives";
import {
  AccountIdentity,
  AccountStatus,
  DestructiveActionConfirmation,
  FundingBadge,
  ProviderAccountRow,
  SecretRevealControl,
  TypedErrorDisplay,
} from "@venom/design-system/domain";
import ReverifyModal from "../auth/ReverifyModal";
import CertificationSummary from "./CertificationSummary";
import QuotaSummary from "./QuotaSummary";
import {
  AuthApiError,
  disconnectAccount,
  isSessionExpired,
  refreshHealth,
  resumeAccount,
  revealCredential,
  stopAccount,
  toApiError,
  updateFunding,
  type AccountProjection,
} from "../api/controlClient";

export interface AccountRowProps {
  account: AccountProjection;
  csrfToken: string;
  onSessionExpired: () => void;
  /** Called after any successful mutation — the parent re-fetches the
   * fleet so this row's own props are refreshed from the server. */
  onChanged: () => void;
}

const FUNDING_OPTIONS = [
  { value: "free", label: "Free" },
  { value: "paid", label: "Paid" },
  { value: "unknown", label: "Unknown" },
];

/**
 * One connected account's row (P2b-UI-003): identity + credential reveal,
 * the server's DERIVED display_status (rendered verbatim), the
 * account-scoped FundingBadge with an override control, and the
 * stop/resume/refresh-health/disconnect lifecycle actions.
 *
 * Credential reveal (security-critical): the masked control shows no
 * secret by default. onRevealRequest calls POST /accounts/{id}/reveal;
 * a reverification_required (401) opens the same ReverifyModal
 * auth/AuthGate's own sensitive actions use — on a successful reverify,
 * reveal is retried once. Both onHide AND losing focus (SecretRevealControl
 * calls onHide for both, see its own doc comment) clear the plaintext from
 * this component's state immediately; it is never logged, never stored,
 * and the DOM shows only the masked placeholder once hidden.
 */
export default function AccountRow(props: AccountRowProps) {
  const { account, csrfToken, onSessionExpired, onChanged } = props;

  const [revealed, setRevealed] = useState(false);
  const [secret, setSecret] = useState("");
  const [revealPending, setRevealPending] = useState(false);
  const [revealError, setRevealError] = useState<AuthApiError | null>(null);
  const [reverifyOpen, setReverifyOpen] = useState(false);

  const [actionPending, setActionPending] = useState(false);
  const [actionError, setActionError] = useState<AuthApiError | null>(null);
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

  function handleStop() {
    void runLifecycleAction(() => stopAccount(account.id, csrfToken));
  }

  function handleResume() {
    void runLifecycleAction(() => resumeAccount(account.id, csrfToken));
  }

  function handleRefreshHealth() {
    void runLifecycleAction(() => refreshHealth(account.id, csrfToken));
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

  return (
    <>
      <ProviderAccountRow
        identity={
          <div className="flex flex-col gap-1">
            <AccountIdentity email={account.identity.email} externalId={account.external_id} plan={account.identity.plan} />
            <SecretRevealControl
              masked="••••••••"
              secret={secret}
              revealed={revealed}
              blocked={!revealed}
              onRevealRequest={handleRevealRequest}
              onHide={handleHide}
              label="credential"
            />
            {revealError ? (
              <TypedErrorDisplay code={revealError.code} message={revealError.message} retryable={revealError.retryable} tone="critical" />
            ) : null}
          </div>
        }
        status={<AccountStatus status={account.display_status} />}
        quota={<QuotaSummary windows={account.quota} />}
        funding={
          <div className="flex items-center gap-2">
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
        }
        actions={
          // TODO(P2b-UI-003 follow-up): reauthentication of an existing
          // OAuth account (POST /accounts/{id}/reauth/begin, P2b-PROV-008)
          // is intentionally NOT wired into this menu yet. It is optional
          // for this task, and cleanly adding it here would need the same
          // begin -> open authorize_url -> poll status shape ConnectDialog
          // already implements for a fresh OAuth connect, just retargeted
          // at reauth/begin's endpoint for an oauth-authed account instead
          // of a "connect account" flow. Left out to keep this row's scope
          // to the account-lifecycle actions explicitly asked for
          // (stop/resume/refresh health/disconnect).
          <DropdownMenu
            trigger={<IconButton icon="ellipsis" label={`Actions for ${account.external_id}`} variant="ghost" size="sm" />}
            align="end"
            items={[
              { label: "Stop", icon: "pause", disabled: actionPending || account.connection_state !== "connected", onSelect: handleStop },
              { label: "Resume", icon: "play", disabled: actionPending || account.connection_state !== "stopped", onSelect: handleResume },
              {
                label: "Refresh health",
                icon: "refresh-cw",
                disabled: actionPending || account.connection_state === "disconnected",
                onSelect: handleRefreshHealth,
              },
              { type: "separator" },
              {
                label: "Disconnect",
                icon: "unplug",
                danger: true,
                disabled: actionPending || account.connection_state === "disconnected",
                onSelect: () => setDisconnectOpen(true),
              },
            ]}
          />
        }
      />

      {/* P3c-UI-001: the certification summary composes entirely from
       * offering-operation data (state/truth/probe-execution/review
       * reasons), none of which GET /accounts or AccountProjection carries
       * today — GET /offerings has no per-account mount point wired into
       * this page yet (FleetOverview's own "Models" StatCard is the same
       * honest "—" for the same reason). Wiring is complete and tested in
       * isolation (CertificationSummary.test.tsx); the moment a later unit
       * supplies this account's offering-operations, passing them here is
       * the only change needed — an empty array renders the same honest
       * "—" idiom QuotaSummary uses for its own empty case, never a
       * fabricated row. */}
      <CertificationSummary operations={[]} />

      {actionError ? (
        <TypedErrorDisplay code={actionError.code} message={actionError.message} retryable={actionError.retryable} tone="critical" />
      ) : null}

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
