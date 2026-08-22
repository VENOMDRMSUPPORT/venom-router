-- Venom Catalog schema.
--
-- Two rules govern every table here:
--   1. Nothing is ever DELETEd. Rows transition between states, so no bug and
--      no bad upstream response can destroy history.
--   2. Every derived number records where it came from. A value without
--      provenance is indistinguishable from a guess.

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS providers (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  roster_url      TEXT NOT NULL,
  feed_key        TEXT,                    -- models.dev provider key, when it has one
  last_sync_at    TEXT,
  last_success_at TEXT,
  last_outcome    TEXT                     -- ok | failed | quarantined
);

-- One row per (provider, model). `status` is the lifecycle; rows are never removed.
CREATE TABLE IF NOT EXISTS models (
  provider_id     TEXT NOT NULL REFERENCES providers(id),
  model_id        TEXT NOT NULL,
  display_name    TEXT,
  context_tokens  INTEGER,
  output_tokens   INTEGER,
  input_modalities TEXT,                   -- JSON array
  tools           INTEGER,                 -- 0/1/NULL — NULL means "not published"
  reasoning       INTEGER,
  structured      INTEGER,
  attachment      INTEGER,
  cost_in_per_m   REAL,                    -- EFFECTIVE price at this provider, per million
  cost_out_per_m  REAL,
  -- What the model costs elsewhere at list price. Kept apart from the effective
  -- price on purpose: a subscription provider's model does not cost you the
  -- market rate, and putting a market rate in the effective column would be a
  -- value from one seller wearing another seller's label.
  ref_cost_in_per_m  REAL,
  ref_cost_out_per_m REAL,
  -- free | included | per_token | unknown. `included` is a real cost semantic
  -- for a subscription model, not a missing number.
  cost_kind       TEXT,
  spec_source     TEXT,                    -- 'models.dev' | NULL when the feed has no entry
  -- What the FEED published for the change-tracked fields on the last sync, as
  -- JSON. The diff compares the feed against this, never against the columns
  -- above: enrichment is authoritative over those (it NULLs the effective price
  -- of a subscription provider and lets a reviewed fact override an output
  -- limit), so comparing against them re-reported the same difference on every
  -- run. NULL = no baseline recorded yet, which reports nothing.
  feed_tracked_json TEXT,
  -- active   served now and PUBLISHED
  -- missing  absent from the last roster (still published while it counts down)
  -- retired  gone from upstream for good (kept for history, never served)
  -- excluded served now but WITHHELD from the published roster by a provider
  --          publish policy (e.g. a free-only provider's paid models). A policy
  --          decision, not an absence — so it never touches the roster delta gate
  --          and the row (history) is preserved. See exclusion_reason.
  status          TEXT NOT NULL,           -- active | missing | retired | excluded
  -- Why an `excluded` row is withheld: paid | not_proven_free | not_served |
  -- plan_required. NULL for every
  -- other status. Cited so the exclusion is accountable, never a silent drop.
  exclusion_reason TEXT,
  first_seen_at   TEXT NOT NULL,
  last_seen_at    TEXT NOT NULL,
  missing_since   TEXT,
  miss_count      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider_id, model_id)
);
CREATE INDEX IF NOT EXISTS idx_models_status ON models(status);

-- Score rows are recomputed, never edited. One row per (model, kind).
CREATE TABLE IF NOT EXISTS model_scores (
  provider_id     TEXT NOT NULL,
  model_id        TEXT NOT NULL,
  kind            TEXT NOT NULL,           -- 'VQ' | 'VO'
  value           REAL,
  uncertainty     REAL,                    -- half-width; NULL when unrated or bounded
  bound           TEXT,                    -- NULL | lower | upper
  -- ONE vocabulary for both kinds. VQ uses measured|calibrated|bounded|unrated;
  -- VO is always 'derived' (computed from published facts). An earlier version
  -- put 'complete'/'partial' here for VO, which meant a consumer grouping by
  -- this column mixed two different meanings — dimension coverage now lives in
  -- `dimensions.missing` where it belongs.
  evidence_level  TEXT NOT NULL,           -- measured | calibrated | bounded | unrated | derived
  source          TEXT,
  source_model_id TEXT,                    -- exact upstream id the value came from
  -- The untransformed upstream figure, kept so any score can be re-derived
  -- without re-fetching: raw_value + transformation + calibration_ver is enough
  -- to reproduce `value` exactly.
  raw_value       REAL,
  raw_field       TEXT,                    -- which upstream field raw_value came from
  transformation  TEXT,                    -- 'identity' | the applied formula
  source_fetched_at TEXT,                  -- when the upstream payload was retrieved
  identity_rule   TEXT,                    -- exact | free-variant | exact-size | overlay
  -- Why an unrated row is unrated. Null whenever it has a value. 'unrated'
  -- alone is not accountable: four different situations produce it and they
  -- need four different kinds of work.
  unrated_reason  TEXT,
  precision_dp    INTEGER NOT NULL DEFAULT 0,
  dimensions      TEXT,                    -- VO only: JSON of per-dimension values
  profile_id      TEXT,
  methodology_ver TEXT NOT NULL,
  calibration_ver TEXT,
  computed_at     TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id, kind),
  FOREIGN KEY (provider_id, model_id) REFERENCES models(provider_id, model_id)
);

