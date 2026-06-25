import { createFileRoute, Link } from "@tanstack/react-router";
import { useSuspenseQuery, queryOptions } from "@tanstack/react-query";
import { Suspense } from "react";
import {
  LayoutDashboard,
  Brain,
  Zap,
  GitBranch,
  Key,
  Activity,
  ArrowUpRight,
  TrendingUp,
  CheckCircle2,
  Server,
  AlertCircle,
  Clock,
} from "lucide-react";
import {
  AreaChart,
  Area,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  CartesianGrid,
  BarChart,
  Bar,
} from "recharts";
import { Header } from "@/components/layout/header";
import { Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import { useDashboardChrome } from "@/lib/use-dashboard-chrome";
import { api } from "@/lib/api-client";
import type { DashboardMetrics } from "@/lib/dashboard-types";
import { cn, formatRelativeTime } from "@/lib/utils";

export const Route = createFileRoute("/_authenticated/overview")({
  head: () => ({ meta: [{ title: "Overview — Venom Router" }] }),
  component: () => (
    <Suspense fallback={<OverviewSkeleton />}>
      <Overview />
    </Suspense>
  ),
});

function OverviewSkeleton() {
  return (
    <>
      <Header
        title="Overview"
        description="Loading…"
        icon={<LayoutDashboard className="h-5 w-5" />}
      />
      <div className="flex-1 p-8">
        <Skeleton className="h-96 rounded-2xl" />
      </div>
    </>
  );
}

function Overview() {
  const { onOpenSidebar } = useDashboardChrome();
  const { data: metrics } = useSuspenseQuery(
    queryOptions({
      queryKey: ["dashboard-metrics"],
      queryFn: () => api.get<DashboardMetrics>("/api/dashboard/metrics"),
    }),
  );

  const checklistDone = [
    metrics.checklist.owner_created,
    metrics.checklist.provider_connected,
    metrics.checklist.routing_configured,
    metrics.checklist.api_key_issued,
    metrics.checklist.first_request_sent,
  ].filter(Boolean).length;

  const allHealthy =
    metrics.accounts_total > 0 && metrics.accounts_healthy === metrics.accounts_total;
  const usageData = metrics.traffic_7d;
  const distributionData = metrics.distribution.map((d) => ({
    name: d.slug,
    v: d.requests,
  }));

  return (
    <>
      <Header
        title="Overview"
        description="System status and traffic across your routed AI models."
        icon={<LayoutDashboard className="h-5 w-5" />}
        onOpenSidebar={onOpenSidebar}
      />

      <div className="flex-1 overflow-y-auto">
        <div className="max-w-[1600px] mx-auto p-4 sm:p-6 lg:p-8 space-y-6">
          <section className="relative overflow-hidden rounded-2xl border border-border bg-gradient-to-br from-card via-card to-primary/5 p-5 sm:p-6">
            <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_right,oklch(0.55_0.22_275/0.12),transparent_55%)]" />
            <div className="relative grid gap-5 sm:grid-cols-[1fr_auto] sm:items-center">
              <div className="min-w-0">
                <div
                  className={cn(
                    "inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-[11px] font-medium",
                    allHealthy
                      ? "border-success/30 bg-success/10 text-success"
                      : "border-amber-500/30 bg-amber-500/10 text-amber-600",
                  )}
                >
                  <span
                    className={cn(
                      "h-1.5 w-1.5 rounded-full animate-pulse",
                      allHealthy ? "bg-success" : "bg-amber-500",
                    )}
                  />
                  {allHealthy ? "All systems operational" : "Some accounts need attention"}
                </div>
                <h2 className="mt-3 font-display text-xl sm:text-2xl font-bold tracking-tight">
                  Welcome back, <span className="text-gradient-brand">Owner</span>
                </h2>
                <p className="mt-1.5 text-sm text-muted-foreground max-w-xl">
                  Your private AI gateway is ready. Connect provider accounts to start routing
                  traffic through
                  <span className="font-mono text-foreground/80"> venom/lite</span>,
                  <span className="font-mono text-foreground/80"> venom/pro</span>, and
                  <span className="font-mono text-foreground/80"> venom/max</span>.
                </p>
              </div>
              {metrics.kpis.provider_models === 0 && (
                <div className="flex flex-col sm:items-end gap-2">
                  <Link
                    to="/providers/oauth"
                    className="inline-flex items-center gap-2 rounded-lg bg-gradient-brand px-4 py-2.5 text-xs font-semibold text-white shadow-elegant hover:shadow-glow transition-shadow"
                  >
                    Connect a provider
                    <ArrowUpRight className="h-3.5 w-3.5" />
                  </Link>
                  <span className="text-[11px] text-muted-foreground">Takes ~2 minutes</span>
                </div>
              )}
            </div>
          </section>

          <section className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <KpiCard
              icon={Brain}
              label="Provider Models"
              value={String(metrics.kpis.provider_models)}
              delta={metrics.working_models > 0 ? `${metrics.working_models} ok` : "—"}
              hint={`${metrics.working_models} working`}
              trend={metrics.kpis.provider_models > 0 ? "up" : "flat"}
            />
            <KpiCard
              icon={Zap}
              label="Venom Models"
              value={String(metrics.kpis.venom_models)}
              delta={metrics.kpis.venom_models > 0 ? String(metrics.kpis.venom_models) : "—"}
              hint="lite · pro · max"
              trend={metrics.kpis.venom_models > 0 ? "up" : "flat"}
              accent
            />
            <KpiCard
              icon={GitBranch}
              label="Routing Rules"
              value={String(metrics.kpis.routing_rules)}
              delta={metrics.kpis.routing_rules > 0 ? "active" : "—"}
              hint={
                metrics.kpis.routing_rules > 0
                  ? `${metrics.kpis.routing_rules} active`
                  : "Create a rule"
              }
              trend={metrics.kpis.routing_rules > 0 ? "up" : "flat"}
            />
            <KpiCard
              icon={Key}
              label="API Keys"
              value={String(metrics.kpis.api_keys)}
              delta={metrics.kpis.api_keys > 0 ? "live" : "—"}
              hint={metrics.kpis.api_keys > 0 ? "non-revoked" : "No keys issued"}
              trend={metrics.kpis.api_keys > 0 ? "up" : "flat"}
            />
          </section>

          <section className="grid gap-4 lg:grid-cols-3">
            <div className="lg:col-span-2 rounded-2xl border border-border bg-card p-5 sm:p-6">
              <div className="mb-4">
                <h3 className="font-display text-sm font-semibold tracking-tight">
                  Request volume
                </h3>
                <p className="text-xs text-muted-foreground mt-0.5">
                  Last 7 days · all venom models
                </p>
              </div>
              <div className="h-56">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={usageData} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
                    <defs>
                      <linearGradient id="reqGrad" x1="0" y1="0" x2="0" y2="1">
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
                      fill="url(#reqGrad)"
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </div>

            <div className="rounded-2xl border border-border bg-card p-5 sm:p-6">
              <div className="flex items-center justify-between mb-4">
                <div>
                  <h3 className="font-display text-sm font-semibold tracking-tight">
                    Distribution
                  </h3>
                  <p className="text-xs text-muted-foreground mt-0.5">By venom tier</p>
                </div>
                <TrendingUp className="h-4 w-4 text-muted-foreground" />
              </div>
              <div className="h-56">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart
                    data={distributionData}
                    margin={{ top: 4, right: 8, left: -20, bottom: 0 }}
                  >
                    <CartesianGrid
                      stroke="oklch(0.55 0.04 260 / 0.12)"
                      strokeDasharray="3 3"
                      vertical={false}
                    />
                    <XAxis
                      dataKey="name"
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
                    <Bar dataKey="v" radius={[6, 6, 0, 0]} fill="oklch(0.55 0.22 275)" />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </div>
          </section>

          <section className="rounded-2xl border border-border bg-card p-5 sm:p-6">
            <div className="flex items-start justify-between gap-4 mb-5">
              <div>
                <h3 className="font-display text-sm font-semibold tracking-tight">
                  Get Venom Router production-ready
                </h3>
                <p className="text-xs text-muted-foreground mt-0.5">
                  Complete these steps to start routing traffic.
                </p>
              </div>
              <div className="text-right">
                <div className="font-display text-2xl font-bold">
                  {checklistDone}
                  <span className="text-muted-foreground text-base font-medium">/5</span>
                </div>
                <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
                  complete
                </div>
              </div>
            </div>
            <ul className="space-y-2.5">
              <ChecklistItem
                done={metrics.checklist.owner_created}
                label="Owner account created"
                hint="Single-owner mode active"
              />
              <ChecklistItem
                done={metrics.checklist.provider_connected}
                label="Connect a provider account"
                hint="OAuth or API key — Anthropic, OpenAI, Together, etc."
              />
              <ChecklistItem
                done={metrics.checklist.routing_configured}
                label="Configure routing rules"
                hint="Decide which provider serves each venom model"
              />
              <ChecklistItem
                done={metrics.checklist.api_key_issued}
                label="Issue an API key"
                hint="Use the OpenAI-compatible endpoint from your apps"
              />
              <ChecklistItem
                done={metrics.checklist.first_request_sent}
                label="Send your first request"
                hint="Verify end-to-end in the Playground"
              />
            </ul>
          </section>

          <section className="grid gap-4 lg:grid-cols-2">
            <div className="rounded-2xl border border-border bg-card p-5 sm:p-6">
              <div className="flex items-center justify-between mb-4">
                <h3 className="font-display text-sm font-semibold tracking-tight">
                  Recent activity
                </h3>
                <Activity className="h-4 w-4 text-muted-foreground" />
              </div>
              {metrics.recent_activity.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-10 text-center">
                  <div className="h-12 w-12 rounded-full bg-muted flex items-center justify-center">
                    <Activity className="h-5 w-5 text-muted-foreground" />
                  </div>
                  <p className="mt-3 text-sm font-medium">No activity yet</p>
                  <p className="mt-1 text-xs text-muted-foreground max-w-xs">
                    Requests, routing decisions, and provider events will appear here.
                  </p>
                </div>
              ) : (
                <ul className="space-y-2">
                  {metrics.recent_activity.map(
                    (item: {
                      id: string;
                      title: string;
                      detail?: string | null;
                      status: string;
                      created_at: string;
                    }) => (
                      <li
                        key={item.id}
                        className="flex items-start gap-3 rounded-lg p-2.5 hover:bg-muted/50 transition-colors"
                      >
                        {item.status === "success" ? (
                          <CheckCircle2 className="h-4 w-4 text-emerald-500 mt-0.5 shrink-0" />
                        ) : item.status === "failure" ? (
                          <AlertCircle className="h-4 w-4 text-red-500 mt-0.5 shrink-0" />
                        ) : (
                          <Clock className="h-4 w-4 text-muted-foreground mt-0.5 shrink-0" />
                        )}
                        <div className="min-w-0 flex-1">
                          <div className="text-sm font-medium truncate">{item.title}</div>
                          {item.detail && (
                            <div className="text-xs text-muted-foreground mt-0.5">
                              {item.detail}
                            </div>
                          )}
                        </div>
                        <span className="text-[10px] text-muted-foreground shrink-0">
                          {formatRelativeTime(item.created_at)}
                        </span>
                      </li>
                    ),
                  )}
                </ul>
              )}
            </div>

            <div className="rounded-2xl border border-border bg-card p-5 sm:p-6">
              <div className="flex items-center justify-between mb-4">
                <h3 className="font-display text-sm font-semibold tracking-tight">
                  Provider health
                </h3>
                <Server className="h-4 w-4 text-muted-foreground" />
              </div>
              {metrics.provider_health.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-10 text-center">
                  <div className="h-12 w-12 rounded-full bg-muted flex items-center justify-center">
                    <Server className="h-5 w-5 text-muted-foreground" />
                  </div>
                  <p className="mt-3 text-sm font-medium">No providers connected</p>
                  <p className="mt-1 text-xs text-muted-foreground max-w-xs">
                    Add a provider account to start monitoring health, quota, and latency.
                  </p>
                </div>
              ) : (
                <ul className="space-y-2">
                  {metrics.provider_health.map(
                    (h: {
                      account_id: string;
                      provider_name: string;
                      provider_slug: string;
                      email?: string | null;
                      label?: string | null;
                      status: string;
                      last_synced_at?: string | null;
                      models_enabled: number;
                      quota_used?: number | null;
                      quota_unit?: string | null;
                    }) => (
                      <li
                        key={h.account_id}
                        className="flex items-center gap-3 rounded-lg p-2.5 hover:bg-muted/50 transition-colors"
                      >
                        <div className="min-w-0 flex-1">
                          <div className="text-sm font-medium truncate">
                            {h.provider_name}
                            {h.email ? (
                              <span className="text-muted-foreground font-normal">
                                {" "}
                                · {h.email}
                              </span>
                            ) : h.label ? (
                              <span className="text-muted-foreground font-normal">
                                {" "}
                                · {h.label}
                              </span>
                            ) : null}
                          </div>
                          <div className="text-[11px] text-muted-foreground mt-0.5">
                            {h.models_enabled} models enabled · synced{" "}
                            {formatRelativeTime(h.last_synced_at ?? null)}
                          </div>
                        </div>
                        <Badge
                          variant="outline"
                          className={cn(
                            "text-[10px] shrink-0",
                            h.status === "healthy"
                              ? "border-emerald-500/30 text-emerald-600"
                              : "border-amber-500/30 text-amber-600",
                          )}
                        >
                          {h.status}
                        </Badge>
                      </li>
                    ),
                  )}
                </ul>
              )}
            </div>
          </section>
        </div>
      </div>
    </>
  );
}

