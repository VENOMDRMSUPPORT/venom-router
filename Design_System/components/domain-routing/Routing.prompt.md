Routing set: TierBadge/TierPolicySummary, score breakdowns, competitive band (Pro 0.08 / Max 0.03), typed rejection reasons, fallback chains, attempt timelines with reservation states, RouteDecisionTrace ("why this route?"), Pro FundingMixIndicator, Max QuotaFairnessIndicator, cooldowns and circuit breakers.

```jsx
<RouteDecisionTrace candidates={[
  {route:"claude-code : sonnet-4.5 : paid", funding:"paid", quality:0.91, score:0.84, outcome:"chosen"},
  {route:"opencode-zen : glm-5 : free", funding:"free", quality:0.88, score:0.80, outcome:"in_band"},
  {route:"agnes-ai : kimi-k2", funding:"unknown", outcome:"excluded", reasons:["funding_unknown"]},
]} />
```

Reasons render the typed codes verbatim; the chosen row is emphasized; clamp notes are info-level.
