// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { OwnerSessionStatus, SessionExpiryWarning, SecretRevealControl, APIKeyPrefix, APIKeyCreationResult, BackupStatus, RestoreStatus } from "../../src/index";

function Card() {

  const [revealed, setRevealed] = React.useState(false);
  return (
    <div className="stack">
      <div className="row">
        <OwnerSessionStatus state="active" idleIn="24m" absoluteIn="9h 12m" />
        <OwnerSessionStatus state="idle_warning" idleIn="2m" />
        <OwnerSessionStatus state="reverified" reverifiedFor="4m 12s" />
        <OwnerSessionStatus state="reverification_required" />
        <OwnerSessionStatus state="locked_out" retryAfter="12m" />
      </div>
      <SessionExpiryWarning kind="idle" inTime="2m" onContinue={() => {}} />
      <div className="row">
        <SecretRevealControl masked="sk-ant-••••••••••••" secret="sk-ant-example-not-a-real-secret" revealed={revealed} onRevealRequest={() => setRevealed(true)} onHide={() => setRevealed(false)} label="credential" />
        <SecretRevealControl masked="ghu_••••••••" blocked onRevealRequest={() => {}} label="credential" />
        <APIKeyPrefix prefix="vk_live_3f8a" />
      </div>
      <div className="row">
        <BackupStatus state="completed" artifact="venom-2026-07-22.vbk" at="2026-07-22 09:14Z" />
        <RestoreStatus state="failed" code="wrong_passphrase" />
        <RestoreStatus state="verifying" />
      </div>
      <APIKeyCreationResult rawKey="vk_live_3f8a-EXAMPLE-FIXTURE-0000-not-real" keyLabel="ide-clients" />
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
