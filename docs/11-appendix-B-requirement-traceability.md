# 11 — Appendix B: Requirement traceability (execution layer)

Companion to [`11-implementation-plan.md`](11-implementation-plan.md). This **adds execution
traceability** on top of [`10-requirements-coverage.md`](10-requirements-coverage.md) — it does not
replace it. Every approved requirement is traced:

`requirement → authoritative source → subsystem → task IDs → phase → tests → acceptance evidence → final release gate`.

Cross-checked against `docs/10`: every row in `docs/10 §1–§3` is present here with concrete task IDs.
**No approved requirement lacks an implementation task and a gate.** The final release gate for every
production requirement is **P8** (a clean-machine install + load readiness + backup/restore run that
re-exercises the invariants); requirement-specific proof lands at the phase gate named in each row.

## 1. Non-negotiable principles (README §2 / docs/10 §1)

```
# | Requirement                                   | Source        | Subsystem            | Task IDs                                   | Phase   | Tests                                              | Acceptance evidence                         | Release gate
1 | Zero hardcoding of models/capabilities        | 04            | intelligence,models  | P3a-DISC-001..006;P3c-CERT-001..007        | 3a/3c   | no-static-list lint;no-model-name-literal;Cartesian| /models from probed facts;lint green        | P3c → P8
2 | Free/Paid = account+offering fact, never provider | 02 §2      | accounts/domain      | P2b-DOM-002;P3a-DISC-003;P4-ROUTE-005       | 2b/3a/4 | funding authority unit;free-safety fail-closed     | funding on account row;no provider-funding  | 2b/3a/4 → P8
3 | Everything account-scoped                     | 02,04 §1      | accounts,intelligence| P2b-DB-002;P3a-DISC-002;P3b-DB-001;P3b-QUOTA-* | 2b/3a/3b | schema/import account-scope tests                 | offerings/quota/health keyed on account_id  | 2b/3a/3b → P8
4 | Offering-operation is the routable/certifiable unit | 02 §3,04 §5 | models,routing     | P3a-DB-001;P3c-CERT-004,005;P4-ROUTE-004    | 3c/4    | type + gate test;Cartesian                         | routing consumes offering-operations only   | 3c/4 → P8
5 | Single-owner local trust (authenticated)      | 01 §6a/§8,09 §5,02 §3 | httpapi(auth) | P2b-SEC-001..007;P2b-CAPI-001               | 2b      | owner-auth suite;403-non-loopback;no-role-system   | P2b-TEST-001;CSRF-before-side-effect        | P2b → P8
6 | Venom decides; transport executes             | 01 §4         | execution,routing    | P0-EXEC-002;P4-EXEC-001;P4-ROUTE-013        | 0/4     | no-reselect;no-slug-switch vet;single-engine       | Bifrost pool-1;P4-TEST-002                   | P0/P4 → P8
7 | Fail closed                                    | 04 §5,05 §3/§8| routing              | P3a-DISC-003;P4-ROUTE-005,015               | 3a/4    | Lite fail-closed;unknown⇒ineligible                | Lite zero-paid gate                          | 4 → P8
8 | Secrets are sacred                             | 01 §8,08 §6   | secrets,sanitize     | P1-SEC-001..006;P2b-OBS-001;P5-OBS-001      | 1       | secret canary (every build);env-only secrets       | canary green;hash-only keys                  | P1 → P8
9 | SQLite is the source of truth                 | 01 §5,02 §5   | storage              | P0-DB-001,002;all M-groups                  | 0       | no-ad-hoc-SQL lint;migration integrity/rollback    | repositories only;integrity check            | P0 → P8
10| One design system, no drift                   | 07            | DS(package)→dashboard | P2a-DS-001..005;all UI                      | 2a      | DS validate 12/12;no-raw-values;no-package-copy    | DS report.md;adherence check                 | P2a → P8
```

## 2. Domain & engine requirements (docs/10 §2)