-- Append-only. Answers "what changed, when, and why".
CREATE TABLE IF NOT EXISTS model_events (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id        INTEGER,
  provider_id   TEXT NOT NULL,
  model_id      TEXT NOT NULL,
  kind          TEXT NOT NULL,             -- added | removed | readded | changed | score_changed
  field         TEXT,
  old_value     TEXT,
  new_value     TEXT,
  reason        TEXT,
  at            TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_at ON model_events(at);

CREATE TABLE IF NOT EXISTS sync_runs (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id   TEXT NOT NULL,
  started_at    TEXT NOT NULL,
  finished_at   TEXT,
  outcome       TEXT,                      -- ok | failed | quarantined
  roster_count  INTEGER,
  added         INTEGER DEFAULT 0,
  removed       INTEGER DEFAULT 0,
  changed       INTEGER DEFAULT 0,
  http_status   INTEGER,
  error         TEXT
);

-- Durable follow-up work for published rows whose score or operational facts
-- are still unresolved. The public state is derived from this row plus the
-- current model facts; no UI timer invents lifecycle state.
CREATE TABLE IF NOT EXISTS resolution_jobs (
  provider_id       TEXT NOT NULL,
  model_id          TEXT NOT NULL,
  status            TEXT NOT NULL,          -- processing | dormant | complete
  reasons_json      TEXT NOT NULL,
  first_detected_at TEXT NOT NULL,
  window_started_at TEXT NOT NULL,
  last_attempt_at   TEXT,
  next_attempt_at   TEXT,
  attempt_count     INTEGER NOT NULL DEFAULT 0,
  updated_at        TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id),
  FOREIGN KEY (provider_id, model_id) REFERENCES models(provider_id, model_id)
);
CREATE INDEX IF NOT EXISTS idx_resolution_jobs_due
  ON resolution_jobs(status, next_attempt_at);

-- Every fit is kept, so any historical score can be reproduced exactly.
CREATE TABLE IF NOT EXISTS calibrations (
  version       TEXT PRIMARY KEY,
  source_from   TEXT NOT NULL,
  source_to     TEXT NOT NULL,
  n             INTEGER NOT NULL,
  rho           REAL NOT NULL,
  r2            REAL,
  slope         REAL NOT NULL,
  intercept     REAL NOT NULL,
  loo_rmse      REAL NOT NULL,
  baseline_sd   REAL NOT NULL,
  accepted      INTEGER NOT NULL,
  excluded_json TEXT,
  bias_json     TEXT,
  fitted_at     TEXT NOT NULL
);

-- One row per resolved field, so any single fact can be traced to its source
-- without inferring it from which pass happened to run last.
CREATE TABLE IF NOT EXISTS model_facts (
  provider_id  TEXT NOT NULL,
  model_id     TEXT NOT NULL,
  field        TEXT NOT NULL,
  value        TEXT,
  source       TEXT NOT NULL,   -- provider_api | models.dev | openrouter | provider_billing | probe
  source_ref   TEXT,            -- the exact upstream id or field the value came from
  -- The URL the value was read from. `source` names WHICH source; this names
  -- exactly WHERE, so a reader can go and check without knowing our code.
  source_url   TEXT,
  -- How strong the evidence is, which `source` alone does not say. A value from
  -- the seller's own feed and a value another seller declared about the same
  -- model both read as 'models.dev' and are not equally authoritative.
  --   first_party         the seller describing its own offer
  --   pooled_third_party  another seller declaring it about the same model
  --   index_confirmation  a canonical index confirming; it can only say yes
  --   declared_policy     a business fact no feed publishes, cited by hand
  --   measured            an active probe observed it
  evidence_state TEXT,
  -- The untransformed upstream figure, so the value can be re-derived and any
  -- later upstream change is detectable. Kept in full rather than as a digest:
  -- these values are small, and the value itself answers every question a hash
  -- would, plus the ones it would not.
  raw_value    TEXT,
  -- Version of the resolver that produced the value, so a fact resolved under
  -- older logic is identifiable rather than silently mixed in.
  resolver_version TEXT,
  -- Null for everything a probe did not produce.
  probe_version TEXT,
  resolved_at  TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id, field)
);

