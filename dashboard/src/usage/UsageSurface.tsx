import { useEffect, useState } from "react";
import { Badge, Card, EmptyState, ErrorState, Spinner, Table } from "@venom/design-system/primitives";
import { TierBadge, type Tier as DSTier } from "@venom/design-system/domain";
import {
  getUsage,
  isSessionExpired,
  toApiError,
  type AuthApiError,
  type UsageAggregate,
  type UsageGroup,
  type UsageMetric,
} from "../api/controlClient";

export interface UsageSurfaceProps {
  onSessionExpired: () => void;
}

const DS_TIERS = new Set<string>(["lite", "pro", "max"]);

/**
 * Renders one metric cell.
 *
 * THIS FUNCTION IS THE CARD'S REAL CONTENT. `/usage` reports a sum, how many rows
 * reported a value, and how many did not — and the three cases below must stay
 * visibly distinct:
 *
 *   sum === null            nothing was measured. Rendered as "unknown", NEVER 0,
 *                          because 0 claims a measured absence of consumption.
 *   unknown_count > 0       partially measured. The number is a FLOOR, and it is
 *                          labelled "at least" plus the shortfall, so nobody reads
 *                          a floor as a total.
 *   unknown_count === 0     fully measured. The number stands on its own.
 */
function MetricCell(props: { metric: UsageMetric; unit: string; testId: string }) {
  const { metric, unit, testId } = props;
  const total = metric.known_count + metric.unknown_count;

  if (metric.sum === null) {
    return (
      <span className="flex flex-col gap-1" data-testid={testId}>
        <Badge tone="unknown" icon="circle-help">
          unknown
        </Badge>
        {metric.unknown_count > 0 ? (
          <span className="vn-caption">
            none of {metric.unknown_count} request{metric.unknown_count === 1 ? "" : "s"} reported a
            value
          </span>
        ) : null}
      </span>
    );
  }

  const partial = metric.unknown_count > 0;
  return (
    <span className="flex flex-col gap-1" data-testid={testId}>
      <span className="vn-tabular">
        {partial ? "at least " : ""}
        {metric.sum.toLocaleString()} {unit}
      </span>
      {partial ? (
        <span className="vn-caption">
          a floor, not a total — {metric.unknown_count} of {total} requests reported no value
        </span>
      ) : (
        <span className="vn-caption">
          {metric.average === null ? "no average" : `avg ${metric.average.toFixed(1)} ${unit}`}
        </span>
      )}
    </span>
  );
}

/** One grouping table. A null key is the UNATTRIBUTED bucket, labelled as such
 * rather than blanked — the rows are real consumption that belongs to no account or
 * model. */
