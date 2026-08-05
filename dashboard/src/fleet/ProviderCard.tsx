import { Badge, Button } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import type { Provider } from "../api/controlClient";
import ProviderLogo from "./ProviderLogo";
import { cardBadgeLabel, providerDescription, providerDisplayName, providerMeta } from "./providerMeta";

export interface ProviderCardProps {
  provider: Provider;
  /** Connected accounts under this provider — the card shows the linked
   * count; account MANAGEMENT lives in the Active Providers view. */
  accountCount: number;
  onConnect: () => void;
}

/**
 * One catalog card in the All Integrations grid (image 4/5/6): logo tile
 * top-left; top-right stack of the green CONNECTED pill (only when
 * connected) over the long-form mono auth badge; name + official-site
 * link; muted site-domain line; the green "{n} account(s) linked" line
 * when connected; per-slug marketing description; and the full-width
 * bottom action — disabled healthy "Connected" when connected, the
 * per-slug CTA ("Login with ChatGPT" for codex) or "Connect Integration"
 * otherwise, with the amber "Setup required. Provide: ENV" note + disabled
 * button for unconfigured providers and "Integration unavailable" for the
 * custom OpenAI-compatible path (no connect flow in this console).
 *
 * There is deliberately NO account disclosure here — accounts are managed
 * from the Active Providers view's rows.
 */
export default function ProviderCard(props: ProviderCardProps) {
  const { provider, accountCount, onConnect } = props;

  const connected = accountCount > 0;
  const setupRequired = !provider.configured;
  const connectable = provider.auth_mode === "api_key" || provider.auth_mode === "oauth2";
  const missingEnv = provider.missing_env ?? [];
  const name = providerDisplayName(provider);
  const meta = providerMeta(provider.id);
  const description = providerDescription(provider);

  return (
    <article
      className={[
        "vn-card vn-provider-card",
        connected ? "vn-provider-card--connected" : "",
      ].filter(Boolean).join(" ")}
    >
      <div className="vn-provider-card-head">
        <div className="vn-provider-card-identity">
          <ProviderLogo slug={provider.id} name={name} size="md" />
          <div className="vn-provider-card-title">
            <div className="vnd-card-name-row">
              <h3 role="heading" aria-level={2}>{name}</h3>
              {meta ? (
                <a
                  className="vnd-icon-link"
                  href={meta.siteUrl}
                  target="_blank"
                  rel="noreferrer noopener"
                  aria-label={`Open ${meta.siteLabel} in a new tab`}
                  title={`Open ${meta.siteLabel} in a new tab`}
                >
                  <Icon name="external-link" size={12} />
                </a>
              ) : null}
            </div>
            {meta ? <span className="vnd-card-domain">{meta.siteLabel}</span> : null}
            <div className="vn-provider-card-status-row">
              {connected ? (
                <span className="vn-provider-card-linked">
                  <span className="vnd-status-dot vnd-status-dot--healthy" />
                  {accountCount} account{accountCount === 1 ? "" : "s"} linked
                </span>
              ) : (
                <span className="vn-provider-card-linked vn-provider-card-linked--idle">
                  <span className="vnd-status-dot vnd-status-dot--idle" />
                  No connections
                </span>
              )}
            </div>
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
              {cardBadgeLabel(provider)}
            </Badge>
          </div>
        </div>
      </div>

      {description ? <p className="vn-provider-card-description">{description}</p> : null}

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

      <div className="vn-provider-card-actions">
        {connected ? (
          <Button variant="secondary" icon="circle-check" disabled className="w-full justify-center text-status-healthy-fg">
            Connected
          </Button>
        ) : setupRequired ? (
          <Button variant="secondary" icon="plug" disabled className="w-full justify-center text-status-warning-fg">
            Setup required
          </Button>
        ) : !connectable ? (
          <Button variant="secondary" icon="plug" disabled className="w-full justify-center text-status-healthy-fg">
            Integration unavailable
          </Button>
        ) : (
          <Button variant="secondary" icon="plug" onClick={onConnect} className="w-full justify-center">
            {meta?.cta ?? "Connect Integration"}
          </Button>
        )}
      </div>
    </article>
  );
}
