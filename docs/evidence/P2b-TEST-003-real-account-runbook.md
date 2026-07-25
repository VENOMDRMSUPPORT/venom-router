# P2b-TEST-003 — Real-account evidence runbook

This is a **manual, dated evidence procedure** for the two real-account acceptance
criteria in P2b-TEST-003 that cannot run automatically in CI:

- **opencode-zen** — actually IS automatable (a real free-tier API key needs no
  interactive browser step) and has its own automated harness:
  `internal/accounts/application/enrollment_e2e_realaccount_test.go`
  (`TestRealAccount_OpenCodeZen_E2E`). It is skipped by every normal CI run
  because no credential env var is set; an owner who wants to actually exercise
  it sets `VENOM_E2E_OPENCODE_ZEN_KEY` to a real free opencode-zen key and runs:

  ```
  VENOM_E2E_OPENCODE_ZEN_KEY=sk-... go test ./internal/accounts/application/... -run TestRealAccount_OpenCodeZen_E2E -v
  ```

  That test itself asserts identity/funding/health and the secret-clean canary
  in code — no manual observation is needed for opencode-zen. This runbook is
  the analogous, MANUAL procedure for the one case that cannot be automated:

- **antigravity** — its OAuth2 flow is a real, interactive Google sign-in
  (confidential client, `access_type=offline&prompt=consent`), which cannot be
  driven headlessly in CI without either a scripted browser (out of scope for
  this phase) or storing a real owner's Google credentials in CI (never — this
  is explicitly disallowed). This section documents the exact steps for an
  owner to run BY HAND, and a place to record what was observed, dated.

Every step below runs against the real Google OAuth endpoints and the real
antigravity/Gemini backend. Only run this with an account you are willing to
use for this purpose.

## Prerequisites

1. A registered Google OAuth2 **confidential client** (client id + secret) with
   `https://accounts.google.com/o/oauth2/v2/auth` authorized for this app's
   redirect URI (`http://127.0.0.1:<port>/api/control/v1/oauth/antigravity/callback` —
   see [01-architecture §6a](../01-architecture.md#6a-control-plane-owner-ui--control-api)
   for the exact loopback bind/port convention).
2. The two confidential-client environment variables set BEFORE starting the
   app (see `internal/platform/env.go`'s `AntigravityOAuthClientCredentials`,
   consumed by `internal/httpapi/controlmux.go`'s
   `registerAntigravityIfConfigured` — antigravity's "Connect account" button
   in the dashboard stays disabled, and `GET /providers/antigravity` reports
   `configured: false` with `missing_env`, until both are set):
   ```
   VENOM_ANTIGRAVITY_CLIENT_ID=<your client id>
   VENOM_ANTIGRAVITY_CLIENT_SECRET=<your client secret>
   ```
3. The dashboard built and embedded (`task dashboard:build-embed`), then the
   app started normally (`go run ./cmd/venom` or the built binary — see the
   repo's own run instructions), reachable at its loopback control-plane URL.
4. A real Google account you are willing to sign in with for this test.

## Steps

1. **Start the app** with both antigravity env vars set, and open the
   dashboard in a browser at the printed loopback URL.
2. **Complete first-run setup** (or log in, if already set up).
3. Navigate to the **Provider Fleet** view (the dashboard's providers/accounts
   screen, named in [07-design-system.md](../07-design-system.md)). Confirm the
   `antigravity` row shows as configured (its "Connect account" button is
   enabled — if it is disabled, re-check step 2's env vars and restart the
   app).
4. Click **Connect account** on the antigravity row, choose the OAuth flow.
   The app opens a new browser tab to the real Google consent screen
   (`accounts.google.com`).
5. Sign in with the real Google account from Prerequisite 4 and grant the
   requested scopes (cloud-platform, userinfo.email, userinfo.profile, cclog,
   experimentsandconfigs — see
   [03-provider-integration-catalog §4](../03-provider-integration-catalog.md)
   for why each is requested).
6. Return to the dashboard tab. It polls `GET /oauth/{transaction_id}/status`
   automatically; wait for it to report **completed**. If it reports
   **failed** or **expired**, note the exact error shown (never a raw
   token/secret — the UI never displays one) and retry from step 4.
7. **Record the observed values** in the dated results table below: the
   connected account's identity (email + project id), funding classification
   (free/paid) and its confidence, and health state, exactly as the Provider
   Fleet UI displays them for the new account row.
8. **Confirm no secret appears in logs**: check the app's stdout/log output
   (and, if structured logging to a file is configured, that file) for the
   OAuth `code`, `state`, the Google client secret, or any substring of the
   access/refresh token. None of these should ever appear — 09 §1's "OAuth
   `code`/`state`/PKCE verifiers and `Authorization` headers are never
   returned or logged" is exactly the invariant this step is manually
   spot-checking. `grep`-ing the log output for the literal client secret
   value is a quick, effective check.
9. Optionally, disconnect the test account afterward via the dashboard (or
   leave it connected) — this runbook does not require cleanup, since it only
   ever touches your own real account.

## Dated results log

Append one dated entry per run. Do not overwrite prior entries — this is a
running evidence log, not a single current-state snapshot.

| Date (UTC) | Run by | Observed identity (email:project_id) | Observed plan | Observed funding / confidence | Observed health | Secrets found in logs? | Notes |
|---|---|---|---|---|---|---|---|
| _(fill in)_ | _(fill in)_ | _(fill in)_ | _(fill in)_ | _(fill in)_ | _(fill in)_ | _(fill in — should always be "none")_ | _(fill in)_ |

## Why this is not automated

- Interactive browser-driven Google sign-in cannot be scripted in CI without
  either a headless-browser harness (not built this phase — it would also
  need a persistent, non-expiring test Google account with 2FA disabled,
  which is itself a security posture this project does not want to carry) or
  storing a real owner's Google session/credentials in CI secrets (explicitly
  out of scope — this project has no cloud identity at all, by design; see
  [09-control-api.md §5](../09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)'s
  single-owner-local-password model, which antigravity's OAuth account is
  deliberately independent of).
- This is why P2b-TEST-003's CI-gating proof for antigravity is the fixture
  contract tests in `internal/accounts/application/antigravity_integration_test.go`
  (fake token/userinfo/loadCodeAssist probes, no real network) — those run on
  every commit, on both OSes, with zero credentials. This runbook is the
  supplementary, human-run confirmation that the real Google/antigravity
  backend still behaves the way those fixtures model.
