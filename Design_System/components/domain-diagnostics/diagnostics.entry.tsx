// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { TypedErrorDisplay, TraceId, DiagnosticEventRow, EvidenceList, AuditEventTimeline, JobStatus, JobProgress, RawPayloadDisclosure } from "../../src/index";

function Card() {

  return (
    <div className="stack">
      <TypedErrorDisplay code="venom_no_eligible_offering" retryable retryAfter="41s" requestId="req_01J9ZK4T7Q"
        message="No certified, funded, capable route is currently available for venom/lite. Earliest cooldown expires in 41s." />
      <div className="row">
        <TraceId id="req_01J9ZK4T7Q" />
        <JobStatus state="running" kind="probe" jobId="job_8842" />
        <JobStatus state="failed" kind="restore" error="wrong_passphrase" />
        <JobStatus state="expired" kind="discovery" />
      </div>
      <JobProgress state="running" kind="discovery" jobId="job_9101" progress={64} detail="41 models normalized · generation 42" />
      <DiagnosticEventRow time="14:03:11Z" kind="route_attempt" scope="offering" code="rate_limit" detail="codex : gpt-5.2 — cooldown 41s applied at offering scope" latency="1,204 ms" tone="warning" />
      <EvidenceList items={[
        { field: "context_length", value: "1,000,000", mono: true, source: "probe", confidence: 0.98, observedAt: "2026-07-22" },
        { field: "cost.input", value: null, source: "models_dev", confidence: 0.4, exactMatch: false, observedAt: "2026-07-20" },
      ]} />
      <AuditEventTimeline events={[
        { action: "credential_reveal", summary: "Owner revealed claude-code credential", at: "2026-07-22 14:01:55Z", tone: "warning" },
        { action: "funding_override", summary: "opencode-zen account set to free (reason: verified plan)", at: "2026-07-21 18:22:03Z", tone: "accent" },
        { action: "usage_gap", summary: "Reservation req_01J9Z…/3 reached unknown_consumption", at: "2026-07-21 11:02:44Z", tone: "critical" },
      ]} />
      <RawPayloadDisclosure label="Discovery evidence (sanitized)" redactedFields={["authorization", "api_key"]}
        payload={'{\n  "model": "glm-5-free",\n  "context_length": 262144,\n  "authorization": "[REDACTED]"\n}'} />
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
