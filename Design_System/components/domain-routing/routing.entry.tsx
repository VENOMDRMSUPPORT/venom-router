// Card content for the sibling .html (loaded as `<script type="module">`). Hand-edit
// directly — this is real source, not a build artifact.
import * as React from "react";
import { createRoot } from "react-dom/client";
import { TierBadge, RouteDecisionTrace, CompetitiveBandIndicator, FallbackChain, FundingMixIndicator, QuotaFairnessIndicator, WorkloadProfileBadge, CooldownBadge, CircuitBreakerState } from "../../src/index";

function Card() {

  return (
    <div className="stack">
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <TierBadge tier="lite" /><TierBadge tier="pro" /><TierBadge tier="max" showId />
        <WorkloadProfileBadge properties={["structured", "vision"]} />
        <CooldownBadge scope="offering" retryAfter="41s" />
        <CircuitBreakerState state="half_open" scope="provider:antigravity" />
      </div>
      <RouteDecisionTrace candidates={[
        { route: "claude-code : sonnet-4-5 : paid", funding: "paid", quality: 0.91, score: 0.84, outcome: "chosen", clamp: "ultra→extended" },
        { route: "opencode-zen : glm-5 : free", funding: "free", quality: 0.88, score: 0.80, outcome: "in_band" },
        { route: "codex : gpt-5.2 : paid", funding: "paid", quality: 0.79, score: null, outcome: "excluded", reasons: ["quota_exhausted", "cooling_down"] },
        { route: "agnes-ai : kimi-k2.5", funding: "unknown", quality: null, score: null, outcome: "excluded", reasons: ["funding_unknown", "context_unverified"] },
      ]} />
      <CompetitiveBandIndicator tier="pro" top={0.91} candidates={[
        { name: "sonnet-4-5", quality: 0.91 }, { name: "glm-5", quality: 0.88 }, { name: "gpt-5.2", quality: 0.79 },
      ]} />
      <FallbackChain budget={4} attempts={[
        { route: "codex : gpt-5.2", outcome: "failed", code: "rate_limit" },
        { route: "claude-code : sonnet-4-5", outcome: "succeeded" },
      ]} />
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 14 }}>
        <FundingMixIndicator paidShare={0.22} bucket="standard" sample={2000} />
        <QuotaFairnessIndicator accounts={[
          { name: "ops@venom.local", weight: 0.5, realized: 0.47 },
          { name: "lab@venom.local", weight: 0.3, realized: 0.34 },
          { name: "dev@venom.local", weight: 0.2, realized: 0.19, saturated: true },
        ]} />
      </div>
    </div>
  );

}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root not found");
createRoot(rootEl).render(<Card />);