function UsageTable(props: { dimension: string; label: string; groups: UsageGroup[] }) {
  const { dimension, label, groups } = props;

  if (groups.length === 0) {
    return (
      <span className="vn-caption">No {label.toLowerCase()} has any recorded usage in this window.</span>
    );
  }

  return (
    <div className="overflow-x-auto">
      <Table label={`Usage by ${label.toLowerCase()}`}>
        <thead>
          <tr>
            <th scope="col">{label}</th>
            <th scope="col">Requests</th>
            <th scope="col">Tokens in</th>
            <th scope="col">Tokens out</th>
            <th scope="col">Latency</th>
          </tr>
        </thead>
        <tbody>
          {groups.map((g) => {
            const key = g.key ?? "__unattributed";
            return (
              <tr key={key} data-testid={`usage-${dimension}-${key}`}>
                <th scope="row">
                  {g.key === null ? (
                    <Badge tone="unknown" icon="circle-help">
                      unattributed
                    </Badge>
                  ) : dimension === "tier" && DS_TIERS.has(g.key) ? (
                    <TierBadge tier={g.key as DSTier} />
                  ) : (
                    <span className="vn-mono-sm">{g.key}</span>
                  )}
                </th>
                {/* A request count is a ROW count — always known. */}
                <td className="vn-tabular">{g.requests.toLocaleString()}</td>
                <td>
                  <MetricCell metric={g.tokens_in} unit="tokens" testId={`usage-${dimension}-${key}-tokens_in`} />
                </td>
                <td>
                  <MetricCell metric={g.tokens_out} unit="tokens" testId={`usage-${dimension}-${key}-tokens_out`} />
                </td>
                <td>
                  <MetricCell metric={g.latency_ms} unit="ms" testId={`usage-${dimension}-${key}-latency_ms`} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </Table>
    </div>
  );
}

/**
 * The Usage & Analytics surface (P6-UI-005, 07 §6, 05 §4/§7).
 *
 * It renders the unknown counts `/usage` reports rather than smoothing them away.
 * Three states stay distinct, and confusing any two of them would misreport the
 * owner's consumption:
 *
 *   no traffic      — zero requests. Nothing happened.
 *   all unknown     — requests happened, and none reported a token count. Shown as
 *                     "unknown", never 0, which would say the traffic was free.
 *   partly unknown  — a number that is a FLOOR, labelled as one with the shortfall.
 *
 * There is no cost estimate anywhere: the API states no cost, and deriving one from
 * token counts would invent a price the router never charged.
 */
export default function UsageSurface(props: UsageSurfaceProps) {
  const { onSessionExpired } = props;

  const [usage, setUsage] = useState<UsageAggregate | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setLoadError(null);
      try {
        const result = await getUsage();
        if (cancelled) return;
        setUsage(result);
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
  }, [reloadToken, onSessionExpired]);

  if (loadError) {
    // An error is its own state: falling through to the empty state would say
    // "you have no usage" when the truth is "we could not ask".
    return (
      <ErrorState
        code={loadError.code}
        title="Could not load usage"
        description={loadError.message}
        onRetry={() => setReloadToken((t) => t + 1)}
      />
    );
  }

  if (usage === null) {
    return <Spinner label="Loading usage…" />;
  }

  // NO TRAFFIC is distinct from ALL-UNKNOWN: zero requests means nothing happened,
  // whereas requests with no token data is real traffic that was not measured.
  if (usage.totals.requests === 0) {
    return (
      <EmptyState
        icon="chart-line"
        title="No usage recorded yet"
        description="No request has been routed through the public /v1 surface in this window, so there is nothing to account for."
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <Card data-testid="usage-totals">
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="vn-h3">Totals</h3>
            {usage.truncated ? (
              <Badge tone="warning" icon="triangle-alert">
                Scan capped at {usage.limit} rows — every number below is a floor
              </Badge>
            ) : null}
          </div>
          <div className="flex flex-wrap items-start gap-6">
            <span className="flex flex-col gap-1">
              <span className="vn-caption">Requests</span>
              <span className="vn-tabular">{usage.totals.requests.toLocaleString()}</span>
            </span>
            <span className="flex flex-col gap-1">
              <span className="vn-caption">Tokens in</span>
              <MetricCell metric={usage.totals.tokens_in} unit="tokens" testId="usage-total-tokens_in" />
            </span>
            <span className="flex flex-col gap-1">
              <span className="vn-caption">Tokens out</span>
              <MetricCell metric={usage.totals.tokens_out} unit="tokens" testId="usage-total-tokens_out" />
            </span>
            <span className="flex flex-col gap-1">
              <span className="vn-caption">Latency</span>
              <MetricCell metric={usage.totals.latency_ms} unit="ms" testId="usage-total-latency_ms" />
            </span>
          </div>
          <span className="vn-caption">
            {usage.window.from === null && usage.window.to === null
              ? "All recorded usage."
              : `Window ${usage.window.from ?? "start"} to ${usage.window.to ?? "now"} (epoch seconds).`}
          </span>
        </div>
      </Card>

      <Card data-testid="usage-by-tier">
        <div className="flex flex-col gap-3">
          <h3 className="vn-h3">By tier</h3>
          <UsageTable dimension="tier" label="Tier" groups={usage.by_tier} />
        </div>
      </Card>

      <Card data-testid="usage-by-account">
        <div className="flex flex-col gap-3">
          <h3 className="vn-h3">By account</h3>
          <UsageTable dimension="account" label="Account" groups={usage.by_account} />
        </div>
      </Card>

      <Card data-testid="usage-by-model">
        <div className="flex flex-col gap-3">
          <h3 className="vn-h3">By model</h3>
          <UsageTable dimension="model" label="Model" groups={usage.by_model} />
        </div>
      </Card>
    </div>
  );
}
