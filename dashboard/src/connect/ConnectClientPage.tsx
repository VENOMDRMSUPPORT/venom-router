import { useCallback, useEffect, useState } from "react";
import {
  Badge,
  Card,
  CodeBlock,
  CopyButton,
  SegmentedControl,
  Switch,
} from "@venom/design-system/primitives";
import {
  getFullSettings,
  isSessionExpired,
  listApiKeys,
  toApiError,
  type ApiKeySummary,
  type AuthApiError,
  type EffectiveConfig,
} from "../api/controlClient";
import {
  CLIENT_TARGETS,
  dataPlaneBaseUrl,
  generateForTarget,
  KEY_PLACEHOLDER,
  TIER_MODEL_IDS,
  type ClientTarget,
} from "./generators";

export interface ConnectClientPageProps {
  csrfToken: string;
  onSessionExpired: () => void;
}

/**
 * The Connect-a-client page (P6-UI-011, docs/06 P6, docs/08 §8).
 *
 * ─── THE KEY IS NEVER WRITTEN INTO GENERATED OUTPUT BY DEFAULT ──────────────
 *
 * A Venom key is shown exactly ONCE, at creation (09 §3.11), and the server keeps
 * only a hash — so this page cannot retrieve an existing key's secret even if it
 * wanted to. Generated config therefore carries a PLACEHOLDER unless the owner both
 * pastes a key here and explicitly ticks "include the key".
 *
 * That opt-in is not ceremony. Copy-paste config lands in dotfiles, chat messages
 * and screenshots, and a key baked into it is a secret the owner did not knowingly
 * publish. Even when included, the key lives only in this component's state for the
 * session: it is never written to localStorage, sessionStorage, a URL, a log, or a
 * file, and it is cleared on unmount.
 *
 * The base URL is derived from the settings `effective_config` rather than
 * hardcoded, so an owner who moved the port is given THEIR port (01 §6b).
 */
export default function ConnectClientPage(props: ConnectClientPageProps) {
  const { onSessionExpired } = props;

  const [effective, setEffective] = useState<EffectiveConfig | null>(null);
  const [settingsError, setSettingsError] = useState<AuthApiError | null>(null);
  const [keys, setKeys] = useState<ApiKeySummary[] | null>(null);
  const [targetID, setTargetID] = useState<string>(CLIENT_TARGETS[0].id);
  // The pasted key. In memory only, for this session — see the component comment.
  const [pastedKey, setPastedKey] = useState("");
  const [includeKey, setIncludeKey] = useState(false);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const settings = await getFullSettings();
        if (!cancelled) setEffective(settings.effective_config);
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        // Non-fatal: the generators fall back to the DOCUMENTED default bind, and
        // the page says so rather than pretending it read the real one.
        setSettingsError(toApiError(err));
      }
      try {
        const list = await listApiKeys();
        if (!cancelled) setKeys(list);
      } catch {
        // Also non-fatal — the key list is a convenience, not a prerequisite.
        if (!cancelled) setKeys([]);
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [onSessionExpired]);

  // Clear the key on unmount, so it does not outlive the page in memory.
  useEffect(() => {
    return () => {
      setPastedKey("");
      setIncludeKey(false);
    };
  }, []);

  const baseUrl = dataPlaneBaseUrl(effective);
  const target: ClientTarget = CLIENT_TARGETS.find((t) => t.id === targetID) ?? CLIENT_TARGETS[0];
  // The key reaches a generator ONLY when both conditions hold.
  const apiKeyForOutput = includeKey && pastedKey.trim() !== "" ? pastedKey.trim() : null;
  const generated = generateForTarget(target, { baseUrl, apiKey: apiKeyForOutput });

  const handleIncludeKey = useCallback((next: boolean) => {
    setIncludeKey(next);
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-col gap-2">
          <h2 className="vn-h2">Connect a client</h2>
          <span className="vn-caption">
            Point any OpenAI- or Anthropic-compatible client at this router and address a tier by
            its model id.
          </span>
        </div>
      </Card>

      {/* Quick Start */}
      <Card data-testid="quick-start">
        <div className="flex flex-col gap-3">
          <h3 className="vn-h3">Quick start</h3>
          <ol className="flex flex-col gap-2">
            <li className="flex flex-wrap items-center gap-2">
              <Badge tone="accent" mono icon="hash">
                1
              </Badge>
              <span>
                Create an API key on the API Keys page. The raw key is shown once — copy it then.
              </span>
              {keys === null ? null : (
                <Badge tone={keys.length > 0 ? "healthy" : "unknown"} icon="key-round">
                  {keys.length === 0
                    ? "no keys yet"
                    : `${keys.length} key${keys.length === 1 ? "" : "s"} exist`}
                </Badge>
              )}
            </li>
            <li className="flex flex-wrap items-center gap-2">
              <Badge tone="accent" mono icon="hash">
                2
              </Badge>
              <span>Connect at least one provider account, so a tier has something to route to.</span>
            </li>
            <li className="flex flex-wrap items-center gap-2">
              <Badge tone="accent" mono icon="hash">
                3
              </Badge>
              <span>Point your client at</span>
              <span className="vn-mono-sm" data-testid="quick-start-base-url">
                {baseUrl}
              </span>
              <CopyButton value={baseUrl} label="Copy the base URL" />
            </li>
            <li className="flex flex-wrap items-center gap-2">
              <Badge tone="accent" mono icon="hash">
                4
              </Badge>
              <span>Send a request, then watch it on the Diagnostics page.</span>
            </li>
          </ol>

          {settingsError ? (
            <span className="vn-caption" data-testid="base-url-fallback-note">
              Could not read the configured bind ({settingsError.code}), so the documented default is
              shown. Check the address against your own configuration before relying on it.
            </span>
          ) : null}

          <div className="flex flex-wrap items-center gap-2">
            <span className="vn-caption">Tier model ids:</span>
            {TIER_MODEL_IDS.map((id) => (
              <Badge key={id} tone="info" mono icon="box">
                {id}
              </Badge>
            ))}
          </div>
        </div>
      </Card>

      {/* Client-setup catalog */}
      <Card data-testid="client-catalog">
        <div className="flex flex-col gap-3">
          <h3 className="vn-h3">Client setup</h3>

          <SegmentedControl
            label="Client"
            value={targetID}
            options={CLIENT_TARGETS.map((t) => ({ value: t.id, label: t.label }))}
            onChange={(value) => setTargetID(value)}
          />

          <span className="vn-caption" data-testid="target-note">
            {target.note}
          </span>

          <div className="flex flex-col gap-2">
            <label className="vn-caption" htmlFor="connect-key-input">
              Your API key (optional — held in memory for this session only, never stored)
            </label>
            <input
              id="connect-key-input"
              className="vn-input"
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={pastedKey}
              placeholder={KEY_PLACEHOLDER}
              onChange={(e) => setPastedKey(e.target.value)}
              data-testid="connect-key-input"
            />
            <Switch
              label="Include the key in the generated config"
              checked={includeKey}
              disabled={pastedKey.trim() === ""}
              onChange={(e) => handleIncludeKey(e.target.checked)}
              data-testid="connect-include-key"
            />
            <span className="vn-caption">
              Off by default. Generated text carries a placeholder instead, because copy-paste config
              ends up in dotfiles, chat messages and screenshots.
            </span>
          </div>

          <div className="flex flex-col gap-2" data-testid="generated-config">
            <CodeBlock label={`${target.label} — ${generated.language}`} code={generated.text} />
            <div>
              <CopyButton value={generated.text} label="Copy the config" />
            </div>
          </div>
        </div>
      </Card>
    </div>
  );
}
