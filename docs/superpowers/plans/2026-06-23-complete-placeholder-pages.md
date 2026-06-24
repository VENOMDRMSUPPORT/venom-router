# Complete Placeholder Pages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the three `PageShell` stub pages — Usage, Diagnostics, and Playground — with fully functional UIs backed by real data from the existing routing engine and `usage_records` table.

**Architecture:** Each page gets a new API endpoint in `dashboard-router.server.ts` and a rewritten route component. Usage and Diagnostics read from `usage_records` via the existing `usage.server.ts` DB layer. Playground sends chat requests through a new dashboard-auth-gated proxy endpoint that calls `routeRequest` directly, bypassing external API key auth.

**Tech Stack:** TanStack React Query (`useSuspenseQuery`), Recharts (already in use on overview page), shadcn/ui, `api` client (`@/lib/api-client`), `usage.server.ts` DB layer, `dashboard-router.server.ts` HTTP router.

## Global Constraints

- Dev server port is always **8081** — `bun dev`
- All new API endpoints go inside `handleDashboardAPI` in `src/lib/api/dashboard-router.server.ts`
- All `.server.ts` imports must never appear in client code; server logic stays server-side
- Use `@/` path alias for all imports — never `../../`
- Follow existing `PageControls` + `Header` pattern used by every other route
- No new npm packages — Recharts and all shadcn/ui components are already installed

---

### Task 1: Usage Analytics API Endpoint

**Files:**

- Modify: `src/lib/db/usage.server.ts` (add 2 new exported types + 1 new function)
- Modify: `src/lib/api/dashboard-router.server.ts` (add handler function + register route)

**Interfaces:**

- Produces: `GET /api/dashboard/usage?period=7d|30d` → `UsageAnalytics`
- Produces exported type `UsagePeriodPoint` and `UsageAnalytics` from `usage.server.ts`

- [ ] **Step 1: Add types and `getUsageAnalytics` to `usage.server.ts`**

Open `src/lib/db/usage.server.ts`. After the existing `export type MetricsSummary` block (around line 16), add:

```typescript
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
```

Then at the end of the file, after `getTraffic7d`, add:

```typescript
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
```

- [ ] **Step 2: Add handler and register route in `dashboard-router.server.ts`**

Add this handler function before `handleDashboardAPI`:

```typescript
async function handleGetUsage(supabase: SupabaseClient, days: 7 | 30): Promise<unknown> {
  const { getUsageAnalytics } = await import("@/lib/db/usage.server");
  return getUsageAnalytics(supabase, { days });
}
```

In `handleDashboardAPI`, add before the final `return err("Not found", 404)` line:

```typescript
// ── GET /api/dashboard/usage?period=7d|30d ────────────────────────────────
if (resource === "usage" && !id && method === "GET") {
  const periodParam = url.searchParams.get("period") ?? "7d";
  const days = periodParam === "30d" ? 30 : 7;
  return ok(await handleGetUsage(supabase, days as 7 | 30));
}
```

- [ ] **Step 3: Verify the build**

```bash
bun build
```

Expected: Build completes with no TypeScript errors. If you see `DAY_LABELS` not found, note that it is defined at the top of `usage.server.ts` as a `const` — make sure the new `getUsageAnalytics` function is in the same file.

- [ ] **Step 4: Commit**

```bash
git add src/lib/db/usage.server.ts src/lib/api/dashboard-router.server.ts
git commit -m "feat(api): add /api/dashboard/usage analytics endpoint"
```

---

### Task 2: Usage & Analytics Page

**Files:**

- Modify: `src/routes/_authenticated/usage.tsx` (full rewrite)

**Interfaces:**

- Consumes: `GET /api/dashboard/usage?period=7d|30d` → `UsageAnalytics` (defined in Task 1)
- Consumes: `api.get<UsageAnalytics>(...)` from `@/lib/api-client`

- [ ] **Step 1: Rewrite `usage.tsx`**

Replace the entire file:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { useSuspenseQuery, queryOptions } from "@tanstack/react-query";
import { Suspense, useState } from "react";
import {
  AreaChart,
  Area,
  BarChart,
  Bar,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  CartesianGrid,
} from "recharts";
import { BarChart3, DollarSign, Clock, Zap, CheckCircle2, AlertCircle } from "lucide-react";
import { Header } from "@/components/layout/header";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";
import { api } from "@/lib/api-client";
import { cn } from "@/lib/utils";
import type { UsageAnalytics } from "@/lib/db/usage.server";

export const Route = createFileRoute("/_authenticated/usage")({
  head: () => ({ meta: [{ title: "Usage & Analytics — Venom Router" }] }),
  component: UsagePage,
});

