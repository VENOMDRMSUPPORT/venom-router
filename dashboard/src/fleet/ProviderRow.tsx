import { Button, EmptyState, IconButton } from "@venom/design-system/primitives";
import { ProviderSummaryCard } from "@venom/design-system/domain";
import type { AccountProjection, Provider } from "../api/controlClient";
import AccountRow from "./AccountRow";

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

/** One provider's row in the fleet: the ProviderSummaryCard header
 * (integration facts + aggregate health, both computed from the live
 * GET /accounts data since the catalog endpoint itself carries none) —
 * ProviderSummaryCard itself already renders the "Setup required" banner
 * naming missing env var NAMES only (setupRequired/missingEnv props), so
 * this component does not duplicate it — and, expanded, the provider's
 * ProviderAccountRows. */
export default function ProviderRow(props: ProviderRowProps) {
  const { provider, accounts, expanded, onToggleExpand, onConnect, csrfToken, onSessionExpired, onChanged } = props;

  const healthyCount = accounts.filter((a) => a.display_status === "healthy").length;

  return (
    <ProviderSummaryCard
      name={provider.display_name}
      slug={provider.id}
      authMode={provider.auth_mode}
      accountCount={accounts.length}
      healthyCount={healthyCount}
      setupRequired={!provider.configured}
      missingEnv={provider.missing_env}
      actions={
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
      }
    >
      {expanded ? (
        accounts.length === 0 ? (
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
        )
      ) : null}
    </ProviderSummaryCard>
  );
}
