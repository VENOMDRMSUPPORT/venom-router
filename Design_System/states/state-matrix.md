# Venom domain state matrix (canonical)

The single mapping from every planning-package domain state to its semantic status token, icon, and rendering rule. Components render from this matrix; screens never re-derive it. Sources: docs/02 (account/credential/funding/reservation), docs/04 (certification/probe/quota), docs/05 (tiers/routing/errors), docs/09 (jobs/owner auth). Rendered stories live in `states/*.html`.

**Global rules**
- Never color alone: every state = icon + label (+ tooltip/supporting text where noted).
- `unknown` anywhere = missing evidence → `status.unknown` + **dashed border** + `circle-help`. Not failure, never blank, never a fabricated value.
- Unknown funding ≠ free. Unknown quota ≠ unlimited. Reconciliation pending ≠ released.

## Account connection (`connection_state` — persisted axis)
| State | Token | Icon | Note |
|---|---|---|---|
| connecting | info | loader-circle (spin) | enrollment/OAuth in flight |
| connected | healthy | circle-check | with usable active credential |
| stopped | inactive | power | owner-disabled; excluded from routing |
| disconnected | inactive | unplug | soft disconnect (V1's only delete); restorable via re-enrollment only — never rendered as an error |

## Account health (`health_state` — observed axis, only while connected)
| State | Token | Icon | Note |
|---|---|---|---|
| unknown | unknown | circle-help | not yet checked; eligible |
| healthy | healthy | circle-check | |
| degraded | degraded | triangle-alert | eligible with score penalty |
| unavailable | critical | circle-x | ineligible until next successful check |
| expired | warning | key-round | credential expired → prompt reauth action |

## Derived `display_status` (first match wins)
disconnected → stopped → connecting → **reauthenticating** (info, refresh-cw spin) → **cooling_down** (warning, hourglass + retry-after countdown) → expired/unavailable/degraded/healthy → unknown.

## Reauthentication (staged-credential flow)
| idle | inactive · — | staged | info · fingerprint | validating | info · loader-circle |
|---|---|---|---|---|---|
| swapping | info · refresh-cw | successful | healthy · circle-check | failed | critical · circle-x (old credential intact) |
| rollback | warning · rotate-ccw (old credential preserved) | interrupted | warning · triangle-alert (startup reconciliation discards stale staged row) | | |

## Funding (account fact; offerings inherit)
| State | Token | Icon | Rule |
|---|---|---|---|
| free | healthy (classification, not "success") | hand-coins | verified zero marginal cost only |
| paid | info (classification, not error) | credit-card | |
| unknown | unknown (dashed) | circle-help | excluded from ALL production routing until classified |
| conflicting | warning | triangle-alert | fail closed → Lite excluded; resolve by precedence |
| stale | warning | clock | within staleness window: eligible + flagged + refresh |
| owner-overridden | accent-tinted outline + user-round | — | always distinguishable from provider evidence; never auto-superseded |
| provider-locked | inactive + lock | lock | override rejected (`funding_locked`); disable override UI |
| evidence-required | unknown (dashed) | circle-help | initial `unknown` stamp, source=provider_policy; overridable |

Evidence `source` vocabulary (exactly four): `provider_policy` · `provider_evidence` · `owner_policy` · `owner_override`.

## Certification (six states — **there is no `rejected` state**)
| State | Token | Icon | Note |
|---|---|---|---|
| discovered | inactive | box | |
| observed | info | eye | first concrete evidence |
| probing | info | flask-conical (pulse) | retryable failures stay here within budget |
| certified | accent (NOT plain green "all good") | badge-check | verdict established — routable only with truth=supported |
| suspended | warning | pause | temporarily non-routable, reason-coded |
| expired | warning | clock | stale evidence → refresh probe scheduled |

**Capability truth (orthogonal):** unknown → dashed/`circle-help`; supported → healthy/`check`; unsupported → quiet muted strike/`x` (confirmed absence, not an alarm).
**Routable = certified AND every required truth = supported** — always shown as the conjunction (RoutableIndicator); `certified + unknown` renders "Not routable yet".
Probe execution (separate layer): pending · running · succeeded · inconclusive · retryable_failure (auto-reschedule) · terminal_failure (blocked; truth unchanged).

## Quota window state (per window; attempt takes most restrictive)
| available | healthy · circle-check | insufficient | warning · triangle-alert (below this request's need) | exhausted | critical · ban (until reset) |
|---|---|---|---|---|---|
| unknown | unknown · dashed hatched meter — never a % | stale | warning · clock (treated as unknown + refresh) | | |

Freshness: fresh (no chrome) · stale (warning clock + age) · unknown (dashed). Sources never conflated: `provider_evidence` vs `local_safety` vs `owner_override` — LocalSafetyBudgetIndicator labels local policy explicitly.

## Reservation (five stored states — **no stored `expired`**; `expires_at` is a deadline)
| State | Token | Icon | Rule |
|---|---|---|---|
| reserved | info | clock | headroom debited |
| reconciliation_pending | **warning** | refresh-cw | unresolved possible consumption — NEVER neutral/success; headroom stays debited |
| settled | healthy | circle-check | may carry `confidence=low` tag |
| released | inactive | check | only if never dispatched or provider proved no consumption |
| unknown_consumption | **critical** | triangle-alert | terminal evidence gap → `usage_gap` audit + re-baseline at next sync; owner action surface |

## Async jobs (shared surface, docs/09 §3.12)
pending → inactive/clock · running → info/loader-circle (progress) · completed → healthy/circle-check · failed → critical/circle-x (typed error code) · expired → warning/hourglass (never reached terminal within TTL).

## Owner authentication & session
| first_run_setup | accent · shield | create password once; flows into session |
|---|---|---|
| unauthenticated / login | neutral · lock | never reveals whether setup is complete |
| invalid_credentials | critical · circle-x | generic message, rate-limited |
| locked_out / rate-limited | critical · ban + retry-after | after 5 failures/15 min |
| session_active | healthy · shield-check | idle 30 m sliding, absolute 12 h |
| session_idle_warning | warning · clock (countdown) | before idle expiry |
| session_expired / absolute-expiry | warning · lock | route to login; nothing sensitive preserved |
| session revoked | inactive · log-out | logout or password change (revokes all) |
| reverification_required | warning · shield | modal password prompt gating sensitive action |
| reverified | healthy · shield-check | fresh ≤ 5 min window, to the second |
| recovery/restore required | info · archive-restore | restore container or documented local reset |

## Tier & routing outcomes
- TierBadge: lite/pro/max via `tier.*` tokens only. Policy chips: Lite "Free only · 256K · no thinking"; Pro "~25/75 paid/free target · 512K · extended"; Max "Quality-first, quota-fair · 1M · ultra · no funding-mix target".
- Tier status: eligible (healthy) · degraded (degraded + reason) · unroutable (critical + reason) · insufficient capability coverage · quota constrained · cooldown constrained (+ earliest retry-after) · certification constrained — each with its typed reason list.
- Pro deficit state: per `(tier, workload_bucket, funding_class)` cell — FundingMixIndicator shows target marker vs realized share + per-bucket table. Max: QuotaFairnessIndicator (DRR capacity-weight vs realized share; saturated accounts marked ineligible), no funding target.
- Exclusion reason codes (verbatim, mono chips): `identity_unresolved` `context_unverified` `capability_not_certified` `funding_unknown` `no_healthy_account` `quota_exhausted` `quota_insufficient` `cooling_down` `account_stopped` `account_disconnected` `credential_expired` `account_unavailable` `reauth_in_progress`. Clamp notes (thinking) are info-level, non-blocking.
- Workload profile: multi-label set {vision, tool_use, structured, large_context, standard} — normalized→sorted→deduped bucket key, shown as chips.
- Circuit breaker: closed (healthy) · open (critical + reopens-at) · half-open (warning + probe note). Cooldown: warning + scope (account/offering/provider) + retry-after countdown.

## Public error envelope (data plane)
`venom_free_capacity_exhausted` (429, Lite fail-closed + retry_after) · `venom_no_eligible_offering` (503) · `venom_context_exceeds_tier` (400) · `venom_capability_unsupported` (400/501) · `venom_invalid_extension` (400, names the field) · `invalid_api_key` (401) · `rate_limited` (429). Control plane adds: `validation_error` `unauthorized` `csrf_failed` `not_loopback` `not_found` `conflict` `precondition_failed` `reauthentication_in_progress` `account_identity_mismatch` `funding_locked` `locked_out` `session_expired` `reverification_required` `setup_already_complete` `internal`. TypedErrorDisplay always shows the code (mono) + user-safe message + retryable flag — never raw provider text.

## Model / offering availability
available (no chrome) · withdrawn (inactive + strike) · `catalog_only` (inactive chip "Not in any tier" — media/image-only, future scope, never an error) · context unknown (dashed "Unknown", **never 0**).

## Providers (fleet)
`setup_required` (warning banner listing missing env var **names** only) · `active` (≥1 account) · `no_accounts` (empty state + connect action). The 11 built-ins: opencode-zen, agnes-ai, gemini-cli, ollama-cloud, nvidia-nim (API key) · antigravity, claude-code, codex, github-copilot, clinepass (paid-locked), xai (OAuth) + Custom OpenAI-Compatible. Provider rows show the integration; **funding badges live on account rows** (funding is never provider-level).

## Backup / restore
backup: idle · running (job) · completed (healthy + artifact ref) · failed (typed code). restore: validating → decrypting → verifying (`PRAGMA integrity_check`) → swapped (healthy) · failed = live state untouched (info note). Errors: `wrong_passphrase` · `corrupted_container` · `schema_incompatible`. Passphrase warning fixed copy: "Without your passphrase, this backup cannot be restored."
