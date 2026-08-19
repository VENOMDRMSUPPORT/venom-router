export interface ExistingDimensionEvidence {
  dimension: string;
  status: string;
  testSetHash: string | null;
}

export function shouldSkipDimension(
  existing: ExistingDimensionEvidence[],
  dimension: string,
  testSetHash: string,
  force: boolean,
): boolean {
  if (force) return false;
  return existing.some((row) => row.dimension === dimension
    && row.status === 'scored'
    && row.testSetHash === testSetHash);
}
