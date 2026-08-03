import { useEffect, useState, type ReactNode } from "react";
import { Badge, Button, Card, ErrorState, Link, Spinner, StatCard } from "@venom/design-system/primitives";
import { AccountStatus, TierBadge, type DisplayStatus as DSDisplayStatus, type Tier as DSTier } from "@venom/design-system/domain";
import {
  isSessionExpired,
  listAccounts,
  listModelGroups,
  listProviders,
  listRouteDecisions,
  getRoutingPolicy,
  toApiError,
  type AccountProjection,
  type AuthApiError,
  type ModelGroup,
  type Provider,
  type RouteDecisionEntry,
  type TierPolicy,
} from "../api/controlClient";
import { pathForRoute } from "../shell/route";
import ReviewQueueBanner from "../models/ReviewQueueBanner";

export interface OverviewSurfaceProps {
  csrfToken: string;
  onSessionExpired: () => void;
  /** Opens the Connect-a-client page (P6-UI-011). That page is reached from here
   * rather than from a nav entry, so the shell hands Overview the navigation. */
  onOpenQuickStart?: () => void;
  /** Opens the Diagnostics surface on one request's route explanation. The shell
   * owns navigation (and the URL), so an activity row asks it to route rather
   * than driving the location itself. */
  onOpenRequest?: (requestID: string) => void;
}

/**
 * One card's independent fetch state.
 *
 * Each card owns its own. That is the load-bearing structural decision of this
 * page: the operator's home must not go blank because one read model is down.
 * A single shared `Promise.all` would do exactly that — one 500 and the whole
 * page renders an error where five working cards should be.
 */
interface CardState<T> {
  data: T | null;
  error: AuthApiError | null;
}

const LOADING: CardState<never> = { data: null, error: null };

/**
 * Runs one card's load, converting a failure into that card's OWN error state.
 *
 * A session expiry is the one thing that escapes to the shell, because it is not
 * a per-card fact — the whole page is unauthenticated at that point.
 */
