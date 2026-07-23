# 11 — Appendix A: Task dependency matrix (machine-readable)

Companion to [`11-implementation-plan.md`](11-implementation-plan.md). One row per implementation
unit. Pipe-delimited, stable columns, sortable by `id`. This is the machine-readable dependency graph
(the readable diagram is §7 of the main plan). Load it as a table; `hard_deps` defines the DAG edges.

**Schema:** `id | phase | ws | hard_deps | parallel_safe_with | blocks | gate`
- `hard_deps` — task IDs that must complete first (DAG edges). `—` = none (phase-entry only).
- `parallel_safe_with` — tasks/tracks safe to run concurrently.
- `blocks` — what this unit unblocks (`+gate` = its phase gate).
- `gate` — the phase gate this task's evidence rolls into.
- **Migration tasks (`*-DB-*`) — parallelism scope:** a `parallel_safe_with` entry between two `DB`
  migration tasks means **design/preparation/isolated-testing parallelism only**. Migration
  numbering, landing, application, and integration **serialize** — no two migration owners
  concurrently modify or land the canonical migration sequence, and a later group is conceptually
  rebased onto the latest accepted schema ordering before landing (main plan §8/§14).

Counts: 179 tasks. P0:15 · P1:6 · P2a:6 · P2b:34 · P3a:14 · P3b:14 · P3c:12 · P4:22 · P5:12 · P6:16 · P7:14 · P8:14.

## Phase P0 — Foundation (gate: P0)

```
id            | phase | ws   | hard_deps            | parallel_safe_with        | blocks                          | gate
P0-ENV-001    | P0    | ENV  | —                    | —                         | P0-FND-001                      | P0
P0-FND-001    | P0    | FND  | P0-ENV-001           | —                         | all P0                          | P0
P0-FND-002    | P0    | FND  | P0-FND-001           | P0-FND-003,006;P0-EXEC-001| P0-FND-007                      | P0
P0-FND-003    | P0    | FND  | P0-FND-001           | P0-FND-002,006            | P0-FND-005,007;P0-DB-001        | P0
P0-FND-004    | P0    | FND  | P0-FND-002           | DB track                  | P0-FND-007;P2b-SEC-007          | P0
P0-FND-005    | P0    | FND  | P0-FND-003           | EXEC track                | P0-FND-007                      | P0
P0-FND-006    | P0    | OBS  | P0-FND-001           | all P0                    | P1-SEC-006;P0-TEST-001          | P0
P0-DB-001     | P0    | DB   | P0-FND-003           | EXEC track                | P0-DB-002                       | P0
P0-DB-002     | P0    | DB   | P0-DB-001            | EXEC track                | P0-FND-007;all M-groups         | P0
P0-EXEC-001   | P0    | EXEC | P0-FND-001           | DB track                  | P0-EXEC-002                     | P0
P0-EXEC-002   | P0    | EXEC | P0-EXEC-001          | DB track                  | P0-EXEC-003;P4-EXEC-001         | P0
P0-EXEC-003   | P0    | EXEC | P0-EXEC-002          | DB track                  | +gate                           | P0
P0-CAPI-001   | P0    | CAPI | P0-FND-007           | —                         | +gate                           | P0
P0-FND-007    | P0    | FND  | P0-DB-002;P0-FND-002,003,004,005,006 | — | P0-CAPI-001;+gate               | P0
P0-TEST-001   | P0    | TEST | P0-FND-001,006;P0-EXEC-002 | all P0              | every later gate                | P0
```

## Phase P1 — Secrets & keyring (gate: P1)

