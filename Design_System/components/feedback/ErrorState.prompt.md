Written, actionable failure view. Pairs with `EmptyState` (nothing to show, not a failure) and `TypedErrorDisplay` (the inline diagnostics trace envelope). Full-page variant replaces a whole page/section; inline variant sits inside a Panel/Card. Never use the copy "Something went wrong" — say what failed and what the operator can do.

```jsx
<ErrorState
  variant="page"
  code="provider_unavailable"
  title="Providers failed to load"
  description="The control API did not respond. Check that the Venom process is running on 127.0.0.1:8081."
  traceId="req_01J9ZK4T7Q"
  onRetry={() => refetch()}
  secondaryAction={{ label: "Open diagnostics", icon: "activity", onClick: () => go("diagnostics") }}
  details={"GET /api/providers -> 000 (connection refused)"}
/>
```

Inline, no retry (terminal failure), still written and typed:

```jsx
<ErrorState
  variant="inline"
  code="venom_no_eligible_offering"
  title="No route available for venom/max"
  description="Every candidate was excluded — see the routing trace for typed reasons."
  secondaryAction={{ label: "View trace", onClick: () => go("diagnostics") }}
/>
```
