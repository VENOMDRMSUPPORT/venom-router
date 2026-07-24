import { useState } from "react";
import { ReverificationPrompt } from "@venom/design-system/domain";
import { AuthApiError, isLockedOut, isSessionExpired, reverify } from "./authClient";
import { formatRetryAfter, useRetryCountdown } from "./useRetryCountdown";

export interface ReverifyModalProps {
  open: boolean;
  /** Human-readable description of the action being gated, e.g. "run the
   * diagnostics export". */
  action?: string;
  /** The current session's CSRF token — required to call POST /auth/reverify. */
  csrfToken: string;
  onSuccess: () => void;
  onCancel: () => void;
  /** The session died out from under this modal (validateSession runs
   * before the password check on the server) — the caller must clear all
   * auth state and route back to Login. */
  onSessionExpired: () => void;
}

/**
 * Reusable modal that gates a sensitive action behind a fresh
 * re-verification (09 §5.5): prompts for the owner password, calls
 * POST /auth/reverify, and only calls onSuccess once the server confirms
 * it. Freshness itself (the 5-minute window) is enforced server-side —
 * this component only reacts to success/failure.
 *
 * Built on the frozen @venom/design-system ReverificationPrompt, which
 * already clears its own internal password field synchronously the
 * instant Verify is clicked (before this component's async call even
 * resolves) — so the password never lingers in the DOM here either.
 */
export default function ReverifyModal(props: ReverifyModalProps) {
  const { open, action, csrfToken, onSuccess, onCancel, onSessionExpired } = props;

  const [error, setError] = useState<string | null>(null);
  const [lockoutSeconds, setLockoutSeconds] = useState<number | null>(null);
  const remainingLockout = useRetryCountdown(lockoutSeconds);
  const locked = lockoutSeconds != null && remainingLockout > 0;

  async function handleConfirm(password: string) {
    setError(null);
    try {
      await reverify(password, csrfToken);
      onSuccess();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      if (isLockedOut(err)) {
        setLockoutSeconds((err as AuthApiError).retryAfterSeconds ?? 0);
        return;
      }
      setError(err instanceof AuthApiError ? err.message : "Could not reach the server. Try again.");
    }
  }

  function handleCancel() {
    setError(null);
    onCancel();
  }

  return (
    <ReverificationPrompt
      open={open}
      action={action}
      error={error}
      locked={locked}
      retryAfter={locked ? formatRetryAfter(remainingLockout) : undefined}
      onConfirm={handleConfirm}
      onCancel={handleCancel}
    />
  );
}