```
id            | phase | ws   | hard_deps            | parallel_safe_with   | blocks                                | gate
P1-SEC-001    | P1    | SEC  | P0-FND-007           | P1-SEC-005           | P1-SEC-002                            | P1
P1-SEC-002    | P1    | SEC  | P1-SEC-001           | P1-SEC-005           | P1-SEC-003,004;P2b-DB-002;P2b-PROV-003| P1
P1-SEC-003    | P1    | SEC  | P1-SEC-002           | P1-SEC-005           | +gate;P8-BKP-001                      | P1
P1-SEC-004    | P1    | SEC  | P1-SEC-002;P0-FND-007| P1-SEC-005           | +gate;P8-REL-002                      | P1
P1-SEC-005    | P1    | SEC  | P0-FND-006           | keyring tasks        | P1-SEC-006                            | P1
P1-SEC-006    | P1    | SEC  | P1-SEC-002,005       | —                    | +gate; runs every build after         | P1
```

## Phase P2a — Design System integration (gate: P2a)

```
id            | phase | ws   | hard_deps            | parallel_safe_with   | blocks                          | gate
P2a-DS-001    | P2a   | DS   | P0-FND-001           | P1                   | P2a-DS-002,003;P2a-UI-001       | P2a
P2a-DS-002    | P2a   | DS   | P2a-DS-001           | P2a-DS-003           | P2a-DS-004                      | P2a
P2a-DS-003    | P2a   | DS   | P2a-DS-001           | P2a-DS-002           | every UI task                   | P2a
P2a-UI-001    | P2a   | UI   | P2a-DS-001;P0-FND-007| P2a-DS-002,003       | P2b UI                          | P2a
P2a-DS-004    | P2a   | DS   | P2a-DS-002,003       | —                    | +gate                           | P2a
P2a-DS-005    | P2a   | DS   | P2a-DS-004           | —                    | +gate                           | P2a
```

## Phase P2b — Providers, accounts, enrollment, owner auth, control plane (gate: P2b)

