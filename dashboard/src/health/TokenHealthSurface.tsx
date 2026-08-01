import { useCallback, useEffect, useState } from "react";
import { Badge, Button, Card, EmptyState, ErrorState, Spinner } from "@venom/design-system/primitives";
import {
  AccountCooldownIndicator,
  AccountStatus,
  ConnectionStateBadge,
  HealthStateBadge,
  type ConnectionState,
  type DisplayStatus as DSDisplayStatus,
  type HealthState,
} from "@venom/design-system/domain";
import {
  isSessionExpired,
  listAccounts,
  refreshHealth,
  toApiError,
  type AccountProjection,
  type AuthApiError,
  type DisplayStatus,
} from "../api/controlClient";

export interface TokenHealthSurfaceProps {
  csrfToken: string;
  onSessionExpired: () => void;
}

/**
 * The FULL display_status vocabulary (docs/02 §3's multi-axis account state
 * model, and @venom/design-system's own DisplayStatus union — the server's
 * domain.DeriveDisplayStatus emits precisely these).
 *
 * It is exported so a test can assert this surface covers exactly the
 * vocabulary and not a subset: a new state landing server-side must not
 * silently fall through to a default here.
 */
export const DISPLAY_STATUSES: readonly DisplayStatus[] = [
  "connecting",
  "healthy",
  "degraded",
  "unavailable",
  "expired",
  "unknown",
  "reauthenticating",
  "cooling_down",
  "stopped",
  "disconnected",
] as const;

const KNOWN_STATUSES = new Set<string>(DISPLAY_STATUSES);

/** The states whose remedy this console can actually offer. */
const NEEDS_FIX_ACTION = new Set<DisplayStatus>(["expired", "reauthenticating"]);

async function fetchAllAccounts(): Promise<AccountProjection[]> {
  const all: AccountProjection[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < 25; page++) {
    const result = await listAccounts({ cursor, limit: 200 });
    all.push(...result.accounts);
    if (!result.nextCursor) break;
    cursor = result.nextCursor;
  }
  return all;
}

function accountLabel(a: AccountProjection): string {
  return a.display_name || a.identity?.email || a.external_id || a.id;
}

function formatTimestamp(value?: string): string | null {
  if (!value) return null;
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return null;
  return parsed.toLocaleString();
}

/**
 * One account's health row.
 *
 * An UNRECOGNIZED display_status is rendered as an explicit, named unknown —
 * never defaulted to healthy and never left blank. The design system's
 * AccountStatus would itself fall back to its `unknown` badge for a value
 * outside the union, but that fallback is indistinguishable from a genuine
 * `unknown` status; an unrecognized value is a DIFFERENT fact (this console is
 * older than the server) and the operator needs to be able to tell them apart,
 * so the raw value is surfaced verbatim.
 */
function AccountHealthRow(props: {
  account: AccountProjection;
  busy: boolean;
  onFix: (id: string) => void;
}) {
  const { account: a, busy, onFix } = props;
  const recognized = KNOWN_STATUSES.has(a.display_status);
  const lastCheck = formatTimestamp(a.last_health_check_at);

  return (
    <Card data-testid={`health-account-${a.id}`}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <h2 className="vn-h3">{accountLabel(a)}</h2>
          <span className="vn-caption">{a.provider}</span>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <span data-testid="health-status-label">
            {recognized ? (
              <AccountStatus status={a.display_status as DSDisplayStatus} />
            ) : (
              <Badge tone="unknown" icon="circle-help">
                Unrecognized status · {a.display_status}
              </Badge>
            )}
          </span>
          <ConnectionStateBadge state={a.connection_state as ConnectionState} />
          <HealthStateBadge state={a.health_state as HealthState} />
        </div>

        {a.display_status === "cooling_down" ? (
          // The accounts projection carries no retry-after (see this batch's
          // report), so the indicator is rendered WITHOUT one and the unknown
          // is stated in words — never a fabricated countdown, and never a
          // bare timer with no textual state.
          <div className="flex flex-wrap items-center gap-2">
            <AccountCooldownIndicator scope="account" />
            <span className="vn-caption">Retry time unknown — not reported by the API yet.</span>
          </div>
        ) : null}

        {!a.eligibility.eligible ? (
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone="warning" icon="triangle-alert">
              Not eligible for routing
            </Badge>
            {a.eligibility.reason ? (
              <span className="vn-reason-code" title="Typed eligibility reason code">
                {a.eligibility.reason}
              </span>
            ) : null}
          </div>
        ) : null}

        <span className="vn-caption">
          {lastCheck ? `Last health check: ${lastCheck}` : "Last health check: never checked"}
        </span>

        {a.last_health_error ? (
          <span className="vn-caption" title="Typed health error code">
            {a.last_health_error}
          </span>
        ) : null}

        {NEEDS_FIX_ACTION.has(a.display_status) ? (
          // The card requires the state to prompt its remedy. Re-checking
          // health is the remedy this console can actually perform today:
          // the account-scoped OAuth re-auth endpoint has no client function
          // yet (see this batch's report).
          <div>
            <Button onClick={() => onFix(a.id)} disabled={busy}>
              Re-check credential
            </Button>
          </div>
        ) : null}
      </div>
    </Card>
  );
}

/**
 * The Token Health surface (P6-UI-007, 02 §3, 05 §3, 07 §5a): per-account
 * display_status, connection/health axes, eligibility reason, last health
 * check, and the fix action for the states that have one.
 *
 * CIRCUIT-BREAKER STATE IS DELIBERATELY ABSENT. `GET /accounts` does not
 * expose breaker scope/state/next-probe, and this unit adds no endpoint, so
 * the surface renders the cooldown/health truth it actually has rather than
 * inventing a breaker display. See this batch's report.
 */
export default function TokenHealthSurface(props: TokenHealthSurfaceProps) {
  const { csrfToken, onSessionExpired } = props;

  const [accounts, setAccounts] = useState<AccountProjection[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setLoadError(null);
      try {
        const list = await fetchAllAccounts();
        if (cancelled) return;
        setAccounts(list);
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setLoadError(toApiError(err));
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [onSessionExpired]);

  const handleFix = useCallback(
    async (accountId: string) => {
      setBusyId(accountId);
      try {
        const updated = await refreshHealth(accountId, csrfToken);
        setAccounts((prev) => (prev ? prev.map((a) => (a.id === updated.id ? updated : a)) : prev));
      } catch (err) {
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setLoadError(toApiError(err));
      } finally {
        setBusyId(null);
      }
    },
    [csrfToken, onSessionExpired],
  );

  if (loadError) {
    return (
      <ErrorState
        code={loadError.code}
        title="Could not load account health"
        description={loadError.message}
      />
    );
  }

  if (accounts === null) {
    return <Spinner label="Loading account health…" />;
  }

  if (accounts.length === 0) {
    return (
      <EmptyState
        icon="heart-pulse"
        title="No connected accounts"
        description="Account health, cooldowns, and credential state appear here once a provider account is connected."
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {accounts.map((a) => (
        <AccountHealthRow key={a.id} account={a} busy={busyId === a.id} onFix={handleFix} />
      ))}
    </div>
  );
}
