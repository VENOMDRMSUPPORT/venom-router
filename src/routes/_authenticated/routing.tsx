import { createFileRoute } from "@tanstack/react-router";
import { useSuspenseQuery, queryOptions } from "@tanstack/react-query";
import { Suspense, useState, useMemo } from "react";
import {
  GitBranch,
  Zap,
  AlertCircle,
  Activity,
  Cpu,
  ShieldCheck,
  Layers,
  Plus,
} from "lucide-react";
import { Header } from "@/components/layout/header";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api-client";
import type { RoutingRule, VenomModel } from "@/lib/db/venom.server";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";
import { RoutingDebugContext } from "@/components/routing/routing-debug-context";
import { RoutingTierSection } from "@/components/routing/routing-tier-section";
import {
  TIERS,
  GLOBAL_EMPTY_MESSAGE,
  TIER_META,
  type Tier,
} from "@/components/routing/routing-constants";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

export const Route = createFileRoute("/_authenticated/routing")({
  head: () => ({ meta: [{ title: "Routing Rules — Venom Router" }] }),
  component: RoutingPage,
});

function RoutingPage() {
  return (
    <>
      <Header
        title="Routing Rules"
        description="Universal capability aliases with tier-specific routing policy, quota strategy, and fallback depth."
        icon={<GitBranch className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-6 bg-background/30 scrollbar-app">
        <Suspense fallback={<RoutingSkeleton />}>
          <RoutingRulesBody />
        </Suspense>
      </div>
    </>
  );
}

function RoutingSkeleton() {
  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {[1, 2, 3, 4].map((i) => (
          <Skeleton key={i} className="h-20 rounded-xl" />
        ))}
      </div>
      <div className="grid grid-cols-3 gap-4">
        {[1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-24 rounded-xl" />
        ))}
      </div>
      <Skeleton className="h-[400px] rounded-2xl" />
    </div>
  );
}

