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
 * One integration card in the Provider Fleet grid: a strong identity row
 * with a large brand mark, compact status metadata, muted description, the
 * amber "Setup required.
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
    <article
      className={[
        "vn-card vn-provider-card",
        connected ? "vn-provider-card--connected" : "",
        showAccounts ? "vn-provider-card--expanded" : "",
      ].filter(Boolean).join(" ")}
    >
      <div className="vn-provider-card-head">
        <div className="vn-provider-card-identity">
          <ProviderLogo slug={provider.id} name={provider.display_name} size="lg" />
          <div className="vn-provider-card-title">
            {/* Level 2, not 3: the shell's ChromeHeader owns the page's only
                h1, so a card title is the next level down and axe's
                heading-order rule (rightly) rejects a jump to h3.
                The ELEMENT stays <h3> because @venom/design-system styles this
                title through the element selector `.vn-provider-card-title h3`
                (css/components-core.css) — that package is frozen, so changing
                the tag here would silently drop the card title's typography.
                aria-level is what assistive tech and axe actually read, so this
                corrects the semantics with zero visual change. */}
            <h3 role="heading" aria-level={2}>{provider.display_name}</h3>
            {connected ? (
              <span className="vn-provider-card-linked">
                {accounts.length} account{accounts.length === 1 ? "" : "s"} linked
              </span>
            ) : <span className="vn-provider-card-linked vn-provider-card-linked--idle">Ready to configure</span>}
          </div>
        </div>
        <div className="vn-provider-card-meta">
          <div className="vn-provider-card-badges">
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

      {provider.description ? <p className="vn-provider-card-description">{provider.description}</p> : null}

      {setupRequired && missingEnv.length > 0 ? (
        <p className="vn-provider-card-setup" role="note">
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
        <div className="vn-fleet-accounts vn-provider-card-accounts">
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

      <div className="vn-provider-card-actions">
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
    </article>
  );
}
