// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { QuotaWindowCard, LocalSafetyBudgetIndicator, QuotaUnknownState, ReservationStateBadge, ReconciliationStatus, MultiWindowQuotaSummary } from "../../src/index";

function Card() {

  return (
    <div className="stack">
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 12 }}>
        <QuotaWindowCard name="5-hour usage" windowKey="provider:five_hour" used={61} total={100} reserved={6} unit="%" resetIn="2h 14m" freshness="fresh" />
        <QuotaWindowCard name="7-day usage" windowKey="provider:seven_day" used={97} total={100} unit="%" state="insufficient" resetIn="2d 6h" freshness="stale" age="22m" />
        <QuotaWindowCard name="Credit balance" windowKey="provider:balance" used={48.2} total={50} unit="USD" state="exhausted" freshness="fresh" />
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
        <LocalSafetyBudgetIndicator concurrencyUsed={1} concurrencyCap={1} consumptionUsed={140} consumptionCap={500} consumptionUnit="requests" />
        <QuotaUnknownState />
      </div>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
        <ReservationStateBadge state="reserved" />
        <ReservationStateBadge state="reconciliation_pending" />
        <ReservationStateBadge state="settled" confidence="low" />
        <ReservationStateBadge state="released" />
        <ReservationStateBadge state="unknown_consumption" />
      </div>
      <ReconciliationStatus state="reconciliation_pending" attemptId="req_01J9ZK4T7Q · attempt 2" windows={["provider:five_hour", "local:concurrency"]} attempts="3/5" nextRetry="28m" onResync={() => {}} onAcceptEstimate={() => {}} />
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