```
id            | phase | ws   | hard_deps                          | parallel_safe_with        | blocks                                   | gate
P2b-DB-001    | P2b   | DB   | P0-DB-002                          | P2b-DOM-*                 | P2b-SEC-001;P2b-DB-002                    | P2b
P2b-DB-002    | P2b   | DB   | P2b-DB-001;P1-SEC-002              | P2b-SEC-*                 | P2b-PROV-*;P2b-CAPI-003,004;P3a-DB-001;P3b-DB-001 | P2b
P2b-DB-003    | P2b   | DB   | P2b-DB-002                         | —                         | P2b-JOBS-001;P2b-OBS-001;P5-DB-001       | P2b
P2b-SEC-001   | P2b   | SEC  | P2b-DB-001                         | P2b-PROV-*                | P2b-SEC-002                              | P2b
P2b-SEC-002   | P2b   | SEC  | P2b-SEC-001                        | —                         | P2b-SEC-003,004;P2b-CAPI-001             | P2b
P2b-SEC-003   | P2b   | SEC  | P2b-SEC-002                        | P2b-SEC-004               | +gate                                    | P2b
P2b-SEC-004   | P2b   | SEC  | P2b-SEC-002                        | P2b-SEC-003               | P2b-CAPI-001; all mutation endpoints     | P2b
P2b-SEC-005   | P2b   | SEC  | P2b-SEC-002                        | P2b-SEC-003,004           | P2b-CAPI-004 (reveal)                    | P2b
P2b-SEC-006   | P2b   | SEC  | P2b-SEC-002;P2b-DB-001             | —                         | +gate                                    | P2b
P2b-SEC-007   | P2b   | SEC  | P2b-SEC-001;P0-FND-004             | —                         | +gate (recovery);P8-OPS-001              | P2b
P2b-CAPI-001  | P2b   | CAPI | P2b-SEC-002,004                    | P2b-DOM-*                 | all control mutation endpoints           | P2b
P2b-CAPI-002  | P2b   | CAPI | P2b-CAPI-001                       | —                         | P2b-CAPI-003,004,005;P5-PAPI-006         | P2b
P2b-JOBS-001  | P2b   | JOBS | P2b-DB-003;P2b-CAPI-002            | —                         | P3a/P3b/P3c/P6/P8 async work              | P2b
P2b-DOM-001   | P2b   | DOM  | P0-FND-001                         | P2b-DOM-002,003;P2b-PROV-001 | P2b-CAPI-004;P4-ROUTE-004              | P2b
P2b-DOM-002   | P2b   | DOM  | P0-FND-001                         | P2b-DOM-001,003           | P2b-PROV-005,007;P2b-CAPI-004;P4 gates   | P2b
P2b-DOM-003   | P2b   | DOM  | P0-FND-001                         | P2b-DOM-001,002           | P2b-PROV-008                             | P2b
P2b-PROV-001  | P2b   | PROV | P0-FND-001                         | P2b-DOM-*                 | PROV-002,005,006,007,008;P4-EXEC-001;all P7 | P2b
P2b-PROV-002  | P2b   | PROV | P2b-PROV-001;P2b-DB-002            | —                         | P2b-CAPI-003;P7                           | P2b
P2b-PROV-003  | P2b   | PROV | P1-SEC-002;P2b-DB-002;P2b-DOM-003  | P2b-PROV-004              | PROV-005,007,008                          | P2b
P2b-PROV-004  | P2b   | PROV | P2b-PROV-001                       | P2b-PROV-003              | PROV-005;P7 adapters;P7-PROV-010          | P2b
P2b-PROV-005  | P2b   | PROV | PROV-002,003,004;P2b-DOM-002       | PROV-006,007              | P2b-CAPI-003;P2b-TEST-003;P3a-DISC-002    | P2b
P2b-PROV-006  | P2b   | PROV | P2b-PROV-001;P2b-DB-002;P2b-CAPI-001| P2b-PROV-005             | PROV-007,008;P7 OAuth providers           | P2b
P2b-PROV-007  | P2b   | PROV | PROV-006,003;P2b-DOM-002           | P2b-PROV-005              | P2b-CAPI-003;P2b-TEST-003                 | P2b
P2b-PROV-008  | P2b   | PROV | PROV-006;P2b-DOM-003;PROV-003      | —                         | +gate                                    | P2b
P2b-CAPI-003  | P2b   | CAPI | P2b-CAPI-002;PROV-005,007;P2b-JOBS-001 | P2b-CAPI-004          | P2b-UI-003;P2b-TEST-003;P7-PROV-010       | P2b
P2b-CAPI-004  | P2b   | CAPI | P2b-CAPI-002;DOM-001,002;SEC-005;PROV-003 | P2b-CAPI-003        | P2b-UI-003;+gate                          | P2b
P2b-CAPI-005  | P2b   | CAPI | P2b-CAPI-002                       | —                         | P2b-UI-001;P3a-CAPI-003;P6-CAPI-001       | P2b
P2b-OBS-001   | P2b   | OBS  | P2b-DB-003;P1-SEC-005              | —                         | +gate                                    | P2b
P2b-UI-001    | P2b   | UI   | P2a gate;P2b-CAPI-005              | P2b-UI-002               | all UI surfaces                           | P2b
P2b-UI-002    | P2b   | UI   | P2a gate;SEC-001,002,003,005,006   | P2b-UI-001               | +gate                                    | P2b
P2b-UI-003    | P2b   | UI   | P2b-UI-001;CAPI-003,004;PROV-002   | P2b-UI-002               | +gate                                    | P2b
P2b-TEST-001  | P2b   | TEST | P2b-SEC-001..007;P2b-CAPI-001      | P2b-TEST-002             | +gate                                    | P2b
P2b-TEST-002  | P2b   | TEST | P2b-DB-002;DOM-003;PROV-008        | P2b-TEST-001             | +gate                                    | P2b
P2b-TEST-003  | P2b   | TEST | CAPI-003,004;PROV-005,007;UI-003   | —                         | +gate                                    | P2b
```

## Phase P3a — Catalog discovery (gate: P3a)

