Security set: session status/expiry, ReverificationPrompt (5-min freshness), SecretRevealControl (masked -> re-verified reveal -> clears on blur), APIKeyPrefix + one-time APIKeyCreationResult, backup/restore states, DestructiveActionConfirmation (type-to-confirm).

```jsx
<SecretRevealControl masked="sk-ant-••••••••" blocked onRevealRequest={openReverify} />
<DestructiveActionConfirmation open title="Disconnect account?" consequence="Routing through this account stops immediately. History is retained; reconnecting requires a new enrollment." confirmWord="disconnect" confirmLabel="Disconnect" />
```

Never render a stored secret unmasked by default; V1 disconnect is soft (retains history) — say so in the consequence copy.