function KpiCard({
  icon: Icon,
  label,
  value,
  delta,
  hint,
  trend,
  accent,
}: {
  icon: typeof Brain;
  label: string;
  value: string;
  delta: string;
  hint: string;
  trend: "up" | "down" | "flat";
  accent?: boolean;
}) {
  return (
    <div
      className={cn(
        "group relative overflow-hidden rounded-2xl border bg-card p-5 transition-all hover:shadow-elegant",
        accent ? "border-primary/30 bg-gradient-to-br from-card to-primary/5" : "border-border",
      )}
    >
      <div className="flex items-center justify-between">
        <div
          className={cn(
            "flex h-9 w-9 items-center justify-center rounded-lg",
            accent ? "bg-gradient-brand text-white" : "bg-muted text-muted-foreground",
          )}
        >
          <Icon className="h-4 w-4" strokeWidth={2} />
        </div>
        <span
          className={cn(
            "text-[10px] font-semibold uppercase tracking-wider px-1.5 py-0.5 rounded",
            trend === "up" && "text-success bg-success/10",
            trend === "down" && "text-destructive bg-destructive/10",
            trend === "flat" && "text-muted-foreground bg-muted",
          )}
        >
          {delta}
        </span>
      </div>
      <div className="mt-4">
        <div className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">
          {label}
        </div>
        <div className="mt-1 font-display text-2xl font-bold tracking-tight">{value}</div>
        <div className="mt-1 text-[11px] text-muted-foreground">{hint}</div>
      </div>
    </div>
  );
}

function ChecklistItem({ done, label, hint }: { done?: boolean; label: string; hint: string }) {
  return (
    <li className="flex items-start gap-3 rounded-lg p-2.5 transition-colors hover:bg-muted/50">
      <div
        className={cn(
          "flex h-6 w-6 shrink-0 items-center justify-center rounded-full mt-0.5",
          done
            ? "bg-success/15 text-success"
            : "border border-dashed border-border text-muted-foreground",
        )}
      >
        {done && <CheckCircle2 className="h-3.5 w-3.5" />}
      </div>
      <div className="min-w-0 flex-1">
        <div className={cn("text-sm font-medium", done && "text-muted-foreground line-through")}>
          {label}
        </div>
        <div className="text-xs text-muted-foreground mt-0.5">{hint}</div>
      </div>
    </li>
  );
}
