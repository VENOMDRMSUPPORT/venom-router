import { useCallback, useEffect, useMemo, useState, type HTMLAttributes } from "react";
import {
  AdaptiveSheet,
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorState,
  FormField,
  Input,
  ResponsiveCollection,
  Spinner,
  type DataTableColumn,
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
  createOpen?: boolean;
  onCreateOpenChange?: (open: boolean) => void;
}

function formatEpoch(seconds: number | null): string | null {
  if (seconds == null) return null;
  return new Date(seconds * 1000).toLocaleString();
}

function formatRateLimit(value: number | null): string {
  return value == null ? "Router default" : `${value} rpm`;
}

function noop() {}

/**
 * Responsive API-key inventory. The raw key exists only in `created` while
 * the adaptive creation sheet is open; closing, dismissing, or navigating
 * away unmounts the reveal and clears that state.
 */
export default function ApiKeysSurface(props: ApiKeysSurfaceProps) {
  const {
    csrfToken,
    onSessionExpired,
    createOpen = false,
    onCreateOpenChange = noop,
  } = props;

  const [keys, setKeys] = useState<ApiKeySummary[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [formError, setFormError] = useState<AuthApiError | null>(null);
  const [label, setLabel] = useState("");
  const [rpmLimit, setRpmLimit] = useState("");
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<{ label: string; rawKey: string } | null>(null);
  const [pendingRevoke, setPendingRevoke] = useState<ApiKeySummary | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      setLoadError(null);
      try {
        const list = await listApiKeys();
        if (!cancelled) setKeys(list);
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
    return () => { cancelled = true; };
  }, [reloadToken, onSessionExpired]);

  useEffect(() => {
    if (createOpen) return;
    setCreated(null);
    setLabel("");
    setRpmLimit("");
    setFormError(null);
    setCreating(false);
  }, [createOpen]);

  const trimmedLabel = label.trim();
  const rpmText = rpmLimit.trim();
  const parsedRpm = rpmText === "" ? undefined : Number(rpmText);
  const rpmIsValid = parsedRpm === undefined || (Number.isInteger(parsedRpm) && parsedRpm > 0);
  const canCreate = trimmedLabel.length > 0 && rpmIsValid && !creating;

  const closeCreate = useCallback(() => {
    const refresh = created !== null;
    setCreated(null);
    setLabel("");
    setRpmLimit("");
    setFormError(null);
    onCreateOpenChange(false);
    if (refresh) setReloadToken((token) => token + 1);
  }, [created, onCreateOpenChange]);

  const handleCreate = useCallback(async () => {
    if (!canCreate) return;
    setCreating(true);
    setFormError(null);
    try {
      const result = await createApiKey(
        {
          label: trimmedLabel,
          ...(parsedRpm !== undefined ? { rpm_limit: parsedRpm } : {}),
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
  }, [canCreate, csrfToken, onSessionExpired, parsedRpm, trimmedLabel]);

  const handleConfirmRevoke = useCallback(async () => {
    const target = pendingRevoke;
    setPendingRevoke(null);
    if (!target) return;
    try {
      await deleteApiKey(target.id, csrfToken);
      setReloadToken((token) => token + 1);
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setLoadError(toApiError(err));
    }
  }, [pendingRevoke, csrfToken, onSessionExpired]);

  const columns = useMemo<DataTableColumn<ApiKeySummary>[]>(() => [
    { key: "index", label: "#", width: 52, numeric: true, render: (_key, index) => index + 1 },
    { key: "label", label: "Label", render: (key) => <span className="vn-title-sub">{key.label}</span> },
    { key: "key_prefix", label: "Key prefix", mono: true, render: (key) => <APIKeyPrefix prefix={key.key_prefix} /> },
    {
      key: "status",
      label: "Status",
      render: (key) => key.revoked_at == null
        ? <Badge tone="healthy" icon="check-circle">Active</Badge>
        : <Badge tone="inactive" icon="ban">Revoked</Badge>,
    },
    {
      key: "rpm_limit",
      label: "Rate limit",
      render: (key) => <span data-testid={`api-key-rpm-${key.id}`}>{formatRateLimit(key.rpm_limit)}</span>,
    },
    { key: "created_at", label: "Created", render: (key) => formatEpoch(key.created_at) },
    { key: "last_used_at", label: "Last used", render: (key) => formatEpoch(key.last_used_at) ?? "Never used" },
    {
      key: "actions",
      label: "Actions",
      render: (key) => key.revoked_at == null ? (
        <Button variant="danger" size="sm" onClick={() => setPendingRevoke(key)}>Revoke</Button>
      ) : <span className="vn-caption">No actions</span>,
    },
  ], []);

  function keyCard(key: ApiKeySummary, index: number) {
    const revokedAt = formatEpoch(key.revoked_at);
    return (
      <Card data-testid={`api-key-${key.id}`}>
        <div className="vn-api-key-card">
          <div className="vn-api-key-card-head">
            <div className="vn-api-key-card-identity">
              <span className="vn-api-key-card-index">#{index + 1}</span>
              <div className="min-w-0">
              <div className="vn-title-sub truncate">{key.label}</div>
              <APIKeyPrefix prefix={key.key_prefix} />
              </div>
            </div>
            {revokedAt ? <Badge tone="inactive" icon="ban">Revoked</Badge> : <Badge tone="healthy" icon="check-circle">Active</Badge>}
          </div>
          <dl className="vn-api-key-card-grid">
            <div><dt>Rate limit</dt><dd data-testid={`api-key-rpm-${key.id}`}>{formatRateLimit(key.rpm_limit)}</dd></div>
            <div><dt>Created</dt><dd>{formatEpoch(key.created_at)}</dd></div>
            <div><dt>Last used</dt><dd>{formatEpoch(key.last_used_at) ?? "Never used"}</dd></div>
            {revokedAt ? <div><dt>Revoked</dt><dd>{revokedAt}</dd></div> : null}
          </dl>
          {revokedAt ? null : <Button variant="danger" size="sm" onClick={() => setPendingRevoke(key)}>Revoke</Button>}
        </div>
      </Card>
    );
  }

  const empty = (
    <EmptyState
      icon="key-round"
      title="No API keys"
      description="Use New API key to connect a client to this router. The raw key is shown once at creation."
    />
  );

  return (
    <div className="vn-api-keys-surface">
      {loadError ? (
        <ErrorState code={loadError.code} title="Could not load API keys" description={loadError.message} />
      ) : keys === null ? (
        <Spinner label="Loading API keys…" />
      ) : (
        <ResponsiveCollection
          rows={keys}
          columns={columns}
          rowKey="id"
          renderCard={keyCard}
          label="API keys"
          empty={empty}
          getRowProps={(key) => ({ "data-testid": `api-key-${key.id}` }) as HTMLAttributes<HTMLTableRowElement>}
        />
      )}

      <AdaptiveSheet
        open={createOpen}
        onClose={closeCreate}
        title="Create an API key"
        description={created ? "Store this secret now. It cannot be shown again." : "Create a gateway credential for an application or environment."}
        footer={created ? undefined : (
          <>
            <Button variant="secondary" onClick={closeCreate}>Cancel</Button>
            <Button variant="primary" onClick={handleCreate} disabled={!canCreate}>Create key</Button>
          </>
        )}
      >
        {created ? (
          <APIKeyCreationResult rawKey={created.rawKey} keyLabel={created.label} onDone={closeCreate} />
        ) : (
          <div className="flex flex-col gap-4">
            <FormField label="Label" required error={formError ? formError.message : undefined}>
              <Input value={label} onChange={(event) => setLabel(event.target.value)} autoComplete="off" />
            </FormField>
            <FormField
              label="Requests per minute"
              description={rpmIsValid ? "Leave empty to use the router default." : undefined}
              error={rpmIsValid ? undefined : "Enter a positive whole number, or leave this field empty."}
            >
              <Input
                type="number"
                inputMode="numeric"
                min={1}
                step={1}
                value={rpmLimit}
                onChange={(event) => setRpmLimit(event.target.value)}
              />
            </FormField>
            {formError ? <span className="vn-reason-code" title="Typed error code from the control API">{formError.code}</span> : null}
          </div>
        )}
      </AdaptiveSheet>

      <DestructiveActionConfirmation
        open={pendingRevoke !== null}
        title="Revoke this API key?"
        consequence={pendingRevoke ? `"${pendingRevoke.label}" (${pendingRevoke.key_prefix}) will stop authenticating immediately. This cannot be undone.` : ""}
        confirmLabel="Revoke key"
        onConfirm={handleConfirmRevoke}
        onCancel={() => setPendingRevoke(null)}
      />
    </div>
  );
}
