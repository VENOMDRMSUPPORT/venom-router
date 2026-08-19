import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { QUALITY_DIMENSIONS, type QualityDimension } from './score.ts';

export { QUALITY_DIMENSIONS } from './score.ts';

const HERE = fileURLToPath(new URL('.', import.meta.url));
const MANIFEST_PATH = join(HERE, '..', '..', 'overlays', 'evaluation-rubrics.json');

export interface RubricCriterion {
  id: string;
  weight: number;
}

export interface RubricScenario {
  id: string;
}

export interface DimensionRubric {
  criteria: RubricCriterion[];
  scenarios: RubricScenario[];
}

export interface RubricManifest {
  version: 'catalog-rubrics-v1';
  dimensions: Record<QualityDimension, DimensionRubric>;
}

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

function canonicalize(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalize).join(',')}]`;
  if (isObject(value)) {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonicalize(value[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

function validateDimension(name: string, raw: unknown): DimensionRubric {
  if (!isObject(raw) || !Array.isArray(raw.criteria) || !Array.isArray(raw.scenarios)) {
    throw new Error(`${name} rubric must contain criteria and scenarios arrays`);
  }
  if (raw.criteria.length !== 5) throw new Error(`${name} rubric must contain exactly five criteria`);
  const criteria: RubricCriterion[] = raw.criteria.map((criterion, index) => {
    if (!isObject(criterion) || typeof criterion.id !== 'string' || !criterion.id.trim() || typeof criterion.weight !== 'number') {
      throw new Error(`${name} criterion ${index} is invalid`);
    }
    return { id: criterion.id, weight: criterion.weight };
  });
  if (new Set(criteria.map((criterion) => criterion.id)).size !== criteria.length) {
    throw new Error(`${name} rubric contains duplicate criterion ids`);
  }
  if (criteria.some((criterion) => Math.abs(criterion.weight - 0.2) > 1e-12)) {
    throw new Error(`${name} criteria must use equal weight 0.2`);
  }
  if (Math.abs(criteria.reduce((sum, criterion) => sum + criterion.weight, 0) - 1) > 1e-12) {
    throw new Error(`${name} criteria weights must sum to one`);
  }
  if (raw.scenarios.length !== 20) throw new Error(`${name} rubric must contain exactly 20 scenarios`);
  const scenarios: RubricScenario[] = raw.scenarios.map((scenario, index) => {
    if (!isObject(scenario) || typeof scenario.id !== 'string' || !scenario.id.trim()) {
      throw new Error(`${name} scenario ${index} is invalid`);
    }
    return { id: scenario.id };
  });
  if (new Set(scenarios.map((scenario) => scenario.id)).size !== scenarios.length) {
    throw new Error(`${name} rubric contains duplicate scenario ids`);
  }
  return { criteria, scenarios };
}

export function loadRubrics(raw?: unknown): RubricManifest {
  const input = raw ?? JSON.parse(readFileSync(MANIFEST_PATH, 'utf8')) as unknown;
  if (!isObject(input) || input.version !== 'catalog-rubrics-v1' || !isObject(input.dimensions)) {
    throw new Error('rubric manifest must use catalog-rubrics-v1 and contain dimensions');
  }
  const parsedDimensions: Record<string, DimensionRubric> = {};
  for (const [name, value] of Object.entries(input.dimensions)) {
    if (!(QUALITY_DIMENSIONS as readonly string[]).includes(name)) throw new Error(`unsupported rubric dimension: ${name}`);
    parsedDimensions[name] = validateDimension(name, value);
  }
  for (const dimension of QUALITY_DIMENSIONS) {
    if (!parsedDimensions[dimension]) throw new Error(`rubric manifest is missing ${dimension}`);
  }
  return {
    version: 'catalog-rubrics-v1',
    dimensions: parsedDimensions as Record<QualityDimension, DimensionRubric>,
  };
}

export function rubricDigest(manifest: RubricManifest): string {
  return createHash('sha256').update(canonicalize(manifest), 'utf8').digest('hex');
}