```
id            | phase | ws   | hard_deps                     | parallel_safe_with   | blocks                                 | gate
P3a-DB-001    | P3a   | DB   | P2b-DB-002                    | P3b-DB-001 (prep/test only) | P3a-DISC-*;P3a-CERT-001          | P3a
P3a-DISC-001  | P3a   | DISC | P3a-DB-001                    | P3a-CERT-001         | P3a-DISC-002,005                       | P3a
P3a-DISC-002  | P3a   | DISC | DISC-001;P2b-PROV-005,003     | —                    | DISC-005;P3a-CAPI-002                   | P3a
P3a-DISC-003  | P3a   | DISC | P3a-DISC-002                  | P3a-DISC-004         | +gate;P4-ROUTE-005 (funding gate)      | P3a
P3a-DISC-004  | P3a   | DISC | P3a-DISC-002                  | P3a-DISC-003         | +gate                                  | P3a
P3a-DISC-005  | P3a   | DISC | DISC-001,002                 | P3a-DISC-006         | P3a-CAPI-001;P4-ROUTE-004;P6 UI        | P3a
P3a-DISC-006  | P3a   | DISC | P3a-DISC-001                 | P3a-DISC-005         | P3c-CERT-006                           | P3a
P3a-CERT-001  | P3a   | CERT | P3a-DB-001                   | P3a-DISC-001         | P3a-CERT-002;P3c-CERT-004              | P3a
P3a-CERT-002  | P3a   | CERT | P3a-CERT-001                 | —                    | +gate                                  | P3a
P3a-CAPI-001  | P3a   | CAPI | P3a-DISC-005;P2b-CAPI-002    | P3a-CAPI-002         | P6 Models UI                           | P3a
P3a-CAPI-002  | P3a   | CAPI | DISC-002;P2b-JOBS-001;CERT-001| P3a-CAPI-001        | +gate                                  | P3a
P3a-JOBS-001  | P3a   | JOBS | P2b-JOBS-001;P3a-DISC-002    | —                    | +gate                                  | P3a
P3a-CAPI-003  | P3a   | CAPI | P2b-CAPI-005;P3a-DISC-004    | —                    | +gate                                  | P3a
P3a-TEST-001  | P3a   | TEST | DISC-002,003,004;CAPI-001    | —                    | +gate                                  | P3a
```

## Phase P3b — Quota & consumption accounting (gate: P3b)

```
id            | phase | ws    | hard_deps                        | parallel_safe_with     | blocks                          | gate
P3b-DB-001    | P3b   | DB    | P2b-DB-002                       | P3a-DB-001 (prep/test only) | P3b-QUOTA-*                 | P3b
P3b-QUOTA-001 | P3b   | QUOTA | P3b-DB-001                       | —                      | QUOTA-004,008                   | P3b
P3b-QUOTA-002 | P3b   | QUOTA | P3b-QUOTA-001                    | QUOTA-003              | QUOTA-004;P4-ROUTE-011          | P3b
P3b-QUOTA-003 | P3b   | QUOTA | P3b-QUOTA-001                    | QUOTA-002              | QUOTA-004                       | P3b
P3b-QUOTA-004 | P3b   | QUOTA | QUOTA-001,002,003                | —                      | QUOTA-005;P4-ROUTE-013;P3c-QUOTA-001 | P3b
P3b-QUOTA-005 | P3b   | QUOTA | P3b-QUOTA-004                    | —                      | QUOTA-006,007;P4-ROUTE-013;P5-TEST-003 | P3b
P3b-QUOTA-006 | P3b   | QUOTA | P3b-QUOTA-005                    | QUOTA-007              | +gate                           | P3b
P3b-QUOTA-007 | P3b   | QUOTA | QUOTA-005;P2b-JOBS-001           | QUOTA-006              | +gate;P3b-CAPI-002              | P3b
P3b-QUOTA-008 | P3b   | QUOTA | QUOTA-001;P2b-PROV-001           | —                      | +gate;P4                        | P3b
P3b-CAPI-001  | P3b   | CAPI  | QUOTA-008;P2b-CAPI-002           | P3b-CAPI-002           | +gate;P3b-UI-001                | P3b
P3b-CAPI-002  | P3b   | CAPI  | QUOTA-007;P2b-CAPI-002           | P3b-CAPI-001           | +gate;P6-UI-008                 | P3b
P3b-JOBS-001  | P3b   | JOBS  | P2b-JOBS-001;QUOTA-007,008       | —                      | +gate                           | P3b
P3b-UI-001    | P3b   | UI    | P2a gate;P3b-CAPI-001            | —                      | P6-UI-006                       | P3b
P3b-TEST-001  | P3b   | TEST  | QUOTA-004..008                  | —                      | +gate                           | P3b
```

