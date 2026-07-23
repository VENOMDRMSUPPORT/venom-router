// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { CapabilityIcon, CapabilityTruthBadge, ProbeStatus, ModelCapabilitySet } from "../src/index";

function Card() {

  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>capability truth (orthogonal to certification state)</div>
        <div className="row">{["unknown","supported","unsupported"].map(t => <CapabilityTruthBadge key={t} truth={t} />)}</div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>per-operation chips (unknown = dashed evidence gap; unsupported = quiet confirmed absence)</div>
        <ModelCapabilitySet truths={{ chat: "supported", streaming: "supported", tools: "unknown", structured_output: "unsupported", vision: "unknown", reasoning: "supported", context_window: "supported" }} />
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>probe execution (separate layer — infra failures NEVER flip a capability to unsupported)</div>
        <div className="row">{["pending","running","succeeded","inconclusive","retryable_failure","terminal_failure"].map(s => <ProbeStatus key={s} state={s} />)}</div>
      </div>
      <p className="vn-caption">429/timeout/5xx ⇒ truth stays unknown, probe rescheduled. 401/403 ⇒ terminal_failure for the probe, truth unchanged. Only a genuine semantic rejection proves unsupported.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
