Capacity meter with an honest missing-evidence state. `state="unknown"` (or the legacy `unknown` boolean) renders the hatched no-value treatment — never a fabricated fill. `state="unavailable"` is visually and textually distinct: there is no meter concept for this metric at all (vs. "we don't have evidence yet"). Zero and exhausted are real numeric values, not unknown.

```jsx
<Meter value={61} max={100} label="5-hour window" />
<Meter value={0} max={100} label="Fresh account — no usage yet" />
<Meter value={100} max={100} label="Exhausted window" />
<Meter state="unknown" label="Provider quota" />
<Meter state="unavailable" label="Not metered by this provider" />
```
