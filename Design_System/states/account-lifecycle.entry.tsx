// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { ConnectionStateBadge, HealthStateBadge, AccountStatus } from "../src/index";

function Card() {

  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>connection_state (persisted axis)</div>
        <div className="row">{["connecting","connected","stopped","disconnected"].map(s => <ConnectionStateBadge key={s} state={s} />)}</div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>health_state (observed axis — meaningful only while connected)</div>
        <div className="row">{["unknown","healthy","degraded","unavailable","expired"].map(s => <HealthStateBadge key={s} state={s} />)}</div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>derived display_status (first match wins — never persisted)</div>
        <div className="row">
          {["disconnected","stopped","connecting","reauthenticating"].map(s => <AccountStatus key={s} status={s} />)}
          <AccountStatus status="cooling_down" retryAfter="4m 12s" />
          {["expired","unavailable","degraded","healthy","unknown"].map(s => <AccountStatus key={s} status={s} />)}
        </div>
      </div>
      <p className="vn-caption">Soft disconnect is V1's only delete — disconnected renders as an inactive fact (restorable via re-enrollment), never as an error. Invalid transitions are rejected and audited, never rendered.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