```
Requirement                                            | Source           | Subsystem        | Task IDs                                 | Phase | Tests                                            | Acceptance evidence                       | Release gate
Credential cardinality (1 active/kind; ≤1 staged/kind) | 02 §3,03 §2e     | accounts         | P2b-DB-002;P2b-DOM-003;P2b-PROV-008      | 2b    | active/staged partial-index;reauth interruption  | P2b-TEST-002;multi-kind coexist           | P2b → P8
Owner authentication (login/session/reverify/recovery) | 09 §5,02 §3      | httpapi          | P2b-SEC-001..007;P2b-CAPI-001,004        | 2b    | 09 §5.9 negative suite;lockout;audit-no-secret   | P2b-TEST-001                              | P2b → P8
Funding-evidence 4-source vocabulary + evidence_required | 02 §2,03 §3    | accounts/domain  | P2b-DOM-002;P2b-PROV-002,005,007         | 2b    | source authority;locked-override reject;unknown ineligible | funding tests                    | 2b → P8
Account lifecycle (multi-axis)                         | 02 §3            | accounts/domain  | P2b-DOM-001                              | 2b    | legal/invalid transitions;display_status;eligibility | P2b-DOM-001 report                     | 2b → P8
Account-scoped discovery + free-safety                 | 04 §1/§2b        | intelligence     | P3a-DISC-002,003                         | 3a    | free-never-paid;fail-closed on dataset miss      | P3a-TEST-001                              | 3a → P8
Free-safety vs. enrichment separation                  | 04 §2b           | intelligence     | P3a-DISC-003,004;P3c-TEST-001            | 3a/3c | disabling enrichment doesn't weaken free-safety  | enrichment-separation test                | 3a → P8
Capability/context probing (truth vs execution)        | 04 §2/§5         | intelligence     | P3c-CERT-001,002,003                     | 3c    | infra-failure-never-flips-false                  | probe taxonomy tests                      | 3c → P8
Certification lifecycle (6-state, no rejected)         | 04 §5            | models,intelligence | P3a-CERT-001;P3c-CERT-004,005,007      | 3c    | Cartesian 18-combo;invalid rejected+audited      | P3c-CERT-007                              | 3c → P8
Multi-window quota + mandatory local-safety            | 02 §3,05 §4,04 §5| quota            | P3b-DB-001;P3b-QUOTA-001,002,003,008     | 3b    | multiple windows/(account,unit);unknown≠unlimited;window_key normalization | P3b-TEST-001         | 3b → P8
Atomic reservation (no overcommit, all-or-nothing)     | 02 §3,05 §2/§4   | quota            | P3b-QUOTA-004                            | 3b    | concurrency no-overcommit;any-window-short rollback | P3b-TEST-001                           | 3b → P8
Reservation state machine + reconciliation             | 02 §3,05 §4      | quota            | P3b-QUOTA-005,006,007                    | 3b    | six no-leak/no-double-charge tests               | P3b-TEST-001                              | 3b → P8
Tier policies + selection                              | 05 §1–§2,§8.5    | routing          | P4-ROUTE-002,004..008,010,011,013        | 4     | Lite 0-paid;Pro ±5pp/N=2000;Max fairness+band    | P4-TEST-001                               | 4 → P8
Workload-profile bucketing (deterministic; per-bucket) | 05 §2 Step 7     | routing          | P4-ROUTE-009,010                         | 4     | same set⇒same bucket;deficit per bucket/funding  | bucket + Pro tests                        | 4 → P8
Public `venom` request extension                       | 05 §1b,01 §6b    | httpapi,routing  | P5-PAPI-004;P4-ROUTE-003                 | 5     | clamp reported;required-caps gate;invalid⇒typed;streaming preserved | P5-TEST-002              | 5 → P8
Thinking-budget normalization                          | 05 §1a           | routing,execution| P4-ROUTE-003                             | 4     | defaults;clamp-to-max;degrade vs explicit-require | thinking tests                          | 4 → P8
Scope-classified fallback + circuit breakers           | 01 §4.2,05 §3    | routing,execution| P4-ROUTE-014;P4-EXEC-002                 | 4     | scope→action;adaptive backoff;funding boundary   | P4-TEST-002                               | 4 → P8
Failure taxonomy (normalized errors)                   | 01 §4.2          | execution        | P4-EXEC-002                              | 4/5   | NormalizeError never leaks secrets/raw text      | NormalizeError tests + canary             | 4 → P8
Public inference API (OpenAI-compat + extension)       | 01 §6b,05 §1b/§5 | httpapi          | P5-PAPI-001,002,003,004;P5-OBS-001       | 5     | SDK chat+stream+tools+vision;usage recorded      | P5-TEST-001,003                           | 5 → P8
Quantitative acceptance gates                          | 06 P4/P8,05 §8.1,08 §5/§9 | routing,all | P4-TEST-001;P8-REL-004;P8-TEST-002    | 4/8   | Pro N=2000/±5pp;load ≥30min/≥20RPS/≤0.5%/zero violations | P4-TEST-001;P8-REL-004 report      | 4/8 → P8
Control API contracts                                  | 09               | httpapi          | P2b-CAPI-001..005;P3a/P3b/P3c/P5/P6/P8-CAPI-* | 2b+ | per-endpoint contract;redaction;audit           | contract tests                            | per phase → P8
Shared async-job status surface                        | 09 §1/§3.12      | httpapi          | P2b-JOBS-001;P3a/P3b/P3c-JOBS-001        | 2b+   | one /jobs surface;no competing per-resource      | jobs contract test                        | 2b → P8
Health endpoints (single choice)                       | 01 §6d,09 §2     | httpapi          | P0-CAPI-001                              | 0     | /health needs no session;no duplicate liveness   | /health test                              | P0 → P8
Provider adapters (11 built-ins + custom)              | 03               | providers        | P2b-PROV-005,007;P7-PROV-001..010        | 2b/7  | offline fixtures (CI);live re-verification (evidence) | fixture reports;dated re-verification  | 2b/7 → P8
Portable encrypted backup/restore                      | 08 §9            | storage,secrets,UI | P8-BKP-001,002,003;P8-CAPI-001;P8-UI-001 | 8     | round-trip;wrong-passphrase;interrupted;cross-device;owner-password re-establish;dashboard backup/restore E2E | P8-BKP-003;P8-UI-001 E2E | 8 → P8
Observability (route/attempt/usage, secret-free)       | 05 §7,01 §6c     | observability    | P4-OBS-001;P5-OBS-001;P2b-OBS-001        | 4/5   | secret-free records;X-Venom sanitized;RouteExplain reads | route-record + header tests + canary | 4/5 → P8
```

