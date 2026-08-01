import { useEffect, useState } from "react";
import { Card, EmptyState, ErrorState, Spinner } from "@venom/design-system/primitives";
import {
  LocalSafetyBudgetIndicator,
  QuotaUnknownState,
  QuotaWindowCard,
  type QuotaEvidenceSource,
  type QuotaFreshness,
  type QuotaWindowState,
} from "@venom/design-system/domain";
import {
  isSessionExpired,
  listAccounts,
  toApiError,
  type AccountProjection,
  type AuthApiError,
  type QuotaWindow,
} from "../api/controlClient";

export interface QuotaSurfaceProps {
  onSessionExpired: () => void;
}

/** Fetches every account page. Bounded like FleetOverview's own loop — an
 * owner console's account count is small, and the cap only guards against a
 * pathological infinite cursor. */
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

/** Renders an epoch-seconds reset_at as a locale timestamp, or `undefined`
 * for an absent reset — QuotaResetCountdown (inside QuotaWindowCard) then
 * renders its own "no reset" wording rather than a fabricated time. */
function formatResetAt(resetAt: number | null): string | undefined {
  if (resetAt == null) return undefined;
  return new Date(resetAt * 1000).toLocaleString();
}

function accountLabel(a: AccountProjection): string {
  return a.display_name || a.identity?.email || a.external_id || a.id;
}

/**
 * One account's quota windows.
 *
 * `provider_evidence` and `owner_override` windows render through
 * QuotaWindowCard (identity, source, state, meter, reset, freshness in one),
 * and `local_safety` windows through LocalSafetyBudgetIndicator, so Venom's
 * own routing-safety budget is never presented as provider evidence
 * (docs/02 §3, 07 §5a) — the same split QuotaSummary already uses per account
 * row on the Providers surface.
 *
 * Every server-side `null` is passed through as `undefined`, NEVER coerced to
 * a number: that is what keeps QuotaWindowMeter on its own "total == null"
 * unknown branch instead of drawing an empty meter that reads as zero
 * consumption.
 */
function AccountQuotaWindows(props: { windows: QuotaWindow[] }) {
  const { windows } = props;

  if (windows.length === 0) {
    return (
      <QuotaUnknownState note="No quota evidence has been observed for this account yet." />
    );
  }

  const evidence = windows.filter((w) => w.source !== "local_safety");
  const localSafety = windows.filter((w) => w.source === "local_safety");
  const concurrency = localSafety.find((w) => w.window_type === "concurrency");
  const consumption = localSafety.find((w) => w.window_type === "estimated_consumption");

  return (
    <div className="flex flex-col gap-3">
      {evidence.map((w) => (
        <div
          key={`${w.source}:${w.unit}:${w.window_type}:${w.window_key}`}
          data-testid={`quota-window-${w.window_key}`}
        >
          <QuotaWindowCard
            name={w.window_type}
            windowKey={w.window_key}
            source={w.source as QuotaEvidenceSource}
            state={w.state as QuotaWindowState}
            used={w.used ?? undefined}
            total={w.total ?? undefined}
            reserved={w.reserved}
            unit={w.unit}
            resetAt={formatResetAt(w.reset_at)}
            freshness={w.freshness as QuotaFreshness}
          />
        </div>
      ))}
      {localSafety.length > 0 ? (
        <LocalSafetyBudgetIndicator
          concurrencyUsed={concurrency?.reserved}
          concurrencyCap={concurrency?.limit_value ?? undefined}
          consumptionUsed={consumption?.used ?? undefined}
          consumptionCap={consumption?.limit_value ?? undefined}
          consumptionUnit={consumption?.unit}
        />
      ) : null}
    </div>
  );
}

/**
 * The Quota & Limits surface (P6-UI-006, 05 §4, 07 §5): every connected
 * account's quota windows, grouped per account then per window, composed
 * ENTIRELY from the frozen `@venom/design-system` Quota components.
 *
 * It adds NO backend call — `GET /accounts` already carries the full window
 * projection, and the server computes each window's `state` (accounts.go's
 * quotaWindowsToJSON, now against the owner's configured staleness window).
 * This surface renders that state verbatim and NEVER re-derives it: a client
 * that recomputed freshness against its own clock would disagree with the
 * router about which windows are usable.
 *
 * Every state in `Design_System/states/state-matrix.md` has a rendering:
 * loading (Spinner), error (ErrorState), empty (EmptyState / QuotaUnknownState),
 * and per window available / insufficient / exhausted / unknown / stale — each
 * carrying an icon AND text from the frozen components, never colour alone.
 */
export default function QuotaSurface(props: QuotaSurfaceProps) {
  const { onSessionExpired } = props;

  const [accounts, setAccounts] = useState<AccountProjection[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);

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
          // The shell owns re-authentication. Deliberately leave `accounts`
          // null so nothing stale (or half-loaded) stays on screen.
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

  // An error is its OWN state. Falling through to the empty state here would
  // tell the owner "you have no quota" when the truth is "we could not ask".
  if (loadError) {
    return (
      <ErrorState
        code={loadError.code}
        title="Could not load quota"
        description={loadError.message}
      />
    );
  }

  if (accounts === null) {
    return <Spinner label="Loading quota…" />;
  }

  if (accounts.length === 0) {
    return (
      <EmptyState
        icon="gauge"
        title="No connected accounts"
        description="Quota windows appear here once a provider account is connected and its limits have been observed."
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {accounts.map((a) => (
        <Card key={a.id} data-testid={`quota-account-${a.id}`}>
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
              <h3 className="vn-h3">{accountLabel(a)}</h3>
              <span className="vn-caption">{a.provider}</span>
            </div>
            <AccountQuotaWindows windows={a.quota} />
          </div>
        </Card>
      ))}
    </div>
  );
}
