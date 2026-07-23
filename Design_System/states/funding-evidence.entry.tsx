// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { FundingBadge, FundingSourceIndicator, FundingEvidenceIndicator } from "../src/index";

function Card() {

  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>funding classification (account fact — offerings inherit; never provider-level)</div>
        <div className="row">
          <FundingBadge funding="free" source="provider_evidence" />
          <FundingBadge funding="paid" plan="Max" source="provider_evidence" />
          <FundingBadge funding="unknown" source="provider_policy" />
          <FundingBadge funding="paid" conflicting source="provider_evidence" />
          <FundingBadge funding="free" stale source="provider_evidence" />
          <FundingBadge funding="free" source="owner_override" />
          <FundingBadge funding="paid" locked source="provider_policy" plan="Credits" />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>evidence source (exactly four values — verbatim)</div>
        <div className="row">{["provider_policy","provider_evidence","owner_policy","owner_override"].map(s => <FundingSourceIndicator key={s} source={s} />)}</div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>current evidence row</div>
        <FundingEvidenceIndicator funding="unknown" source="provider_policy" confidence={0.3} observedAt="2026-07-22 09:14Z" />
      </div>
      <p className="vn-caption">evidence_required providers stamp unknown/provider_policy (overridable). Unknown ≠ free — excluded from all routing until classified. Locked rejects override (<span className="vn-code-inline">funding_locked</span>); owner overrides are never auto-superseded and always distinguishable from provider evidence.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