function RoutingRulesBody() {
  const { data: rules } = useSuspenseQuery(
    queryOptions({
      queryKey: ["routing-rules"],
      queryFn: () => api.get<RoutingRule[]>("/api/dashboard/routing-rules"),
    }),
  );

  const { data: venomModels } = useSuspenseQuery(
    queryOptions({
      queryKey: ["venom-models"],
      queryFn: () => api.get<VenomModel[]>("/api/dashboard/venom-models"),
    }),
  );

  const [activeTab, setActiveTab] = useState("all");
  const [activeTier, setActiveTier] = useState<Tier>("pro");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  function startDebug(op: string, req: unknown): string {
    const entry: DebugEntry = {
      id: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
      ts: Date.now(),
      op,
      label: "Rules",
      req,
      status: "pending",
    };
    setDebugLog((prev) => [entry, ...prev.slice(0, 49)]);
    return entry.id;
  }

  function resolveDebug(entryId: string, res: unknown, ms: number) {
    setDebugLog((prev) =>
      prev.map((e) => (e.id === entryId ? { ...e, res, ms, status: "success" } : e)),
    );
  }

  function rejectDebug(entryId: string, err: string, ms: number) {
    setDebugLog((prev) =>
      prev.map((e) => (e.id === entryId ? { ...e, err, ms, status: "error" } : e)),
    );
  }

  const debugController = useMemo(
    () => ({ start: startDebug, resolve: resolveDebug, reject: rejectDebug }),
    [],
  );

  const displayedRules = useMemo(() => {
    return activeTab === "active" ? rules.filter((r) => r.active) : rules;
  }, [rules, activeTab]);

  const byTier = useMemo(() => {
    return Object.fromEntries(
      TIERS.map((t) => [t, displayedRules.filter((r) => r.venom_slug === t)]),
    ) as Record<Tier, RoutingRule[]>;
  }, [displayedRules]);

  const venomByTier = useMemo(() => {
    return Object.fromEntries(venomModels.map((m) => [m.slug, m])) as Record<
      Tier,
      VenomModel | undefined
    >;
  }, [venomModels]);

  const allTiersEmpty = TIERS.every((t) => rules.filter((r) => r.venom_slug === t).length === 0);

  const stats = useMemo(() => {
    const total = rules.length;
    const active = rules.filter((r) => r.active).length;
    const activeTiers = TIERS.filter((t) =>
      rules.some((r) => r.venom_slug === t && r.active),
    ).length;
    return { total, active, activeTiers };
  }, [rules]);

  const tabs = [
    {
      id: "all",
      label: "All Rules",
      count: rules.length,
      icon: <GitBranch className="h-3.5 w-3.5" />,
    },
    {
      id: "active",
      label: "Active Rules",
      count: rules.filter((r) => r.active).length,
      icon: <Zap className="h-3.5 w-3.5" />,
    },
  ];

  return (
    <RoutingDebugContext.Provider value={debugController}>
      <div className="space-y-6">
        <PageControls
          breadcrumbs={["Dashboard", "Routing", "Rules"]}
          debugLog={debugLog}
          onClearDebug={() => setDebugLog([])}
          tabs={tabs}
          activeTab={activeTab}
          onTabChange={setActiveTab}
        />

        {/* 1. Dashboard Stats Overview */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <Card className="border-border/50 bg-card/45 backdrop-blur-sm shadow-elegant hover:bg-card/70 transition-all duration-300">
            <CardContent className="p-4 flex items-center justify-between">
              <div className="space-y-1">
                <p className="text-[10px] font-mono tracking-wider uppercase text-muted-foreground">
                  Active Rules
                </p>
                <div className="flex items-baseline gap-1.5">
                  <span className="text-2xl font-bold font-display">{stats.active}</span>
                  <span className="text-xs text-muted-foreground font-mono">
                    / {stats.total} total
                  </span>
                </div>
              </div>
              <div className="h-10 w-10 rounded-xl bg-primary/10 flex items-center justify-center text-primary shadow-glow border border-primary/20">
                <GitBranch className="h-5 w-5 animate-pulse" />
              </div>
            </CardContent>
          </Card>

          <Card className="border-border/50 bg-card/45 backdrop-blur-sm shadow-elegant hover:bg-card/70 transition-all duration-300">
            <CardContent className="p-4 flex items-center justify-between">
              <div className="space-y-1">
                <p className="text-[10px] font-mono tracking-wider uppercase text-muted-foreground">
                  Active Tiers
                </p>
                <div className="flex items-baseline gap-1.5">
                  <span className="text-2xl font-bold font-display">{stats.activeTiers}</span>
                  <span className="text-xs text-muted-foreground font-mono">/ 3 configured</span>
                </div>
              </div>
              <div className="h-10 w-10 rounded-xl bg-violet-500/10 flex items-center justify-center text-violet-400 border border-violet-500/20">
                <Layers className="h-5 w-5" />
              </div>
            </CardContent>
          </Card>

          <Card className="border-border/50 bg-card/45 backdrop-blur-sm shadow-elegant hover:bg-card/70 transition-all duration-300">
            <CardContent className="p-4 flex items-center justify-between">
              <div className="space-y-1">
                <p className="text-[10px] font-mono tracking-wider uppercase text-muted-foreground">
                  Router Status
                </p>
                <div className="flex items-center gap-2">
                  <span className="text-sm font-semibold text-emerald-400 font-display">
                    Active Failover
                  </span>
                  <span className="relative flex h-2 w-2">
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
                    <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
                  </span>
                </div>
              </div>
              <div className="h-10 w-10 rounded-xl bg-emerald-500/10 flex items-center justify-center text-emerald-400 border border-emerald-500/20">
                <ShieldCheck className="h-5 w-5" />
              </div>
            </CardContent>
          </Card>

          <Card className="border-border/50 bg-card/45 backdrop-blur-sm shadow-elegant hover:bg-card/70 transition-all duration-300">
            <CardContent className="p-4 flex items-center justify-between">
              <div className="space-y-1">
                <p className="text-[10px] font-mono tracking-wider uppercase text-muted-foreground">
                  Routing Latency
                </p>
                <div className="flex items-baseline gap-1.5">
                  <span className="text-2xl font-bold font-display">~45ms</span>
                  <span className="text-xs text-muted-foreground font-mono">gateway avg</span>
                </div>
              </div>
              <div className="h-10 w-10 rounded-xl bg-sky-500/10 flex items-center justify-center text-sky-400 border border-sky-500/20">
                <Activity className="h-5 w-5" />
              </div>
            </CardContent>
          </Card>
        </div>

        {allTiersEmpty && (
          <div className="rounded-xl border border-amber-500/20 bg-amber-500/5 px-4 py-3 flex items-start gap-3">
            <AlertCircle className="h-4 w-4 text-amber-400 shrink-0 mt-0.5" />
            <p className="text-xs text-amber-300/90 leading-relaxed">{GLOBAL_EMPTY_MESSAGE}</p>
          </div>
        )}

        {/* 2. Interactive Tier Selector Card Deck */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {TIERS.map((tier) => {
            const meta = TIER_META[tier];
            const isActive = activeTier === tier;
            const tierRulesCount = byTier[tier].length;
            const tierActiveRulesCount = byTier[tier].filter((r) => r.active).length;

            return (
              <button
                key={tier}
                onClick={() => setActiveTier(tier)}
                className={`text-left rounded-xl border p-4.5 transition-all duration-300 relative overflow-hidden backdrop-blur-sm cursor-pointer select-none group flex flex-col justify-between h-[105px]
                  ${
                    isActive
                      ? `border-primary bg-primary/[0.04] shadow-glow ring-1 ring-primary/40`
                      : "border-border/60 bg-card/30 hover:border-border hover:bg-card/55 hover:shadow-elegant"
                  }
                `}
              >
                {/* Visual hover gradient */}
                <div
                  className={`absolute inset-0 bg-gradient-to-br ${meta.gradient} opacity-0 group-hover:opacity-100 transition-opacity duration-500 -z-10`}
                />

                <div className="flex items-start justify-between w-full">
                  <div>
                    <span className={`text-xs font-mono font-bold tracking-tight ${meta.color}`}>
                      {meta.label}
                    </span>
                    <h3 className="text-sm font-bold text-foreground mt-0.5 tracking-tight font-display">
                      {tier === "lite"
                        ? "Cost-First"
                        : tier === "pro"
                          ? "Balanced Quality"
                          : "Max Quality"}
                    </h3>
                  </div>
                  <div
                    className={`flex h-7 w-7 items-center justify-center rounded-lg border border-border/80 bg-card shadow-sm group-hover:scale-105 transition-transform duration-300`}
                  >
                    <Zap
                      className={`h-3.5 w-3.5 ${isActive ? meta.color : "text-muted-foreground/60"}`}
                    />
                  </div>
                </div>

                <div className="flex items-center justify-between w-full mt-2">
                  <span className="text-[11px] text-muted-foreground font-medium truncate max-w-[150px]">
                    {meta.subtitle.split(" · ")[0]}
                  </span>
                  <Badge
                    variant={isActive ? "default" : "outline"}
                    className={`text-[9px] font-mono font-semibold px-2 py-0.5 rounded-full shrink-0 transition-colors
                      ${
                        isActive
                          ? "bg-primary text-primary-foreground border-transparent"
                          : tierRulesCount > 0
                            ? "border-emerald-500/20 text-emerald-400 bg-emerald-500/5"
                            : "border-amber-500/20 text-amber-400 bg-amber-500/5"
                      }
                    `}
                  >
                    {tierRulesCount === 0
                      ? "no rules"
                      : `${tierActiveRulesCount}/${tierRulesCount} active`}
                  </Badge>
                </div>
              </button>
            );
          })}
        </div>

        {/* 3. Selected Tier Settings & Rules Area */}
        <div className="transition-all duration-300">
          <RoutingTierSection
            key={activeTier}
            tier={activeTier}
            rules={byTier[activeTier]}
            venomModel={venomByTier[activeTier]}
            allRules={rules}
          />
        </div>
      </div>
    </RoutingDebugContext.Provider>
  );
}