## Phase P3c — Probing & certification (gate: P3c)

```
id            | phase | ws    | hard_deps                       | parallel_safe_with   | blocks                          | gate
P3c-CERT-001  | P3c   | CERT  | P3a-CERT-001                    | —                    | P3c-CERT-004                    | P3c
P3c-CERT-002  | P3c   | CERT  | CERT-001;P0-EXEC-002;P3c-QUOTA-001 | P3c-CERT-003       | P3c-CERT-004                    | P3c
P3c-CERT-003  | P3c   | CERT  | CERT-001;P0-EXEC-002;P3c-QUOTA-001 | P3c-CERT-002       | P3c-CERT-004                    | P3c
P3c-CERT-004  | P3c   | CERT  | CERT-001,002,003;P3a-CERT-001   | P3c-CERT-006         | CERT-005,007                    | P3c
P3c-CERT-005  | P3c   | CERT  | P3c-CERT-004                    | —                    | CERT-007;P4-ROUTE-004,005;P6-UI-012 | P3c
P3c-CERT-006  | P3c   | CERT  | CERT-002,003;P3a-DISC-006       | P3c-CERT-004         | +gate                           | P3c
P3c-QUOTA-001 | P3c   | QUOTA | P3b-QUOTA-004                   | —                    | CERT-002,003;P3c-CAPI-001       | P3c
P3c-CAPI-001  | P3c   | CAPI  | CERT-004;QUOTA-001;P2b-JOBS-001 | —                    | +gate                           | P3c
P3c-JOBS-001  | P3c   | JOBS  | P2b-JOBS-001;CERT-004           | —                    | +gate                           | P3c
P3c-CERT-007  | P3c   | CERT  | P3c-CERT-005                    | —                    | +gate;P4                        | P3c
P3c-UI-001    | P3c   | UI    | P2a gate;P3a-CAPI-002           | —                    | P6-UI-002                       | P3c
P3c-TEST-001  | P3c   | TEST  | CERT-001..006;P3a-DISC-003,004  | P3c-CERT-007         | +gate                           | P3c
```

## Phase P4 — Tier engine & routing (gate: P4)

