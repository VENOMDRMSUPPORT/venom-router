import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { useOAuthRelayCompletion } from "./useOAuthRelay";
import { toast } from "@venom/design-system";
import {
  Alert,
  Button,
  Dialog,
  FormField,
  Input,
  RadioGroup,
  Spinner,
  Textarea,
} from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import { TypedErrorDisplay } from "@venom/design-system/domain";
import {
  connectApiKeyAccount,
  isSessionExpired,
  oauthBegin,
  oauthCompleteCode,
  pollOAuthStatus,
  toApiError,
  AuthApiError,
  type Provider,
} from "../api/controlClient";
import { providerDisplayName } from "./providerMeta";

export interface ConnectDialogProps {
  /** The provider being connected, or null when the dialog is closed. */
  provider: Provider | null;
  csrfToken: string;
  onSessionExpired: () => void;
  onClose: () => void;
  onConnected: () => void;
}

/** The 2x2 Billing Type radio cards (image 9). "" = inherit: the funding
 * field is OMITTED from the connect body so the catalog's own policy
 * decides — never sent as a value. */
const BILLING_OPTIONS = [
  {
    value: "",
    label: "Inherit from provider",
    description: "Use the provider's default billing classification",
  },
  { value: "free", label: "Free", description: "Account uses free-tier quotas only" },
  { value: "paid", label: "Paid", description: "Account has paid subscription or credits" },
  { value: "unknown", label: "Unknown", description: "Billing status is unclear or mixed" },
];

const OAUTH_POLL_INTERVAL_MS = 2000;

/** The popup window features the OAuth flow opens with (image 10). */
const OAUTH_POPUP_NAME = "venom-oauth";
const OAUTH_POPUP_FEATURES = "popup,width=560,height=720";

