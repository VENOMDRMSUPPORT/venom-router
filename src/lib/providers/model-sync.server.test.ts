import { describe, expect, it } from "bun:test";
import {
  deleteModelIfOrphaned,
  syncModelsForAccount,
  unlinkAccountModels,
} from "./model-sync.server";

type Row = Record<string, unknown>;

function createMockSupabase(seed: { models?: Row[]; account_models?: Row[]; providers?: Row[] }) {
  const models = [...(seed.models ?? [])];
  const accountModels = [...(seed.account_models ?? [])];

  const from = (table: string) => {
    const api: any = {
      _filters: [] as Array<(row: Row) => boolean>,
      _select: "*",
      _head: false,
      _single: false,
      _maybeSingle: false,
      select(columns?: string, opts?: { count?: string; head?: boolean }) {
        api._select = columns ?? "*";
        api._head = opts?.head === true;
        return api;
      },
      eq(col: string, val: unknown) {
        api._filters.push((row) => row[col] === val);
        return api;
      },
      in(col: string, vals: unknown[]) {
        api._filters.push((row) => vals.includes(row[col]));
        return api;
      },
      order() {
        return api;
      },
      maybeSingle() {
        api._maybeSingle = true;
        return api;
      },
      single() {
        api._single = true;
        return api;
      },
      async insert(row: Row) {
        if (table === "models") {
          const id = `model-${models.length + 1}`;
          const inserted = { id, ...row };
          models.push(inserted);
          return { data: inserted, error: null };
        }
        if (table === "account_models") {
          const id = `am-${accountModels.length + 1}`;
          const inserted = { id, ...row };
          accountModels.push(inserted);
          return { data: inserted, error: null };
        }
        return { data: null, error: null };
      },
      delete() {
        const chain: any = {
          eq(col: string, val: unknown) {
            api._filters.push((row) => row[col] === val);
            return chain;
          },
          then(resolve: (v: unknown) => void) {
            const list = table === "models" ? models : accountModels;
            const kept = list.filter((row) => !api._filters.every((f) => f(row)));
            if (table === "models") models.splice(0, models.length, ...kept);
            if (table === "account_models") accountModels.splice(0, accountModels.length, ...kept);
            resolve({ error: null });
          },
        };
        return chain;
      },
      then(resolve: (v: unknown) => void) {
        const list = table === "models" ? models : accountModels;
        let filtered = list.filter((row) => api._filters.every((f) => f(row)));
        if (
          table === "account_models" &&
          typeof api._select === "string" &&
          api._select.includes("models")
        ) {
          filtered = filtered.map((row) => ({
            ...row,
            models: models.find((m) => m.id === row.model_id) ?? null,
          }));
        }
        if (api._head) {
          resolve({ count: filtered.length, error: null });
          return;
        }
        if (api._single || api._maybeSingle) {
          resolve({ data: filtered[0] ?? null, error: null });
          return;
        }
        resolve({ data: filtered, error: null });
      },
    };
    return api;
  };

  return { from, tables: { models, accountModels } };
}

describe("model-sync.server", () => {
  it("skips existing account link as unchanged", async () => {
    const supabase = createMockSupabase({
      models: [
        {
          id: "m1",
          provider_id: "p1",
          external_id: "gemini-a",
          display_name: "Gemini A",
          capabilities: { provider_external_id: "gemini-a" },
        },
      ],
      account_models: [
        {
          id: "am1",
          account_id: "a1",
          model_id: "m1",
          enabled: true,
          test_status: "working",
          lifecycle: "approved",
        },
      ],
    });

    const stats = await syncModelsForAccount(supabase, {
      accountId: "a1",
      providerId: "p1",
      providerSlug: "antigravity",
      liveModels: [{ external_id: "gemini-a", display_name: "Gemini A", capabilities: ["chat"] }],
      creds: { kind: "oauth2" },
      testModel: async () => ({ external_id: "x", ok: true, latency_ms: 1 }),
    });

    expect(stats.unchanged).toBe(1);
    expect(stats.added).toEqual([]);
    expect(supabase.tables.accountModels).toHaveLength(1);
  });

  it("links catalog model to new account without test", async () => {
    const supabase = createMockSupabase({
      models: [
        {
          id: "m1",
          provider_id: "p1",
          external_id: "gemini-a",
          display_name: "Gemini A",
          capabilities: {},
        },
      ],
    });

    const stats = await syncModelsForAccount(supabase, {
      accountId: "a2",
      providerId: "p1",
      providerSlug: "antigravity",
      liveModels: [{ external_id: "gemini-a", display_name: "Gemini A", capabilities: ["chat"] }],
      creds: { kind: "oauth2" },
      testModel: async () => {
        throw new Error("should not test when catalog exists");
      },
    });

    expect(stats.linked).toBe(1);
    expect(supabase.tables.accountModels).toHaveLength(1);
  });

  it("deletes orphan catalog model when last account unlinks", async () => {
    const supabase = createMockSupabase({
      models: [{ id: "m1", provider_id: "p1", external_id: "only-me", display_name: "Only" }],
      account_models: [{ id: "am1", account_id: "a1", model_id: "m1" }],
    });

    await unlinkAccountModels(supabase, "a1");
    expect(supabase.tables.accountModels).toHaveLength(0);
    expect(supabase.tables.models).toHaveLength(0);
  });

  it("keeps catalog model when another account still linked", async () => {
    const supabase = createMockSupabase({
      models: [{ id: "m1", provider_id: "p1", external_id: "shared", display_name: "Shared" }],
      account_models: [
        { id: "am1", account_id: "a1", model_id: "m1" },
        { id: "am2", account_id: "a2", model_id: "m1" },
      ],
    });

    await supabase.from("account_models").delete().eq("account_id", "a1");
    const deleted = await deleteModelIfOrphaned(supabase, "m1");
    expect(deleted).toBe(false);
    expect(supabase.tables.models).toHaveLength(1);
  });
});
