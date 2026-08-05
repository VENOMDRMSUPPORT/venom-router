import { useState, type AnimationEvent, type ChangeEvent, type FormEvent } from "react";
import { Button, FormField, Input } from "@venom/design-system/primitives";
import { OwnerSessionStatus, TypedErrorDisplay } from "@venom/design-system/domain";
import { AuthApiError, isLockedOut, login, type LiveSession } from "./authClient";
import { formatRetryAfter, useRetryCountdown } from "./useRetryCountdown";
import { autofillValueFromAnimation } from "./autofill";

export interface LoginScreenProps {
  /** Called once with the newly-created session + CSRF token on success. */
  onSuccess: (live: LiveSession) => void;
}

/** Owner login (09 §5.2). Single owner, no account picker. Failure is
 * always the generic `invalid_credentials` message the backend returns —
 * it must never be reworded into anything that could reveal whether
 * setup has been completed. */
export default function LoginScreen(props: LoginScreenProps) {
  const { onSuccess } = props;

  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [apiError, setApiError] = useState<AuthApiError | null>(null);
  const [lockoutSeconds, setLockoutSeconds] = useState<number | null>(null);
  const remainingLockout = useRetryCountdown(lockoutSeconds);
  const locked = lockoutSeconds != null && remainingLockout > 0;

  function handleAutofillStart(e: AnimationEvent<HTMLInputElement>) {
    const value = autofillValueFromAnimation(e.animationName, e.currentTarget.value);
    if (value !== null) setPassword(value);
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (submitting || locked || password.length === 0) return;

    setApiError(null);

    // Clear the field the instant submission starts, not after the
    // network round trip — the password must not linger in the DOM.
    const candidate = password;
    setPassword("");
    setSubmitting(true);
    try {
      const live = await login(candidate);
      onSuccess(live);
    } catch (err) {
      if (isLockedOut(err)) {
        setLockoutSeconds((err as AuthApiError).retryAfterSeconds ?? 0);
      } else if (err instanceof AuthApiError) {
        setApiError(err);
      } else {
        setApiError(new AuthApiError(0, { code: "network_error", message: "Could not reach the server. Try again.", request_id: "", retryable: true }));
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-surface-canvas p-4">
      <div className="flex w-full max-w-sm flex-col gap-4">
        <div className="text-center">
          <h1 className="vn-display">Venom Router</h1>
          <p className="vn-caption">Owner console · single owner, no accounts to pick</p>
        </div>

        <form className="vn-card vn-card--pad flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <FormField label="Owner password">
            <Input
              type="password"
              autoComplete="current-password"
              value={password}
              disabled={submitting || locked}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setPassword(e.target.value)}
              onAnimationStart={handleAutofillStart}
            />
          </FormField>

          {apiError ? (
            <TypedErrorDisplay code={apiError.code} message={apiError.message} retryable={apiError.retryable} tone="critical" />
          ) : null}

          {locked ? (
            <TypedErrorDisplay
              code="locked_out"
              message="Too many failed attempts. Sign-in is temporarily disabled."
              retryable
              retryAfter={formatRetryAfter(remainingLockout)}
              tone="critical"
            />
          ) : null}

          <Button type="submit" variant="primary" icon="lock" loading={submitting} disabled={submitting || locked || password.length === 0}>
            Sign in
          </Button>
          <p className="vn-caption text-center">
            Sessions: 30 min idle · 12 h absolute. Lost password → restore a backup or run the local reset.
          </p>
        </form>

        <div className="text-center">
          <OwnerSessionStatus
            state={locked ? "locked_out" : "unauthenticated"}
            retryAfter={locked ? formatRetryAfter(remainingLockout) : undefined}
          />
        </div>
      </div>
    </main>
  );
}
