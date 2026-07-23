# Content & terminology style guide

**Voice:** terse, factual, operator-to-operator. Sentence case everywhere. No exclamation marks, no cheer, no apology theater, no emoji.

**Canonical vocabulary — never casually renamed:** provider (integration definition) · account (connected instance) · credential · offering · offering-operation · operation · canonical model · capability truth · certification · probe · evidence · funding (free/paid/unknown) · funding evidence/source · quota window · local safety budget · reservation · reconciliation · route decision · route attempt · cooldown · tier/policy · Venom API key · owner.

**Products:** Lite, Pro, Max in prose; `venom/lite`, `venom/pro`, `venom/max` as public IDs (mono). Never `venom/standard`, never `venom/plus`. "Ultra" names Max's thinking profile, not a product.

**Titles:** noun phrases ("Connected accounts", "Quota windows"). **Buttons:** verb-first ("Connect provider", "Run discovery", "Reveal credential", "Create backup"). **Confirmations:** state the consequence, then the verb ("Routing through this account stops immediately. History is retained." → "Disconnect").

**Warnings:** concrete condition + concrete effect + next step. "Setup required — missing environment variable: `VENOM_ANTIGRAVITY_CLIENT_SECRET`" (names only, never values).

**Typed errors:** always code (mono) + user-safe sentence + retryable/retry-after. Banned phrases: "Something went wrong", "Error occurred", "Not available", "Oops". Never raw provider errors, never secrets, never account identifiers in public surfaces.

**Empty states:** what's missing + why it matters + the one action. "No providers connected — connect a provider account to start account-scoped discovery."

**Destructive actions:** name the real semantics. V1 disconnect is soft: "stops routing; retains sanitized history; reconnect requires a new enrollment" — never "permanently delete".

**Diagnostics:** IDs, codes, scores, timestamps verbatim in mono. Exclusion reasons are the typed codes (`funding_unknown`), shown as chips, optionally followed by a plain-language gloss.

**Unknown states:** the word "Unknown" (or "No evidence", "Not yet probed", "Not classified") + what resolves it. Never a fabricated 0, blank cell, or fake percentage. Unknown funding ≠ free; unknown quota ≠ unlimited; `unsupported` = confirmed absence ("Vision: unsupported (verified)"), distinct from unknown.

**Timestamps:** UTC mono (`2026-07-22 14:03:11Z`) in tables/diagnostics; relative ("2m ago") in summaries, with the absolute value in a tooltip/detail.

**Security copy fixtures:** masked secrets only (`sk-ant-••••`, `vk_live_3f8a…`); example keys in docs/stories are obviously fake (`EXAMPLE`/`not-real` markers) and never realistic full-length credentials.