function UsagePage() {
  const [activeTab, setActiveTab] = useState("7d");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  const tabs = [
    { id: "7d", label: "Last 7 days", icon: <Clock className="h-3.5 w-3.5" /> },
    { id: "30d", label: "Last 30 days", icon: <BarChart3 className="h-3.5 w-3.5" /> },
  ];

  return (
    <>
      <Header
        title="Usage & Analytics"
        description="Requests, tokens, latency, and cost across venom models."
        icon={<BarChart3 className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="space-y-6">
          <PageControls
            breadcrumbs={["Dashboard", "Analytics", "Usage"]}
            debugLog={debugLog}
            onClearDebug={() => setDebugLog([])}
            tabs={tabs}
            activeTab={activeTab}
            onTabChange={setActiveTab}
          />
          <Suspense fallback={<Skeleton className="h-96 rounded-2xl" />}>
            <UsageBody period={activeTab as "7d" | "30d"} />
          </Suspense>
        </div>
      </div>
    </>
  );
}

function UsageBody({ period }: { period: "7d" | "30d" }) {
  const { data } = useSuspenseQuery(
    queryOptions({
      queryKey: ["usage-analytics", period],
      queryFn: () => api.get<UsageAnalytics>(`/api/dashboard/usage?period=${period}`),
    }),
  );

  const { summary, traffic, by_model, recent } = data;

  if (summary.total_requests === 0) {
    return (
      <Card className="border-border/60 p-12 text-center space-y-3">
        <BarChart3 className="size-10 mx-auto text-muted-foreground" />
        <p className="font-medium">No usage yet</p>
        <p className="text-sm text-muted-foreground">
          Once your external apps call the gateway, charts and breakdowns appear here.
        </p>
      </Card>
    );
  }

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <KpiCard icon={BarChart3} label="Total Requests" value={String(summary.total_requests)} />
        <KpiCard
          icon={Zap}
          label="Total Tokens"
          value={
            summary.total_tokens >= 1_000_000
              ? `${(summary.total_tokens / 1_000_000).toFixed(1)}M`
              : summary.total_tokens >= 1_000
                ? `${Math.round(summary.total_tokens / 1_000)}K`
                : String(summary.total_tokens)
          }
        />
        <KpiCard
          icon={DollarSign}
          label="Total Cost"
          value={`$${summary.total_cost_usd.toFixed(4)}`}
        />
        <KpiCard
          icon={CheckCircle2}
          label="Success Rate"
          value={`${(summary.success_rate * 100).toFixed(1)}%`}
          accent={
            summary.success_rate >= 0.95
              ? "success"
              : summary.success_rate < 0.8
                ? "error"
                : undefined
          }
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="lg:col-span-2 rounded-2xl border border-border bg-card p-5">
          <h3 className="font-display text-sm font-semibold mb-1">Request volume</h3>
          <p className="text-xs text-muted-foreground mb-4">Requests per day</p>
          <div className="h-52">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={traffic} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
                <defs>
                  <linearGradient id="usageGrad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="oklch(0.68 0.21 275)" stopOpacity={0.4} />
                    <stop offset="100%" stopColor="oklch(0.68 0.21 275)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid
                  stroke="oklch(0.55 0.04 260 / 0.12)"
                  strokeDasharray="3 3"
                  vertical={false}
                />
                <XAxis
                  dataKey="day"
                  stroke="oklch(0.55 0.04 260)"
                  tick={{ fontSize: 11 }}
                  tickLine={false}
                  axisLine={false}
                />
                <YAxis
                  stroke="oklch(0.55 0.04 260)"
                  tick={{ fontSize: 11 }}
                  tickLine={false}
                  axisLine={false}
                />
                <Tooltip
                  contentStyle={{
                    background: "var(--color-card)",
                    border: "1px solid var(--color-border)",
                    borderRadius: 8,
                    fontSize: 12,
                  }}
                />
                <Area
                  type="monotone"
                  dataKey="requests"
                  stroke="oklch(0.55 0.22 275)"
                  strokeWidth={2}
                  fill="url(#usageGrad)"
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </div>

        <div className="rounded-2xl border border-border bg-card p-5">
          <h3 className="font-display text-sm font-semibold mb-1">By model</h3>
          <p className="text-xs text-muted-foreground mb-4">Requests per venom tier</p>
          <div className="h-52">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={by_model} margin={{ top: 4, right: 8, left: -20, bottom: 0 }}>
                <CartesianGrid
                  stroke="oklch(0.55 0.04 260 / 0.12)"
                  strokeDasharray="3 3"
                  vertical={false}
                />
                <XAxis
                  dataKey="slug"
                  stroke="oklch(0.55 0.04 260)"
                  tick={{ fontSize: 11 }}
                  tickLine={false}
                  axisLine={false}
                />
                <YAxis
                  stroke="oklch(0.55 0.04 260)"
                  tick={{ fontSize: 11 }}
                  tickLine={false}
                  axisLine={false}
                />
                <Tooltip
                  contentStyle={{
                    background: "var(--color-card)",
                    border: "1px solid var(--color-border)",
                    borderRadius: 8,
                    fontSize: 12,
                  }}
                />
                <Bar dataKey="requests" radius={[6, 6, 0, 0]} fill="oklch(0.55 0.22 275)" />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </div>
      </div>

      <div className="rounded-2xl border border-border bg-card overflow-hidden">
        <div className="px-5 py-4 border-b border-border">
          <h3 className="font-display text-sm font-semibold">Recent requests</h3>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/30 text-left text-[11px] uppercase tracking-wider text-muted-foreground">
                <th className="px-4 py-3 font-medium">Model</th>
                <th className="px-4 py-3 font-medium">Status</th>
                <th className="px-4 py-3 font-medium">Tokens</th>
                <th className="px-4 py-3 font-medium">Cost</th>
                <th className="px-4 py-3 font-medium">Fallback</th>
                <th className="px-4 py-3 font-medium">Time</th>
              </tr>
            </thead>
            <tbody>
              {recent.map((r) => (
                <tr key={r.id} className="border-b border-border/50 hover:bg-muted/20">
                  <td className="px-4 py-2.5">
                    <code className="text-xs font-mono text-primary">venom/{r.venom_slug}</code>
                  </td>
                  <td className="px-4 py-2.5">
                    {r.success !== false ? (
                      <span className="inline-flex items-center gap-1 text-emerald-500 text-xs">
                        <CheckCircle2 className="h-3.5 w-3.5" /> ok
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-red-500 text-xs">
                        <AlertCircle className="h-3.5 w-3.5" /> failed
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-2.5 tabular-nums text-xs text-muted-foreground">
                    {((r.input_tokens ?? 0) + (r.output_tokens ?? 0)).toLocaleString()}
                  </td>
                  <td className="px-4 py-2.5 tabular-nums text-xs text-muted-foreground">
                    {r.cost_usd != null ? `$${Number(r.cost_usd).toFixed(5)}` : "—"}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-muted-foreground">
                    {r.fallback_used ? "yes" : "—"}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-muted-foreground">
                    {new Date(r.created_at).toLocaleTimeString(undefined, {
                      hour: "2-digit",
                      minute: "2-digit",
                    })}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function KpiCard({
  icon: Icon,
  label,
  value,
  accent,
}: {
  icon: typeof BarChart3;
  label: string;
  value: string;
  accent?: "success" | "error";
}) {
  return (
    <Card className="p-4 border-border/60">
      <div className="flex items-center gap-3">
        <div
          className={cn(
            "flex h-9 w-9 items-center justify-center rounded-lg",
            accent === "success"
              ? "bg-emerald-500/10 text-emerald-600"
              : accent === "error"
                ? "bg-red-500/10 text-red-600"
                : "bg-muted text-muted-foreground",
          )}
        >
          <Icon className="h-4 w-4" />
        </div>
        <div>
          <div className="text-[11px] text-muted-foreground uppercase tracking-wider">{label}</div>
          <div className="font-display text-xl font-bold tabular-nums">{value}</div>
        </div>
      </div>
    </Card>
  );
}
```

- [ ] **Step 2: Start the dev server and verify the page renders**

```bash
bun dev
```

Navigate to `http://localhost:8081/usage`. Expected: page loads, shows either empty state card (if no `usage_records`) or KPI cards + charts. No TypeScript errors in the console.

- [ ] **Step 3: Commit**

```bash
git add src/routes/_authenticated/usage.tsx
git commit -m "feat(ui): implement usage analytics page with charts and request table"
```

---

### Task 3: Diagnostics API Endpoint

**Files:**

- Modify: `src/lib/api/dashboard-router.server.ts` (add handler + register route)

**Interfaces:**

- Produces: `GET /api/dashboard/diagnostics` → `DiagnosticsData`

```typescript
// Shape returned by the new endpoint — inline type used in diagnostics.tsx
type DiagnosticsData = {
  account_health: {
    account_id: string;
    provider_name: string;
    provider_slug: string;
    email: string | null;
    label: string | null;
    status: string;
    last_checked_at: string | null;
    models_enabled: number;
  }[];
  recent_traces: {
    id: string;
    venom_slug: string;
    success: boolean;
    fallback_used: boolean;
    fallback_count: number | null;
    error_code: string | null;
    latency_ms: number | null;
    input_tokens: number | null;
    output_tokens: number | null;
    cost_usd: number | null;
    created_at: string;
  }[];
  stats_24h: {
    total: number;
    errors: number;
    fallbacks: number;
    error_rate: number;
    fallback_rate: number;
    avg_latency_ms: number | null;
  };
};
```

- [ ] **Step 1: Add `handleGetDiagnostics` to `dashboard-router.server.ts`**

Add this function before `handleDashboardAPI`:

```typescript
async function handleGetDiagnostics(supabase: SupabaseClient): Promise<unknown> {
  const since24h = new Date(Date.now() - 24 * 60 * 60_000).toISOString();

  const [stats24hResult, recentTracesResult, accountRowsResult] = await Promise.all([
    supabase
      .from("usage_records")
      .select("id,success,fallback_used,latency_ms")
      .gte("created_at", since24h),
    supabase
      .from("usage_records")
      .select(
        "id,venom_slug,success,fallback_used,fallback_count,error_code,latency_ms,input_tokens,output_tokens,cost_usd,created_at",
      )
      .order("created_at", { ascending: false })
      .limit(25),
    supabase
      .from("accounts")
      .select("id,email,label,status,last_health_check_at,providers(name,slug)")
      .order("last_health_check_at", { ascending: false, nullsFirst: false }),
  ]);

  const records24h = (stats24hResult.data ?? []) as Array<{
    success: boolean;
    fallback_used: boolean;
    latency_ms: number | null;
  }>;
  const total = records24h.length;
  const errors = records24h.filter((r) => r.success === false).length;
  const fallbacks = records24h.filter((r) => r.fallback_used).length;
  const latencies = records24h.map((r) => r.latency_ms).filter((n): n is number => n != null);
  const avg_latency_ms = latencies.length
    ? Math.round(latencies.reduce((s, n) => s + n, 0) / latencies.length)
    : null;

  const accountModelCounts: Record<string, number> = {};
  if ((accountRowsResult.data ?? []).length) {
    const ids = (accountRowsResult.data ?? []).map((a: any) => a.id as string);
    const { data: models } = await supabase
      .from("account_models")
      .select("account_id")
      .in("account_id", ids)
      .eq("enabled", true);
    for (const m of (models ?? []) as Array<{ account_id: string }>) {
      accountModelCounts[m.account_id] = (accountModelCounts[m.account_id] ?? 0) + 1;
    }
  }

  return {
    account_health: (accountRowsResult.data ?? []).map((a: any) => ({
      account_id: a.id as string,
      provider_name: (a.providers?.name ?? "Provider") as string,
      provider_slug: (a.providers?.slug ?? "") as string,
      email: a.email as string | null,
      label: a.label as string | null,
      status: a.status as string,
      last_checked_at: a.last_health_check_at as string | null,
      models_enabled: accountModelCounts[a.id] ?? 0,
    })),
    recent_traces: (recentTracesResult.data ?? []).map((r: any) => ({
      id: r.id as string,
      venom_slug: r.venom_slug as string,
      success: r.success !== false,
      fallback_used: r.fallback_used ?? false,
      fallback_count: r.fallback_count ?? null,
      error_code: r.error_code ?? null,
      latency_ms: r.latency_ms ?? null,
      input_tokens: r.input_tokens ?? null,
      output_tokens: r.output_tokens ?? null,
      cost_usd: r.cost_usd ?? null,
      created_at: r.created_at as string,
    })),
    stats_24h: {
      total,
      errors,
      fallbacks,
      error_rate: total ? errors / total : 0,
      fallback_rate: total ? fallbacks / total : 0,
      avg_latency_ms,
    },
  };
}
```

- [ ] **Step 2: Register the route**

In `handleDashboardAPI`, add before `return err("Not found", 404)`:

```typescript
// ── GET /api/dashboard/diagnostics ───────────────────────────────────────
if (resource === "diagnostics" && !id && method === "GET") {
  return ok(await handleGetDiagnostics(supabase));
}
```

- [ ] **Step 3: Verify build**

```bash
bun build
```

Expected: No TypeScript errors.

- [ ] **Step 4: Commit**

```bash
git add src/lib/api/dashboard-router.server.ts
git commit -m "feat(api): add /api/dashboard/diagnostics endpoint"
```

---

### Task 4: Diagnostics Page

**Files:**

- Modify: `src/routes/_authenticated/diagnostics.tsx` (full rewrite)

**Interfaces:**

- Consumes: `GET /api/dashboard/diagnostics` → `DiagnosticsData` (shape defined in Task 3)

- [ ] **Step 1: Rewrite `diagnostics.tsx`**

Replace the entire file:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { useSuspenseQuery, queryOptions } from "@tanstack/react-query";
import { Suspense, useState } from "react";
import {
  Bug,
  FileText,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Server,
  Zap,
  Clock,
  RefreshCw,
} from "lucide-react";
import { Header } from "@/components/layout/header";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";
import { api } from "@/lib/api-client";
import { ProviderIcon } from "@/components/providers/provider-icon";
import { cn, formatRelativeTime } from "@/lib/utils";

type DiagnosticsData = {
  account_health: {
    account_id: string;
    provider_name: string;
    provider_slug: string;
    email: string | null;
    label: string | null;
    status: string;
    last_checked_at: string | null;
    models_enabled: number;
  }[];
  recent_traces: {
    id: string;
    venom_slug: string;
    success: boolean;
    fallback_used: boolean;
    fallback_count: number | null;
    error_code: string | null;
    latency_ms: number | null;
    input_tokens: number | null;
    output_tokens: number | null;
    cost_usd: number | null;
    created_at: string;
  }[];
  stats_24h: {
    total: number;
    errors: number;
    fallbacks: number;
    error_rate: number;
    fallback_rate: number;
    avg_latency_ms: number | null;
  };
};

export const Route = createFileRoute("/_authenticated/diagnostics")({
  head: () => ({ meta: [{ title: "Diagnostics — Venom Router" }] }),
  component: DiagnosticsPage,
});

function DiagnosticsPage() {
  const [activeTab, setActiveTab] = useState("overview");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  const tabs = [
    { id: "overview", label: "Overview", icon: <Bug className="h-3.5 w-3.5" /> },
    { id: "traces", label: "Traces", icon: <FileText className="h-3.5 w-3.5" /> },
  ];

  return (
    <>
      <Header
        title="Diagnostics"
        description="Health checks, recent errors, and routing traces."
        icon={<Bug className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-6">
        <div className="space-y-6">
          <PageControls
            breadcrumbs={["Dashboard", "System", "Diagnostics"]}
            debugLog={debugLog}
            onClearDebug={() => setDebugLog([])}
            tabs={tabs}
            activeTab={activeTab}
            onTabChange={setActiveTab}
          />
          <Suspense fallback={<Skeleton className="h-96 rounded-2xl" />}>
            <DiagnosticsBody activeTab={activeTab} />
          </Suspense>
        </div>
      </div>
    </>
  );
}

function DiagnosticsBody({ activeTab }: { activeTab: string }) {
  const { data } = useSuspenseQuery(
    queryOptions({
      queryKey: ["diagnostics"],
      queryFn: () => api.get<DiagnosticsData>("/api/dashboard/diagnostics"),
      staleTime: 30_000,
    }),
  );

  return activeTab === "overview" ? (
    <OverviewTab data={data} />
  ) : (
    <TracesTab traces={data.recent_traces} />
  );
}

function OverviewTab({ data }: { data: DiagnosticsData }) {
  const { stats_24h, account_health } = data;

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard icon={Zap} label="Requests (24h)" value={String(stats_24h.total)} />
        <StatCard
          icon={XCircle}
          label="Errors (24h)"
          value={`${stats_24h.errors} (${(stats_24h.error_rate * 100).toFixed(1)}%)`}
          accent={stats_24h.error_rate > 0.05 ? "error" : undefined}
        />
        <StatCard
          icon={RefreshCw}
          label="Fallbacks (24h)"
          value={`${stats_24h.fallbacks} (${(stats_24h.fallback_rate * 100).toFixed(1)}%)`}
        />
        <StatCard
          icon={Clock}
          label="Avg Latency"
          value={stats_24h.avg_latency_ms != null ? `${stats_24h.avg_latency_ms}ms` : "—"}
        />
      </div>

      <Card className="border-border/60 overflow-hidden">
        <div className="px-5 py-4 border-b border-border flex items-center gap-2">
          <Server className="h-4 w-4 text-muted-foreground" />
          <h3 className="font-display text-sm font-semibold">Provider health</h3>
        </div>
        {account_health.length === 0 ? (
          <div className="p-8 text-center text-sm text-muted-foreground">
            No providers connected.
          </div>
        ) : (
          <ul className="divide-y divide-border/50">
            {account_health.map((a) => (
              <li
                key={a.account_id}
                className="flex items-center gap-4 px-5 py-3 hover:bg-muted/20"
              >
                <ProviderIcon slug={a.provider_slug} className="h-8 w-8 shrink-0" />
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium truncate">
                    {a.provider_name}
                    {a.email ? (
                      <span className="text-muted-foreground font-normal"> · {a.email}</span>
                    ) : a.label ? (
                      <span className="text-muted-foreground font-normal"> · {a.label}</span>
                    ) : null}
                  </div>
                  <div className="text-[11px] text-muted-foreground mt-0.5">
                    {a.models_enabled} models enabled
                    {a.last_checked_at
                      ? ` · checked ${formatRelativeTime(a.last_checked_at)}`
                      : " · never checked"}
                  </div>
                </div>
                <Badge
                  variant="outline"
                  className={cn(
                    "text-[10px] shrink-0",
                    a.status === "healthy"
                      ? "border-emerald-500/30 text-emerald-600"
                      : "border-amber-500/30 text-amber-600",
                  )}
                >
                  {a.status}
                </Badge>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}

function TracesTab({ traces }: { traces: DiagnosticsData["recent_traces"] }) {
  if (traces.length === 0) {
    return (
      <Card className="border-border/60 p-12 text-center space-y-3">
        <FileText className="size-10 mx-auto text-muted-foreground" />
        <p className="font-medium">No routing traces yet</p>
        <p className="text-sm text-muted-foreground">
          Send a request through the gateway to see routing trace details here.
        </p>
      </Card>
    );
  }

  return (
    <Card className="border-border/60 overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/30 text-left text-[11px] uppercase tracking-wider text-muted-foreground">
              <th className="px-4 py-3 font-medium">Model</th>
              <th className="px-4 py-3 font-medium">Status</th>
              <th className="px-4 py-3 font-medium">Latency</th>
              <th className="px-4 py-3 font-medium">Tokens</th>
              <th className="px-4 py-3 font-medium">Fallback</th>
              <th className="px-4 py-3 font-medium">Error</th>
              <th className="px-4 py-3 font-medium">Time</th>
            </tr>
          </thead>
          <tbody>
            {traces.map((t) => (
              <tr key={t.id} className="border-b border-border/50 hover:bg-muted/20">
                <td className="px-4 py-2.5">
                  <code className="text-xs font-mono text-primary">venom/{t.venom_slug}</code>
                </td>
                <td className="px-4 py-2.5">
                  {t.success ? (
                    <span className="inline-flex items-center gap-1 text-emerald-500 text-xs">
                      <CheckCircle2 className="h-3.5 w-3.5" /> ok
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-red-500 text-xs">
                      <XCircle className="h-3.5 w-3.5" /> failed
                    </span>
                  )}
                </td>
                <td className="px-4 py-2.5 tabular-nums text-xs text-muted-foreground">
                  {t.latency_ms != null ? `${t.latency_ms}ms` : "—"}
                </td>
                <td className="px-4 py-2.5 tabular-nums text-xs text-muted-foreground">
                  {((t.input_tokens ?? 0) + (t.output_tokens ?? 0)).toLocaleString()}
                </td>
                <td className="px-4 py-2.5 text-xs text-muted-foreground">
                  {t.fallback_used ? (
                    <span className="text-amber-500 flex items-center gap-1">
                      <AlertTriangle className="h-3.5 w-3.5" />
                      {t.fallback_count ?? 1}x
                    </span>
                  ) : (
                    "—"
                  )}
                </td>
                <td className="px-4 py-2.5 text-xs font-mono text-muted-foreground">
                  {t.error_code ?? "—"}
                </td>
                <td className="px-4 py-2.5 text-xs text-muted-foreground">
                  {formatRelativeTime(t.created_at)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function StatCard({
  icon: Icon,
  label,
  value,
  accent,
}: {
  icon: typeof Bug;
  label: string;
  value: string;
  accent?: "error";
}) {
  return (
    <Card className="p-4 border-border/60">
      <div className="flex items-center gap-3">
        <div
          className={cn(
            "flex h-9 w-9 items-center justify-center rounded-lg",
            accent === "error" ? "bg-red-500/10 text-red-600" : "bg-muted text-muted-foreground",
          )}
        >
          <Icon className="h-4 w-4" />
        </div>
        <div>
          <div className="text-[11px] text-muted-foreground uppercase tracking-wider">{label}</div>
          <div className="font-display text-lg font-bold tabular-nums leading-tight">{value}</div>
        </div>
      </div>
    </Card>
  );
}
```

- [ ] **Step 2: Start the dev server and verify**

```bash
bun dev
```

Navigate to `http://localhost:8081/diagnostics`. Expected: "Overview" tab shows 4 stat cards + provider health table. "Traces" tab shows routing traces table or empty state.

- [ ] **Step 3: Commit**

```bash
git add src/routes/_authenticated/diagnostics.tsx
git commit -m "feat(ui): implement diagnostics page with health overview and trace log"
```

---

### Task 5: Playground API Proxy Endpoint

**Files:**

- Modify: `src/lib/api/dashboard-router.server.ts` (add Zod schema + handler + register route)

The playground needs to call the routing engine from the dashboard without requiring the user to have an API key in hand. This endpoint uses dashboard auth (Supabase session) instead of `vk_live_` API key auth.

**Interfaces:**

- Produces: `POST /api/dashboard/playground/chat`
  - Request: `{ venom_slug: "lite"|"pro"|"max", messages: { role: "user"|"assistant"|"system", content: string }[] }`
  - Response: `{ content: string, input_tokens: number, output_tokens: number, latency_ms: number, provider_adapter: string|null, fallback_used: boolean }`

Note on `RoutingResult`: the field names are `inputTokens`, `outputTokens`, `latencyMs`, `fallbackUsed`, `content`, `providerAdapter` — all camelCase. The handler maps them to snake_case for the wire format.

- [ ] **Step 1: Add Zod schema in `dashboard-router.server.ts`**

Add this near the other Zod schemas at the top of the file (after the existing `toggleAccountSchema`):

```typescript
const playgroundChatSchema = z.object({
  venom_slug: z.enum(["lite", "pro", "max"]),
  messages: z
    .array(
      z.object({
        role: z.enum(["user", "assistant", "system"]),
        content: z.string().min(1),
      }),
    )
    .min(1),
});
```

- [ ] **Step 2: Add `handlePlaygroundChat` function**

Add before `handleDashboardAPI`:

```typescript
async function handlePlaygroundChat(
  supabase: SupabaseClient,
  body: { venom_slug: "lite" | "pro" | "max"; messages: { role: string; content: string }[] },
): Promise<unknown> {
  const { routeRequest } = await import("@/lib/routing/engine.server");
  const t0 = Date.now();

  const result = await routeRequest({
    venomSlug: body.venom_slug,
    messages: body.messages as import("@/lib/providers/adapters/types").ChatMessage[],
  });

  if (!result.success) {
    throw Object.assign(new Error(result.errorCode ?? "Routing failed"), { status: 422 });
  }

  return {
    content: result.content ?? "",
    input_tokens: result.inputTokens,
    output_tokens: result.outputTokens,
    latency_ms: Date.now() - t0,
    provider_adapter: result.providerAdapter ?? null,
    fallback_used: result.fallbackUsed,
  };
}
```

- [ ] **Step 3: Register the route**

In `handleDashboardAPI`, add before `return err("Not found", 404)`:

```typescript
// ── POST /api/dashboard/playground/chat ──────────────────────────────────
if (resource === "playground" && id === "chat" && method === "POST") {
  const body = playgroundChatSchema.parse(await parseBody(request));
  return ok(await handlePlaygroundChat(supabase, body));
}
```

- [ ] **Step 4: Verify build**

```bash
bun build
```

Expected: No TypeScript errors. If you see "Property 'providerAdapter' does not exist on type 'RoutingResult'", open `src/lib/routing/types.ts` and find the correct field name, then update the handler.

- [ ] **Step 5: Commit**

```bash
git add src/lib/api/dashboard-router.server.ts
git commit -m "feat(api): add playground proxy endpoint for dashboard-auth chat"
```

---

### Task 6: Playground Page

**Files:**

- Modify: `src/routes/_authenticated/playground.tsx` (full rewrite)

**Interfaces:**

- Consumes: `POST /api/dashboard/playground/chat` (defined in Task 5)
- Consumes: `GET /api/dashboard/usage?period=7d` → `UsageAnalytics` (defined in Task 1; reuses same query cache key as Usage page)

- [ ] **Step 1: Check that `Textarea` component exists**

```bash
ls src/components/ui/textarea.tsx
```

If the file is missing, add it:

```bash
bunx shadcn@latest add textarea
```

- [ ] **Step 2: Rewrite `playground.tsx`**

Replace the entire file:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { useSuspenseQuery, useQueryClient, queryOptions } from "@tanstack/react-query";
import { Suspense, useState, useRef } from "react";
import {
  FlaskConical,
  History,
  Send,
  Loader2,
  CheckCircle2,
  AlertCircle,
  RefreshCw,
} from "lucide-react";
import { Header } from "@/components/layout/header";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Skeleton } from "@/components/ui/skeleton";
import { Card } from "@/components/ui/card";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";
import { api } from "@/lib/api-client";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";
import type { UsageAnalytics } from "@/lib/db/usage.server";

export const Route = createFileRoute("/_authenticated/playground")({
  head: () => ({ meta: [{ title: "Playground — Venom Router" }] }),
  component: PlaygroundPage,
});

type VenomSlug = "lite" | "pro" | "max";

type PlaygroundResponse = {
  content: string;
  input_tokens: number;
  output_tokens: number;
  latency_ms: number;
  provider_adapter: string | null;
  fallback_used: boolean;
};

function PlaygroundPage() {
  const [activeTab, setActiveTab] = useState("playground");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  const tabs = [
    { id: "playground", label: "Playground", icon: <FlaskConical className="h-3.5 w-3.5" /> },
    { id: "history", label: "History", icon: <History className="h-3.5 w-3.5" /> },
  ];

  return (
    <>
      <Header
        title="Playground"
        description="Test prompts directly against the routing engine."
        icon={<FlaskConical className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="space-y-6">
          <PageControls
            breadcrumbs={["Dashboard", "Testing", "Playground"]}
            debugLog={debugLog}
            onClearDebug={() => setDebugLog([])}
            tabs={tabs}
            activeTab={activeTab}
            onTabChange={setActiveTab}
          />
          {activeTab === "playground" ? (
            <PlaygroundTab debugLog={debugLog} setDebugLog={setDebugLog} />
          ) : (
            <Suspense fallback={<Skeleton className="h-64 rounded-2xl" />}>
              <HistoryTab />
            </Suspense>
          )}
        </div>
      </div>
    </>
  );
}

function PlaygroundTab({
  debugLog,
  setDebugLog,
}: {
  debugLog: DebugEntry[];
  setDebugLog: React.Dispatch<React.SetStateAction<DebugEntry[]>>;
}) {
  const [selectedModel, setSelectedModel] = useState<VenomSlug>("pro");
  const [prompt, setPrompt] = useState("");
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<PlaygroundResponse | null>(null);
  const qc = useQueryClient();
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  async function send() {
    if (!prompt.trim() || loading) return;
    setLoading(true);
    setResult(null);

    const t0 = Date.now();
    const req = {
      venom_slug: selectedModel,
      messages: [{ role: "user" as const, content: prompt.trim() }],
    };
    const dbId = `${t0}-${Math.random().toString(36).slice(2)}`;
    setDebugLog((prev) => [
      {
        id: dbId,
        ts: t0,
        op: "playground/chat",
        label: `venom/${selectedModel}`,
        req,
        status: "pending",
      },
      ...prev.slice(0, 49),
    ]);

    try {
      const res = await api.post<PlaygroundResponse>("/api/dashboard/playground/chat", req);
      setResult(res);
      setDebugLog((prev) =>
        prev.map((e) =>
          e.id === dbId ? { ...e, res, ms: Date.now() - t0, status: "success" } : e,
        ),
      );
      qc.invalidateQueries({ queryKey: ["usage-analytics"] });
      qc.invalidateQueries({ queryKey: ["diagnostics"] });
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Request failed";
      toast.error(msg);
      setDebugLog((prev) =>
        prev.map((e) =>
          e.id === dbId ? { ...e, err: msg, ms: Date.now() - t0, status: "error" } : e,
        ),
      );
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="space-y-4 max-w-3xl">
      <div className="flex gap-2">
        {(["lite", "pro", "max"] as const).map((slug) => (
          <button
            key={slug}
            type="button"
            onClick={() => setSelectedModel(slug)}
            className={cn(
              "flex-1 rounded-lg border py-2 text-xs font-mono font-semibold transition-all",
              selectedModel === slug
                ? "border-primary/50 bg-primary/10 text-primary"
                : "border-border/50 bg-background/50 text-muted-foreground hover:border-border",
            )}
          >
            venom/{slug}
          </button>
        ))}
      </div>

      <div className="rounded-2xl border border-border bg-card p-4 space-y-3">
        <Textarea
          ref={textareaRef}
          placeholder="Enter your prompt…"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) send();
          }}
          className="min-h-[140px] resize-none border-0 bg-transparent p-0 focus-visible:ring-0 text-sm"
        />
        <div className="flex items-center justify-between border-t border-border/50 pt-3">
          <span className="text-[11px] text-muted-foreground">⌘Enter or Ctrl+Enter to send</span>
          <Button size="sm" onClick={send} disabled={loading || !prompt.trim()}>
            {loading ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Routing…
              </>
            ) : (
              <>
                <Send className="h-3.5 w-3.5" /> Send
              </>
            )}
          </Button>
        </div>
      </div>

      {result && (
        <div className="rounded-2xl border border-border bg-card p-5 space-y-4">
          <div className="flex items-center justify-between flex-wrap gap-2">
            <span className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              Response
            </span>
            <div className="flex items-center gap-3 text-[10px] text-muted-foreground flex-wrap">
              <span className="flex items-center gap-1">
                <CheckCircle2 className="h-3 w-3 text-emerald-500" />
                {result.latency_ms}ms
              </span>
              <span>{(result.input_tokens + result.output_tokens).toLocaleString()} tokens</span>
              {result.fallback_used && (
                <span className="text-amber-500 flex items-center gap-1">
                  <RefreshCw className="h-3 w-3" /> fallback used
                </span>
              )}
              {result.provider_adapter && (
                <code className="font-mono text-primary">{result.provider_adapter}</code>
              )}
            </div>
          </div>
          <div className="text-sm leading-relaxed whitespace-pre-wrap">{result.content}</div>
        </div>
      )}
    </div>
  );
}

function HistoryTab() {
  const { data } = useSuspenseQuery(
    queryOptions({
      queryKey: ["usage-analytics", "7d"],
      queryFn: () => api.get<UsageAnalytics>("/api/dashboard/usage?period=7d"),
    }),
  );

  const { recent } = data;

  if (recent.length === 0) {
    return (
      <Card className="border-border/60 p-12 text-center space-y-3">
        <History className="size-10 mx-auto text-muted-foreground" />
        <p className="font-medium">No history yet</p>
        <p className="text-sm text-muted-foreground">Send your first request above.</p>
      </Card>
    );
  }

  return (
    <Card className="border-border/60 overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/30 text-left text-[11px] uppercase tracking-wider text-muted-foreground">
              <th className="px-4 py-3 font-medium">Model</th>
              <th className="px-4 py-3 font-medium">Status</th>
              <th className="px-4 py-3 font-medium">Tokens</th>
              <th className="px-4 py-3 font-medium">Cost</th>
              <th className="px-4 py-3 font-medium">Time</th>
            </tr>
          </thead>
          <tbody>
            {recent.map((r) => (
              <tr key={r.id} className="border-b border-border/50 hover:bg-muted/20">
                <td className="px-4 py-2.5">
                  <code className="text-xs font-mono text-primary">venom/{r.venom_slug}</code>
                </td>
                <td className="px-4 py-2.5">
                  {r.success !== false ? (
                    <span className="inline-flex items-center gap-1 text-emerald-500 text-xs">
                      <CheckCircle2 className="h-3.5 w-3.5" /> ok
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-red-500 text-xs">
                      <AlertCircle className="h-3.5 w-3.5" /> failed
                    </span>
                  )}
                </td>
                <td className="px-4 py-2.5 tabular-nums text-xs text-muted-foreground">
                  {((r.input_tokens ?? 0) + (r.output_tokens ?? 0)).toLocaleString()}
                </td>
                <td className="px-4 py-2.5 tabular-nums text-xs text-muted-foreground">
                  {r.cost_usd != null ? `$${Number(r.cost_usd).toFixed(5)}` : "—"}
                </td>
                <td className="px-4 py-2.5 text-xs text-muted-foreground">
                  {formatRelativeTime(r.created_at)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
```

- [ ] **Step 3: Start server and verify**

```bash
bun dev
```

Navigate to `http://localhost:8081/playground`. Expected:

- Three model buttons (lite / pro / max)
- Textarea with send button
- After clicking Send: response card appears showing content + latency + token count
- If no routing rules are configured, the toast shows "Routing failed" or "VENOM_MODEL_NOT_FOUND" — this is expected for a fresh install

Switch to the History tab. Expected: shows recent requests from `usage_records` or empty state.

- [ ] **Step 4: Commit**

```bash
git add src/routes/_authenticated/playground.tsx
git commit -m "feat(ui): implement playground page with model picker and response view"
```

---

## Self-Review

**Spec coverage:**

- ✅ Usage page: DB layer extension (Task 1) + full UI with 4 KPI cards, 2 charts, recent requests table (Task 2)
- ✅ Diagnostics page: API endpoint (Task 3) + full UI with stats, provider health, routing trace table (Task 4)
- ✅ Playground page: server-side proxy (Task 5) + full UI with model picker, prompt input, response card, history tab (Task 6)

**Placeholder scan:**

- All 6 tasks have complete code in every step — no "TBD" or "TODO"
- All commands include expected output
- No "add appropriate error handling" vagueness — error handling is explicit in the code

**Type consistency:**

- `UsageAnalytics` exported from `usage.server.ts` (Task 1), imported in `usage.tsx` (Task 2) and `playground.tsx` (Task 6) ✅
- `DiagnosticsData` defined inline in `diagnostics.tsx` (Task 4) to match the shape returned by `handleGetDiagnostics` (Task 3) ✅
- `PlaygroundResponse` defined inline in `playground.tsx` (Task 6) matches the return shape of `handlePlaygroundChat` (Task 5) ✅
- `RoutingResult.providerAdapter` confirmed to exist in `src/lib/routing/types.ts` ✅
- `RoutingResult` has no `stream` field — removed from `RoutingRequest` call in Task 5 ✅
