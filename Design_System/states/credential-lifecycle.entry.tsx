// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { CredentialKindBadge, ReauthenticationStatus } from "../src/index";

function Card() {

  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>credential kinds (one ACTIVE per (account, kind); different kinds coexist)</div>
        <div className="row">
          <CredentialKindBadge kind="api_key" />
          <CredentialKindBadge kind="oauth2" expiresAt="54m" />
          <CredentialKindBadge kind="github_oauth" />
          <CredentialKindBadge kind="copilot_service" expiresAt="24m" />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>credential states (staged coexists with active during reauth; ≤1 staged per kind)</div>
        <div className="row">
          <CredentialKindBadge kind="oauth2" state="active" />
          <CredentialKindBadge kind="oauth2" state="staged" />
          <CredentialKindBadge kind="oauth2" state="retired" />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>reauthentication (staged-credential flow — old credential intact until the atomic swap)</div>
        <div className="row">{["idle","staged","validating","swapping","successful","failed","rollback","interrupted"].map(s => <ReauthenticationStatus key={s} state={s} />)}</div>
      </div>
      <p className="vn-caption">A second reauth for the same (account, kind) is rejected with <span className="vn-code-inline">reauthentication_in_progress</span>; identity mismatch returns <span className="vn-code-inline">account_identity_mismatch</span> with the old credential untouched.</p>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
