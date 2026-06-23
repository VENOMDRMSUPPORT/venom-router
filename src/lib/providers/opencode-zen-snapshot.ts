/* OpenCode Zen live catalog parser — client-safe, no server imports. */

export type OpenCodeZenModelCost = {
  input?: number;
  output?: number;
  cache_read?: number;
  cache_write?: number;
};

export type OpenCodeZenCatalogEntry = {
  cost?: OpenCodeZenModelCost;
  name?: string;
  status?: string;
};

export type OpenCodeZenFetchedModel = {
  id: string;
  displayName: string;
  cost: OpenCodeZenModelCost;
};

export function isZeroCost(cost?: OpenCodeZenModelCost): boolean {
  return cost?.input === 0 && cost?.output === 0;
}

/** models.dev marks retired Zen free tiers as deprecated — exclude without hardcoding IDs. */
export function isCatalogEntryAvailable(meta: OpenCodeZenCatalogEntry): boolean {
  if (meta.status === "deprecated") return false;
  return isZeroCost(meta.cost);
}

/** Intersect live Zen model IDs with models.dev — free = zero cost and not deprecated. */
export function buildOpenCodeZenFreeCatalog(
  liveModelIds: string[],
  catalog: Record<string, OpenCodeZenCatalogEntry>,
): OpenCodeZenFetchedModel[] {
  return liveModelIds
    .filter((id) => {
      const meta = catalog[id];
      return meta != null && isCatalogEntryAvailable(meta);
    })
    .map((id) => ({
      id,
      displayName: catalog[id].name ?? id,
      cost: catalog[id].cost ?? { input: 0, output: 0 },
    }));
}