-- Fields whose sellers disagreed.
--
-- The resolver already refuses to pick a winner — a disputed field resolves to
-- unknown, never to a majority and never to whichever source was read first.
-- This table exists because refusing silently is not enough: a `—` produced by
-- two sellers contradicting each other and a `—` nobody ever published are the
-- same character on screen and completely different facts about the world. Both
-- sides are kept so the disagreement can be shown, audited, and one day resolved
-- by a human the same way an ambiguous identity is.
CREATE TABLE IF NOT EXISTS model_conflicts (
  provider_id   TEXT NOT NULL,
  model_id      TEXT NOT NULL,
  field         TEXT NOT NULL,
  -- JSON array of every distinct side: [{ "value": ..., "by": "provider/model" }]
  sides_json    TEXT NOT NULL,
  conflict_type TEXT NOT NULL,             -- source_disagreement
  status        TEXT NOT NULL DEFAULT 'open',  -- open | resolved
  resolved_to   TEXT,
  detected_at   TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id, field)
);
CREATE INDEX IF NOT EXISTS idx_conflicts_status ON model_conflicts(status);

-- Identity candidates that were examined and REFUSED, with the evidence.
--
-- A third identity state, not a third way of saying "resolved". `models` answers
-- "which upstream model is this" and `identity_review` answers "who must decide";
-- neither can answer "what did we already try, and why did it fail". Without that
-- record, a row parked in review is indistinguishable from one nobody looked at,
-- and the same plausible-but-wrong bind gets proposed again.
--
-- Nothing here may ever populate a canonical identity. `verdict` separates the
-- two findings this table holds so they are never read as one:
--   candidate_rejected   a specific candidate was examined and refused
--   no_candidate_exists  the search established there is nothing to examine
--
-- One row per (provider, model, candidate): two rejected candidates are two
-- facts, and collapsing them would lose which reason belonged to which.
CREATE TABLE IF NOT EXISTS identity_rejections (
  provider_id        TEXT NOT NULL,
  model_id           TEXT NOT NULL,
  -- The refused upstream id, or '' for a no_candidate_exists finding. Empty
  -- rather than NULL because it is part of the primary key.
  rejected_candidate TEXT NOT NULL,
  verdict            TEXT NOT NULL,   -- candidate_rejected | no_candidate_exists
  reason             TEXT NOT NULL,
  evidence_json      TEXT,            -- JSON array of re-verifiable evidence lines
  -- The same provenance contract every resolved fact carries.
  source             TEXT NOT NULL,   -- identity_overlay
  source_ref         TEXT,            -- the overlay key this came from
  source_url         TEXT,            -- the primary source the evidence cites
  evidence_state     TEXT NOT NULL,   -- declared_policy: a reviewed human decision
  resolver_version   TEXT NOT NULL,
  -- What was known about the candidate at decision time (context, benchmarks),
  -- so a later reader can see what the decision was weighed against.
  candidate_meta_json TEXT,
  reviewed_at        TEXT,            -- when the human reviewed it, from the overlay
  recorded_at        TEXT NOT NULL,   -- when this ingestion ran
  PRIMARY KEY (provider_id, model_id, rejected_candidate)
);
CREATE INDEX IF NOT EXISTS idx_rejections_model ON identity_rejections(model_id);

-- Ambiguous identities park here until a human resolves them once.
CREATE TABLE IF NOT EXISTS identity_review (
  provider_id     TEXT NOT NULL,
  model_id        TEXT NOT NULL,
  candidates_json TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'open',   -- open | resolved
  resolved_to     TEXT,
  first_seen_at   TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id)
);

-- overall-score-v1 quality evidence belongs to an exact canonical identity.
-- It is intentionally not foreign-keyed to one provider offering: several
-- conformant offers may reference the same identity without copying evidence.
CREATE TABLE IF NOT EXISTS model_identity_scores (
  identity_id       TEXT NOT NULL,
  dimension         TEXT NOT NULL,
  score             REAL,
  raw_rate          REAL,
  uncertainty       REAL,
  confidence        REAL,
  sample_count      REAL,
  status            TEXT NOT NULL, -- scored | supported | unsupported | unknown | evaluating
  rubric_version    TEXT NOT NULL,
  test_set_hash     TEXT,
  evidence_json     TEXT NOT NULL DEFAULT '[]',
  evaluated_at      TEXT,
  methodology_ver   TEXT NOT NULL,
  PRIMARY KEY (identity_id, dimension, methodology_ver)
);
CREATE INDEX IF NOT EXISTS idx_identity_scores_methodology
  ON model_identity_scores(methodology_ver, dimension, status);

