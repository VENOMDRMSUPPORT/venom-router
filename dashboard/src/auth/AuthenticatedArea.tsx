import { useState } from "react";
import { Badge, Button } from "@venom/design-system/primitives";
import { OwnerSessionStatus } from "@venom/design-system/domain";
import SmokeInventory from "../SmokeInventory";
import { logout, type SessionTimes } from "./authClient";
import ReverifyModal from "./ReverifyModal";

export interface AuthenticatedAreaProps {
  session: SessionTimes;
  csrfToken: string;
  /** Any authenticated call (here: logout, reverify) that comes back
   * session_expired must route back to Login with no auth state left
   * behind — this is that hook. */
  onSessionExpired: () => void;
  onLoggedOut: () => void;
}

/**
 * Minimal placeholder for the authenticated area (P2b-UI-002 scope): no
 * nav shell, no top bar — that is UI-001's job. This exists only to prove
 * the auth gate gets the owner somewhere real, to host a sign-out
 * control, and to demonstrate the reverify modal gating one sensitive
 * (stub) action before rendering the existing design-system smoke
 * inventory.
 */
export default function AuthenticatedArea(props: AuthenticatedAreaProps) {
  const { csrfToken, onSessionExpired, onLoggedOut } = props;

  const [loggingOut, setLoggingOut] = useState(false);
  const [reverifyOpen, setReverifyOpen] = useState(false);
  const [sensitiveActionUnlocked, setSensitiveActionUnlocked] = useState(false);

  async function handleSignOut() {
    setLoggingOut(true);
    try {
      await logout(csrfToken);
    } catch {
      // Logout is documented as idempotent/always-200 (authsession.go
      // ServeLogout) — a network failure here still means the owner
      // wants out, so proceed to clear local state regardless.
    } finally {
      setLoggingOut(false);
      onLoggedOut();
    }
  }

  return (
    <section aria-label="Venom Router — signed in" className="flex flex-col gap-4 p-4">
      <div className="flex items-center gap-3">
        <OwnerSessionStatus state="active" />
        <span className="flex-1" />
        <Button
          variant="secondary"
          icon="shield"
          onClick={() => setReverifyOpen(true)}
        >
          Run sensitive action (stub)
        </Button>
        <Button variant="ghost" icon="log-out" loading={loggingOut} onClick={handleSignOut}>
          Sign out
        </Button>
      </div>

      {sensitiveActionUnlocked ? (
        <Badge tone="healthy" icon="shield-check">
          Sensitive action executed after fresh re-verification
        </Badge>
      ) : null}

      <ReverifyModal
        open={reverifyOpen}
        action="run this sensitive action"
        csrfToken={csrfToken}
        onCancel={() => setReverifyOpen(false)}
        onSuccess={() => {
          setReverifyOpen(false);
          setSensitiveActionUnlocked(true);
        }}
        onSessionExpired={onSessionExpired}
      />

      <SmokeInventory />
    </section>
  );
}
