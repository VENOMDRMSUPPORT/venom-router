import type { SupabaseClient } from "@supabase/supabase-js";

// ── Types ──────────────────────────────────────────────────────────────────────

export type UsageRecord = {
  id: string;
  venom_slug: string;
  cost_usd: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  success: boolean;
  fallback_used: boolean;
  created_at: string;
};

export type MetricsSummary = {
  total_requests: number;
  total_tokens: number;
  total_cost_usd: number;
  success_rate: number;
  fallback_rate: number;
};

export type UsagePeriodPoint = {
  day: string;
  requests: number;
  tokens: number;
  cost_usd: number;
};

export type UsageAnalytics = {
  summary: MetricsSummary;
  traffic: UsagePeriodPoint[];
  by_model: { slug: string; requests: number; tokens: number; cost_usd: number }[];
  recent: UsageRecord[];
};

// ── Internal helpers ───────────────────────────────────────────────────────────

const DAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"] as const;

// ── Exported functions ─────────────────────────────────────────────────────────

export async function listUsageRecords(
  supabase: SupabaseClient,
  opts?: { since?: string; venomSlug?: string; limit?: number },
): Promise<UsageRecord[]> {
  let q = supabase
    .from("usage_records")
    .select("id,venom_slug,cost_usd,input_tokens,output_tokens,success,fallback_used,created_at")
    .order("created_at", { ascending: false });
  if (opts?.since) q = (q as any).gte("created_at", opts.since);
  if (opts?.venomSlug) q = (q as any).eq("venom_slug", opts.venomSlug);
  if (opts?.limit) q = (q as any).limit(opts.limit);

  const { data, error } = await q;
  if (error) throw new Error(`listUsageRecords: ${error.message}`);
  return (data ?? []) as unknown as UsageRecord[];
}

export async function getMetricsSummary(
  supabase: SupabaseClient,
  opts?: { since?: string },
): Promise<MetricsSummary> {
  let q = supabase
    .from("usage_records")
    .select("success,fallback_used,cost_usd,input_tokens,output_tokens");
  if (opts?.since) q = (q as any).gte("created_at", opts.since);

  const { data, error } = await q;
  if (error) throw new Error(`getMetricsSummary: ${error.message}`);

  const records = (data ?? []) as Array<{
    success: boolean;
    fallback_used: boolean;
    cost_usd: number | null;
    input_tokens: number | null;
    output_tokens: number | null;
  }>;

  const total_requests = records.length;
  const total_tokens = records.reduce(
    (s, r) => s + (r.input_tokens ?? 0) + (r.output_tokens ?? 0),
    0,
  );
  const total_cost_usd = records.reduce((s, r) => s + Number(r.cost_usd ?? 0), 0);
  const successes = records.filter((r) => r.success !== false).length;
  const fallbacks = records.filter((r) => r.fallback_used).length;

  return {
    total_requests,
    total_tokens,
    total_cost_usd,
    success_rate: total_requests ? successes / total_requests : 0,
    fallback_rate: total_requests ? fallbacks / total_requests : 0,
  };
}

export async function getTraffic7d(
  supabase: SupabaseClient,
): Promise<{ day: string; requests: number }[]> {
  const since = new Date(Date.now() - 7 * 86400000).toISOString();
  const { data, error } = await supabase
    .from("usage_records")
    .select("created_at")
    .gte("created_at", since);
  if (error) throw new Error(`getTraffic7d: ${error.message}`);

  const buckets = new Map<string, number>();
  const now = new Date();
  for (let i = 6; i >= 0; i--) {
    const d = new Date(now);
    d.setDate(d.getDate() - i);
    d.setHours(0, 0, 0, 0);
    buckets.set(d.toISOString().slice(0, 10), 0);
  }
  for (const r of (data ?? []) as Array<{ created_at: string }>) {
    const key = new Date(r.created_at).toISOString().slice(0, 10);
    if (buckets.has(key)) buckets.set(key, (buckets.get(key) ?? 0) + 1);
  }
  return [...buckets.entries()].map(([key, requests]) => {
    const d = new Date(key + "T12:00:00");
    return { day: DAY_LABELS[d.getDay()]!, requests };
  });
}

export async function getUsageAnalytics(
  supabase: SupabaseClient,
  opts: { days: 7 | 30 },
): Promise<UsageAnalytics> {
  const since = new Date(Date.now() - opts.days * 86400000).toISOString();

  const { data, error } = await supabase
    .from("usage_records")
    .select("id,venom_slug,cost_usd,input_tokens,output_tokens,success,fallback_used,created_at")
    .gte("created_at", since)
    .order("created_at", { ascending: false });
  if (error) throw new Error(`getUsageAnalytics: ${error.message}`);

  const records = (data ?? []) as UsageRecord[];

  const summary: MetricsSummary = {
    total_requests: records.length,
    total_tokens: records.reduce((s, r) => s + (r.input_tokens ?? 0) + (r.output_tokens ?? 0), 0),
    total_cost_usd: records.reduce((s, r) => s + Number(r.cost_usd ?? 0), 0),
    success_rate: records.length
      ? records.filter((r) => r.success !== false).length / records.length
      : 0,
    fallback_rate: records.length
      ? records.filter((r) => r.fallback_used).length / records.length
      : 0,
  };

  const buckets = new Map<string, { requests: number; tokens: number; cost_usd: number }>();
  const now = new Date();
  for (let i = opts.days - 1; i >= 0; i--) {
    const d = new Date(now);
    d.setDate(d.getDate() - i);
    d.setHours(0, 0, 0, 0);
    buckets.set(d.toISOString().slice(0, 10), { requests: 0, tokens: 0, cost_usd: 0 });
  }
  for (const r of records) {
    const key = new Date(r.created_at).toISOString().slice(0, 10);
    const bucket = buckets.get(key);
    if (bucket) {
      bucket.requests++;
      bucket.tokens += (r.input_tokens ?? 0) + (r.output_tokens ?? 0);
      bucket.cost_usd += Number(r.cost_usd ?? 0);
    }
  }
  const traffic: UsagePeriodPoint[] = [...buckets.entries()].map(([key, v]) => {
    const d = new Date(key + "T12:00:00");
    return { day: DAY_LABELS[d.getDay()]!, ...v };
  });

  const modelMap = new Map<string, { requests: number; tokens: number; cost_usd: number }>();
  for (const r of records) {
    const slug = r.venom_slug;
    const entry = modelMap.get(slug) ?? { requests: 0, tokens: 0, cost_usd: 0 };
    entry.requests++;
    entry.tokens += (r.input_tokens ?? 0) + (r.output_tokens ?? 0);
    entry.cost_usd += Number(r.cost_usd ?? 0);
    modelMap.set(slug, entry);
  }
  const by_model = [...modelMap.entries()].map(([slug, v]) => ({ slug, ...v }));

  return { summary, traffic, by_model, recent: records.slice(0, 50) };
}
