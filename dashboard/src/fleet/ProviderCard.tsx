import { Badge, Button, IconButton } from "@venom/design-system/primitives";
import type { AccountProjection, Provider } from "../api/controlClient";
import AccountRow from "./AccountRow";
import ProviderLogo from "./ProviderLogo";

export interface ProviderCardProps {
  provider: Provider;
  accounts: AccountProjection[];
  expanded: boolean;
  onToggleExpand: () => void;
  onConnect: () => void;
  csrfToken: string;
  onSessionExpired: () => void;
  onChanged: () => void;
}

/** The legacy integration-card auth badge strings: "API KEY" and
 * "OAUTH 2 · PKCE". `custom_openai` has no legacy card equivalent (the
 * legacy grid never rendered the custom path), so it gets an honest
 * OpenAI-compatible label instead of a wrong "API KEY". */
function authBadgeLabel(mode: Provider["auth_mode"]): string {
  if (mode === "oauth2") return "OAUTH 2 · PKCE";
  if (mode === "api_key") return "API KEY";
  return "OPENAI COMPATIBLE";
}

/**
 * One integration card in the Provider Fleet grid (legacy-parity layout):
 * logo tile top-left, auth badge top-right (plus CONNECTED when accounts
 * exist), bold name, muted description, the amber "Setup required.
 * Provide: <ENV VARS>." note when applicable, and a full-width bottom
 * action button — "Connect Integration" (enabled), "Setup required"
 * (disabled, warning-tinted) or "Integration unavailable" (disabled,
 * muted green; only the custom OpenAI-compatible path template, which has
 * no connect flow in this console).
 *
 * Connected accounts stay reachable: the chevron next to the badges
 * expands the account rows inside the card, and the expanded card spans
 * the full grid width so the account grid keeps its legacy column layout.
 */
export default function ProviderCard(props: ProviderCardProps) {
  const { provider, accounts, expanded, onToggleExpand, onConnect, csrfToken, onSessionExpired, onChanged } = props;

  const connected = accounts.length > 0;
  const setupRequired = !provider.configured;
  const connectable = provider.auth_mode === "api_key" || provider.auth_mode === "oauth2";
  const missingEnv = provider.missing_env ?? [];
  const showAccounts = expanded && connected;

  return (
    <div
      className={"vn-panel flex flex-col gap-4 p-5" + (showAccounts ? " md:col-span-2" : "")}
      style={connected ? { borderLeftWidth: 2, borderLeftColor: "var(--status-healthy-border)" } : undefined}
    >
      <div className="flex items-start justify-between gap-3">
        <ProviderLogo slug={provider.id} name={provider.display_name} size="lg" />
        <div className="flex items-start gap-1.5">
          <div className="flex flex-col items-end gap-1.5">
            {connected ? (
              <Badge tone="healthy" icon="circle-check" mono>
                CONNECTED
              </Badge>
            ) : null}
            <Badge tone="inactive" mono outline title={"auth_mode: " + provider.auth_mode}>
              {authBadgeLabel(provider.auth_mode)}
            </Badge>
          </div>
          {connected ? (
            <IconButton
              icon={expanded ? "chevron-down" : "chevron-right"}
              label={expanded ? `Collapse ${provider.display_name} accounts` : `Expand ${provider.display_name} accounts`}
              variant="ghost"
              size="sm"
              onClick={onToggleExpand}
            />
          ) : null}
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <h3 className="text-md font-semibold text-text-primary">{provider.display_name}</h3>
        {connected ? (
          <span className="text-xs font-medium text-status-healthy-fg">
            {accounts.length} account{accounts.length === 1 ? "" : "s"} linked
          </span>
        ) : null}
      </div>

      {provider.description ? <p className="vn-caption flex-1">{provider.description}</p> : null}

      {setupRequired && missingEnv.length > 0 ? (
        <p className="text-xs leading-relaxed text-status-warning-fg" role="note">
          Setup required. Provide:{" "}
          {missingEnv.map((v, i) => (
            <span key={v}>
              {i > 0 ? ", " : null}
              <span className="vn-code-inline">{v}</span>
            </span>
          ))}
          .
        </p>
      ) : null}

      {showAccounts ? (
        <div className="vn-fleet-accounts overflow-x-auto rounded-md border border-border-subtle">
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
      ) : null}

      {setupRequired ? (
        <Button variant="secondary" icon="plug" disabled className="w-full justify-center text-status-warning-fg">
          Setup required
        </Button>
      ) : !connectable ? (
        <Button variant="secondary" icon="plug" disabled className="w-full justify-center text-status-healthy-fg">
          Integration unavailable
        </Button>
      ) : (
        <Button variant="secondary" icon="plug" onClick={onConnect} className="w-full justify-center">
          Connect Integration
        </Button>
      )}
    </div>
  );
}
