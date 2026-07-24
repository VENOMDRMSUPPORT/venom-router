import { useEffect, useState } from "react";
import { DropdownMenu, IconButton } from "@venom/design-system/primitives";
import { OwnerSessionStatus } from "@venom/design-system/domain";
import type { SessionTimes } from "../auth/authClient";

export interface OwnerMenuProps {
  session: SessionTimes;
  onSignOut: () => void | Promise<void>;
}

/** Formats a millisecond duration as the plain "Xh Ym" / "Xm" text
 * OwnerSessionStatus's idleIn/absoluteIn slots expect — never a raw
 * styled value, just content. Floors at "0m" rather than going negative
 * once a countdown has technically elapsed client-side (the server, not
 * this display, is the source of truth on the next real request). */
function formatRemaining(targetIso: string, now: number): string {
  const remainingMs = new Date(targetIso).getTime() - now;
  if (Number.isNaN(remainingMs) || remainingMs <= 0) return "0m";
  const totalMinutes = Math.floor(remainingMs / 60000);
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`;
}

/**
 * The top bar's owner menu (P2b-UI-001): a DropdownMenu surfacing the
 * live OwnerSessionStatus (idle/absolute countdowns) plus a Sign out
 * item. Ticks once a minute purely to keep the displayed countdowns
 * fresh — the session's actual expiry is always enforced server-side.
 */
export default function OwnerMenu(props: OwnerMenuProps) {
  const { session, onSignOut } = props;
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(id);
  }, []);

  return (
    <DropdownMenu
      trigger={<IconButton icon="user-round" label="Owner menu" variant="ghost" />}
      align="end"
      items={[
        {
          type: "label",
          label: (
            <OwnerSessionStatus
              state="active"
              idleIn={formatRemaining(session.idleExpiresAt, now)}
              absoluteIn={formatRemaining(session.absoluteExpiresAt, now)}
            />
          ),
        },
        { type: "separator" },
        {
          label: "Sign out",
          icon: "log-out",
          onSelect: () => {
            void onSignOut();
          },
        },
      ]}
    />
  );
}
