// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { CertificationStateBadge, CertificationTimeline } from "../src/index";

function Card() {

  const states = ["discovered","observed","probing","certified","suspended","expired"];
  const truths = ["unknown","supported","unsupported"];
  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>the six states (there is NO rejected state)</div>
        <div className="row">
          {states.map(s => <CertificationStateBadge key={s} state={s} />)}
          <CertificationStateBadge state="suspended" reason="credential_blocked" />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>lifecycle positions</div>
        <div style={{display:"flex",flexDirection:"column",gap:6}}>
          <CertificationTimeline state="observed" />
          <CertificationTimeline state="certified" />
          <CertificationTimeline state="suspended" />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>routability — the full 6 × 3 Cartesian (mirrors the CI-blocking test)</div>
        <table aria-label="Certification state by capability truth routability matrix">
          <thead><tr><th>state \ truth</th>{truths.map(t => <th key={t}>{t}</th>)}</tr></thead>
          <tbody>
            {states.map(s => (
              <tr key={s}><th style={{textAlign:"left"}}>{s}</th>
                {truths.map(t => {
                  const r = s === "certified" && t === "supported";
                  return <td key={t} data-r={String(r)}>{r ? "ROUTABLE" : "not routable"}</td>;
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="vn-caption">certified + unknown reads "not routable yet" — never visually equate certified with all-capabilities-supported.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