```
id            | phase | ws    | hard_deps                          | parallel_safe_with   | blocks                          | gate
P4-DB-001     | P4    | DB    | P3a-DB-001                         | —                    | OBS-001;ROUTE-010,011,014       | P4
P4-ROUTE-001  | P4    | ROUTE | P3a-DISC-005                       | P4-ROUTE-002         | ROUTE-004,009                   | P4
P4-ROUTE-002  | P4    | ROUTE | P0-FND-001                         | P4-ROUTE-001         | ROUTE-005,007,008,013           | P4
P4-ROUTE-003  | P4    | ROUTE | P4-ROUTE-002                       | P4-ROUTE-004         | ROUTE-013;P5-PAPI-004           | P4
P4-ROUTE-004  | P4    | ROUTE | ROUTE-001;P3c-CERT-005;P3a-DISC-005;P2b-DOM-001 | P4-ROUTE-003 | ROUTE-005                       | P4
P4-ROUTE-005  | P4    | ROUTE | ROUTE-004,002;P3b-QUOTA-001;P2b-DOM-002 | —               | ROUTE-007;ROUTE-013             | P4
P4-ROUTE-006  | P4    | ROUTE | P4-ROUTE-005                       | —                    | P4-ROUTE-007                    | P4
P4-ROUTE-007  | P4    | ROUTE | ROUTE-006,002                     | —                    | ROUTE-008,010,011               | P4
P4-ROUTE-008  | P4    | ROUTE | P4-ROUTE-007                      | P4-ROUTE-009         | ROUTE-010,011                   | P4
P4-ROUTE-009  | P4    | ROUTE | P4-ROUTE-001                      | P4-ROUTE-008         | P4-ROUTE-010                    | P4
P4-ROUTE-010  | P4    | ROUTE | ROUTE-008,009;P4-DB-001           | P4-ROUTE-011         | ROUTE-013;P4-TEST-001           | P4
P4-ROUTE-011  | P4    | ROUTE | ROUTE-008;P3b-QUOTA-002           | P4-ROUTE-010         | ROUTE-013;P4-TEST-001           | P4
P4-ROUTE-012  | P4    | ROUTE | ROUTE-010,011                    | —                    | ROUTE-013;P4-TEST-002           | P4
P4-ROUTE-013  | P4    | ROUTE | ROUTE-005,012;P3b-QUOTA-004,005;EXEC-001,002 | —          | +gate;P5-PAPI-002               | P4
P4-ROUTE-014  | P4    | ROUTE | ROUTE-013;EXEC-002               | —                    | +gate                           | P4
P4-ROUTE-015  | P4    | ROUTE | ROUTE-013;P2b-CAPI-002           | —                    | P5-PAPI-006                     | P4
P4-EXEC-001   | P4    | EXEC  | P0-EXEC-002;P2b-PROV-001         | P4-ROUTE-*           | ROUTE-013;P7 transports         | P4
P4-EXEC-002   | P4    | EXEC  | P4-EXEC-001                      | —                    | ROUTE-013,014;P5                | P4
P4-EXEC-003   | P4    | EXEC  | P4-EXEC-001                      | P4-EXEC-002          | ROUTE-013;P5-PAPI-002           | P4
P4-OBS-001    | P4    | OBS   | P4-DB-001;P4-ROUTE-013           | —                    | P6 Diagnostics;P5-OBS-001       | P4
P4-TEST-001   | P4    | TEST  | ROUTE-010,011,005,008           | P4-TEST-002          | +gate                           | P4
P4-TEST-002   | P4    | TEST  | ROUTE-012,013,014;EXEC-001      | P4-TEST-001          | +gate                           | P4
```

## Phase P5 — Public API surface (gate: P5)

```
id            | phase | ws    | hard_deps                       | parallel_safe_with   | blocks                          | gate
P5-DB-001     | P5    | DB    | P2b-DB-003                      | —                    | PAPI-001;CAPI-001               | P5
P5-PAPI-001   | P5    | PAPI  | P5-DB-001                       | P5-CAPI-001          | PAPI-002,003                    | P5
P5-CAPI-001   | P5    | CAPI  | P5-DB-001;P2b-CAPI-002          | P5-PAPI-001          | P6-UI-009                       | P5
P5-PAPI-002   | P5    | PAPI  | PAPI-001;P4-ROUTE-013;P4-EXEC-003 | P5-PAPI-003        | PAPI-004;+gate                  | P5
P5-PAPI-003   | P5    | PAPI  | P5-PAPI-001                     | P5-PAPI-002          | +gate                           | P5
P5-PAPI-004   | P5    | PAPI  | PAPI-002;P4-ROUTE-003           | P5-OBS-001           | +gate                           | P5
P5-OBS-001    | P5    | OBS   | PAPI-002;P4-OBS-001             | P5-PAPI-004          | +gate                           | P5
P5-PAPI-005   | P5    | PAPI  | PAPI-001;P2b-CAPI-001           | P5-PAPI-004          | +gate                           | P5
P5-PAPI-006   | P5    | PAPI  | P4-ROUTE-015;P2b-CAPI-002       | —                    | +gate                           | P5
P5-TEST-001   | P5    | TEST  | PAPI-002,003,004;OBS-001        | TEST-002,003         | +gate                           | P5
P5-TEST-002   | P5    | TEST  | P5-PAPI-004                     | TEST-001,003         | +gate                           | P5
P5-TEST-003   | P5    | TEST  | PAPI-002;P4-OBS-001;P3b-QUOTA-005 | TEST-001,002       | +gate                           | P5
```

