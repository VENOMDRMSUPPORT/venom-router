import { Button, EmptyState, IconButton } from "@venom/design-system/primitives";
import { ProviderBadge } from "@venom/design-system/domain";
import { Icon } from "@venom/design-system/icons";
import type { AccountProjection, Provider } from "../api/controlClient";
import AccountRow from "./AccountRow";
import ProviderLogo from "./ProviderLogo";

export interface ProviderRowProps {
  provider: Provider;
  accounts: AccountProjection[];
  expanded: boolean;
  onToggleExpand: () => void;
  onConnect: () => void;
  csrfToken: string;
  onSessionExpired: () => void;
  onChanged: () => void;
}

/** One provider's row in the fleet: a provider-summary header composed
 * locally from the design system's building blocks (the frozen DS
 * ProviderSummaryCard hardcodes its letter Mark with no logo slot, so this
 * row assembles the same header — vn-panel/vn-fleet-* layout, ProviderBadge,
 * the setup-required banner — with ProviderLogo in the mark position: the
 * provider's official logo when one ships, the DS letter mark otherwise).
 * Integration facts + aggregate health are computed from the live
 * GET /accounts data (the catalog endpoint carries none); the "Setup
 * required" banner names missing env var NAMES only, values are never
 * shown. Expanded, it renders the provider's account rows. */
export default function ProviderRow(props: ProviderRowProps) {
  const { provider, accounts, expanded, onToggleExpand, onConnect, csrfToken, onSessionExpired, onChanged } = props;

  const healthyCount = accounts.filter((a) => a.display_status === "healthy").length;
  const missingEnv = provider.missing_env ?? [];

  return (
    <div className="vn-panel">
      <div className="vn-fleet-provider">
        <ProviderLogo slug={provider.id} name={provider.display_name} size="lg" />
        <span className="flex min-w-0 flex-1 flex-col" style={{ gap: 2 }}>
          <span className="flex items-center gap-2">
            <span className="vn-fleet-name">{provider.display_name}</span>
            <span className="vn-fleet-slug">{provider.id}</span>
            <ProviderBadge authMode={provider.auth_mode} />
          </span>
          <span className="vn-caption">
            {accounts.length === 0
              ? "No accounts connected"
              : `${healthyCount} healthy of ${accounts.length} account${accounts.length === 1 ? "" : "s"}`}
          </span>
        </span>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="secondary" icon="plus" onClick={onConnect} disabled={!provider.configured}>
            Connect account
          </Button>
          <IconButton
            icon={expanded ? "chevron-down" : "chevron-right"}
            label={expanded ? `Collapse ${provider.display_name} accounts` : `Expand ${provider.display_name} accounts`}
            variant="ghost"
            size="sm"
            onClick={onToggleExpand}
          />
        </div>
      </div>
      {!provider.configured ? (
        <div className="vn-banner" role="status" style={{ borderLeft: 0, borderRight: 0 }}>
          <Icon name="triangle-alert" size={15} />
          <span className="flex-1">
            Setup required — missing environment {missingEnv.length === 1 ? "variable" : "variables"}:{" "}
            {missingEnv.map((v, i) => (
              <span key={v}>
                {i > 0 ? " " : null}
                <span className="vn-code-inline">{v}</span>
              </span>
            ))}{" "}
            (names only, values are never shown).
          </span>
        </div>
      ) : null}
      {expanded ? (
        <div className="vn-fleet-accounts">
          {accounts.length === 0 ? (
            <EmptyState icon="user-round" title="No accounts connected" description="Connect an account to start routing through this provider." />
          ) : (
            <div className="flex flex-col gap-2">
              {accounts.map((account) => (
                <AccountRow
                  key={account.id}
                  account={account}
                  csrfToken={csrfToken}
                  onSessionExpired={onSessionExpired}
                  onChanged={onChanged}
                />
              ))}
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}
