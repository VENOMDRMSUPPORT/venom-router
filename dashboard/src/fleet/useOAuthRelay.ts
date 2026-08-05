import { useEffect, useRef } from "react";
import { toast } from "@venom/design-system";

/**
 * The state-less OAuth completion leg, shared by every flow that opens an
 * authorize popup.
 *
 * WHY THIS EXISTS: some providers (clinepass — `OmitStateFromCallback() ==
 * true`) do NOT echo `state` back on the redirect. The backend therefore
 * CANNOT complete such a callback server-side: with no state there is nothing
 * to bind the incoming `code` to one transaction, so `GET /callback` renders a
 * relay page that `postMessage`s the code to `window.opener` — the dashboard
 * window, the only party holding the unguessable transaction id from Begin.
 * Whoever opened the popup must therefore finish the flow with
 * `POST /oauth/complete`.
 *
 * This lived only in ConnectDialog once, which is why first-time enrollment
 * worked while RE-authentication silently could not: AccountRow opened the
 * popup, polled transaction status, and never listened for the relay, so the
 * code was dropped and the transaction expired unconsumed. Verified live
 * 2026-08-04: three `reauth_begin` successes, three `consumed = 0`
 * transactions, an account row untouched for seven hours. One shared hook so
 * a third caller cannot repeat it.
 *
 * The relay posts with targetOrigin `"*"` and this listener does not filter on
 * `event.origin` BY DESIGN: the callback page is served by the BACKEND origin
 * (127.0.0.1:8081) while the dashboard may be on the dev port (127.0.0.1:8088)
 * — different origins. Safety does not rest on the origin check: the payload
 * carries no credential (only a single-use authorization code), completion is
 * owner-session + CSRF gated, and the transaction id it is spent against comes
 * from OUR OWN Begin response, never from the message. A forged message can at
 * worst spend a code the caller is already waiting for.
 */
/** The relay page's message type (renderOAuthRelayPage, internal/httpapi/
 * oauth.go). Not exported: every consumer goes through the hook, so a second
 * hand-rolled listener cannot drift from this string. */
const OAUTH_RELAY_MESSAGE_TYPE = "venom_oauth_callback";

interface OAuthRelayPayload {
  type?: string;
  data?: { code?: string | null; error?: string | null };
}

export interface UseOAuthRelayCompletionParams {
  /** The transaction awaiting completion, or null when no flow is pending. */
  transactionId: string | null;
  /** Set false to stay dormant (e.g. a provider using the manual-code flow,
   * where the owner pastes the code instead of a redirect delivering it). */
  enabled?: boolean;
  /** Spend the relayed code against transactionId. */
  onCode: (transactionId: string, code: string) => void;
  /** The provider reported a denial instead of a code. */
  onDenied?: (error: string) => void;
}

export function useOAuthRelayCompletion({
  transactionId,
  enabled = true,
  onCode,
  onDenied,
}: UseOAuthRelayCompletionParams): void {
  // Callbacks are read through refs so a re-render with a fresh closure does
  // NOT tear down and re-add the listener — a resubscribe between the popup
  // returning and the message arriving would lose the code outright.
  const onCodeRef = useRef(onCode);
  onCodeRef.current = onCode;
  const onDeniedRef = useRef(onDenied);
  onDeniedRef.current = onDenied;

  useEffect(() => {
    if (!enabled || !transactionId) return;

    // Pinned into a local const: the guard above narrows the destructured
    // parameter, but that narrowing does NOT reach inside the listener
    // closure, and the id must be the one this subscription was armed with.
    const pendingTransactionID = transactionId;

    // A single-use authorization code: the relay page may post more than once
    // (React StrictMode double-mounts, a re-delivered message), and spending
    // the same code twice fails the second time and would surface as a bogus
    // error on an already-successful flow.
    let finished = false;

    function onMessage(event: MessageEvent) {
      const payload = event.data as OAuthRelayPayload | null;
      if (!payload || payload.type !== OAUTH_RELAY_MESSAGE_TYPE || finished) return;
      const code = payload.data?.code;
      if (!code) {
        const denial = payload.data?.error;
        if (denial) {
          finished = true;
          toast.danger("OAuth connection failed", { detail: denial });
          onDeniedRef.current?.(denial);
        }
        return;
      }
      finished = true;
      toast.success("OAuth account connected", { detail: "Authenticated with provider" });
      onCodeRef.current(pendingTransactionID, code);
    }

    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [enabled, transactionId]);
}