## Phase P6 — Dashboard completion & operations (gate: P6)

```
id            | phase | ws   | hard_deps                       | parallel_safe_with   | blocks                          | gate
P6-CAPI-001   | P6    | CAPI | P4-OBS-001;P3b-CAPI-002;P2b-CAPI-005 | UI tasks         | UI-003,005,008,010              | P6
P6-UI-001     | P6    | UI   | P2b-UI-001                      | other UI             | +gate                           | P6
P6-UI-002     | P6    | UI   | P3a-CAPI-001;P3c-UI-001         | other UI             | +gate                           | P6
P6-UI-003     | P6    | UI   | P6-CAPI-001                     | other UI             | +gate                           | P6
P6-UI-004     | P6    | UI   | P5-PAPI-002;P5-CAPI-001         | other UI             | +gate                           | P6
P6-UI-005     | P6    | UI   | P5-TEST-003;P6-CAPI-001         | other UI             | +gate                           | P6
P6-UI-006     | P6    | UI   | P3b-UI-001                     | other UI             | +gate                           | P6
P6-UI-007     | P6    | UI   | P4-ROUTE-014;P2b-CAPI-004       | other UI             | +gate                           | P6
P6-UI-008     | P6    | UI   | P6-CAPI-001;P3b-CAPI-002        | other UI             | +gate                           | P6
P6-UI-009     | P6    | UI   | P5-CAPI-001                    | other UI             | +gate                           | P6
P6-UI-010     | P6    | UI   | P6-CAPI-001                    | other UI             | +gate                           | P6
P6-UI-011     | P6    | UI   | P5-CAPI-001;P5-PAPI-002        | other UI             | +gate                           | P6
P6-UI-012     | P6    | UI   | P3c-CERT-005;P6-UI-002         | other UI             | +gate                           | P6
P6-FND-001    | P6    | FND  | P0-FND-004                     | UI tasks             | +gate                           | P6
P6-TEST-001   | P6    | TEST | P6-UI-001..012                 | P6-TEST-002          | +gate                           | P6
P6-TEST-002   | P6    | TEST | P6-UI-001..012;P6-FND-001      | —                    | +gate                           | P6
```

## Phase P7 — Provider breadth & custom integrations (gate: P7)

```
id            | phase | ws   | hard_deps                       | parallel_safe_with        | blocks                          | gate
P7-EXEC-001   | P7    | EXEC | P4-EXEC-001                     | all P7 adapters           | adapters needing native         | P7
P7-PROV-001   | P7    | PROV | P2b-PROV-006;P7-EXEC-001        | other P7 adapters         | +gate                           | P7
P7-PROV-002   | P7    | PROV | P2b-PROV-006;P7-EXEC-001        | other P7 adapters         | +gate                           | P7
P7-PROV-003   | P7    | PROV | P2b-PROV-006;P7-EXEC-001        | other P7 adapters         | +gate                           | P7
P7-PROV-004   | P7    | PROV | P2b-PROV-006;P7-EXEC-001        | other P7 adapters         | +gate                           | P7
P7-PROV-005   | P7    | PROV | P2b-PROV-006;P7-EXEC-001        | other P7 adapters         | +gate                           | P7
P7-PROV-006   | P7    | PROV | P2b-PROV-004                   | other P7 adapters         | +gate                           | P7
P7-PROV-007   | P7    | PROV | P2b-PROV-004;P7-EXEC-001        | other P7 adapters         | +gate                           | P7
P7-PROV-008   | P7    | PROV | P2b-PROV-004                   | other P7 adapters         | +gate                           | P7
P7-PROV-009   | P7    | PROV | P2b-PROV-004                   | other P7 adapters         | +gate                           | P7
P7-PROV-010   | P7    | PROV | PROV-004;P2b-CAPI-003;PROV-003 | P7 adapters               | +gate                           | P7
P7-PROV-011   | P7    | PROV | all P7 adapters                | P7-TEST-001               | +gate (evidence)                | P7
P7-TEST-001   | P7    | TEST | P7 adapters;P7-PROV-010        | P7-PROV-011               | +gate                           | P7
P7-TEST-002   | P7    | TEST | TEST-001;PROV-011;PROV-010     | —                         | +gate                           | P7
```

