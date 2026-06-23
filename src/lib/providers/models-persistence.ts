import { providerExternalId } from "./model-keys";

export interface ProviderModelInput {
  external_id: string;
  display_name: string;
  capabilities: string[];
}

export interface ModelUpsertStats {
  count: number;
  added: number;
  updated: number;
  removed: number;
}

export interface ModelRow {
  id: string;
  provider_id: string;
  account_id: string;
  external_id: string;
  display_name: string;
  capabilities: Record<string, unknown>;
  enabled: boolean;
  lifecycle: string;
}

/** In-memory Supabase-shaped store for simulations. */
export function createModelStore(providerUnique = true) {
  const rows = new Map<string, ModelRow>();
  let seq = 0;

  function key(providerId: string, externalId: string) {
    return `${providerId}\0${externalId}`;
  }

  function findByAccountAndProviderExt(
    accountId: string,
    providerExt: string,
  ): ModelRow | undefined {
    for (const row of rows.values()) {
      if (row.account_id === accountId && providerExternalId(row.external_id, row.capabilities) === providerExt) {
        return row;
      }
    }
    return undefined;
  }

  return {
    rows,
    providerUnique,
    listByAccount(accountId: string) {
      return [...rows.values()].filter((r) => r.account_id === accountId);
    },
    selectExisting(accountId: string) {
      return this.listByAccount(accountId).map((r) => ({
        id: r.id,
        external_id: r.external_id,
        capabilities: r.capabilities,
      }));
    },
    insert(row: Omit<ModelRow, "id">) {
      if (providerUnique) {
        const pk = key(row.provider_id, row.external_id);
        if (rows.has(pk)) {
          return { error: { code: "23505", message: "models_provider_id_external_id_key" } };
        }
        const id = `row-${++seq}`;
        const full = { ...row, id };
        rows.set(pk, full);
        return { error: null };
      }
      const id = `row-${++seq}`;
      rows.set(key(row.provider_id, row.external_id), { ...row, id });
      return { error: null };
    },
    updateById(id: string, patch: Partial<ModelRow>) {
      for (const [pk, row] of rows) {
        if (row.id === id) {
          rows.set(pk, { ...row, ...patch });
          return { error: null };
        }
      }
      return { error: { message: "not found" } };
    },
    deleteById(id: string) {
      for (const [pk, row] of rows) {
        if (row.id === id) {
          rows.delete(pk);
          return { error: null };
        }
      }
      return { error: null };
    },
    findByAccountAndProviderExt,
  };
}

export type ModelStore = ReturnType<typeof createModelStore>;

export function upsertModelsForAccountStore(
  store: ModelStore,
  accountId: string,
  providerId: string,
  models: ProviderModelInput[],
  removeStale = false,
): ModelUpsertStats {
  const existing = store.selectExisting(accountId);
  const byProviderExt = new Map(
    existing.map((m) => [
      providerExternalId(m.external_id, m.capabilities as Record<string, unknown>),
      m,
    ]),
  );
  let added = 0;
  let updated = 0;
  let removed = 0;

  for (const m of models) {
    const dbExternalId = m.external_id;
    const capabilities = {
      list: m.capabilities,
      provider_external_id: m.external_id,
    };
    const prev = byProviderExt.get(m.external_id);
    if (prev) {
      const err = store.updateById(prev.id, {
        display_name: m.display_name,
        capabilities,
        enabled: true,
      });
      if (err.error)
        throw new Error(`Model update failed (${m.external_id}): ${err.error.message}`);
      updated++;
      continue;
    }

    const err = store.insert({
      provider_id: providerId,
      account_id: accountId,
      external_id: dbExternalId,
      display_name: m.display_name,
      capabilities,
      enabled: true,
      lifecycle: "discovered",
    });
    if (err.error) {
      throw new Error(`Model insert failed (${m.external_id}): ${err.error.message}`);
    }
    added++;
  }

  if (removeStale && models.length) {
    const keep = new Set(models.map((m) => m.external_id));
    for (const [providerExt, row] of byProviderExt) {
      if (!keep.has(providerExt)) {
        store.deleteById(row.id);
        removed++;
      }
    }
  }

  return { count: models.length, added, updated, removed };
}

export function countAccountModelsStore(store: ModelStore, accountId: string) {
  const list = store.listByAccount(accountId);
  return {
    total: list.length,
    enabled: list.filter((r) => r.enabled).length,
  };
}
