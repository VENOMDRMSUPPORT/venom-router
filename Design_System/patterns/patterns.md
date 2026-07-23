# Venom interaction patterns (composition rules)

How inventory components compose into surfaces. Screens follow these patterns; they never invent one-off structures. Demonstrations: `ui_kits/venom-console/` (all 17 surfaces) and the component cards.

## App shell
`.vn-shell`: persistent left nav (grouped: **Overview · Operate (Providers, Models, Routing, Playground) · Insights (Usage & Analytics, Quota & Limits, Token Health, Diagnostics) · Manage (API Keys, Settings)**), 48px topbar (environment/health summary, theme + density switchers, owner session pill), scrollable content column (max 1400px). Nav items: icon + label, active = accent text + subtle bg + 2px left indicator. Never add Teams/Billing/Marketplace/Prompt Library/Agent Builder/Org Settings/User Management or a standalone Orchestration page.

## Page anatomy
Every page: optional persistent `Banner` (setup required, review queue) → `.vn-page-header` (breadcrumbs where nested, one `vn-title-page`, one-line description, primary action top-right) → optional `.vn-toolbar` (SearchField + filters + density-sensitive controls) → content (Panel/Table/cards) → written empty/loading/error states. Stat tiles (`StatCard`) only on Overview and section landings.

## Forms
`FormField` everywhere (label + control + description + inline error; automatic ARIA wiring). Primary action bottom-right of the dialog/page; secondary to its left. Destructive = `Button variant="danger"` + `DestructiveActionConfirmation` (consequence copy + optional type-to-confirm). Secrets input: mono Input; never echoed back after submit.

## Feedback
Toasts (bottom-right stack, aria-live polite) for transient results; Banner for persistent conditions; Alert for inline contextual state; TypedErrorDisplay for any failure — always the typed code + user-safe message + retryable flag. Async mutations return a job: render `JobProgress` and poll; terminal failure surfaces the typed job error.

## Data density
Tables default comfortable; `data-density="compact"` switches via tokens only. Numerics right-aligned tabular; identifiers mono; status always icon + text. Column priority for narrow widths: identity → state → primary metric first; secondary columns drop into the row's Drawer detail (never lose access to critical state). Tables scroll horizontally inside `.vn-table-wrap` before truncating meaning.

## Loading
Skeletons for first paint (sized to final content — no layout shift); inline `Spinner` for in-place refresh; `Progress`/`JobProgress` for jobs. Never a blank pane.

## Domain flows (see ui_kits + cards)
- **Account connection (API key):** Dialog → FormField(key, mono) + funding RadioGroup (free/paid/unknown; default = provider policy) → "Validate & connect" (authentic zero-cost probe; 429/5xx renders provider-unavailable, retryable — not invalid-key) → account row appears with first funding evidence.
- **OAuth enrollment:** Dialog → Stepper (Authorize → Validate → Sync) → browser handoff → poll transaction status (pending/completed/failed/expired). Second Auth0 account note (`prompt=login`) shown as info. `setup_required` providers show the env-var-names Banner instead of a connect button.
- **Reauthentication:** from `expired`/`reauthenticating` status → same-identity reconnect → ReauthenticationStatus staged → validating → swapping → successful; failure/rollback keeps the old credential and says so. `reauthentication_in_progress` and `account_identity_mismatch` render as typed errors.
- **Credential reveal:** SecretRevealControl (masked) → if re-verification stale → ReverificationPrompt → reveal once (no-store), clears on hide/blur, audited.
- **Soft disconnect:** danger action + DestructiveActionConfirmation stating V1 semantics: routing stops, history retained, restore only via re-enrollment. Never worded as deletion/purge (hard delete is future scope).
- **Discovery / probes / certification review:** async jobs; discovery snapshot facts (generation, withdrawn count); probe results split execution vs truth; review queue grouped by reason; certification detail = CertificationTimeline + truth set + RoutableIndicator + evidence.
- **Quota inspection:** per-account MultiWindowQuotaSummary → QuotaWindowCards per window (+ LocalSafetyBudgetIndicator always present) → reservations list → reconciliation queue with Re-sync / Accept-estimate owner actions.
- **Routing trace inspection:** Diagnostics → request row → Drawer: RouteDecisionTrace + CompetitiveBandIndicator + FallbackChain + RoutingAttemptTimeline (+ clamp notes).
- **API key creation:** Dialog (label + RPM) → APIKeyCreationResult (one-time raw key, copy, "I stored the key") → list shows APIKeyPrefix only.
- **Backup / restore:** backup = passphrase form + fixed warning ("Without your passphrase, this backup cannot be restored.") + job. Restore = upload + passphrase + Stepper(validate → decrypt → verify → swap); failure keeps live state untouched and says so.
- **Partial failure / retryable / stale / unknown:** degraded results render what succeeded + an Alert for what didn't (typed, retryable flag); stale data carries the stale badge + refresh affordance; unknown evidence renders the dashed unknown treatment with the action that resolves it (probe, classify, sync).
- **No eligible candidates:** TypedErrorDisplay with `venom_no_eligible_offering` / `venom_free_capacity_exhausted` + earliest retry-after + a "why" link to the trace. Lite's copy states it fails closed rather than using paid/unknown.

## Prohibited implications
Never imply: hard delete/purge; image generation (V1); TopologyGraph (optional/future); unknown funding as free; unknown quota as unlimited; `reconciliation_pending` as auto-releasing; a provider being "free" or "paid"; more tiers than `venom/lite|pro|max`.