## Phase P8 — Packaging & hardening (gate: P8)

```
id            | phase | ws   | hard_deps                       | parallel_safe_with   | blocks                          | gate
P8-REL-001    | P8    | REL  | P2a-UI-001;P6 gate              | P8-BKP-*             | +gate;P8-TEST-001               | P8
P8-REL-002    | P8    | REL  | P0-FND-007;P1-SEC-004           | P8-BKP-*             | +gate;P8-TEST-001;P8-OPS-001    | P8
P8-DB-001     | P8    | DB   | P5-DB-001                       | —                    | P8-BKP-001                      | P8
P8-BKP-001    | P8    | BKP  | P1-SEC-002,003;P8-DB-001        | P8-REL-*             | BKP-002,003;P8-CAPI-001         | P8
P8-BKP-002    | P8    | BKP  | P8-BKP-001                      | P8-REL-*             | BKP-003;P8-CAPI-001;P8-OPS-001  | P8
P8-BKP-003    | P8    | BKP  | P8-BKP-001,002                  | —                    | +gate                           | P8
P8-CAPI-001   | P8    | CAPI | P8-BKP-001,002;P2b-JOBS-001     | —                    | P8-UI-001;+gate                 | P8
P8-UI-001     | P8    | UI   | P8-CAPI-001;P2b-SEC-005;P2b-JOBS-001;P2a gate | P8-REL-*  | +gate                           | P8
P8-REL-003    | P8    | REL  | P5-PAPI-001;P2b-CAPI-001        | P8-BKP-*             | +gate                           | P8
P8-REL-004    | P8    | REL  | P4 gate;P5 gate                 | P8-BKP-*             | +gate;P8-TEST-002               | P8
P8-REL-005    | P8    | REL  | REL-001,002,004;BKP-003         | P8-OPS-001           | +gate                           | P8
P8-OPS-001    | P8    | OPS  | REL-002;BKP-002;P2b-SEC-007     | P8-REL-005           | +gate                           | P8
P8-TEST-001   | P8    | TEST | REL-001,002;P7 gate             | P8-TEST-002          | +gate                           | P8
P8-TEST-002   | P8    | TEST | P8-REL-004                      | P8-TEST-001          | +gate                           | P8
```

## Critical path (hard-dependency spine)

```
P0-FND-001 → P0-DB-001 → P0-DB-002 → P0-FND-007 → P0-CAPI-001 (P0 gate)
  → P1-SEC-001 → P1-SEC-002 → P1-SEC-006 (P1 gate)
  → P2b-DB-001 → P2b-SEC-001 → P2b-SEC-002 → P2b-SEC-004 → P2b-CAPI-001 → P2b-CAPI-004 (P2b gate)
  → P3a-DB-001 → P3a-DISC-002 → P3a-DISC-003 (P3a gate)   [P3b runs in parallel off P2b]
  → P3b-QUOTA-004 → P3b-QUOTA-005 (P3b gate)
  → P3c-CERT-004 → P3c-CERT-005 → P3c-CERT-007 (P3c gate)
  → P4-ROUTE-013 → P4-TEST-001 (P4 gate)
  → P5-PAPI-002 → P5-TEST-001 (P5 gate)
  → P6-TEST-002 (P6 gate) → P8-REL-004 / P8-BKP-003 (P8 gate → V1)
```

Off-critical parallel tracks: `P2a` (DS integration, alongside P1/early P2b); `P3a ∥ P3b`; all `P7`
adapters (after `P2b-PROV-001` + `P4-EXEC-001` freeze); most `P6` UI surfaces (after their contract + P2a).
