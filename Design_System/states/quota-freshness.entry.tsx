// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { QuotaWindowMeter, QuotaFreshnessBadge, MultiWindowQuotaSummary } from "../src/index";

function Card() {

  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>per-window state (attempt takes the most restrictive)</div>
        <div style={{display:"grid",gridTemplateColumns:"repeat(5,1fr)",gap:10}}>
          <div><span className="vn-caption">available</span><QuotaWindowMeter used={38} total={100} unit="%" state="available" label="available" /></div>
          <div><span className="vn-caption">insufficient</span><QuotaWindowMeter used={92} total={100} unit="%" state="insufficient" label="insufficient" /></div>
          <div><span className="vn-caption">exhausted</span><QuotaWindowMeter used={100} total={100} unit="%" state="exhausted" label="exhausted" /></div>
          <div><span className="vn-caption">unknown</span><QuotaWindowMeter state="unknown" label="unknown" /></div>
          <div><span className="vn-caption">stale</span><QuotaWindowMeter state="stale" label="stale" /></div>
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>freshness (fresh renders no chrome)</div>
        <div className="row"><QuotaFreshnessBadge state="stale" age="22m" /><QuotaFreshnessBadge state="unknown" /><span className="vn-caption">stale (&gt;~15 min) is treated as unknown + background refresh</span></div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>multi-window rollup (window identity is mono: provider:* / rolling:* / local:*)</div>
        <MultiWindowQuotaSummary windows={[
          { windowKey: "provider:five_hour", used: 61, total: 100, unit: "%", state: "available", freshness: "fresh" },
          { windowKey: "provider:seven_day", used: 97, total: 100, unit: "%", state: "insufficient", freshness: "stale", age: "22m" },
          { windowKey: "rolling:60s", used: 12, total: 60, unit: "req", state: "available", freshness: "fresh" },
          { windowKey: "local:concurrency", used: 1, total: 1, unit: "in-flight", state: "exhausted", freshness: "fresh" },
        ]} />
      </div>
      <p className="vn-caption">Unknown provider quota never means unlimited and never skips a reservation — the local-safety windows always apply.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
