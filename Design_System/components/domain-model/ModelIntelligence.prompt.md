Model intelligence set: certification lifecycle (6 states, no "rejected"), capability truth chips, probe execution, provenance/confidence, context display, offering rows, and the RoutableIndicator conjunction.

```jsx
<ModelOfferingRow
  identity={<ModelIdentity name="Claude Sonnet 4.5" providerModelId="claude-sonnet-4-5" />}
  context={<ContextWindowDisplay tokens={1000000} verified />}
  capabilities={<ModelCapabilitySet truths={{chat:"supported",tools:"supported",vision:"unknown"}} />}
  certification={<CertificationStateBadge state="certified" />}
  routable={<RoutableIndicator state="certified" truths={{chat:"supported",vision:"unknown"}} required={["chat","vision"]} />}
/>
```

Never visually equate certified with "all capabilities supported" — always show the conjunction.
