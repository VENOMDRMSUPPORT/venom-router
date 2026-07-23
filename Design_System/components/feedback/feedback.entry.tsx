// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { Alert, Banner, Toast, EmptyState, ErrorState, Spinner, Progress, Meter, Skeleton, Button } from "../../src/index";

function Card() {

  return (
    <div className="stack">
      <Banner tone="warning" actions={<Button size="sm">How to configure</Button>}>
        Setup required — missing environment variable: <span className="vn-code-inline">VENOM_ANTIGRAVITY_CLIENT_SECRET</span>
      </Banner>
      <div className="cols">
        <div className="stack">
          <Alert tone="critical" title="Restore failed" code="wrong_passphrase">The container could not be decrypted. The live state is untouched.</Alert>
          <Alert tone="unknown" title="No quota evidence">This provider exposes no quota endpoint. Execution is bounded by the local safety budget.</Alert>
          <Toast tone="healthy" title="Backup created" detail="venom-2026-07-22.vbk · 4.2 MB" onDismiss={() => {}} />
        </div>
        <div className="stack">
          <EmptyState icon="server" title="No providers connected" description="Connect a provider account to start account-scoped discovery." action={<Button variant="primary" size="sm" icon="plus">Connect provider</Button>} />
        </div>
      </div>
      <div className="cols">
        <ErrorState variant="inline" code="provider_unavailable" title="Providers failed to load"
          description="The control API did not respond." traceId="req_01J9ZK4T7Q"
          onRetry={() => {}} secondaryAction={{ label: "Diagnostics", icon: "activity", onClick: () => {} }}
          details={"GET /api/providers -> 000 (connection refused)"} />
        <ErrorState variant="inline" code="venom_no_eligible_offering" title="No route available for venom/max"
          description="Every candidate was excluded — see the routing trace for typed reasons." />
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "max-content 1fr 1fr 1fr 1fr", gap: 14, alignItems: "center" }}>
        <Spinner label="Refreshing" />
        <Progress value={3} max={5} label="Probing operations" />
        <div><Meter value={82} max={100} label="5-hour window" /><span className="vn-caption">82% · warning ≥75</span></div>
        <div><Meter state="unknown" label="Provider quota" /><span className="vn-caption">unknown — hatched, never a number</span></div>
        <div><Meter state="unavailable" label="Not metered" /><span className="vn-caption">unavailable — no meter concept here</span></div>
      </div>
      <div style={{ display: "flex", gap: 8 }}>
        <Skeleton width={180} height={14} /><Skeleton width={90} height={14} /><Skeleton width={240} height={14} />
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
