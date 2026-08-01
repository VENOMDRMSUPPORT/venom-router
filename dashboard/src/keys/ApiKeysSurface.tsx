import { useCallback, useEffect, useState } from "react";
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorState,
  FormField,
  Input,
  Spinner,
} from "@venom/design-system/primitives";
import {
  APIKeyCreationResult,
  APIKeyPrefix,
  DestructiveActionConfirmation,
} from "@venom/design-system/domain";
import {
  createApiKey,
  deleteApiKey,
  isSessionExpired,
  listApiKeys,
  toApiError,
  type ApiKeySummary,
  type AuthApiError,
} from "../api/controlClient";

export interface ApiKeysSurfaceProps {
  csrfToken: string;
  onSessionExpired: () => void;
}

function formatEpoch(seconds: number | null): string | null {
  if (seconds == null) return null;
  return new Date(seconds * 1000).toLocaleString();
}

/**
 * The API Keys surface (P6-UI-009, 09 §3.11, 07 §5a).
 *
 * SECRET HANDLING is the whole point of this surface, so it is spelled out:
 *
 *   - The raw key exists in exactly ONE place — the `created` state below —
 *     and only between the create response and the owner dismissing the
 *     reveal. `handleDismissCreated` sets it back to null, which unmounts
 *     APIKeyCreationResult and removes the value from the DOM entirely.
 *   - It is NEVER written to localStorage, sessionStorage, a URL, a log, or
 *     any state that outlives the reveal. The list is refreshed from the
 *     server after a create, so the new key appears there through its
 *     non-secret projection (prefix only) rather than by reusing anything
 *     from the create response.
 *   - The list model (ApiKeySummary) has no `raw_key` field at all, so a
 *     stray one in a response cannot reach the DOM by structural accident.
 */
export default function ApiKeysSurface(props: ApiKeysSurfaceProps) {
  const { csrfToken, onSessionExpired } = props;

  const [keys, setKeys] = useState<ApiKeySummary[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [formError, setFormError] = useState<AuthApiError | null>(null);
  const [label, setLabel] = useState("");
  const [rpmLimit, setRpmLimit] = useState("");
  const [creating, setCreating] = useState(false);
  // The ONE-TIME raw key. Null except between create and dismiss.
  const [created, setCreated] = useState<{ label: string; rawKey: string } | null>(null);
  const [pendingRevoke, setPendingRevoke] = useState<ApiKeySummary | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setLoadError(null);
      try {
        const list = await listApiKeys();
        if (cancelled) return;
        setKeys(list);
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setLoadError(toApiError(err));
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [reloadToken, onSessionExpired]);

  const handleCreate = useCallback(async () => {
    setCreating(true);
    setFormError(null);
    try {
      const parsedRpm = rpmLimit.trim() === "" ? undefined : Number(rpmLimit);
      const result = await createApiKey(
        {
          label,
          // An empty field means "no limit" — it is omitted, never sent as 0.
          ...(parsedRpm !== undefined && Number.isFinite(parsedRpm) ? { rpm_limit: parsedRpm } : {}),
        },
        csrfToken,
      );
      setCreated({ label: result.label, rawKey: result.raw_key });
      setLabel("");
      setRpmLimit("");
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setFormError(toApiError(err));
    } finally {
      setCreating(false);
    }
  }, [label, rpmLimit, csrfToken, onSessionExpired]);

  /** Clears the one-time raw key and refreshes the list from the server. */
  const handleDismissCreated = useCallback(() => {
    setCreated(null);
    setReloadToken((t) => t + 1);
  }, []);

  const handleConfirmRevoke = useCallback(async () => {
    const target = pendingRevoke;
    setPendingRevoke(null);
    if (!target) return;
    try {
      await deleteApiKey(target.id, csrfToken);
      setReloadToken((t) => t + 1);
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setLoadError(toApiError(err));
    }
  }, [pendingRevoke, csrfToken, onSessionExpired]);

  const createForm = (
    <Card>
      <div className="flex flex-col gap-3">
        <h3 className="vn-h3">Create an API key</h3>
        <FormField label="Label" required error={formError ? formError.message : undefined}>
          <Input value={label} onChange={(e) => setLabel(e.target.value)} />
        </FormField>
        <FormField
          label="Requests per minute"
          description="Leave empty for no limit. Never sent as 0."
        >
          <Input
            inputMode="numeric"
            value={rpmLimit}
            onChange={(e) => setRpmLimit(e.target.value)}
          />
        </FormField>
        {formError ? (
          <span className="vn-reason-code" title="Typed error code from the control API">
            {formError.code}
          </span>
        ) : null}
        <div>
          <Button variant="primary" onClick={handleCreate} disabled={creating}>
            Create key
          </Button>
        </div>
      </div>
    </Card>
  );

  return (
    <div className="flex flex-col gap-4">
      {created ? (
        <APIKeyCreationResult
          rawKey={created.rawKey}
          keyLabel={created.label}
          onDone={handleDismissCreated}
        />
      ) : (
        createForm
      )}

      {loadError ? (
        <ErrorState
          code={loadError.code}
          title="Could not load API keys"
          description={loadError.message}
        />
      ) : keys === null ? (
        <Spinner label="Loading API keys…" />
      ) : keys.length === 0 ? (
        <EmptyState
          icon="key-round"
          title="No API keys"
          description="Create a key above to point a client at this router. The raw key is shown once, at creation."
        />
      ) : (
        <div className="flex flex-col gap-3">
          {keys.map((k) => {
            const lastUsed = formatEpoch(k.last_used_at);
            const revoked = formatEpoch(k.revoked_at);
            return (
              <Card key={k.id} data-testid={`api-key-${k.id}`}>
                <div className="flex flex-col gap-2">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="vn-title-sub">{k.label}</span>
                      {/* The ONLY persistent representation of a key. */}
                      <APIKeyPrefix prefix={k.key_prefix} />
                      {revoked ? (
                        <Badge tone="inactive" icon="ban">
                          Revoked
                        </Badge>
                      ) : null}
                    </div>
                    {revoked ? null : (
                      <Button variant="danger" onClick={() => setPendingRevoke(k)}>
                        Revoke
                      </Button>
                    )}
                  </div>
                  <div className="flex flex-wrap gap-4">
                    <span className="vn-caption" data-testid={`api-key-rpm-${k.id}`}>
                      {/* An absent limit is "no limit", never 0 — 0 would read
                          as a key that can make no requests at all. */}
                      {k.rpm_limit == null ? "Rate limit: no limit" : `Rate limit: ${k.rpm_limit} rpm`}
                    </span>
                    <span className="vn-caption">Created: {formatEpoch(k.created_at)}</span>
                    <span className="vn-caption">
                      {lastUsed ? `Last used: ${lastUsed}` : "Last used: never used"}
                    </span>
                    {revoked ? <span className="vn-caption">Revoked: {revoked}</span> : null}
                  </div>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      <DestructiveActionConfirmation
        open={pendingRevoke !== null}
        title="Revoke this API key?"
        consequence={
          pendingRevoke
            ? `"${pendingRevoke.label}" (${pendingRevoke.key_prefix}) will stop authenticating immediately. This cannot be undone.`
            : ""
        }
        confirmLabel="Revoke key"
        onConfirm={handleConfirmRevoke}
        onCancel={() => setPendingRevoke(null)}
      />
    </div>
  );
}
