Provider Fleet set: provider rows (integration facts) + expandable account rows (status, funding, quota). The ONLY rendering path for connection/health/display_status, funding evidence, credential kinds, reauth staging, cooldowns.

```jsx
<ProviderSummaryCard name="Claude Code" slug="claude-code" authMode="oauth2" accountCount={2} healthyCount={1} verification="proven">
  <ProviderAccountRow identity={<AccountIdentity email="ops@venom.local" externalId="acct_9f2e" plan="Max" />} status={<AccountStatus status="healthy" />} funding={<FundingBadge funding="paid" plan="Max" source="provider_evidence" />} actions={<IconButton icon="ellipsis" label="Actions" />} />
</ProviderSummaryCard>
```

Invariants: unknown funding is dashed and excluded from routing; locked funding disables override; provider rows never carry funding.
