import { useState, type ChangeEvent, type ComponentProps, type FormEvent } from "react";
import {
  Alert,
  Button,
  FormField,
  IconButton,
  Input,
  Stepper,
} from "@venom/design-system/primitives";
import { OwnerSessionStatus, TypedErrorDisplay } from "@venom/design-system/domain";
import { AuthApiError, isLockedOut, setupOwner, type LiveSession } from "./authClient";
import { formatRetryAfter, useRetryCountdown } from "./useRetryCountdown";

// Mirrors internal/secrets/password.go's MinPasswordLength — a UX nicety
// only; the server (ValidateOwnerPassword / DeriveOwnerPasswordHash) is
// the sole source of truth and is re-checked on every submit regardless.
const MIN_PASSWORD_LENGTH = 12;

interface PasswordInputProps extends Omit<ComponentProps<typeof Input>, "type"> {
  revealed: boolean;
  revealLabel: string;
  onRevealChange: () => void;
}

function PasswordInput(props: PasswordInputProps) {
  const { revealed, revealLabel, onRevealChange, disabled, className = "", ...inputProps } = props;

  return (
    <div className="relative">
      <Input
        {...inputProps}
        type={revealed ? "text" : "password"}
        disabled={disabled}
        className={["pr-10", className].filter(Boolean).join(" ")}
      />
      <IconButton
        icon={revealed ? "eye-off" : "eye"}
        label={`${revealed ? "Hide" : "Show"} ${revealLabel}`}
        variant="ghost"
        size="sm"
        aria-pressed={revealed}
        disabled={disabled}
        onClick={onRevealChange}
        className="absolute right-1 top-1/2 -translate-y-1/2"
      />
    </div>
  );
}

export interface FirstRunSetupProps {
  /** Called once with the newly-created session + CSRF token on success. */
  onComplete: (live: LiveSession) => void;
}

/** First-run setup (09 §5.1): create the single owner password. There is
 * no sign-up flow, no role picker, no second account — ever. */
export default function FirstRunSetup(props: FirstRunSetupProps) {
  const { onComplete } = props;

  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordRevealed, setPasswordRevealed] = useState(false);
  const [confirmationRevealed, setConfirmationRevealed] = useState(false);
  const [validationError, setValidationError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [apiError, setApiError] = useState<AuthApiError | null>(null);
  const [lockoutSeconds, setLockoutSeconds] = useState<number | null>(null);
  const remainingLockout = useRetryCountdown(lockoutSeconds);
  const locked = lockoutSeconds != null && remainingLockout > 0;

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (submitting || locked) return;

    setValidationError(null);
    setApiError(null);

    if (password.length < MIN_PASSWORD_LENGTH) {
      setValidationError(`Password must be at least ${MIN_PASSWORD_LENGTH} characters.`);
      return;
    }
    if (password !== confirmPassword) {
      setValidationError("Passwords do not match.");
      return;
    }

    // Capture the value and clear both fields immediately, before the
    // request even goes out — the password must never linger in the DOM
    // or in this component's own state past the moment of submission.
    const candidate = password;
    setPassword("");
    setConfirmPassword("");
    setSubmitting(true);
    try {
      const live = await setupOwner(candidate);
      onComplete(live);
    } catch (err) {
      if (isLockedOut(err)) {
        setLockoutSeconds((err as AuthApiError).retryAfterSeconds ?? 0);
      } else if (err instanceof AuthApiError) {
        setApiError(err);
      } else {
        setApiError(
          new AuthApiError(0, {
            code: "network_error",
            message: "Could not reach the server. Try again.",
            request_id: "",
            retryable: true,
          }),
        );
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface-canvas p-4">
      <div className="flex w-full max-w-sm flex-col gap-4">
        <div className="text-center">
          <div className="vn-display">Welcome to Venom Router</div>
          <p className="vn-caption">
            First run — create the single owner password. There are no users, roles, or teams.
          </p>
        </div>

        <Stepper steps={["Create password", "Sign in", "Connect a provider"]} current={0} />

        <form
          className="vn-card vn-card--pad flex flex-col gap-3"
          onSubmit={handleSubmit}
          noValidate
        >
          <FormField
            label="Owner password"
            required
            description="Stored as an Argon2id hash only — the password itself is never written anywhere."
          >
            <PasswordInput
              revealed={passwordRevealed}
              revealLabel="password"
              onRevealChange={() => setPasswordRevealed((revealed) => !revealed)}
              autoComplete="new-password"
              value={password}
              disabled={submitting || locked}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setPassword(e.target.value)}
            />
          </FormField>
          <FormField label="Confirm password" required>
            <PasswordInput
              revealed={confirmationRevealed}
              revealLabel="password confirmation"
              onRevealChange={() => setConfirmationRevealed((revealed) => !revealed)}
              autoComplete="new-password"
              value={confirmPassword}
              disabled={submitting || locked}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setConfirmPassword(e.target.value)}
            />
          </FormField>

          {validationError ? (
            <TypedErrorDisplay tone="warning" code="validation_error" message={validationError} />
          ) : null}

          {apiError ? (
            <TypedErrorDisplay
              code={apiError.code}
              message={apiError.message}
              retryable={apiError.retryable}
              tone="critical"
            />
          ) : null}

          {locked ? (
            <TypedErrorDisplay
              code="locked_out"
              message="Too many attempts. Try again shortly."
              retryable
              retryAfter={formatRetryAfter(remainingLockout)}
              tone="critical"
            />
          ) : null}

          <Alert tone="info" title="No recovery email exists.">
            If you lose this password, recovery is a backup restore or the documented local reset.
            Consider creating an encrypted backup once set up.
          </Alert>

          <Button
            type="submit"
            variant="primary"
            icon="shield"
            loading={submitting}
            disabled={submitting || locked}
          >
            Create password &amp; continue
          </Button>
        </form>

        <div className="text-center">
          <OwnerSessionStatus
            state={locked ? "locked_out" : "unauthenticated"}
            retryAfter={locked ? formatRetryAfter(remainingLockout) : undefined}
          />
        </div>
      </div>
    </div>
  );
}
