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
        const status = await fetchAuthStatus();
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
