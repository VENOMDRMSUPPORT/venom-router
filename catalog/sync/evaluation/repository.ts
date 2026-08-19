import type { Db } from '../../db/index.ts';

export type EvaluationStatus = 'scored' | 'supported' | 'unsupported' | 'unknown' | 'evaluating';
export type OverallStatus = 'complete' | 'evaluating' | 'insufficient_evidence' | 'unknown';

export interface DimensionCoverage {
  scored: number;
  applicable: number;
  percent: number;
}

export interface IdentityDimensionRow {
  identityId: string;
  dimension: string;
  score: number | null;
  rawRate: number | null;
  uncertainty: number | null;
  confidence: number | null;
  sampleCount: number | null;
  status: EvaluationStatus;
  rubricVersion: string;
  testSetHash: string | null;
  evidence: string[];
  evaluatedAt: string | null;
  methodologyVersion: string;
}

export interface OfferDimensionRow {
  providerId: string;
  modelId: string;
  dimension: string;
  score: number | null;
  rawRate: number | null;
  uncertainty: number | null;
  confidence: number | null;
  sampleCount: number | null;
  status: EvaluationStatus;
  evidence: string[];
  evaluatedAt: string | null;
  methodologyVersion: string;
}

export interface OverallScoreRow {
  providerId: string;
  modelId: string;
  value: number | null;
  qualityScore: number | null;
  operationalScore: number | null;
  qualityCoverage: DimensionCoverage;
  overallCoverage: DimensionCoverage;
  includedDimensions: string[];
  excludedDimensions: string[];
  status: OverallStatus;
  uncertainty: number | null;
  reasons: string[];
  methodologyVersion: string;
  computedAt: string;
}

export interface EvaluationRunRow {
  providerId: string | null;
  modelId: string | null;
  identityId: string | null;
  dimension: string;
  runKind: 'runtime' | 'speed' | 'external' | 'conformance';
  status: 'running' | 'complete' | 'failed' | 'insufficient_evidence';
  evaluatorVersion: string;
  rubricVersion: string;
  testSetVersion: string;
  testSetHash: string | null;
  methodologyVersion: string;
  region: string;
  independentRunKey: string;
  errorCode: string | null;
  startedAt: string;
  finishedAt: string | null;
}

export interface EvaluationSampleRow {
  runId: number;
  scenarioId: string;
  repetition: number;
  outcome: 'passed' | 'failed' | 'provider_failure' | 'evaluator_failure';
  weightedSuccesses: number | null;
  weightedCriteria: number | null;
  metrics: Record<string, number | string | boolean | null> | null;
  artifactRef: string | null;
  errorCode: string | null;
  recordedAt: string;
}

interface IdentityScoreSqlRow {
  identity_id: string;
  dimension: string;
  score: number | null;
  raw_rate: number | null;
  uncertainty: number | null;
  confidence: number | null;
  sample_count: number | null;
  status: EvaluationStatus;
  rubric_version: string;
  test_set_hash: string | null;
  evidence_json: string;
  evaluated_at: string | null;
  methodology_ver: string;
}

interface OfferScoreSqlRow {
  provider_id: string;
  model_id: string;
  dimension: string;
  score: number | null;
  raw_rate: number | null;
  uncertainty: number | null;
  confidence: number | null;
  sample_count: number | null;
  status: EvaluationStatus;
  evidence_json: string;
  evaluated_at: string | null;
  methodology_ver: string;
}

interface OverallSqlRow {
  provider_id: string;
  model_id: string;
  overall_score: number | null;
  quality_score: number | null;
  operational_score: number | null;
  quality_coverage_json: string;
  overall_coverage_json: string;
  included_dimensions_json: string;
  excluded_dimensions_json: string;
  status: OverallStatus;
  uncertainty: number | null;
  reasons_json: string;
  methodology_ver: string;
  computed_at: string;
}

const parseArray = (value: string | null | undefined): string[] => {
  if (!value) return [];
  const parsed = JSON.parse(value) as unknown;
  return Array.isArray(parsed) && parsed.every((item) => typeof item === 'string') ? parsed : [];
};

