import { useState } from "react";
import { Badge, IconButton } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import { TypedErrorDisplay } from "@venom/design-system/domain";
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

/** The aggregate health dot's tone + explanation for a provider row. */
function providerHealth(accounts: AccountProjection[]): { tone: string; title: string } {
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

  const name = providerDisplayName(provider);
  const meta = providerMeta(provider.id);
  const health = providerHealth(accounts);
  const healthyCount = accounts.filter((a) => a.display_status === "healthy").length;

  async function runRowAction(action: () => Promise<void>) {
    setBusy(true);
    setRowError(null);
    try {
      await action();
      onChanged();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setRowError(toApiError(err));
    } finally {
      setBusy(false);
    }
  }

  function handleSyncAll() {
    void runRowAction(async () => {
      await syncProvider(provider.id, csrfToken);
    });
  }

  /** Discovery for EVERY account of this provider, each job polled to its
   * terminal status before the refetch — a 202 is never reported as done. */
  function handleRefreshModels() {
    void runRowAction(async () => {
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
    });
  }

  return (
    <div className="vnd-provider-row">
      <div className="vnd-provider-row-main">
        <IconButton
          icon={expanded ? "chevron-down" : "chevron-right"}
          label={expanded ? `Collapse ${name} accounts` : `Expand ${name} accounts`}
          variant="ghost"
          size="sm"
          aria-expanded={expanded}
          onClick={onToggleExpand}
        />
        <ProviderLogo slug={provider.id} name={name} size="sm" />
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
              >
                <Icon name="external-link" size={13} />
              </a>
            ) : null}
          </div>
          <div className="vnd-provider-row-sub">
            <span>
              <span className="text-status-healthy-fg font-semibold">{uniqueModelCount == null ? "—" : uniqueModelCount}</span>{" "}
              unique models
            </span>
            <span aria-hidden="true">·</span>
            <span>
              <span className="text-status-healthy-fg font-semibold">{healthyCount}</span> / {accounts.length} account
              {accounts.length === 1 ? "" : "s"} healthy
            </span>
          </div>
        </div>
        <div className="vnd-provider-row-actions">
          <IconButton
            icon="zap"
            label="Sync all accounts"
            title="Sync all accounts"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={handleSyncAll}
          />
          <IconButton
            icon="refresh-cw"
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
