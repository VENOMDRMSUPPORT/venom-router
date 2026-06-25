import { createFileRoute } from "@tanstack/react-router";
import { useQuery, queryOptions } from "@tanstack/react-query";
import { useState } from "react";
import {
  AreaChart,
  Area,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import { BarChart3, Activity, DollarSign, Zap, CheckCircle2, TrendingUp } from "lucide-react";
import { Header } from "@/components/layout/header";
import { Skeleton } from "@/components/ui/skeleton";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api-client";
import { cn, formatRelativeTime } from "@/lib/utils";
import type { UsageAnalytics } from "@/lib/dashboard-types";

export const Route = createFileRoute("/_authenticated/usage")({
  head: () => ({ meta: [{ title: "Usage & Analytics — Venom Router" }] }),
  component: UsagePage,
});

const PERIODS = [
  { id: "7d", label: "7 days" },
  { id: "30d", label: "30 days" },
] as const;

type PeriodId = (typeof PERIODS)[number]["id"];

function UsagePage() {
  const [period, setPeriod] = useState<PeriodId>("7d");

  const { data, isLoading } = useQuery(
    queryOptions({
      queryKey: ["usage-analytics", period],
      queryFn: () => api.get<UsageAnalytics>(`/api/dashboard/usage?period=${period}`),
    }),
  );

  return (
    <>
      <Header
        title="Usage & Analytics"
        description="Requests, tokens, latency, and cost across venom models."
        icon={<BarChart3 className="h-5 w-5 text-primary" />}
      />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        <div className="flex items-center justify-end gap-2">
          {PERIODS.map((p) => (
            <Button
              key={p.id}
              size="sm"
              variant={period === p.id ? "default" : "outline"}
              onClick={() => setPeriod(p.id)}
              className="h-8"
            >
              {p.label}
            </Button>
          ))}
        </div>

        {isLoading || !data ? (
          <div className="space-y-6">
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-24 rounded-2xl" />
              ))}
            </div>
            <Skeleton className="h-72 rounded-2xl" />
          </div>
        ) : data.summary.total_requests === 0 ? (
          <EmptyState />
        ) : (
          <UsageBody data={data} />
        )}
      </div>
    </>
  );
}

