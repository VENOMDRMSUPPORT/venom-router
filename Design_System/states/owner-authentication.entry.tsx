// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { OwnerSessionStatus, SessionExpiryWarning, Badge, TypedErrorDisplay } from "../src/index";

function Card() {

  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>session states (single owner — no users, roles, or teams)</div>
        <div className="row">
          <Badge tone="accent" icon="shield">first-run setup</Badge>
          <OwnerSessionStatus state="unauthenticated" />
          <OwnerSessionStatus state="active" idleIn="24m" absoluteIn="9h" />
          <OwnerSessionStatus state="idle_warning" idleIn="2m" />
          <OwnerSessionStatus state="expired" />
          <OwnerSessionStatus state="absolute_expiry" />
          <OwnerSessionStatus state="revoked" />
          <OwnerSessionStatus state="reverification_required" />
          <OwnerSessionStatus state="reverified" reverifiedFor="4m 58s" />
          <OwnerSessionStatus state="locked_out" retryAfter="12m" />
          <Badge tone="info" icon="archive-restore">recovery: restore or local reset</Badge>
        </div>
      </div>
      <SessionExpiryWarning kind="idle" inTime="2m" onContinue={() => {}} />
      <div style={{height:8}}></div>
      <TypedErrorDisplay tone="critical" code="invalid_credentials" retryable message="The password is incorrect. After 5 consecutive failures within 15 minutes, sign-in is rate-limited." />
      <p className="vn-caption">Login errors are generic (never reveal setup status); no attempted secret is ever stored. Re-verification freshness is exactly 5 minutes; password change revokes all sessions.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
