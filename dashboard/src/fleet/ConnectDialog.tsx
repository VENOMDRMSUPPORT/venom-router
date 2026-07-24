import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { Alert, Button, Dialog, FormField, Input, Select, Spinner } from "@venom/design-system/primitives";
import { TypedErrorDisplay } from "@venom/design-system/domain";
import {
  connectApiKeyAccount,
  isSessionExpired,
  oauthBegin,
  pollOAuthStatus,
  toApiError,
  AuthApiError,
  type Provider,
} from "../api/controlClient";

export interface ConnectDialogProps {
  /** The provider being connected, or null when the dialog is closed. */
  provider: Provider | null;
  csrfToken: string;
  onSessionExpired: () => void;
  onClose: () => void;
  onConnected: () => void;
}

const FUNDING_OPTIONS = [
  { value: "", label: "Let the provider's policy decide" },
  { value: "free", label: "Free" },
  { value: "paid", label: "Paid" },
  { value: "unknown", label: "Unknown — classify later" },
];

const OAUTH_POLL_INTERVAL_MS = 2000;

function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `idem-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

/**
 * The provider's "Connect account" dialog (P2b-UI-003): API-key mode
 * (FormField+Input, an Idempotency-Key header so a retried submit never
 * double-connects, the key never echoed and cleared from state the
 * instant submit starts) or OAuth mode (begin -> open the authorize URL
 * in a new tab -> poll status until completed/failed/expired). Neither
 * mode ever displays a code, token, or the submitted key anywhere.
 */
export default function ConnectDialog(props: ConnectDialogProps) {
  const { provider, csrfToken, onSessionExpired, onClose, onConnected } = props;

  const [apiKey, setApiKey] = useState("");
  const [funding, setFunding] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<AuthApiError | null>(null);

  const [oauthPhase, setOauthPhase] = useState<"idle" | "pending" | "failed" | "expired">("idle");
  const [oauthError, setOauthError] = useState<AuthApiError | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  function stopPolling() {
    if (pollRef.current != null) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }

  useEffect(() => stopPolling, []);

  function reset() {
    setApiKey("");
    setFunding("");
    setSubmitting(false);
    setError(null);
    setOauthPhase("idle");
    setOauthError(null);
    stopPolling();
  }

  function handleClose() {
    reset();
    onClose();
  }

  async function handleApiKeySubmit() {
    if (!provider || apiKey.length === 0) return;
    // Clear the field synchronously at submit time — the key must never
    // linger in this component's own state past the moment of submission.
    const candidate = apiKey;
    setApiKey("");
    setSubmitting(true);
    setError(null);
    try {
      await connectApiKeyAccount(
        provider.id,
        { api_key: candidate, funding: (funding || undefined) as "free" | "paid" | "unknown" | undefined },
        csrfToken,
        newIdempotencyKey(),
      );
      reset();
      onConnected();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setError(toApiError(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleOAuthBegin() {
    if (!provider) return;
    setOauthError(null);
    setOauthPhase("pending");
    try {
      const begin = await oauthBegin(provider.id, csrfToken);
      window.open(begin.authorize_url, "_blank", "noopener,noreferrer");

      const expiresAtMs = new Date(begin.expires_at).getTime();
      pollRef.current = setInterval(() => {
        void (async () => {
          if (Date.now() > expiresAtMs) {
            stopPolling();
            setOauthPhase("expired");
            return;
          }
          try {
            const status = await pollOAuthStatus(begin.transaction_id);
            if (status.status === "completed") {
              stopPolling();
              reset();
              onConnected();
            } else if (status.status === "failed") {
              stopPolling();
              setOauthPhase("failed");
              setOauthError(new AuthApiError(0, { code: status.error ?? "failed", message: "The OAuth connection failed.", request_id: "", retryable: true }));
            } else if (status.status === "expired") {
              stopPolling();
              setOauthPhase("expired");
            }
          } catch (err) {
            if (isSessionExpired(err)) {
              stopPolling();
              onSessionExpired();
              return;
            }
            // A transient poll failure is not itself fatal — keep polling
            // until expires_at, since the transaction may still complete.
          }
        })();
      }, OAUTH_POLL_INTERVAL_MS);
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setOauthPhase("failed");
      setOauthError(toApiError(err));
    }
  }

  if (!provider) return null;

  const isOAuth = provider.auth_mode === "oauth2";

  return (
    <Dialog
      open={provider != null}
      onClose={handleClose}
      title={`Connect ${provider.display_name} account`}
      description={
        isOAuth
          ? "Sign in with the provider in the new tab that opens, then return here."
          : "Validated with a real, zero-cost credential check before the account is created."
      }
      footer={
        isOAuth ? (
          <Button variant="ghost" onClick={handleClose}>
            Close
          </Button>
        ) : (
          <>
            <Button variant="ghost" onClick={handleClose}>
              Cancel
            </Button>
            <Button variant="primary" loading={submitting} disabled={apiKey.length === 0} onClick={() => void handleApiKeySubmit()}>
              Validate &amp; connect
            </Button>
          </>
        )
      }
    >
      {isOAuth ? (
        <div className="flex flex-col gap-3">
          {oauthPhase === "idle" ? (
            <Button variant="primary" icon="external-link" onClick={() => void handleOAuthBegin()}>
              Continue with {provider.display_name}
            </Button>
          ) : null}
          {oauthPhase === "pending" ? (
            <div className="flex items-center gap-2">
              <Spinner size="sm" label="Waiting for the provider" />
              <span className="vn-caption">Complete sign-in in the new tab — this closes automatically once connected.</span>
            </div>
          ) : null}
          {oauthPhase === "failed" ? (
            <TypedErrorDisplay
              code={oauthError?.code ?? "failed"}
              message={oauthError?.message ?? "The OAuth connection failed."}
              retryable
            />
          ) : null}
          {oauthPhase === "expired" ? (
            <Alert tone="warning" title="The connection attempt expired">
              Start again from "Connect account".
            </Alert>
          ) : null}
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <FormField
            label="API key"
            required
            description="Validated authentically with a zero-cost check. The key is never echoed back or stored anywhere in this app."
          >
            <Input
              mono
              type="password"
              autoComplete="off"
              value={apiKey}
              disabled={submitting}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setApiKey(e.target.value)}
            />
          </FormField>
          <FormField label="Funding classification" description="Recorded as owner evidence.">
            <Select
              options={FUNDING_OPTIONS}
              value={funding}
              disabled={submitting}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setFunding(e.target.value)}
            />
          </FormField>
          {error ? <TypedErrorDisplay code={error.code} message={error.message} retryable={error.retryable} tone="critical" /> : null}
        </div>
      )}
    </Dialog>
  );
}
