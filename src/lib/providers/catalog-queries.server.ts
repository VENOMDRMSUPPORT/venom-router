/** Map account_models + models joins for catalog and list views. Server-only. */

import { providerExternalId } from "./model-keys";

export type AccountModelJoinRow = {
  id: string;
  account_id: string;
  model_id: string;
  enabled: boolean;
  test_status: string | null;
  lifecycle: string;
  latency_ms: number | null;
  last_test_error: string | null;
  last_tested_at: string | null;
  models: {
    id: string;
    external_id: string;
    display_name: string;
    capabilities: Record<string, unknown> | null;
    quality_rating: number | null;
    context_window: number | null;
    input_cost_per_mtok: number | null;
    output_cost_per_mtok: number | null;
    providers: { slug: string; name: string } | { slug: string; name: string }[] | null;
  } | null;
  accounts: {
    id: string;
    email: string | null;
    label: string | null;
    status: string;
  } | null;
};

function unwrap<T>(value: T | T[] | null | undefined): T | null {
  if (value == null) return null;
  return Array.isArray(value) ? (value[0] ?? null) : value;
}

export function mapJoinToCatalogRow(row: AccountModelJoinRow) {
  const model = unwrap(row.models);
  const account = unwrap(row.accounts);
  const provider = unwrap(model?.providers ?? null);
  const caps = model?.capabilities ?? null;

  return {
    id: row.id,
    model_id: row.model_id,
    account_id: row.account_id,
    external_id: model?.external_id ?? "",
    display_name: model?.display_name ?? "",
    capabilities: caps,
    quality_rating: model?.quality_rating ?? null,
    context_window: model?.context_window ?? null,
    input_cost_per_mtok: model?.input_cost_per_mtok ?? null,
    output_cost_per_mtok: model?.output_cost_per_mtok ?? null,
    test_status: row.test_status,
    latency_ms: row.latency_ms,
    last_tested_at: row.last_tested_at,
    lifecycle: row.lifecycle,
    enabled: row.enabled,
    accounts: account,
    providers: provider,
  };
}

export function mapJoinToAccountModelView(row: AccountModelJoinRow) {
  const model = unwrap(row.models);
  const caps = model?.capabilities ?? null;
  return {
    id: row.id,
    model_id: row.model_id,
    external_id: providerExternalId(model?.external_id ?? "", caps),
    display_name: model?.display_name ?? "",
    capabilities: caps,
    latency_ms: row.latency_ms,
    test_status: row.test_status,
    enabled: row.enabled,
    last_test_error: row.last_test_error,
    last_tested_at: row.last_tested_at,
  };
}

export const ACCOUNT_MODELS_SELECT = `
  id,
  account_id,
  model_id,
  enabled,
  test_status,
  lifecycle,
  latency_ms,
  last_test_error,
  last_tested_at,
  models!inner (
    id,
    external_id,
    display_name,
    capabilities,
    quality_rating,
    context_window,
    input_cost_per_mtok,
    output_cost_per_mtok,
    providers!inner (slug, name)
  ),
  accounts!inner (id, email, label, status)
`;
