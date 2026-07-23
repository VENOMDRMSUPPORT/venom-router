Quota set: per-window cards/meters (provider-native units, reserved overlay), freshness, reset countdowns, the local-safety budget (dashed, explicitly local policy), reservation states, reconciliation queue rows, and the most-restrictive-wins account summary.

```jsx
<QuotaWindowCard name="5-hour usage" windowKey="provider:five_hour" used={61} total={100} unit="%" resetIn="2h 14m" />
<QuotaUnknownState />
<ReservationStateBadge state="reconciliation_pending" />
```

Invariants: unknown is hatched (never a number); reconciliation_pending/unknown_consumption never look neutral or successful.
