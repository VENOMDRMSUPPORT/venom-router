// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { JobStatus, JobProgress } from "../src/index";

function Card() {

  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>the five canonical states (one shared GET /jobs/id surface)</div>
        <div className="row">
          <JobStatus state="pending" kind="discovery" />
          <JobStatus state="running" kind="probe" jobId="job_8842" />
          <JobStatus state="completed" kind="backup" />
          <JobStatus state="failed" kind="restore" error="wrong_passphrase" />
          <JobStatus state="expired" kind="benchmark" />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>polled progress (aria-live polite)</div>
        <div style={{display:"flex",flexDirection:"column",gap:10}}>
          <JobProgress state="running" kind="discovery" jobId="job_9101" progress={64} detail="41 models normalized · generation 42 · snapshot applies atomically only if still newest" />
          <JobProgress state="running" kind="probe" jobId="job_9102" detail="context-window probe · 1 in-flight per provider · cost-capped" />
        </div>
      </div>
      <p className="vn-caption">result_ref points at read models — never inline secrets or content. expired = never reached a terminal state within its TTL; terminal jobs are reaped after ~24h.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