## 3. Design System (docs/10 §3) — completed package, consumed at P2a

The DS creation task is **complete** (`@venom/design-system@1.0.0`, 12/12 `validate` gates PASS —
`Design_System/validation/report.md`). Its requirements are satisfied by the package; this plan traces
the **consumption/integration** obligations of the application.

```
Requirement                               | Source     | Owner (state)          | Integration task IDs      | Phase | Tests / evidence                                    | Release gate
Product identity & owner-console context  | 07 §0      | DS task (done)         | P2a-DS-004                | 2a    | DS report.md;identity applied in app                | 2a → P8
Token source of truth + 3 outputs         | 07 §2/§10  | DS task (done)         | P2a-DS-002,003            | 2a    | DS gate 1 (build-tokens);Tailwind preset consumed   | 2a → P8
Themes: Dark/Light/High-Contrast          | 07 §3      | DS task (done)         | P2a-DS-002,004            | 2a    | DS gate 2 (contrast);app renders 3 themes           | 2a → P8
Component inventory (primitives/composite/domain) | 07 §5/§5a | DS task (done)  | P2a-DS-004;all UI         | 2a/6  | DS gate 3;app UI uses /primitives + /domain only    | 2a/6 → P8
Domain state coverage                     | 07 §5a     | DS task (done)         | P2b-UI-002,003;P3b-UI-001;P3c-UI-001;P6-UI-*;P8-UI-001 | 2b/3/6/8 | states/state-matrix rendering;axe    | 2b/3/6/8 → P8
Accessibility (AA; AAA-HC)                 | 07 §7      | DS task (done)         | P6-TEST-001               | 6     | DS gate 2 (contrast);app axe on flows               | 6 → P8
Integration handoff contract              | 07 §10     | DS task → dashboard    | P2a-DS-001,002,003,005    | 2a    | file: dependency;no-package-copy;offline;no-CDN     | 2a → P8
```

## 4. Traceability results (summary)

- **Requirements mapped:** all of `docs/10 §1` (10 principles), `§2` (25 domain/engine rows), `§3`
  (7 DS rows) — **42/42**, each to ≥1 task, phase, test, evidence, and release gate.
- **Unmapped requirements:** none.
- **Tasks without tests:** none — every implementation unit's `Tests` field names concrete tests
  (unit/integration/fixture/API/UI/E2E/load) or, for evidence-only units (`P7-PROV-011`), records
  dated live evidence with the fixture suite as the CI gate.
- **Tasks without gates:** none — every task's evidence rolls into a phase gate (main plan §21).
- **Circular dependencies:** none — Appendix A `hard_deps` form a DAG (critical-path spine confirms no back-edge).
- **Unresolved decisions:** none — all product decisions are frozen (main plan §23.1); Design System
  status is synchronized across the docs (`DEC-DS-STATUS`) and the remaining discovery sequencing
  observation (`DEC-DISC-LAYERING`) is recorded, not open.
- **Out-of-plan by design:** `docs/05 §9` future scope (image generation, embeddings/audio,
  cross-provider equivalence, per-offering funding override) — no V1 phase or gate, as intended.
