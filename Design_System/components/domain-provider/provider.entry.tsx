// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { ProviderSummaryCard, ProviderAccountRow, AccountIdentity, AccountStatus, FundingBadge, CredentialKindBadge, ReauthenticationStatus, FundingEvidenceIndicator, IconButton, Meter } from "../../src/index";

function Card() {

  return (
    <div className="stack">
      <ProviderSummaryCard name="Claude Code" slug="claude-code" authMode="oauth2" accountCount={2} healthyCount={1} verification="proven"
        actions={<IconButton icon="plus" label="Connect another account" variant="secondary" />}>
        <ProviderAccountRow
          identity={<AccountIdentity name="ops@venom.local" externalId="acct_9f2e11c4" plan="Max" />}
          status={<AccountStatus status="healthy" />}
          funding={<FundingBadge funding="paid" plan="Max" source="provider_evidence" />}
          quota={<div style={{ width: 120 }}><Meter value={61} max={100} label="5-hour window" /></div>}
          actions={<IconButton icon="ellipsis" label="Account actions" size="sm" />} />
        <ProviderAccountRow
          identity={<AccountIdentity name="lab@venom.local" externalId="acct_77aa02b9" plan="Pro" />}
          status={<AccountStatus status="cooling_down" retryAfter="4m 12s" />}
          funding={<FundingBadge funding="paid" plan="Pro" source="provider_evidence" />}
          quota={<div style={{ width: 120 }}><Meter value={97} max={100} label="7-day window" /></div>}
          actions={<IconButton icon="ellipsis" label="Account actions" size="sm" />} />
      </ProviderSummaryCard>
      <ProviderSummaryCard name="Antigravity" slug="antigravity" authMode="oauth2" accountCount={0} verification="proven" setupRequired missingEnv={["VENOM_ANTIGRAVITY_CLIENT_SECRET", "VENOM_ANTIGRAVITY_CLIENT_ID"]} />
      <div style={{ display: "flex", gap: 10, flexWrap: "wrap", alignItems: "center" }}>
        <CredentialKindBadge kind="github_oauth" />
        <CredentialKindBadge kind="copilot_service" expiresAt="24m" />
        <CredentialKindBadge kind="oauth2" state="staged" />
        <ReauthenticationStatus state="validating" />
        <FundingEvidenceIndicator funding="free" source="owner_override" confidence={1} observedAt="2026-07-21" />
        <FundingBadge funding="paid" locked source="provider_policy" plan="Credits" />
        <FundingBadge funding="unknown" source="provider_policy" />
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
