import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { QUALITY_DIMENSIONS, type QualityDimension } from './score.ts';

const HERE = fileURLToPath(new URL('.', import.meta.url));
const CROSSWALK_PATH = join(HERE, '..', '..', 'overlays', 'evaluation-benchmark-crosswalk.json');

export interface BenchmarkCrosswalkEntry {
  task: string;
  source: 'inspect-evals' | 'venom-catalog';
  sampleCount: 20;
  repetitions: 3;
  methodologyUrl: string;
  metric: string;
}

export interface BenchmarkCrosswalk {
  version: 'catalog-benchmark-crosswalk-v1';
  dimensions: Record<QualityDimension, BenchmarkCrosswalkEntry>;
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

export function loadBenchmarkCrosswalk(raw?: unknown): BenchmarkCrosswalk {
  const input = raw ?? JSON.parse(readFileSync(CROSSWALK_PATH, 'utf8')) as unknown;
  if (!isObject(input) || input.version !== 'catalog-benchmark-crosswalk-v1' || !isObject(input.dimensions)) {
    throw new Error('benchmark crosswalk must use catalog-benchmark-crosswalk-v1');
  }
  const dimensions: Partial<Record<QualityDimension, BenchmarkCrosswalkEntry>> = {};
  for (const dimension of QUALITY_DIMENSIONS) {
    const entry = input.dimensions[dimension];
    if (!isObject(entry)
      || typeof entry.task !== 'string' || !entry.task
      || (entry.source !== 'inspect-evals' && entry.source !== 'venom-catalog')
      || entry.sampleCount !== 20 || entry.repetitions !== 3
      || typeof entry.methodologyUrl !== 'string' || !/^https:\/\//.test(entry.methodologyUrl)
      || typeof entry.metric !== 'string' || !entry.metric) {
      throw new Error(`invalid benchmark crosswalk entry: ${dimension}`);
    }
    dimensions[dimension] = entry as unknown as BenchmarkCrosswalkEntry;
  }
  if (Object.keys(input.dimensions).some((name) => !(QUALITY_DIMENSIONS as readonly string[]).includes(name))) {
    throw new Error('benchmark crosswalk contains an unsupported dimension');
  }
  return { version: 'catalog-benchmark-crosswalk-v1', dimensions: dimensions as Record<QualityDimension, BenchmarkCrosswalkEntry> };
}

export function benchmarkCrosswalkDigest(crosswalk: BenchmarkCrosswalk): string {
  return createHash('sha256').update(canonicalize(crosswalk), 'utf8').digest('hex');
}