-- Offer-scoped dimensions: speed, cost efficiency, and provider-specific
-- quality measurements. Nothing in this table may be copied to another seller.
CREATE TABLE IF NOT EXISTS provider_model_scores (
  provider_id       TEXT NOT NULL,
  model_id          TEXT NOT NULL,
  dimension         TEXT NOT NULL,
  score             REAL,
  raw_rate          REAL,
  uncertainty       REAL,
  confidence        REAL,
  sample_count      REAL,
  status            TEXT NOT NULL,
  evidence_json     TEXT NOT NULL DEFAULT '[]',
  evaluated_at      TEXT,
  methodology_ver   TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id, dimension, methodology_ver),
  FOREIGN KEY (provider_id, model_id) REFERENCES models(provider_id, model_id)
);
CREATE INDEX IF NOT EXISTS idx_provider_scores_methodology
  ON provider_model_scores(methodology_ver, dimension, status);

-- One server-owned projection per offer and methodology. Legacy model-score-v1
-- is kept separately and is never rewritten into this table.
CREATE TABLE IF NOT EXISTS overall_model_scores (
  provider_id          TEXT NOT NULL,
  model_id             TEXT NOT NULL,
  overall_score        REAL,
  quality_score        REAL,
  operational_score    REAL,
  quality_coverage_json TEXT NOT NULL,
  overall_coverage_json TEXT NOT NULL,
  included_dimensions_json TEXT NOT NULL,
  excluded_dimensions_json TEXT NOT NULL,
  status               TEXT NOT NULL, -- complete | evaluating | insufficient_evidence | unknown
  uncertainty          REAL,
  reasons_json         TEXT NOT NULL DEFAULT '[]',
  methodology_ver      TEXT NOT NULL,
  computed_at          TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id, methodology_ver),
  FOREIGN KEY (provider_id, model_id) REFERENCES models(provider_id, model_id)
);
CREATE INDEX IF NOT EXISTS idx_overall_scores_rank
  ON overall_model_scores(methodology_ver, status, overall_score DESC);

-- Reproducible evaluation envelope. No credential or raw private response is
-- accepted by this schema; samples retain only outcome, metrics and hashes.
CREATE TABLE IF NOT EXISTS evaluation_runs (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id         TEXT,
  model_id            TEXT,
  identity_id         TEXT,
  dimension           TEXT NOT NULL,
  run_kind            TEXT NOT NULL, -- runtime | speed | external | conformance
  status              TEXT NOT NULL, -- running | complete | failed | insufficient_evidence
  evaluator_version   TEXT NOT NULL,
  rubric_version      TEXT NOT NULL,
  test_set_version    TEXT NOT NULL,
  test_set_hash       TEXT,
  methodology_ver     TEXT NOT NULL,
  region              TEXT NOT NULL,
  independent_run_key TEXT NOT NULL,
  error_code          TEXT,
  started_at          TEXT NOT NULL,
  finished_at         TEXT,
  FOREIGN KEY (provider_id, model_id) REFERENCES models(provider_id, model_id)
);
CREATE INDEX IF NOT EXISTS idx_evaluation_runs_offer
  ON evaluation_runs(provider_id, model_id, dimension, status);

CREATE TABLE IF NOT EXISTS evaluation_samples (
  run_id              INTEGER NOT NULL REFERENCES evaluation_runs(id),
  scenario_id         TEXT NOT NULL,
  repetition          INTEGER NOT NULL,
  outcome             TEXT NOT NULL, -- passed | failed | provider_failure | evaluator_failure
  weighted_successes  REAL,
  weighted_criteria   REAL,
  metrics_json        TEXT,
  artifact_ref        TEXT,
  -- The provider's own answer, credential-redacted. Retained so a grader repair
  -- can be replayed offline instead of buying the whole corpus a second time.
  response_json       TEXT,
  error_code          TEXT,
  recorded_at         TEXT NOT NULL,
  PRIMARY KEY (run_id, scenario_id, repetition)
);

-- Proven conformance divergence never mutates the shared identity score.
CREATE TABLE IF NOT EXISTS provider_quality_overrides (
  provider_id       TEXT NOT NULL,
  model_id          TEXT NOT NULL,
  dimension         TEXT NOT NULL,
  score             REAL NOT NULL,
  raw_rate          REAL NOT NULL,
  uncertainty       REAL NOT NULL,
  confidence        REAL NOT NULL,
  sample_count      REAL NOT NULL,
  reason            TEXT NOT NULL,
  run_ids_json      TEXT NOT NULL,
  evidence_json     TEXT NOT NULL,
  status            TEXT NOT NULL, -- provisional | override
  methodology_ver   TEXT NOT NULL,
  evaluated_at      TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id, dimension, methodology_ver),
  FOREIGN KEY (provider_id, model_id) REFERENCES models(provider_id, model_id)
);