function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `idem-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

/**
 * The provider's connect dialog (image 9/10).
 *
 * API-key mode: a mono textarea for the key (cleared from state
 * SYNCHRONOUSLY at submit — it never lingers past the moment of
 * submission), the 2x2 Billing Type radio cards (inherit = funding omitted
 * from the body), an Idempotency-Key header so a retried submit never
 * double-connects, and the "Save & encrypt" primary. The AES-256-GCM claim
 * in the description is the real storage contract (internal/secrets).
 *
 * OAuth mode: begin -> open the authorize URL in a POPUP window -> poll
 * status every 2s until completed/failed/expired. "Re-open sign-in window"
 * re-opens the SAME authorize URL — it is also the recovery path when the
 * popup was blocked (window.open returned null). Neither mode ever
 * displays a code, token, or the submitted key anywhere.
 */
export default function ConnectDialog(props: ConnectDialogProps) {
  const { provider, csrfToken, onSessionExpired, onClose, onConnected } = props;

  const [apiKey, setApiKey] = useState("");
  const [label, setLabel] = useState("");
  const [funding, setFunding] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<AuthApiError | null>(null);

  const [oauthPhase, setOauthPhase] = useState<"idle" | "pending" | "failed" | "expired">("idle");
  const [oauthError, setOauthError] = useState<AuthApiError | null>(null);
  // The live transaction's authorize URL — held ONLY so "Re-open sign-in
  // window" can re-open the same one; cleared with the rest of the state.
  const [authorizeUrl, setAuthorizeUrl] = useState<string | null>(null);
  const [popupBlocked, setPopupBlocked] = useState(false);
  // Manual-code providers (capability "manual_code"; claude-code): the
  // provider NEVER redirects back — the owner copies a code from the hosted
  // page and pastes it here. transactionId is the live transaction to complete.
  const [oauthTransactionId, setOauthTransactionId] = useState<string | null>(null);
  const [manualCode, setManualCode] = useState("");
  const [completing, setCompleting] = useState(false);
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
    setLabel("");
    setFunding("");
    setSubmitting(false);
    setError(null);
    setOauthPhase("idle");
    setOauthError(null);
    setAuthorizeUrl(null);
    setPopupBlocked(false);
    setOauthTransactionId(null);
    setManualCode("");
    setCompleting(false);
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
        {
          api_key: candidate,
          funding: (funding || undefined) as "free" | "paid" | "unknown" | undefined,
          label: label.trim() || undefined,
        },
        csrfToken,
        newIdempotencyKey(),
      );
      toast.success("Account connected successfully", {
        detail: `Connected account "${label || "default"}"`,
      });
      reset();
      onConnected();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      toast.danger("Failed to connect account", {
        detail: err instanceof Error ? err.message : String(err),
      });
      setError(toApiError(err));
    } finally {
      setSubmitting(false);
    }
  }

  /** Opens (or re-opens) the sign-in popup for `url`, tracking whether the
   * browser blocked it so the pending state can say so. */
  function openPopup(url: string) {
    const popup = window.open(url, OAUTH_POPUP_NAME, OAUTH_POPUP_FEATURES);
    setPopupBlocked(popup == null);
  }

  async function handleOAuthBegin() {
    if (!provider) return;
    setOauthError(null);
    setOauthPhase("pending");
    try {
      const begin = await oauthBegin(provider.id, csrfToken);
      setAuthorizeUrl(begin.authorize_url);
      setOauthTransactionId(begin.transaction_id);

      // Claude Code (manual_code): Anthropic only allows their hosted
      // callback page — it displays a code to paste. Open that page and
      // wait for the owner to paste; no status poll can succeed.
      if (provider.capabilities.includes("manual_code")) {
        window.open(begin.authorize_url, "_blank", "noopener,noreferrer");
        return;
      }

      // ClinePass and other redirect-back providers: popup + poll, with a
      // postMessage relay fallback when the provider omits `state`.
      openPopup(begin.authorize_url);

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
              setOauthError(
                new AuthApiError(0, {
                  code: status.error ?? "failed",
                  message: "The OAuth connection failed.",
                  request_id: "",
                  retryable: true,
                }),
              );
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

  /** Completes OAuth when the callback page relays a code (ClinePass omits
   * state) or when the owner pastes a hosted Claude Code. */
  async function completeWithCode(transactionId: string, code: string) {
    setCompleting(true);
    setOauthError(null);
    try {
      const status = await oauthCompleteCode(transactionId, code, csrfToken);
      if (status.status === "completed") {
        stopPolling();
        reset();
        onConnected();
        return;
      }
      setOauthPhase("failed");
      setOauthError(
        new AuthApiError(0, {
          code: status.error ?? "failed",
          message: "The OAuth connection failed.",
          request_id: "",
          retryable: true,
        }),
      );
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setOauthPhase("failed");
      setOauthError(toApiError(err));
    } finally {
      setCompleting(false);
    }
  }

  async function handleManualCodeSubmit() {
    if (!oauthTransactionId || manualCode.trim().length === 0) return;
    await completeWithCode(oauthTransactionId, manualCode.trim());
  }

  // Legacy-style relay: /callback posts venom_oauth_callback when the
  // provider omitted state (ClinePass). Completes via transaction_id.
  // The same state-less completion leg AccountRow's reauth uses — one hook, so
  // a provider that omits `state` cannot work in one flow and silently fail in
  // the other (which is exactly what happened before useOAuthRelay existed).
  // The manual-code providers opt out: there the owner pastes the code, no
  // redirect ever relays one.
  useOAuthRelayCompletion({
    transactionId: oauthPhase === "pending" ? oauthTransactionId : null,
    enabled: !provider?.capabilities.includes("manual_code"),
    onCode: (transactionId, code) => void completeWithCode(transactionId, code),
    onDenied: () => {
      stopPolling();
      setOauthPhase("failed");
      setOauthError(
        new AuthApiError(0, {
          code: "oauth_denied",
          message: "The OAuth connection failed.",
          request_id: "",
          retryable: true,
        }),
      );
    },
  });

  if (!provider) return null;

  const isOAuth = provider.auth_mode === "oauth2";
  const isManualCode = isOAuth && provider.capabilities.includes("manual_code");
  const name = providerDisplayName(provider);

  return (
    <Dialog
      open={provider != null}
      onClose={handleClose}
      title={`Connect ${name}`}
      description={
        isManualCode
          ? "Open the sign-in page, authorize, then paste the code it displays back here. We complete the connection after you paste it."
          : isOAuth
            ? "Sign in via the popup window. We complete the connection automatically when the provider redirects back."
            : "Paste your API key. It is encrypted with AES-256-GCM before storage and never shown again."
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
            <Button
              variant="primary"
              loading={submitting}
              disabled={apiKey.length === 0}
              onClick={() => void handleApiKeySubmit()}
            >
              Save &amp; encrypt
            </Button>
          </>
        )
      }
    >
      {isOAuth ? (
        <div className="flex flex-col gap-3">
          {oauthPhase === "idle" ? (
            <Button variant="primary" icon="external-link" onClick={() => void handleOAuthBegin()}>
              Continue with {name}
            </Button>
          ) : null}
          {oauthPhase === "pending" ? (
            isManualCode && oauthTransactionId ? (
              <div className="flex flex-col gap-3">
                <p className="vn-caption">
                  Sign in and authorize. The provider then shows a code — paste it below (it may
                  look like <span className="vn-input--mono">code#fragment</span>; paste it whole).
                </p>
                {authorizeUrl ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    icon="external-link"
                    onClick={() => window.open(authorizeUrl, "_blank", "noopener,noreferrer")}
                  >
                    Open sign-in page
                  </Button>
                ) : null}
                <FormField label="Authorization code" required>
                  <Textarea
                    className="vn-input--mono"
                    rows={3}
                    placeholder="Paste the code here…"
                    autoComplete="off"
                    spellCheck={false}
                    value={manualCode}
                    disabled={completing}
                    onChange={(e: ChangeEvent<HTMLTextAreaElement>) =>
                      setManualCode(e.target.value)
                    }
                  />
                </FormField>
                <Button
                  variant="primary"
                  loading={completing}
                  disabled={manualCode.trim().length === 0}
                  onClick={() => void handleManualCodeSubmit()}
                >
                  Complete connection
                </Button>
              </div>
            ) : (
              <div className="flex flex-col items-center gap-3 py-4">
                <Spinner size="lg" label="Waiting for the provider" />
                <span className="vn-caption">Waiting for authorization in popup…</span>
                {popupBlocked ? (
                  <Alert tone="warning" title="The popup may have been blocked">
                    Use "Re-open sign-in window" to try again.
                  </Alert>
                ) : null}
                {authorizeUrl ? (
                  <Button variant="secondary" size="sm" onClick={() => openPopup(authorizeUrl)}>
                    Re-open sign-in window
                  </Button>
                ) : null}
              </div>
            )
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
              Start again from "Add account".
            </Alert>
          ) : null}
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <FormField label="API key" required>
            <Textarea
              className="vn-input--mono"
              rows={4}
              placeholder="sk-…"
              autoComplete="off"
              spellCheck={false}
              value={apiKey}
              disabled={submitting}
              onChange={(e: ChangeEvent<HTMLTextAreaElement>) => setApiKey(e.target.value)}
            />
          </FormField>
          {/* Not a FormField: its <label htmlFor> would dangle on the
              radiogroup DIV — the group is named via its own aria-label and
              each card is a real <label><input type="radio"/></label>. */}
          <div className="vn-field">
            <span className="vn-label">Billing Type</span>
            <RadioGroup
              name={`billing-${provider.id}`}
              label="Billing Type"
              className="vnd-billing-grid"
              options={BILLING_OPTIONS}
              value={funding}
              disabled={submitting}
              onChange={setFunding}
            />
          </div>
          <FormField
            label="Label"
            description="Optional. Shown instead of the auto-generated account number."
          >
            <Input
              placeholder="#account 01"
              maxLength={100}
              value={label}
              disabled={submitting}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setLabel(e.target.value)}
            />
          </FormField>
          <p className="vn-caption flex items-center gap-2" style={{ margin: 0 }}>
            <Icon name="shield-check" size={13} />
            Stored encrypted. A health check runs immediately after connect.
          </p>
          {error ? (
            <TypedErrorDisplay
              code={error.code}
              message={error.message}
              retryable={error.retryable}
              tone="critical"
            />
          ) : null}
        </div>
      )}
    </Dialog>
  );
}