const parseCoverage = (value: string): DimensionCoverage => {
  const parsed = JSON.parse(value) as Partial<DimensionCoverage>;
  if (typeof parsed.scored !== 'number' || typeof parsed.applicable !== 'number' || typeof parsed.percent !== 'number') {
    throw new Error('invalid evaluation coverage JSON');
  }
  return parsed as DimensionCoverage;
};

export interface EvaluationRepository {
  saveIdentityDimension(row: IdentityDimensionRow): void;
  saveOfferDimension(row: OfferDimensionRow): void;
  saveOverall(row: OverallScoreRow): void;
  identityDimensions(identityId: string, methodologyVersion?: string): IdentityDimensionRow[];
  offerDimensions(providerId: string, modelId: string, methodologyVersion?: string): OfferDimensionRow[];
  overall(providerId: string, modelId: string, methodologyVersion?: string): OverallScoreRow | null;
  createRun(row: EvaluationRunRow): number;
  appendSample(row: EvaluationSampleRow): void;
}

export function createEvaluationRepository(db: Db): EvaluationRepository {
  const saveIdentity = db.prepare(`
    INSERT INTO model_identity_scores (
      identity_id, dimension, score, raw_rate, uncertainty, confidence, sample_count,
      status, rubric_version, test_set_hash, evidence_json, evaluated_at, methodology_ver
    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
    ON CONFLICT(identity_id, dimension, methodology_ver) DO UPDATE SET
      score=excluded.score, raw_rate=excluded.raw_rate, uncertainty=excluded.uncertainty,
      confidence=excluded.confidence, sample_count=excluded.sample_count, status=excluded.status,
      rubric_version=excluded.rubric_version, test_set_hash=excluded.test_set_hash,
      evidence_json=excluded.evidence_json, evaluated_at=excluded.evaluated_at
  `);
  const saveOffer = db.prepare(`
    INSERT INTO provider_model_scores (
      provider_id, model_id, dimension, score, raw_rate, uncertainty, confidence,
      sample_count, status, evidence_json, evaluated_at, methodology_ver
    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
    ON CONFLICT(provider_id, model_id, dimension, methodology_ver) DO UPDATE SET
      score=excluded.score, raw_rate=excluded.raw_rate, uncertainty=excluded.uncertainty,
      confidence=excluded.confidence, sample_count=excluded.sample_count, status=excluded.status,
      evidence_json=excluded.evidence_json, evaluated_at=excluded.evaluated_at
  `);
  const saveOverall = db.prepare(`
    INSERT INTO overall_model_scores (
      provider_id, model_id, overall_score, quality_score, operational_score,
      quality_coverage_json, overall_coverage_json, included_dimensions_json,
      excluded_dimensions_json, status, uncertainty, reasons_json, methodology_ver, computed_at
    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
    ON CONFLICT(provider_id, model_id, methodology_ver) DO UPDATE SET
      overall_score=excluded.overall_score, quality_score=excluded.quality_score,
      operational_score=excluded.operational_score, quality_coverage_json=excluded.quality_coverage_json,
      overall_coverage_json=excluded.overall_coverage_json, included_dimensions_json=excluded.included_dimensions_json,
      excluded_dimensions_json=excluded.excluded_dimensions_json, status=excluded.status,
      uncertainty=excluded.uncertainty, reasons_json=excluded.reasons_json, computed_at=excluded.computed_at
  `);

  return {
    saveIdentityDimension(row) {
      saveIdentity.run(
        row.identityId, row.dimension, row.score, row.rawRate, row.uncertainty,
        row.confidence, row.sampleCount, row.status, row.rubricVersion, row.testSetHash,
        JSON.stringify(row.evidence), row.evaluatedAt, row.methodologyVersion,
      );
    },
    saveOfferDimension(row) {
      saveOffer.run(
        row.providerId, row.modelId, row.dimension, row.score, row.rawRate, row.uncertainty,
        row.confidence, row.sampleCount, row.status, JSON.stringify(row.evidence),
        row.evaluatedAt, row.methodologyVersion,
      );
    },
    saveOverall(row) {
      saveOverall.run(
        row.providerId, row.modelId, row.value, row.qualityScore, row.operationalScore,
        JSON.stringify(row.qualityCoverage), JSON.stringify(row.overallCoverage),
        JSON.stringify(row.includedDimensions), JSON.stringify(row.excludedDimensions),
        row.status, row.uncertainty, JSON.stringify(row.reasons), row.methodologyVersion, row.computedAt,
      );
    },
    identityDimensions(identityId, methodologyVersion = 'overall-score-v1') {
      return (db.prepare(`SELECT * FROM model_identity_scores WHERE identity_id=? AND methodology_ver=? ORDER BY dimension`)
        .all(identityId, methodologyVersion) as unknown as IdentityScoreSqlRow[]).map((row) => ({
        identityId: row.identity_id, dimension: row.dimension, score: row.score, rawRate: row.raw_rate,
        uncertainty: row.uncertainty, confidence: row.confidence, sampleCount: row.sample_count,
        status: row.status, rubricVersion: row.rubric_version, testSetHash: row.test_set_hash,
        evidence: parseArray(row.evidence_json), evaluatedAt: row.evaluated_at,
        methodologyVersion: row.methodology_ver,
      }));
    },
    offerDimensions(providerId, modelId, methodologyVersion = 'overall-score-v1') {
      return (db.prepare(`SELECT * FROM provider_model_scores WHERE provider_id=? AND model_id=? AND methodology_ver=? ORDER BY dimension`)
        .all(providerId, modelId, methodologyVersion) as unknown as OfferScoreSqlRow[]).map((row) => ({
        providerId: row.provider_id, modelId: row.model_id, dimension: row.dimension, score: row.score,
        rawRate: row.raw_rate, uncertainty: row.uncertainty, confidence: row.confidence,
        sampleCount: row.sample_count, status: row.status, evidence: parseArray(row.evidence_json),
        evaluatedAt: row.evaluated_at, methodologyVersion: row.methodology_ver,
      }));
    },
    overall(providerId, modelId, methodologyVersion = 'overall-score-v1') {
      const row = db.prepare(`SELECT * FROM overall_model_scores WHERE provider_id=? AND model_id=? AND methodology_ver=?`)
        .get(providerId, modelId, methodologyVersion) as unknown as OverallSqlRow | undefined;
      if (!row) return null;
      return {
        providerId: row.provider_id, modelId: row.model_id, value: row.overall_score,
        qualityScore: row.quality_score, operationalScore: row.operational_score,
        qualityCoverage: parseCoverage(row.quality_coverage_json),
        overallCoverage: parseCoverage(row.overall_coverage_json),
        includedDimensions: parseArray(row.included_dimensions_json),
        excludedDimensions: parseArray(row.excluded_dimensions_json), status: row.status,
        uncertainty: row.uncertainty, reasons: parseArray(row.reasons_json),
        methodologyVersion: row.methodology_ver, computedAt: row.computed_at,
      };
    },
    createRun(row) {
      const result = db.prepare(`
        INSERT INTO evaluation_runs (
          provider_id, model_id, identity_id, dimension, run_kind, status,
          evaluator_version, rubric_version, test_set_version, test_set_hash,
          methodology_ver, region, independent_run_key, error_code, started_at, finished_at
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
      `).run(
        row.providerId, row.modelId, row.identityId, row.dimension, row.runKind, row.status,
        row.evaluatorVersion, row.rubricVersion, row.testSetVersion, row.testSetHash,
        row.methodologyVersion, row.region, row.independentRunKey, row.errorCode,
        row.startedAt, row.finishedAt,
      ) as unknown as { lastInsertRowid: number | bigint };
      return Number(result.lastInsertRowid);
    },
    appendSample(row) {
      db.prepare(`
        INSERT INTO evaluation_samples (
          run_id, scenario_id, repetition, outcome, weighted_successes,
          weighted_criteria, metrics_json, artifact_ref, error_code, recorded_at
        ) VALUES (?,?,?,?,?,?,?,?,?,?)
      `).run(
        row.runId, row.scenarioId, row.repetition, row.outcome, row.weightedSuccesses,
        row.weightedCriteria, row.metrics ? JSON.stringify(row.metrics) : null,
        row.artifactRef, row.errorCode, row.recordedAt,
      );
    },
  };
}