function UsageBody({ data }: { data: UsageAnalytics }) {
  const { summary, traffic, by_model, recent } = data;

  return (
    <div className="space-y-6">
      {/* KPI cards */}
      <section className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <KpiCard
          icon={<Activity className="h-4 w-4" />}
          label="Requests"
          value={summary.total_requests.toLocaleString()}
          accent="primary"
        />
        <KpiCard
          icon={<Zap className="h-4 w-4" />}
          label="Tokens"
          value={formatTokens(summary.total_tokens)}
          accent="blue"
        />
        <KpiCard
          icon={<DollarSign className="h-4 w-4" />}
          label="Est. cost"
          value={`$${summary.total_cost_usd.toFixed(4)}`}
          accent="emerald"
        />
        <KpiCard
          icon={<CheckCircle2 className="h-4 w-4" />}
          label="Success rate"
          value={`${(summary.success_rate * 100).toFixed(1)}%`}
          accent={summary.success_rate >= 0.95 ? "success" : "amber"}
        />
      </section>

      {/* Traffic chart */}
      <Card className="p-5 space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-sm font-semibold">Traffic over time</h3>
            <p className="text-xs text-muted-foreground">Requests per day</p>
          </div>
          {summary.fallback_rate > 0 && (
            <Badge variant="outline" className="text-[10px]">
              <TrendingUp className="h-3 w-3 mr-1" />
              {Math.round(summary.fallback_rate * 100)}% used fallback
            </Badge>
          )}
        </div>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={traffic} margin={{ top: 4, right: 4, bottom: 0, left: -16 }}>
              <defs>
                <linearGradient id="usageArea" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="oklch(0.62 0.19 277)" stopOpacity={0.4} />
                  <stop offset="95%" stopColor="oklch(0.62 0.19 277)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid
                strokeDasharray="3 3"
                stroke="oklch(0.25 0 0 / 0.4)"
                vertical={false}
              />
              <XAxis
                dataKey="day"
                tick={{ fill: "oklch(0.65 0 0)", fontSize: 11 }}
                tickLine={false}
                axisLine={false}
              />
              <YAxis
                tick={{ fill: "oklch(0.65 0 0)", fontSize: 11 }}
                tickLine={false}
                axisLine={false}
                allowDecimals={false}
              />
              <Tooltip
                contentStyle={{
                  background: "oklch(0.18 0 0)",
                  border: "1px solid oklch(0.28 0 0)",
                  borderRadius: 8,
                  fontSize: 12,
                }}
                labelStyle={{ color: "oklch(0.85 0 0)" }}
              />
              <Area
                type="monotone"
                dataKey="requests"
                stroke="oklch(0.62 0.19 277)"
                strokeWidth={2}
                fill="url(#usageArea)"
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </Card>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* By model breakdown */}
        <Card className="p-5 space-y-4">
          <div>
            <h3 className="text-sm font-semibold">By venom model</h3>
            <p className="text-xs text-muted-foreground">Requests and tokens per tier</p>
          </div>
          {by_model.length === 0 ? (
            <p className="text-xs text-muted-foreground py-8 text-center">No model data yet.</p>
          ) : (
            <div className="h-56">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={by_model} margin={{ top: 4, right: 4, bottom: 0, left: -16 }}>
                  <CartesianGrid
                    strokeDasharray="3 3"
                    stroke="oklch(0.25 0 0 / 0.4)"
                    vertical={false}
                  />
                  <XAxis
                    dataKey="slug"
                    tick={{ fill: "oklch(0.65 0 0)", fontSize: 11 }}
                    tickLine={false}
                    axisLine={false}
                  />
                  <YAxis
                    tick={{ fill: "oklch(0.65 0 0)", fontSize: 11 }}
                    tickLine={false}
                    axisLine={false}
                    allowDecimals={false}
                  />
                  <Tooltip
                    contentStyle={{
                      background: "oklch(0.18 0 0)",
                      border: "1px solid oklch(0.28 0 0)",
                      borderRadius: 8,
                      fontSize: 12,
                    }}
                    cursor={{ fill: "oklch(0.25 0 0 / 0.3)" }}
                  />
                  <Bar dataKey="requests" fill="oklch(0.62 0.19 277)" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}
          <div className="space-y-2">
            {by_model.map((m) => (
              <div
                key={m.slug}
                className="flex items-center justify-between text-xs py-1.5 border-b border-border/40 last:border-0"
              >
                <span className="font-mono text-primary">venom/{m.slug}</span>
                <div className="flex items-center gap-4 text-muted-foreground">
                  <span className="tabular-nums">{m.requests} req</span>
                  <span className="tabular-nums">{formatTokens(m.tokens)}</span>
                  <span className="tabular-nums text-emerald-500">${m.cost_usd.toFixed(4)}</span>
                </div>
              </div>
            ))}
          </div>
        </Card>

        {/* Recent requests */}
        <Card className="p-5 space-y-4">
          <div>
            <h3 className="text-sm font-semibold">Recent requests</h3>
            <p className="text-xs text-muted-foreground">
              Last {Math.min(recent.length, 20)} routed
            </p>
          </div>
          <div className="space-y-1 max-h-80 overflow-y-auto scrollbar-app">
            {recent.slice(0, 20).map((r) => (
              <div
                key={r.id}
                className="flex items-center justify-between gap-3 py-2 px-2 -mx-2 rounded-md hover:bg-muted/40"
              >
                <div className="flex items-center gap-2 min-w-0">
                  <span
                    className={cn(
                      "h-1.5 w-1.5 rounded-full shrink-0",
                      r.success ? "bg-emerald-500" : "bg-red-500",
                    )}
                  />
                  <code className="text-[11px] font-mono text-primary">venom/{r.venom_slug}</code>
                  {r.fallback_used && (
                    <Badge variant="outline" className="text-[9px] h-4 px-1">
                      fallback
                    </Badge>
                  )}
                </div>
                <div className="flex items-center gap-3 text-[11px] text-muted-foreground shrink-0">
                  <span className="tabular-nums">
                    {(r.input_tokens ?? 0) + (r.output_tokens ?? 0)} tok
                  </span>
                  {r.cost_usd != null && (
                    <span className="tabular-nums text-emerald-500">${r.cost_usd.toFixed(4)}</span>
                  )}
                  <span>{formatRelativeTime(r.created_at)}</span>
                </div>
              </div>
            ))}
            {recent.length === 0 && (
              <p className="text-xs text-muted-foreground py-8 text-center">No requests yet.</p>
            )}
          </div>
        </Card>
      </div>
    </div>
  );
}

const ACCENTS = {
  primary: "text-primary bg-primary/10 border-primary/20",
  blue: "text-sky-400 bg-sky-500/10 border-sky-500/20",
  emerald: "text-emerald-400 bg-emerald-500/10 border-emerald-500/20",
  success: "text-emerald-400 bg-emerald-500/10 border-emerald-500/20",
  amber: "text-amber-500 bg-amber-500/10 border-amber-500/20",
} as const;

function KpiCard({
  icon,
  label,
  value,
  accent,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  accent: keyof typeof ACCENTS;
}) {
  return (
    <Card className="p-4 space-y-2">
      <div
        className={cn(
          "inline-flex items-center justify-center h-7 w-7 rounded-lg border",
          ACCENTS[accent],
        )}
      >
        {icon}
      </div>
      <div>
        <div className="text-xl font-semibold tabular-nums">{value}</div>
        <div className="text-[11px] text-muted-foreground">{label}</div>
      </div>
    </Card>
  );
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return n.toString();
}

function EmptyState() {
  return (
    <Card className="p-12 flex flex-col items-center justify-center text-center">
      <div className="h-12 w-12 rounded-full bg-muted/50 flex items-center justify-center mb-4">
        <BarChart3 className="h-6 w-6 text-muted-foreground" />
      </div>
      <h3 className="text-sm font-semibold">No usage yet</h3>
      <p className="mt-1.5 text-xs text-muted-foreground max-w-sm">
        Once your external apps call the gateway via the{" "}
        <code className="text-foreground/80">/v1/chat/completions</code> endpoint, charts and
        breakdowns will appear here.
      </p>
    </Card>
  );
}
