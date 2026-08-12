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
  status          TEXT NOT NULL,           -- active | missing | retired
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
  source       TEXT NOT NULL,   -- models.dev | openrouter | provider_billing | probe
  source_ref   TEXT,            -- the exact upstream id or field the value came from
  resolved_at  TEXT NOT NULL,
  PRIMARY KEY (provider_id, model_id, field)
);

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
