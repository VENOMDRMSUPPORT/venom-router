import { createFileRoute } from "@tanstack/react-router";
import { useQuery, queryOptions } from "@tanstack/react-query";
import { Bug, Activity, AlertTriangle, CheckCircle2, Server, Clock } from "lucide-react";
import { Header } from "@/components/layout/header";
import { Skeleton } from "@/components/ui/skeleton";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api-client";
import { cn, formatRelativeTime } from "@/lib/utils";
import type { DiagnosticsResponse } from "@/lib/dashboard-types";

export const Route = createFileRoute("/_authenticated/diagnostics")({
  head: () => ({ meta: [{ title: "Diagnostics — Venom Router" }] }),
  component: DiagnosticsPage,
});

function DiagnosticsPage() {
  const { data, isLoading } = useQuery(
    queryOptions({
      queryKey: ["diagnostics"],
      queryFn: () => api.get<DiagnosticsResponse>("/api/dashboard/diagnostics"),
      refetchInterval: 30_000,
    }),
  );

  return (
    <>
      <Header
        title="Diagnostics"
        description="Health checks, recent routing failures, and account status."
        icon={<Bug className="h-5 w-5 text-primary" />}
      />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        {isLoading || !data ? (
          <div className="space-y-6">
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-24 rounded-2xl" />
              ))}
            </div>
            <Skeleton className="h-64 rounded-2xl" />
          </div>
        ) : data.degraded_accounts.length === 0 && data.failed_traces.length === 0 ? (
          <AllHealthy />
        ) : (
          <DiagnosticsBody data={data} />
        )}
      </div>
    </>
  );
}

function DiagnosticsBody({ data }: { data: DiagnosticsResponse }) {
  const { degraded_accounts, failed_traces, health_check_runs } = data;
  const hasIssues = degraded_accounts.length > 0 || failed_traces.length > 0;

  return (
    <div className="space-y-6">
      {/* Health-check summary */}
      <section className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          icon={<CheckCircle2 className="h-4 w-4" />}
          label="Healthy checks (24h)"
          value={health_check_runs.healthy}
          accent="success"
        />
        <StatCard
          icon={<AlertTriangle className="h-4 w-4" />}
          label="Degraded checks"
          value={health_check_runs.degraded}
          accent={health_check_runs.degraded > 0 ? "amber" : "muted"}
        />
        <StatCard
          icon={<Server className="h-4 w-4" />}
          label="Unreachable"
          value={health_check_runs.unreachable}
          accent={health_check_runs.unreachable > 0 ? "red" : "muted"}
        />
        <StatCard
          icon={<Activity className="h-4 w-4" />}
          label="Failed traces (24h)"
          value={failed_traces.length}
          accent={failed_traces.length > 0 ? "red" : "muted"}
        />
      </section>

      {/* Degraded accounts */}
      <Card className="p-5 space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold">Degraded & unreachable accounts</h3>
          <Badge variant="outline" className="text-[10px]">
            {degraded_accounts.length}
          </Badge>
        </div>
        {degraded_accounts.length === 0 ? (
          <p className="text-xs text-muted-foreground py-6 text-center">
            All accounts are healthy.
          </p>
        ) : (
          <div className="space-y-2">
            {degraded_accounts.map((a) => (
              <div
                key={a.id}
                className="flex items-center justify-between gap-3 py-2.5 px-3 rounded-lg border border-border/60 bg-muted/20"
              >
                <div className="flex items-center gap-3 min-w-0">
                  <StatusDot status={a.status} />
                  <div className="min-w-0">
                    <div className="text-xs font-medium truncate">{a.label || a.provider_name}</div>
                    <div className="text-[11px] text-muted-foreground truncate">
                      {a.provider_name}
                      {a.email ? ` · ${a.email}` : ""}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-3 shrink-0">
                  <Badge variant="outline" className="text-[10px]">
                    {a.status}
                  </Badge>
                  <span className="text-[11px] text-muted-foreground flex items-center gap-1">
                    <Clock className="h-3 w-3" />
                    {a.last_health_check_at ? formatRelativeTime(a.last_health_check_at) : "—"}
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* Failed routing traces */}
      <Card className="p-5 space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold">Recent routing failures (24h)</h3>
          <Badge variant="outline" className="text-[10px]">
            {failed_traces.length}
          </Badge>
        </div>
        {failed_traces.length === 0 ? (
          <p className="text-xs text-muted-foreground py-6 text-center">
            No routing failures in the last 24 hours.
          </p>
        ) : (
          <div className="space-y-2 max-h-96 overflow-y-auto scrollbar-app">
            {failed_traces.map((t) => (
              <div
                key={t.id}
                className="py-2.5 px-3 rounded-lg border border-border/60 bg-muted/20 space-y-1.5"
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2 min-w-0">
                    <code className="text-[11px] font-mono text-primary">venom/{t.venom_slug}</code>
                    {t.fallback_attempts > 0 && (
                      <Badge variant="outline" className="text-[9px] h-4 px-1">
                        {t.fallback_attempts} fallbacks
                      </Badge>
                    )}
                  </div>
                  <span className="text-[11px] text-muted-foreground shrink-0">
                    {formatRelativeTime(t.created_at)}
                  </span>
                </div>
                <p className="text-[11px] text-muted-foreground leading-relaxed">
                  {t.decision_reason || t.reason || "Routing failed — no reason recorded."}
                </p>
                <div className="flex items-center gap-4 text-[10px] text-muted-foreground/80">
                  <span>{t.candidates_evaluated} evaluated</span>
                  <span>{t.candidates_filtered} filtered</span>
                  {t.request_id && (
                    <code className="font-mono truncate">{t.request_id.slice(0, 8)}</code>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      {!hasIssues && (
        <p className="text-center text-[11px] text-muted-foreground">
          Auto-refreshing every 30 seconds.
        </p>
      )}
    </div>
  );
}

const STAT_ACCENTS = {
  success: "text-emerald-400 bg-emerald-500/10 border-emerald-500/20",
  amber: "text-amber-500 bg-amber-500/10 border-amber-500/20",
  red: "text-red-400 bg-red-500/10 border-red-500/20",
  muted: "text-muted-foreground bg-muted/40 border-border/40",
} as const;

function StatCard({
  icon,
  label,
  value,
  accent,
}: {
  icon: React.ReactNode;
  label: string;
  value: number;
  accent: keyof typeof STAT_ACCENTS;
}) {
  return (
    <Card className="p-4 space-y-2">
      <div
        className={cn(
          "inline-flex items-center justify-center h-7 w-7 rounded-lg border",
          STAT_ACCENTS[accent],
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

function StatusDot({ status }: { status: string }) {
  const color =
    status === "healthy"
      ? "bg-emerald-500"
      : status === "degraded" || status === "expired"
        ? "bg-amber-500"
        : "bg-red-500";
  return <span className={cn("h-2 w-2 rounded-full shrink-0", color)} />;
}

function AllHealthy() {
  return (
    <Card className="p-12 flex flex-col items-center justify-center text-center">
      <div className="h-12 w-12 rounded-full bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center mb-4">
        <CheckCircle2 className="h-6 w-6 text-emerald-400" />
      </div>
      <h3 className="text-sm font-semibold">All systems operational</h3>
      <p className="mt-1.5 text-xs text-muted-foreground max-w-sm">
        No degraded accounts and no routing failures in the last 24 hours. Auto-refreshing every 30
        seconds.
      </p>
    </Card>
  );
}
