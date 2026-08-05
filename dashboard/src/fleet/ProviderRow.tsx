import { useState } from "react";
import { toast } from "@venom/design-system";
import { Badge, IconButton } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import { TypedErrorDisplay } from "@venom/design-system/domain";
import { countsTowardFleet } from "./accountScope";
import {
  AuthApiError,
  isSessionExpired,
  startDiscovery,
  syncProvider,
  toApiError,
  type AccountProjection,
  type Provider,
} from "../api/controlClient";
import AccountRow from "./AccountRow";
import ProviderLogo from "./ProviderLogo";
import { pollJobToTerminal } from "./jobs";
import { providerDisplayName, providerMeta, rowBadgeLabel } from "./providerMeta";

export interface ProviderRowProps {
  provider: Provider;
  /** This provider's connected accounts — a row only renders with >= 1. */
  accounts: AccountProjection[];
  /** Distinct provider_model_id count across this provider's accounts, or
   * null while the offerings read is loading/failed (rendered "—"). */
  uniqueModelCount: number | null;
  /** Verified-working distinct model count (certification truth = supported),
   * or null while unknown. Shown as "{working} working / {total} live". */
  workingModelCount: number | null;
  /** Per-account distinct model counts keyed by account id (null = unknown). */
  accountModelCounts: (accountId: string) => number | null;
  expanded: boolean;
  onToggleExpand: () => void;
  /** Opens the connect dialog for THIS provider (the "+ Add account"
   * action — both auth modes; this is how a 2nd/3rd account is added). */
  onAddAccount: () => void;
  onOpenModelReport: (account: AccountProjection) => void;
  csrfToken: string;
  onSessionExpired: () => void;
  onChanged: () => void;
}

/** The aggregate health dot's tone + explanation for a provider row. Scoped to
 * the COUNTED accounts (countsTowardFleet), so an account the owner disabled
 * cannot hold the whole provider at "warning" forever. */
function providerHealth(allAccounts: AccountProjection[]): { tone: string; title: string } {
  const accounts = allAccounts.filter(countsTowardFleet);
  const healthy = accounts.filter((a) => a.display_status === "healthy").length;
  const title = `${healthy}/${accounts.length} account${accounts.length === 1 ? "" : "s"} healthy`;
  if (healthy === accounts.length && accounts.length > 0) return { tone: "healthy", title };
  if (healthy > 0) return { tone: "warning", title };
  return { tone: "critical", title };
}

/**
 * One Active Providers row (image 1/2): chevron disclosure, logo, aggregate
 * health dot, name, short mono auth badge, official-site link, the "{N}
 * unique models · {X}/{Y} accounts healthy" line, and the row action
 * cluster — Sync all accounts (POST /providers/{id}/sync), Refresh models
 * for every account (per-account discovery jobs polled to terminal), and
 * Add account. Expanding reveals the numbered account rows.
 */
