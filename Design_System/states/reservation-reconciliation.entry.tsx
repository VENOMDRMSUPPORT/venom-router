// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { ReservationStateBadge, ReconciliationStatus } from "../src/index";

function Card() {

  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>the five stored states (no stored "expired")</div>
        <div className="row">
          {["reserved","reconciliation_pending","settled","released","unknown_consumption"].map(s => <ReservationStateBadge key={s} state={s} />)}
          <ReservationStateBadge state="settled" confidence="low" />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>reconciliation queue (owner recovery surface)</div>
        <div style={{display:"flex",flexDirection:"column",gap:8}}>
          <ReconciliationStatus state="reconciliation_pending" attemptId="req_01J9ZK4T7Q · attempt 2" windows={["provider:five_hour","local:concurrency"]} attempts="3/5" nextRetry="28m" onResync={() => {}} onAcceptEstimate={() => {}} />
          <ReconciliationStatus state="unknown_consumption" attemptId="req_01J9YH2M8P · attempt 1" windows={["provider:balance"]} onResync={() => {}} />
        </div>
      </div>
      <p className="vn-caption">Janitor branches are discriminated by dispatched_at: never-dispatched past deadline → released; dispatched → reconciliation_pending (headroom stays debited, never auto-released); terminal retry boundary → unknown_consumption + usage_gap audit + re-baseline at next sync.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
