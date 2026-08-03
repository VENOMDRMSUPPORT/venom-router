import { useEffect, useState } from "react";
import { Button, Spinner } from "@venom/design-system/primitives";
import { TypedErrorDisplay } from "@venom/design-system/domain";
import AppShell from "../shell/AppShell";
import { AuthApiError, fetchAuthSession, fetchAuthStatus, type LiveSession, type SessionTimes } from "./authClient";
import FirstRunSetup from "./FirstRunSetup";
import LoginScreen from "./LoginScreen";

type GateState =
  | { kind: "loading" }
  | { kind: "bootstrap_error"; error: AuthApiError }
  | { kind: "first_run" }
  | { kind: "login" }
  | { kind: "authenticated"; session: SessionTimes; csrfToken: string };

// Bootstrap auto-retry: a dashboard opened while its backend is still booting
// (notably the dev dashboard on 8088 whose backend on 8081 is mid-`air`-build)
// gets a transient 5xx or a connection-refused on GET /auth/status. Retry a
// few times before surfacing the error screen, so a brief startup gap heals
// itself instead of sticking until a manual retry.
const MAX_BOOTSTRAP_ATTEMPTS = 4;
const BOOTSTRAP_RETRY_MS = 600;

const delay = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/** A bootstrap failure worth retrying: a raw fetch rejection (offline /
 * connection refused before the backend is listening) — not an AuthApiError —
 * or a typed error that is 5xx, an explicit network_error, or flagged
 * retryable. A 4xx (e.g. a real bad request) is surfaced immediately. */
function isTransientBootstrapError(err: unknown): boolean {
  if (!(err instanceof AuthApiError)) return true;
  return err.retryable || err.status >= 500 || err.code === "network_error";
}

/** GET /auth/status, retrying transient failures up to the attempt cap with a
 * fixed backoff. isCancelled short-circuits if the component unmounted. */
async function fetchAuthStatusWithRetry(isCancelled: () => boolean) {
  for (let attempt = 1; ; attempt++) {
    try {
      return await fetchAuthStatus();
    } catch (err) {
      if (isCancelled() || attempt >= MAX_BOOTSTRAP_ATTEMPTS || !isTransientBootstrapError(err)) {
        throw err;
      }
      await delay(BOOTSTRAP_RETRY_MS);
      if (isCancelled()) throw err;
    }
  }
}

/**
 * App-wide owner-auth gate (P2b-UI-002). On mount: GET /auth/status to
 * decide first-run vs. an existing owner, then (for an existing owner)
 * GET /auth/session to decide login vs. an already-live session — status
 * alone never reports session liveness (auth.go's ServeStatus returns
 * only `setup_complete`), so the second call is what actually answers
 * "is there a live session".
 *
 * This component is the single place that holds the CSRF token and
 * session timestamps; a session_expired response anywhere below routes
 * back here, which drops that state entirely (no secret, and nothing
 * session-bound, survives the transition).
 */
export default function AuthGate() {
  const [state, setState] = useState<GateState>({ kind: "loading" });
  const [bootstrapAttempt, setBootstrapAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function bootstrap() {
      let setupComplete: boolean;
      try {
        const status = await fetchAuthStatusWithRetry(() => cancelled);
        setupComplete = status.setupComplete;
      } catch (err) {
        if (!cancelled) {
          setState({
            kind: "bootstrap_error",
            error: err instanceof AuthApiError ? err : new AuthApiError(0, { code: "network_error", message: "Could not reach the server.", request_id: "", retryable: true }),
          });
        }
        return;
      }

      if (!setupComplete) {
        if (!cancelled) setState({ kind: "first_run" });
        return;
      }

      try {
        const live = await fetchAuthSession();
        if (!cancelled) setState({ kind: "authenticated", session: live.session, csrfToken: live.csrfToken });
      } catch {
        // No live session (session_expired or invalid_credentials, i.e.
        // no/invalid cookie) — either way, the owner needs to log in.
        if (!cancelled) setState({ kind: "login" });
      }
    }

    void bootstrap();
    return () => {
      cancelled = true;
    };
  }, [bootstrapAttempt]);

  function handleAuthenticated(live: LiveSession) {
    setState({ kind: "authenticated", session: live.session, csrfToken: live.csrfToken });
  }

  function handleReturnToLogin() {
    setState({ kind: "login" });
  }

  function handleRetryBootstrap() {
    setState({ kind: "loading" });
    setBootstrapAttempt((n) => n + 1);
  }

  switch (state.kind) {
    case "loading":
      return (
        <div className="flex min-h-screen items-center justify-center bg-surface-canvas p-4">
          <Spinner size="lg" label="Checking session" />
        </div>
      );
    case "bootstrap_error":
      return (
        <div className="flex min-h-screen items-center justify-center bg-surface-canvas p-4">
          <div className="flex w-full max-w-sm flex-col gap-3">
            <TypedErrorDisplay code={state.error.code} message={state.error.message} retryable tone="critical" />
            <Button variant="primary" icon="refresh-cw" onClick={handleRetryBootstrap}>
              Retry
            </Button>
          </div>
        </div>
      );
    case "first_run":
      return <FirstRunSetup onComplete={handleAuthenticated} />;
    case "login":
      return <LoginScreen onSuccess={handleAuthenticated} />;
    case "authenticated":
      return (
        <AppShell
          session={state.session}
          csrfToken={state.csrfToken}
          onSessionExpired={handleReturnToLogin}
          onLoggedOut={handleReturnToLogin}
        />
      );
  }
}
