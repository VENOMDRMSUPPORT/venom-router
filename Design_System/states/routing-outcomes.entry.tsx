// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { CandidateRejectionReason, Badge, TierBadge, CooldownBadge, CircuitBreakerState, WorkloadProfileBadge, TypedErrorDisplay } from "../src/index";

function Card() {

  const codes = ["identity_unresolved","context_unverified","capability_not_certified","funding_unknown","no_healthy_account","quota_exhausted","quota_insufficient","cooling_down","account_stopped","account_disconnected","credential_expired","account_unavailable","reauth_in_progress"];
  return (
    <div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>typed exclusion reason codes (verbatim — never paraphrased)</div>
        <div className="row">{codes.map(c => <CandidateRejectionReason key={c} code={c} />)}
          <CandidateRejectionReason code="thinking clamp ultra→extended" blocking={false} />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>tier status</div>
        <div className="row">
          <TierBadge tier="lite" /><Badge tone="healthy" icon="circle-check">eligible</Badge>
          <TierBadge tier="pro" /><Badge tone="degraded" icon="triangle-alert">degraded · quota constrained</Badge>
          <TierBadge tier="max" /><Badge tone="critical" icon="ban">unroutable · certification constrained</Badge>
          <Badge tone="warning" icon="hourglass">cooldown constrained · earliest retry 41s</Badge>
          <Badge tone="critical" icon="ban">insufficient capability coverage: vision</Badge>
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>scoped cooldowns & circuit breakers</div>
        <div className="row">
          <CooldownBadge scope="account" retryAfter="41s" />
          <CooldownBadge scope="offering" retryAfter="8m" />
          <CooldownBadge scope="provider" retryAfter="90s" />
          <CircuitBreakerState state="closed" scope="account" />
          <CircuitBreakerState state="open" scope="provider:codex" reopensIn="3m 12s" cycle={2} />
          <CircuitBreakerState state="half_open" scope="offering" />
        </div>
      </div>
      <div className="sec"><div className="vn-overline" style={{marginBottom:6}}>public tier failure (Lite fails closed — never paid, never unknown)</div>
        <TypedErrorDisplay tone="warning" code="venom_free_capacity_exhausted" retryable retryAfter="6m 40s" requestId="req_01J9ZP8QXA"
          message="venom/lite free capacity is temporarily exhausted. Lite never falls back to paid or unknown-cost accounts." />
      </div>
      <div className="row"><WorkloadProfileBadge properties={["standard"]} /><WorkloadProfileBadge properties={["vision","structured"]} /><WorkloadProfileBadge properties={["tool_use","large_context"]} /></div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
