/**
 * Full sync-button simulation — no Supabase, no browser.
 * Run: npm run test:sync-sim
 */
import {
  countAccountModelsStore,
  createModelStore,
  upsertModelsForAccountStore,
  type ProviderModelInput,
} from "../src/lib/providers/models-persistence.ts";
import { formatSyncToast, patchAccountInProviders } from "../src/lib/providers/sync-cache.ts";
import type { SyncAccountResponse } from "../src/lib/providers/sync-response.types.ts";
import type { ProviderRow } from "../src/components/providers/account-row.tsx";

const PROVIDER_ID = "11111111-1111-1111-1111-111111111111";
const ACCOUNTS = [
  "acff5076-b850-4c9c-9776-f9a8531c6e03",
  "bbbb5076-b850-4c9c-9776-f9a8531c6e03",
  "cccc5076-b850-4c9c-9776-f9a8531c6e03",
] as const;

const SAMPLE_MODELS: ProviderModelInput[] = [
  { external_id: "gemini-2.5-pro", display_name: "Gemini 2.5 Pro", capabilities: ["chat"] },
  { external_id: "gemini-2.5-flash", display_name: "Gemini 2.5 Flash", capabilities: ["chat"] },
  { external_id: "claude-sonnet-4-6", display_name: "Claude Sonnet 4.6", capabilities: ["chat"] },
  {
    external_id: "claude-opus-4-6-thinking",
    display_name: "Claude Opus 4.6",
    capabilities: ["chat"],
  },
  { external_id: "gpt-oss-120b-medium", display_name: "GPT-OSS 120B", capabilities: ["chat"] },
];

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(`FAIL: ${msg}`);
}

function buildSyncResponse(
  accountId: string,
  stats: { added: number; updated: number; removed: number },
  counts: { total: number; enabled: number },
): SyncAccountResponse {
  return {
    ok: true,
    account_id: accountId,
    provider_slug: "antigravity",
    synced_at: new Date().toISOString(),
    account: {
      email: `${accountId.slice(0, 8)}@test.com`,
      label: `${accountId.slice(0, 8)}@test.com`,
      plan: "Pro",
      status: "healthy",
      last_synced_at: new Date().toISOString(),
      last_health_check_at: new Date().toISOString(),
      quota_used: 0,
      quota_total: 100,
      quota_unit: "%",
      quota_extra: {
        groups: [
          { name: "Gemini Models", modelIds: ["gemini-2.5-pro", "gemini-2.5-flash"] },
          { name: "Claude and GPT Models", modelIds: ["claude-sonnet-4-6", "gpt-oss-120b-medium"] },
        ],
      },
    },
    health: { ok: true, latency_ms: 948, checked_at: new Date().toISOString() },
    models: {
      fetched: SAMPLE_MODELS.length,
      added: stats.added,
      updated: stats.updated,
      removed: stats.removed,
      enabled: counts.enabled,
      total: counts.total,
    },
    quota: {
      synced: true,
      used: 0,
      total: 100,
      unit: "%",
      groups: [
        { name: "Gemini Models", short_label: "GEM", model_count: 2 },
        { name: "Claude and GPT Models", short_label: "OPT", model_count: 2 },
      ],
    },
    meta: {
      provider_calls: ["loadCodeAssist", "fetchAvailableModels"],
      db_writes: ["accounts", "models"],
      duration_ms: 1200,
    },
  };
}

function simulateSyncButton(
  store: ReturnType<typeof createModelStore>,
  providers: ProviderRow[],
  accountId: string,
) {
  const stats = upsertModelsForAccountStore(store, accountId, PROVIDER_ID, SAMPLE_MODELS, true);
  const counts = countAccountModelsStore(store, accountId);
  const response = buildSyncResponse(accountId, stats, counts);
  const toast = formatSyncToast(response);
  const nextProviders = patchAccountInProviders(providers, response);
  const account = nextProviders?.flatMap((p) => p.accounts).find((a) => a.id === accountId);

  return { response, toast, account, stats, counts };
}

function main() {
  console.log("=== Sync button simulation (ideal + legacy DB constraint) ===\n");

  const store = createModelStore(true);
  let providers: ProviderRow[] = [
    {
      id: PROVIDER_ID,
      slug: "antigravity",
      name: "Antigravity",
      category: "oauth",
      auth_type: "oauth2_secret",
      description: null,
      homepage: null,
      accounts: ACCOUNTS.map((id, i) => ({
        id,
        label: `user${i + 1}@test.com`,
        email: `user${i + 1}@test.com`,
        plan: "Pro",
        status: "healthy",
        quota_used: 0,
        quota_total: 100,
        quota_unit: "%",
        last_synced_at: null,
        last_health_check_at: null,
        modelsTotal: 0,
        modelsEnabled: 0,
      })),
    },
  ];

  for (const accountId of ACCOUNTS) {
    const { response, toast, account, stats, counts } = simulateSyncButton(
      store,
      providers,
      accountId,
    );
    providers = patchAccountInProviders(providers, response) ?? providers;

    console.log(`Account ${accountId.slice(0, 8)}…`);
    console.log(
      `  DB: +${stats.added} added, ${stats.updated} updated → total=${counts.total} enabled=${counts.enabled}`,
    );
    console.log(`  Toast: ${toast}`);
    console.log(
      `  Badge: modelsEnabled=${account?.modelsEnabled} modelsTotal=${account?.modelsTotal}`,
    );

    assert(counts.total === SAMPLE_MODELS.length, `expected ${SAMPLE_MODELS.length} models in DB`);
    assert(counts.enabled === SAMPLE_MODELS.length, "all models should be enabled");
    assert(account?.modelsEnabled === SAMPLE_MODELS.length, "badge should show enabled count");
    assert(account?.modelsEnabled! > 0, 'badge must not show "—"');
    assert(toast.includes(`${SAMPLE_MODELS.length} models`), "toast must include model count");
    assert(response.ok === true, "response.ok");
    console.log("  ✓ pass\n");
  }

  assert(
    store.rows.size === ACCOUNTS.length * SAMPLE_MODELS.length,
    "each account owns its own model rows",
  );
  console.log(
    `Total model rows in simulated DB: ${store.rows.size} (${ACCOUNTS.length} accounts × ${SAMPLE_MODELS.length} models)`,
  );

  const account2Resync = simulateSyncButton(store, providers, ACCOUNTS[1]!);
  assert(account2Resync.stats.added === 0, "re-sync should not add duplicates");
  assert(
    account2Resync.stats.updated === SAMPLE_MODELS.length,
    "re-sync should update existing rows",
  );
  console.log("\nRe-sync account 2: 0 added, all updated ✓");

  console.log("\n=== ALL SIMULATIONS PASSED ===");
}

main();
