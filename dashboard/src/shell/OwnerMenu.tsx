import { useEffect, useState } from "react";
import { DropdownMenu } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import { OwnerSessionStatus } from "@venom/design-system/domain";
import type { SessionTimes } from "../auth/authClient";

export interface OwnerMenuProps {
  session: SessionTimes;
  onSignOut: () => void | Promise<void>;
  onNavigate?: (navKey: string) => void;
}

function formatRemaining(targetIso: string, now: number): string {
  const remainingMs = new Date(targetIso).getTime() - now;
  if (Number.isNaN(remainingMs) || remainingMs <= 0) return "0m";
  const totalMinutes = Math.floor(remainingMs / 60000);
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`;
}

export default function OwnerMenu(props: OwnerMenuProps) {
  const { session, onSignOut, onNavigate } = props;
  const [now, setNow] = useState(() => Date.now());
  const [isOpen, setIsOpen] = useState(false);

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(id);
  }, []);

  return (
    <DropdownMenu
      align="end"
      trigger={
        <button
          type="button"
          aria-label="Owner menu"
          onClick={() => setIsOpen(!isOpen)}
          className="flex items-center gap-2 rounded-lg p-1.5 hover:bg-surface-secondary text-text-primary text-xs font-medium transition-colors"
        >
          {/* The redesign's hardcoded #33373B avatar maps onto the neutral
              status-inactive chip pair — themable, and the closest grey the
              token scale offers. */}
          <span className="flex h-7 w-7 items-center justify-center rounded-full bg-status-inactive-bg text-text-primary font-semibold text-xs shadow-sm">
            O
          </span>
          <span className="hidden sm:inline-block text-xs font-semibold text-text-primary">
            Venom Owner
          </span>
          <Icon
            name="chevron-down"
            size={14}
            className={`text-text-muted transition-transform duration-200 ${isOpen ? "rotate-180" : ""}`}
          />
        </button>
      }
      items={[
        {
          type: "label",
          label: (
            <div className="flex items-center gap-3 p-1 min-w-48">
              <span className="flex h-9 w-9 flex-none items-center justify-center rounded-full bg-status-inactive-bg text-text-primary font-bold text-sm">
                O
              </span>
              <div className="flex flex-col min-w-0">
                <span className="font-semibold text-sm text-text-primary truncate">
                  Venom Owner
                </span>
                <span className="text-xs text-text-muted truncate">
                  owner@venom.local
                </span>
              </div>
            </div>
          ),
        },
        { type: "separator" },
        {
          type: "label",
          label: (
            <div className="px-1 py-0.5">
              <OwnerSessionStatus
                state="active"
                idleIn={formatRemaining(session.idleExpiresAt, now)}
                absoluteIn={formatRemaining(session.absoluteExpiresAt, now)}
              />
            </div>
          ),
        },
        { type: "separator" },
        {
          label: "Settings",
          icon: "settings",
          onSelect: () => {
            if (onNavigate) onNavigate("settings");
            // Fallback only if this menu is ever mounted without the shell's
            // navigate wired (it always is in-app): a real path navigation lands
            // on Settings via the SPA fallback.
            else window.location.assign("/settings");
          },
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