function useCardData<T>(
  load: () => Promise<T>,
  onSessionExpired: () => void,
  // `load` is a fresh closure on every render, so it can never be a dependency;
  // this explicit list is the real one. Each card's read is a mount-time
  // one-shot, so the default empty list is correct.
  deps: unknown[] = [],
): CardState<T> {
  const [state, setState] = useState<CardState<T>>(LOADING);

  useEffect(() => {
    let cancelled = false;

    async function run() {
      try {
        const data = await load();
        if (!cancelled) setState({ data, error: null });
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        // Contained: this card reports the failure, the page carries on.
        setState({ data: null, error: toApiError(err) });
      }
    }

    void run();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return state;
}

/**
 * A card shell that renders exactly one of loading / error / content.
 *
 * The error branch never falls through to the content branch with empty data —
 * "we could not ask" and "there is nothing" are different facts, and only one of
 * them is good news.
 */
function OverviewCard<T>(props: {
  testId: string;
  title: string;
  state: CardState<T>;
  loadingLabel: string;
  errorTitle: string;
  children: (data: T) => ReactNode;
}) {
  const { testId, title, state, loadingLabel, errorTitle, children } = props;

  return (
    <Card data-testid={testId}>
      <div className="flex flex-col gap-2">
        <span className="vn-caption">{title}</span>
        {state.error ? (
          <ErrorState
            variant="inline"
            code={state.error.code}
            title={errorTitle}
            description={state.error.message}
          />
        ) : state.data === null ? (
          <Spinner label={loadingLabel} />
        ) : (
          children(state.data)
        )}
      </div>
    </Card>
  );
}

/** Fetches every account page — the same bounded loop the Quota and Token Health
 * surfaces use. */
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

/** How many recent decisions the activity card shows. */
const ACTIVITY_LIMIT = 10;

/** Renders `null` as the word, never as a number or a blank. */
function unknownOr(value: number | null, render: (v: number) => string): string {
  return value === null ? "unknown" : render(value);
}

/** One activity row's rolled-up outcome.
 *
 * Both nulls here are the whole reason this card exists in this shape:
 *
 *   terminal_status null -> the decision has NO attempt rows. It made no attempt.
 *     That is not a status, and it is emphatically not `success` — rendering it
 *     as success would report a delivered response the router never sent.
 *   total_latency_ms null -> at least one attempt's latency is unknown, so the
 *     total is unknown. "0 ms" would read as an instantaneous response.
 */
function ActivityOutcome(props: { entry: RouteDecisionEntry }) {
  const { entry } = props;
  const { terminal_status: status, total_latency_ms: latency } = entry.outcome;

  return (
    <div className="flex flex-wrap items-center gap-2">
      <span data-testid={`activity-status-${entry.request_id}`}>
        {status === null ? (
          <Badge tone="unknown" icon="circle-help">
            No attempt recorded
          </Badge>
        ) : (
          <Badge
            tone={status === "success" ? "healthy" : status === "unknown" ? "unknown" : "warning"}
            icon="activity"
          >
            {status}
          </Badge>
        )}
      </span>
      <span className="vn-caption" data-testid={`activity-latency-${entry.request_id}`}>
        {latency === null ? "Latency unknown" : `${latency} ms`}
      </span>
    </div>
  );
}

/** One recent-activity row. */
function ActivityRow(props: { entry: RouteDecisionEntry; onOpenRequest?: (requestID: string) => void }) {
  const { entry, onOpenRequest } = props;
  const chosen = entry.chosen;
  const clamped = entry.thinking.tier_clamped || entry.thinking.certified_clamped;

  return (
    <div
      className="flex flex-col gap-2 border-b border-border-subtle pb-2 last:border-b-0 last:pb-0"
      data-testid={`activity-${entry.request_id}`}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          {["lite", "pro", "max"].includes(entry.tier) ? (
            <TierBadge tier={entry.tier as DSTier} />
          ) : (
            <Badge tone="unknown" icon="circle-help">
              Unrecognized tier · {entry.tier}
            </Badge>
          )}
          {chosen.provider_id === null || chosen.provider_model_id === null ? (
            // Every candidate was excluded. An empty-string provider here would
            // read as a real, nameless route.
            <Badge tone="warning" icon="circle-slash">
              No route chosen
            </Badge>
          ) : (
            <span className="vn-mono-sm">
              {chosen.provider_id} · {chosen.provider_model_id}
            </span>
          )}
          {chosen.funding === null ? (
            <Badge tone="unknown" icon="circle-help">
              Funding unknown
            </Badge>
          ) : (
            <Badge tone="info" icon="wallet">
              {chosen.funding}
            </Badge>
          )}
        </div>
        {/* P6-UI-008 owns the per-request detail view. This links by request id
            to the real diagnostics path (so it is a shareable URL and opens on
            refresh) and asks the shell to route client-side on click. */}
        <Link
          href={pathForRoute("diagnostics", entry.request_id)}
          data-testid={`activity-link-${entry.request_id}`}
          onClick={
            onOpenRequest
              ? (e) => {
                  e.preventDefault();
                  onOpenRequest(entry.request_id);
                }
              : undefined
          }
        >
          Explain
        </Link>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <ActivityOutcome entry={entry} />
        <span className="vn-caption">{new Date(entry.created_at).toLocaleString()}</span>
        {clamped ? (
          <Badge tone="info" icon="scissors">
            Thinking clamped · {entry.thinking.requested ?? "unknown"} &rarr;{" "}
            {entry.thinking.applied ?? "unknown"}
          </Badge>
        ) : null}
      </div>
    </div>
  );
}

/**
 * The Overview surface (P6-UI-001, 07 §6): the at-a-glance operator home,
 * composed from the read models the other P6 surfaces already use.
 *
 * TWO structural commitments.
 *
 * First, per-card fetch isolation. Every card loads independently and renders its
 * own loading / empty / error state, so one failing read model degrades one card
 * instead of blanking the operator's home page. A shared Promise.all would turn
 * any single 500 into a fully broken page.
 *
 * Second, the recent-activity row tells the truth about nulls. A decision with no
 * attempt rows reports "No attempt recorded", never `success`; an unknown total
 * latency reports "Latency unknown", never `0 ms`; a decision that chose nothing
 * reports "No route chosen", never a nameless provider. Each of those wrong
 * renderings would describe a request outcome that did not happen.
 *
 * Nothing here re-derives a truth the API stated: account health is the server's
 * `display_status`, and the tier list comes from the policy read rather than from
 * a literal list of three names.
 */
export default function OverviewSurface(props: OverviewSurfaceProps) {
  const { onSessionExpired, onOpenQuickStart, onOpenRequest } = props;

  const providers = useCardData<Provider[]>(() => listProviders(), onSessionExpired);
  const accounts = useCardData<AccountProjection[]>(() => fetchAllAccounts(), onSessionExpired);
  const models = useCardData<ModelGroup[]>(
    () => listModelGroups({ limit: 200 }).then((page) => page.groups),
    onSessionExpired,
  );
  const tiers = useCardData<TierPolicy[]>(() => getRoutingPolicy(), onSessionExpired);
  const activity = useCardData<RouteDecisionEntry[]>(
    () => listRouteDecisions({ limit: ACTIVITY_LIMIT }).then((page) => page.decisions),
    onSessionExpired,
  );

  // The fleet card needs BOTH reads, so it reports whichever failed — it never
  // shows a provider count next to a silently-missing account count.
  const fleet: CardState<{ providers: Provider[]; accounts: AccountProjection[] }> = {
    data:
      providers.data && accounts.data
        ? { providers: providers.data, accounts: accounts.data }
        : null,
    error: providers.error ?? accounts.error,
  };

  return (
    <div className="flex flex-col gap-4">
      {/* The same banner the Models surface renders (P6-UI-012). Its own
          all-clear/error states are its business, not this page's. */}
      <ReviewQueueBanner
        onSessionExpired={onSessionExpired}
        // Overview has no catalog to filter; the banner's action is a no-op hint
        // here, and the banner only offers it when a backlog exists.
        onReviewBacklog={() => {}}
      />

      {onOpenQuickStart ? (
        <Card data-testid="overview-quick-start">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex flex-col gap-1">
              <span className="vn-title-sub">Connect a client</span>
              <span className="vn-caption">
                Create a key, point your editor or SDK at this router, and watch the requests arrive.
              </span>
            </div>
            <Button variant="secondary" onClick={onOpenQuickStart}>
              Open quick start
            </Button>
          </div>
        </Card>
      ) : null}

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <OverviewCard
          testId="overview-card-fleet"
          title="Provider fleet"
          state={fleet}
          loadingLabel="Loading the provider fleet…"
          errorTitle="Could not load the provider fleet"
        >
          {({ providers: provs, accounts: accts }) => {
            const connected = accts.filter((a) => a.connection_state === "connected");
            const withAccounts = new Set(accts.map((a) => a.provider));
            return (
              <StatCard
                label="Connected accounts"
                value={String(connected.length)}
                meta={`${withAccounts.size} of ${provs.length} providers in use`}
                icon="server"
                tone={connected.length > 0 ? "healthy" : "unknown"}
              />
            );
          }}
        </OverviewCard>

        <OverviewCard
          testId="overview-card-health"
          title="Account health"
          state={accounts}
          loadingLabel="Loading account health…"
          errorTitle="Could not load account health"
        >
          {(accts) => {
            if (accts.length === 0) {
              return <span className="vn-caption">No connected accounts yet.</span>;
            }
            // Grouped by the SERVER's derived display_status, rendered verbatim.
            const counts = new Map<string, number>();
            for (const a of accts) counts.set(a.display_status, (counts.get(a.display_status) ?? 0) + 1);
            return (
              <div className="flex flex-wrap items-center gap-2">
                {[...counts.entries()].map(([status, count]) => (
                  <span key={status} className="flex items-center gap-1">
                    <AccountStatus status={status as DSDisplayStatus} />
                    <span className="vn-mono-sm">{count}</span>
                  </span>
                ))}
              </div>
            );
          }}
        </OverviewCard>

        <OverviewCard
          testId="overview-card-quota"
          title="Quota windows"
          state={accounts}
          loadingLabel="Loading quota…"
          errorTitle="Could not load quota"
        >
          {(accts) => {
            const windows = accts.flatMap((a) => a.quota);
            const blocking = windows.filter((w) => w.state === "exhausted" || w.state === "insufficient");
            const unknown = windows.filter((w) => w.state === "unknown" || w.state === "stale");
            return (
              <div className="flex flex-col gap-1">
                <StatCard
                  label="Tracked windows"
                  value={String(windows.length)}
                  meta={`${blocking.length} blocking`}
                  icon="gauge"
                  tone={blocking.length > 0 ? "warning" : "healthy"}
                />
                {/* `unknown` never means unlimited (04 §5) and is counted apart
                    from "available" rather than folded into it. */}
                <span className="vn-caption">
                  {unknown.length} window{unknown.length === 1 ? "" : "s"} unknown or stale — unknown
                  quota is not unlimited.
                </span>
              </div>
            );
          }}
        </OverviewCard>

        <OverviewCard
          testId="overview-card-models"
          title="Model catalog"
          state={models}
          loadingLabel="Loading the model catalog…"
          errorTitle="Could not load the model catalog"
        >
          {(groups) => (
            <StatCard
              label="Canonical models"
              value={String(groups.length)}
              meta={`${groups.reduce((n, g) => n + g.offerings.length, 0)} offerings`}
              icon="box"
              tone={groups.length > 0 ? "healthy" : "unknown"}
            />
          )}
        </OverviewCard>

        <OverviewCard
          testId="overview-card-tiers"
          title="Tiers"
          state={tiers}
          loadingLabel="Loading the tier policy…"
          errorTitle="Could not load the tier policy"
        >
          {(policies) => (
            <div className="flex flex-col gap-2">
              {/* The tiers come from the POLICY READ. A literal list of three
                  names would keep claiming a tier the engine no longer serves. */}
              {policies.map((p) => (
                <div key={p.tier} className="flex flex-wrap items-center justify-between gap-2">
                  {["lite", "pro", "max"].includes(p.tier) ? (
                    <TierBadge tier={p.tier as DSTier} />
                  ) : (
                    <Badge tone="unknown" icon="circle-help">
                      {p.tier}
                    </Badge>
                  )}
                  <span className="vn-caption">
                    {unknownOr(p.context_ceiling_tokens ?? null, (v) => `${v.toLocaleString()} tok`)} ·{" "}
                    {p.thinking_ceiling ?? "unknown"} thinking
                  </span>
                </div>
              ))}
            </div>
          )}
        </OverviewCard>
      </div>

      <OverviewCard
        testId="overview-card-activity"
        title="Recent activity"
        state={activity}
        loadingLabel="Loading recent activity…"
        errorTitle="Could not load recent activity"
      >
        {(entries) =>
          entries.length === 0 ? (
            <span className="vn-caption">
              No requests yet — nothing has been routed through this router.
            </span>
          ) : (
            <div className="flex flex-col gap-3">
              {entries.map((entry) => (
                <ActivityRow key={entry.decision_id} entry={entry} onOpenRequest={onOpenRequest} />
              ))}
            </div>
          )
        }
      </OverviewCard>
    </div>
  );
}
