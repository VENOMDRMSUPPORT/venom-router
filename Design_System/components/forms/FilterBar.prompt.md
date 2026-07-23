Search + structured filters + active-filter summary, in one toolbar. Replaces ad-hoc `SearchField` + `SegmentedControl` rows in page toolbars.

```jsx
<FilterBar
  searchValue={q}
  onSearchChange={setQ}
  searchPlaceholder="Filter by model, provider, account"
  activeFilters={[
    { key: "tier", label: "Tier", value: "max", onClear: () => setTier(null) },
    { key: "funding", label: "Funding", value: "free", onClear: () => setFunding(null) },
  ]}
  onClearAll={() => { setTier(null); setFunding(null); }}
  loading={isFetching}
>
  <SegmentedControl label="View" options={["All", "Routable", "Review queue"]} value={view} onChange={setView} />
</FilterBar>
```