export default function ProviderRow(props: ProviderRowProps) {
  const {
    provider,
    accounts,
    uniqueModelCount,
    workingModelCount,
    accountModelCounts,
    expanded,
    onToggleExpand,
    onAddAccount,
    onOpenModelReport,
    csrfToken,
    onSessionExpired,
    onChanged,
  } = props;

  const [busy, setBusy] = useState(false);
  const [rowError, setRowError] = useState<AuthApiError | null>(null);
  const [syncAllState, setSyncAllState] = useState<"idle" | "loading" | "success" | "failure">("idle");
  const [refreshModelsState, setRefreshModelsState] = useState<"idle" | "loading" | "success" | "failure">("idle");

  const name = providerDisplayName(provider);
  const meta = providerMeta(provider.id);
  const health = providerHealth(accounts);
  // Header counters use the COUNTED scope; the row list below still renders
  // every listed account, disabled ones included, so there is always a way
  // back to enabling them.
  const countedAccounts = accounts.filter(countsTowardFleet);
  const healthyCount = countedAccounts.filter((a) => a.display_status === "healthy").length;
  const requireActionCount = countedAccounts.length - healthyCount;
  const connectedCount = accounts.filter((a) => a.connection_state === "connected").length;

  async function runRowAction(action: () => Promise<void>): Promise<boolean> {
    setBusy(true);
    setRowError(null);
    try {
      await action();
      onChanged();
      return true;
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return false;
      }
      setRowError(toApiError(err));
      return false;
    } finally {
      setBusy(false);
    }
  }

  function handleSyncAll() {
    setSyncAllState("loading");
    void runRowAction(async () => {
      try {
        await syncProvider(provider.id, csrfToken);
        toast.success("Provider configuration synced");
      } catch (err) {
        toast.danger("Provider sync failed", {
          detail: err instanceof Error ? err.message : String(err),
        });
        throw err;
      }
    }).then((success) => {
      setSyncAllState(success ? "success" : "failure");
      setTimeout(() => setSyncAllState("idle"), 2000);
    });
  }

  /** Discovery for EVERY account of this provider, each job polled to its
   * terminal status before the refetch — a 202 is never reported as done. */
  function handleRefreshModels() {
    setRefreshModelsState("loading");
    void runRowAction(async () => {
      try {
        for (const account of accounts) {
          const handle = await startDiscovery(account.id, csrfToken);
          const job = await pollJobToTerminal(handle.job_id);
          if (job.status !== "completed") {
            throw new AuthApiError(0, {
              code: job.error?.code ?? `job_${job.status}`,
              message: job.error?.message ?? `A discovery job is ${job.status}.`,
              request_id: "",
              retryable: true,
            });
          }
        }
        toast.success("Discovery completed", {
          detail: `Discovered models for ${provider.id}`,
        });
      } catch (err) {
        toast.danger("Discovery failed", {
          detail: err instanceof Error ? err.message : String(err),
        });
        throw err;
      }
    }).then((success) => {
      setRefreshModelsState(success ? "success" : "failure");
      setTimeout(() => setRefreshModelsState("idle"), 2000);
    });
  }

  function handleHeaderClick() {
    const selection = window.getSelection();
    if (selection && selection.toString().trim().length > 0) {
      return;
    }
    onToggleExpand();
  }

  return (
    <div className={`vnd-provider-row${expanded ? " vnd-provider-row--expanded" : ""}`}>
      <div className="vnd-provider-row-main" onClick={handleHeaderClick}>
        <IconButton
          icon={expanded ? "chevron-down" : "chevron-right"}
          label={expanded ? `Collapse ${name} accounts` : `Expand ${name} accounts`}
          variant="ghost"
          size="sm"
          className="vnd-expand-btn"
          aria-expanded={expanded}
          onClick={(e) => {
            e.stopPropagation();
            onToggleExpand();
          }}
        />
        <ProviderLogo slug={provider.id} name={name} size="md" />
        <div className="vnd-provider-row-body">
          <div className="vnd-provider-row-title">
            <span className={`vnd-health-dot vnd-health-dot--${health.tone}`} title={health.title} />
            <span className="vnd-provider-row-name">{name}</span>
            <Badge tone={provider.auth_mode === "api_key" ? "healthy" : "inactive"} mono outline title={`auth_mode: ${provider.auth_mode}`}>
              {rowBadgeLabel(provider.auth_mode)}
            </Badge>
            {meta ? (
              <a
                className="vnd-icon-link"
                href={meta.siteUrl}
                target="_blank"
                rel="noreferrer noopener"
                aria-label={`Open ${meta.siteLabel} in a new tab`}
                title={`Open ${meta.siteLabel} in a new tab`}
                onClick={(e) => e.stopPropagation()}
              >
                <Icon name="external-link" size={13} />
              </a>
            ) : null}
          </div>
          <div className="vnd-provider-row-sub">
            <span className="vnd-metric-chip vnd-metric-chip--healthy" title={`${workingModelCount ?? 0} out of ${uniqueModelCount ?? 0} models verified working`}>
              <span className="vnd-metric-dot vnd-metric-dot--healthy" aria-hidden="true" />
              {uniqueModelCount == null ? "—" : uniqueModelCount} models
            </span>
            <span aria-hidden="true" className="vnd-metric-sep">·</span>
            <span className="vnd-metric-chip vnd-metric-chip--info" title={`${connectedCount} out of ${accounts.length} accounts connected`}>
              <span className="vnd-metric-dot vnd-metric-dot--info" aria-hidden="true" />
              {connectedCount} accounts
            </span>
            <span aria-hidden="true" className="vnd-metric-sep">·</span>
            <span className={`vnd-metric-chip ${requireActionCount > 0 ? "vnd-metric-chip--warning" : "vnd-metric-chip--healthy"}`} title={`${requireActionCount} accounts require owner action`}>
              <span className={`vnd-metric-dot ${requireActionCount > 0 ? "vnd-metric-dot--warning" : "vnd-metric-dot--healthy"}`} aria-hidden="true" />
              {requireActionCount} require action
            </span>
          </div>
        </div>
        <div className="vnd-provider-row-actions" onClick={(e) => e.stopPropagation()}>
          <IconButton
            icon={
              syncAllState === "loading"
                ? "loader-circle"
                : syncAllState === "success"
                  ? "check"
                  : syncAllState === "failure"
                    ? "x"
                    : "zap"
            }
            className={
              syncAllState === "loading"
                ? "vnd-spinner"
                : syncAllState === "success"
                  ? "vnd-btn--success"
                  : syncAllState === "failure"
                    ? "vnd-btn--failure"
                    : ""
            }
            label="Sync all accounts"
            title="Sync all accounts"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={handleSyncAll}
          />
          <IconButton
            icon={
              refreshModelsState === "loading"
                ? "loader-circle"
                : refreshModelsState === "success"
                  ? "check"
                  : refreshModelsState === "failure"
                    ? "x"
                    : "refresh-cw"
            }
            className={
              refreshModelsState === "loading"
                ? "vnd-spinner"
                : refreshModelsState === "success"
                  ? "vnd-btn--success"
                  : refreshModelsState === "failure"
                    ? "vnd-btn--failure"
                    : ""
            }
            label="Refresh models for every account"
            title="Refresh models for every account"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={handleRefreshModels}
          />
          <IconButton
            icon="plus"
            label="Add account"
            title="Add account"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={onAddAccount}
          />
        </div>
      </div>

      {rowError ? (
        <TypedErrorDisplay code={rowError.code} message={rowError.message} retryable={rowError.retryable} tone="critical" />
      ) : null}

      {expanded ? (
        <div className="vnd-provider-row-accounts">
          {accounts.map((account, i) => (
            <AccountRow
              key={account.id}
              account={account}
              index={i + 1}
              modelCount={accountModelCounts(account.id)}
              csrfToken={csrfToken}
              onSessionExpired={onSessionExpired}
              onChanged={onChanged}
              onOpenModelReport={() => onOpenModelReport(account)}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}
