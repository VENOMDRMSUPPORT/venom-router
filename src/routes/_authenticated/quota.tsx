import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import {
  Gauge,
  RefreshCw,
  Zap,
  ShieldCheck,
  Mail,
  AlertTriangle,
  ArrowUpRight,
  Search,
  Activity,
  Sparkles,
  AlertCircle,
  Info,
  Clock,
  Layers,
  ArrowRight,
} from "lucide-react";
import { Header } from "@/components/layout/header";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api-client";
import {
  formatSyncToast,
  parseSyncResponse,
  patchAccountInProviders,
  invalidateModelViews,
} from "@/lib/providers/sync-cache";
import type { SyncAccountResult } from "@/lib/providers/sync-response.types";
import type { ProviderRow, AccountRow } from "@/components/providers/account-row";
import { ProviderIcon } from "@/components/providers/provider-icon";
import {
  QuotaRing,
  QuotaPeriodRow,
  QuotaGroupCard,
  PlanInfoCard,
} from "@/components/providers/antigravity-quota-details";
import type { QuotaGroup } from "@/lib/providers/adapters/_shared/quota-types";

export const Route = createFileRoute("/_authenticated/quota")({
  head: () => ({ meta: [{ title: "Quota & Limits — Venom Router" }] }),
  component: QuotaDashboardRoute,
});

function QuotaDashboardRoute() {
  return (
    <>
      <Header
        title="Quota & Limits"
        description="Per-account quota snapshots, rolling limit windows, and rate limit headroom."
        icon={<Gauge className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-6 bg-background/30 scrollbar-thin">
        <QuotaDashboard />
      </div>
    </>
  );
}

function QuotaDashboard() {
  const qc = useQueryClient();

  const [search, setSearch] = useState("");
  const [filterCategory, setFilterCategory] = useState<"all" | "oauth" | "free">("all");
  const [filterStatus, setFilterStatus] = useState<"all" | "healthy" | "issues">("all");
  const [syncingAll, setSyncingAll] = useState(false);

  // Fetch OAuth and Free providers in parallel
  const {
    data: oauthProviders,
    isLoading: oauthLoading,
    isRefetching: oauthRefetching,
  } = useQuery({
    queryKey: ["integrations", "oauth"],
    queryFn: () => api.get<ProviderRow[]>("/api/dashboard/integrations?category=oauth"),
  });

  const {
    data: freeProviders,
    isLoading: freeLoading,
    isRefetching: freeRefetching,
  } = useQuery({
    queryKey: ["integrations", "free"],
    queryFn: () => api.get<ProviderRow[]>("/api/dashboard/integrations?category=free"),
  });

  const isLoading = oauthLoading || freeLoading;
  const isRefetching = oauthRefetching || freeRefetching;

  // Flatten and enrich accounts
  const oauthAccounts = (oauthProviders ?? []).flatMap((p) =>
    p.accounts.map((a) => ({
      ...a,
      providerSlug: p.slug,
      providerName: p.name,
      category: "oauth" as const,
    })),
  );

  const freeAccounts = (freeProviders ?? []).flatMap((p) =>
    p.accounts.map((a) => ({
      ...a,
      providerSlug: p.slug,
      providerName: p.name,
      category: "free" as const,
    })),
  );

  const allAccounts = [...oauthAccounts, ...freeAccounts];

  // Sync handler for single account
  const [syncingAccounts, setSyncingAccounts] = useState<Record<string, boolean>>({});

  async function handleSyncAccount(accountId: string, email: string, category: "oauth" | "free") {
    setSyncingAccounts((prev) => ({ ...prev, [accountId]: true }));
    try {
      const r = await parseSyncResponse(
        await api.post<SyncAccountResult>(`/api/dashboard/accounts/${accountId}/sync`, {
          account_id: accountId,
        }),
      );
      if (r?.ok) {
        qc.setQueryData(["integrations", category], (prev: ProviderRow[] | undefined) =>
          patchAccountInProviders(prev, r),
        );
        toast.success(`Synced ${email}: ${formatSyncToast(r)}`);
        await invalidateModelViews(qc);
      } else {
        toast.error(`Sync failed for ${email}`);
      }
    } catch (e: any) {
      toast.error(e?.message ?? `Sync failed for ${email}`);
      await qc.invalidateQueries({ queryKey: ["integrations", category] });
    } finally {
      setSyncingAccounts((prev) => ({ ...prev, [accountId]: false }));
    }
  }

  // Sync all accounts
  async function handleSyncAll() {
    if (allAccounts.length === 0) return;
    setSyncingAll(true);
    let successCount = 0;
    let failCount = 0;

    toast.info(`Syncing quota details for ${allAccounts.length} account(s)...`);

    await Promise.all(
      allAccounts.map(async (acc) => {
        try {
          const r = await parseSyncResponse(
            await api.post<SyncAccountResult>(`/api/dashboard/accounts/${acc.id}/sync`, {
              account_id: acc.id,
            }),
          );
          if (r?.ok) {
            qc.setQueryData(["integrations", acc.category], (prev: ProviderRow[] | undefined) =>
              patchAccountInProviders(prev, r),
            );
            successCount++;
          } else {
            failCount++;
          }
        } catch {
          failCount++;
        }
      }),
    );

    await invalidateModelViews(qc);
    setSyncingAll(false);

    if (failCount === 0) {
      toast.success(`Successfully synced all ${successCount} accounts!`);
    } else {
      toast.warning(`Sync finished: ${successCount} succeeded, ${failCount} failed.`);
    }
  }

  // Filter accounts
  const filteredAccounts = allAccounts.filter((acc) => {
    const matchesSearch =
      acc.email?.toLowerCase().includes(search.toLowerCase()) ||
      acc.label?.toLowerCase().includes(search.toLowerCase()) ||
      acc.providerName.toLowerCase().includes(search.toLowerCase());

    const matchesCategory = filterCategory === "all" || acc.category === filterCategory;

    const matchesStatus =
      filterStatus === "all" ||
      (filterStatus === "healthy" && acc.status === "healthy") ||
      (filterStatus === "issues" && acc.status !== "healthy");

    return matchesSearch && matchesCategory && matchesStatus;
  });

  // Calculate statistics
  const totalAccounts = allAccounts.length;
  const healthyAccounts = allAccounts.filter((a) => a.status === "healthy").length;
  const issueAccounts = totalAccounts - healthyAccounts;
  const accountsWithQuotas = allAccounts.filter((a) => {
    const extra = a.quota_extra as Record<string, unknown> | null;
    const groups = (extra?.groups as QuotaGroup[] | undefined) ?? [];
    return (
      groups.length > 0 ||
      extra?.fiveHour != null ||
      extra?.sevenDay != null ||
      (a.quota_used != null && a.quota_total != null)
    );
  }).length;

  if (isLoading) {
    return (
      <div className="space-y-6 animate-pulse">
        {/* Stats Grid Skeleton */}
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {[...Array(4)].map((_, i) => (
            <div key={i} className="h-28 rounded-xl border border-border bg-card/40 p-5" />
          ))}
        </div>

        {/* Filter Bar Skeleton */}
        <div className="h-12 rounded-xl border border-border bg-card/20" />

        {/* Cards Grid Skeleton */}
        <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-2">
          {[...Array(2)].map((_, i) => (
            <div key={i} className="h-96 rounded-2xl border border-border bg-card/30 p-6" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Premium Stats Row */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="relative overflow-hidden rounded-xl border border-border/60 bg-gradient-to-br from-card/60 to-card/20 p-5 shadow-sm backdrop-blur-sm">
          <div className="flex items-center justify-between">
            <span className="text-[10px] uppercase tracking-[0.15em] text-muted-foreground font-semibold">
              Total Accounts
            </span>
            <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-sky-500/10 text-sky-400 border border-sky-500/20">
              <Layers className="h-4 w-4" />
            </div>
          </div>
          <div className="mt-3 font-display text-3xl font-bold text-sky-400 leading-none">
            {totalAccounts}
          </div>
          <div className="mt-2 text-[11px] text-muted-foreground">
            across {new Set(allAccounts.map((a) => a.providerSlug)).size} active providers
          </div>
        </div>

        <div className="relative overflow-hidden rounded-xl border border-border/60 bg-gradient-to-br from-card/60 to-card/20 p-5 shadow-sm backdrop-blur-sm">
          <div className="flex items-center justify-between">
            <span className="text-[10px] uppercase tracking-[0.15em] text-muted-foreground font-semibold">
              Active Quotas
            </span>
            <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-violet-500/10 text-violet-400 border border-violet-500/20">
              <Gauge className="h-4 w-4" />
            </div>
          </div>
          <div className="mt-3 font-display text-3xl font-bold text-violet-400 leading-none">
            {accountsWithQuotas}
          </div>
          <div className="mt-2 text-[11px] text-muted-foreground">
            accounts delivering live usage limits
          </div>
        </div>

        <div className="relative overflow-hidden rounded-xl border border-border/60 bg-gradient-to-br from-card/60 to-card/20 p-5 shadow-sm backdrop-blur-sm">
          <div className="flex items-center justify-between">
            <span className="text-[10px] uppercase tracking-[0.15em] text-muted-foreground font-semibold">
              Health & Status
            </span>
            <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
              <ShieldCheck className="h-4 w-4" />
            </div>
          </div>
          <div className="mt-3 font-display text-3xl font-bold text-emerald-400 leading-none">
            {healthyAccounts}{" "}
            <span className="text-sm font-medium text-muted-foreground">/ {totalAccounts}</span>
          </div>
          <div className="mt-2 text-[11px] text-muted-foreground flex items-center gap-1.5">
            {issueAccounts > 0 ? (
              <span className="text-yellow-500 flex items-center gap-1 font-medium">
                <AlertTriangle className="h-3 w-3" /> {issueAccounts} account(s) need sync/attention
              </span>
            ) : (
              <span className="text-emerald-500 font-medium">All systems normal and healthy</span>
            )}
          </div>
        </div>

        <button
          type="button"
          onClick={handleSyncAll}
          disabled={syncingAll || isRefetching || allAccounts.length === 0}
          className={cn(
            "relative overflow-hidden rounded-xl border border-border/60 p-5 text-left shadow-sm backdrop-blur-sm transition-all duration-300 group",
            allAccounts.length === 0
              ? "bg-muted/10 opacity-50 cursor-not-allowed"
              : "bg-gradient-to-br from-primary/10 to-card/40 hover:border-primary/45 hover:shadow-md cursor-pointer",
          )}
        >
          <div className="flex items-center justify-between">
            <span className="text-[10px] uppercase tracking-[0.15em] text-primary/80 font-bold group-hover:text-primary transition-colors">
              Global Refresh
            </span>
            <div
              className={cn(
                "flex h-7 w-7 items-center justify-center rounded-lg bg-primary/15 text-primary border border-primary/30 group-hover:scale-105 transition-all",
                (syncingAll || isRefetching) && "animate-spin",
              )}
            >
              <RefreshCw className="h-4 w-4" />
            </div>
          </div>
          <div className="mt-3 font-display text-lg font-bold text-foreground leading-tight flex items-center gap-1">
            Sync All Data
            <ArrowRight className="h-4 w-4 opacity-0 -translate-x-1 group-hover:opacity-100 group-hover:translate-x-0 transition-all" />
          </div>
          <div className="mt-2 text-[11px] text-muted-foreground">
            Fetch latest quota limits for all accounts in parallel
          </div>
        </button>
      </div>

      {/* Filter and Action Bar */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 p-3 rounded-xl border border-border/50 bg-card/25 backdrop-blur-sm">
        {/* Search */}
        <div className="relative flex-1 max-w-md">
          <Search className="absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground/60" />
          <input
            type="text"
            placeholder="Search accounts, providers, emails..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full h-9.5 rounded-lg border border-border/50 bg-background/50 pl-10 pr-4 text-xs placeholder-muted-foreground/60 focus:border-primary/65 focus:outline-none focus:ring-1 focus:ring-primary/60 transition-all"
          />
        </div>

        {/* Filters */}
        <div className="flex flex-wrap items-center gap-3">
          <div className="inline-flex rounded-lg border border-border/50 bg-background/30 p-0.5 shadow-sm">
            <button
              onClick={() => setFilterCategory("all")}
              className={cn(
                "px-3 py-1 rounded-md text-[11.5px] font-medium transition-all",
                filterCategory === "all"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              All Types
            </button>
            <button
              onClick={() => setFilterCategory("oauth")}
              className={cn(
                "px-3 py-1 rounded-md text-[11.5px] font-medium transition-all",
                filterCategory === "oauth"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              OAuth
            </button>
            <button
              onClick={() => setFilterCategory("free")}
              className={cn(
                "px-3 py-1 rounded-md text-[11.5px] font-medium transition-all",
                filterCategory === "free"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              API Key / Free
            </button>
          </div>

          <div className="inline-flex rounded-lg border border-border/50 bg-background/30 p-0.5 shadow-sm">
            <button
              onClick={() => setFilterStatus("all")}
              className={cn(
                "px-3 py-1 rounded-md text-[11.5px] font-medium transition-all",
                filterStatus === "all"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              All Statuses
            </button>
            <button
              onClick={() => setFilterStatus("healthy")}
              className={cn(
                "px-3 py-1 rounded-md text-[11.5px] font-medium transition-all",
                filterStatus === "healthy"
                  ? "bg-background text-emerald-500 shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              Healthy
            </button>
            <button
              onClick={() => setFilterStatus("issues")}
              className={cn(
                "px-3 py-1 rounded-md text-[11.5px] font-medium transition-all",
                filterStatus === "issues"
                  ? "bg-background text-yellow-500 shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              Needs Attention
            </button>
          </div>
        </div>
      </div>

      {/* Accounts Grid */}
      {filteredAccounts.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-border/70 bg-card/15 py-16 px-4 text-center max-w-2xl mx-auto space-y-4">
          <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-muted/40">
            <Gauge className="h-6 w-6 text-muted-foreground" />
          </div>
          <div className="space-y-2">
            <h3 className="text-sm font-semibold text-foreground">No accounts found</h3>
            <p className="text-xs text-muted-foreground max-w-sm mx-auto leading-relaxed">
              No provider accounts match your filters or search query. Connect a provider or adjust
              filters.
            </p>
          </div>
          <div className="pt-2 flex justify-center gap-3">
            <Link
              to="/providers/oauth"
              className="inline-flex items-center gap-1.5 rounded-lg bg-primary px-3.5 py-1.5 text-xs font-semibold text-primary-foreground shadow transition-colors hover:bg-primary/90"
            >
              OAuth Providers
              <ArrowUpRight className="h-3.5 w-3.5" />
            </Link>
            <Link
              to="/providers/free"
              className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-background px-3.5 py-1.5 text-xs font-semibold text-foreground shadow-sm transition-colors hover:bg-accent"
            >
              Free Providers
            </Link>
          </div>
        </div>
      ) : (
        <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-2">
          {filteredAccounts.map((account) => (
            <AccountQuotaCard
              key={account.id}
              account={account}
              isSyncing={!!syncingAccounts[account.id]}
              onSync={() =>
                handleSyncAccount(
                  account.id,
                  account.email ?? account.label ?? "account",
                  account.category,
                )
              }
            />
          ))}
        </div>
      )}
    </div>
  );
}

function AccountQuotaCard({
  account,
  isSyncing,
  onSync,
}: {
  account: AccountRow & { providerSlug: string; providerName: string; category: "oauth" | "free" };
  isSyncing: boolean;
  onSync: () => void;
}) {
  const healthy = account.status === "healthy";
  const isUnreachable = account.status === "expired";
  const extra = account.quota_extra as Record<string, any> | null;
  const groups = (extra?.groups as QuotaGroup[] | undefined) ?? [];
  const isAntigravity = account.providerSlug === "antigravity";
  const isClaude = account.providerSlug === "claude-code";

  const lastFetch = account.last_synced_at;
  const isQuotaOld = lastFetch ? Date.now() - new Date(lastFetch).getTime() > 30 * 60_000 : true;

  const hasQuota =
    groups.length > 0 ||
    extra?.fiveHour != null ||
    extra?.sevenDay != null ||
    (account.quota_used != null && account.quota_total != null);

  const emailVal = account.email ?? account.label ?? "Active Credentials";

  return (
    <div
      className={cn(
        "relative rounded-2xl border bg-gradient-to-b from-card/75 to-card/25 shadow-md hover:shadow-lg transition-all duration-300 overflow-hidden flex flex-col justify-between group",
        healthy
          ? "border-emerald-500/10 hover:border-emerald-500/30"
          : isUnreachable
            ? "border-red-500/10 hover:border-red-500/30"
            : "border-amber-500/10 hover:border-amber-500/30",
      )}
    >
      {/* Top indicator bar */}
      <div
        className={cn(
          "h-[3px] w-full transition-colors",
          isSyncing
            ? "bg-amber-500 animate-pulse"
            : healthy
              ? "bg-emerald-500/70"
              : isUnreachable
                ? "bg-red-500/70"
                : "bg-amber-500/70",
        )}
      />

      <div className="p-5 sm:p-6 space-y-5 flex-1 flex flex-col justify-between">
        {/* Card Header */}
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-center gap-3.5 min-w-0">
            <ProviderIcon slug={account.providerSlug} className="h-10 w-10 shrink-0" />
            <div className="min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <h4 className="font-semibold text-sm text-foreground truncate max-w-[180px] sm:max-w-[240px]">
                  {emailVal}
                </h4>
                {account.plan && (
                  <span className="shrink-0 rounded-md border border-primary/20 bg-primary/10 text-primary px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider leading-none">
                    {account.plan}
                  </span>
                )}
              </div>
              <p className="text-[11px] text-muted-foreground mt-0.5 flex items-center gap-1.5">
                <span>{account.providerName}</span>
                <span className="text-muted-foreground/40">•</span>
                <span className="capitalize">{account.category} account</span>
              </p>
            </div>
          </div>

          <div className="flex items-center gap-1.5 shrink-0">
            {/* Health status badge */}
            <span
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-medium border",
                healthy
                  ? "bg-emerald-500/10 text-emerald-400 border-emerald-500/20"
                  : isUnreachable
                    ? "bg-red-500/10 text-red-400 border-red-500/20"
                    : "bg-amber-500/10 text-amber-400 border-amber-500/20",
              )}
            >
              <span
                className={cn(
                  "h-1.5 w-1.5 rounded-full shrink-0",
                  healthy
                    ? "bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.5)]"
                    : isUnreachable
                      ? "bg-red-400"
                      : "bg-amber-400",
                )}
              />
              {account.status}
            </span>

            {/* Sync button */}
            <button
              type="button"
              onClick={onSync}
              disabled={isSyncing}
              title="Sync Account Quota"
              className="h-7 w-7 rounded-md flex items-center justify-center border border-border/50 bg-background/50 text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-40 disabled:cursor-not-allowed transition-all"
            >
              <RefreshCw className={cn("h-3.5 w-3.5", isSyncing && "animate-spin")} />
            </button>
          </div>
        </div>

        {/* Card Body - Quota Visual Meters */}
        <div className="space-y-4 py-2 flex-1">
          {!hasQuota ? (
            <div className="rounded-lg border border-border/50 bg-muted/5 p-4 text-center">
              {isSyncing ? (
                <div className="flex items-center justify-center gap-2 py-2 text-xs text-muted-foreground">
                  <RefreshCw className="h-3.5 w-3.5 animate-spin" />
                  <span>Fetching initial quota snapshots...</span>
                </div>
              ) : (
                <div className="space-y-2 py-1">
                  <p className="text-xs text-muted-foreground leading-relaxed">
                    No quota data has been synced for this account yet.
                  </p>
                  <button
                    onClick={onSync}
                    type="button"
                    className="inline-flex items-center gap-1 text-[11px] font-semibold text-primary hover:text-primary/80 transition-colors"
                  >
                    <Zap className="h-3 w-3" /> Sync quota details now
                  </button>
                </div>
              )}
            </div>
          ) : isAntigravity ? (
            // Antigravity (Google / Gemini & Claude & GPT Models)
            <div className="space-y-4">
              <div className="rounded-lg border border-border bg-muted/10 p-3.5 space-y-1">
                <h5 className="text-[11.5px] font-bold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
                  <Sparkles className="h-3.5 w-3.5 text-yellow-400" /> Model Quotas (5-Hour Window)
                </h5>
                <p className="text-xs text-muted-foreground leading-relaxed">
                  Within each group, models share a 5-hour rolling quota window. Quota is consumed
                  proportionally to token cost.
                </p>
              </div>

              {groups.length > 0 ? (
                <div className="grid gap-3.5 sm:grid-cols-2">
                  {groups.map((g) => (
                    <QuotaGroupCard key={g.name} group={g} />
                  ))}
                </div>
              ) : (
                <div className="rounded-lg border border-border/50 bg-card/40 p-4 text-xs text-muted-foreground text-center">
                  No active quota groups found. Sync account to refresh.
                </div>
              )}

              {extra?.planInfo && <PlanInfoCard planInfo={extra.planInfo} />}
            </div>
          ) : isClaude ? (
            // Claude Code
            <div className="space-y-4">
              <div className="rounded-lg border border-border/50 bg-card/30 p-4 divide-y divide-border/50">
                {extra?.fiveHour && extra.fiveHour.total > 0 && (
                  <div className="py-2.5 first:pt-0">
                    <QuotaPeriodRow
                      label="Session Quota (5-Hour)"
                      period={{
                        remainingFraction: 1 - extra.fiveHour.used / extra.fiveHour.total,
                        resetTime: extra.fiveHour.resetAt ?? new Date().toISOString(),
                        isExhausted: extra.fiveHour.used >= extra.fiveHour.total,
                      }}
                      description="5-hour session limit"
                    />
                  </div>
                )}
                {extra?.sevenDay && extra.sevenDay.total > 0 && (
                  <div className="py-2.5 last:pb-0">
                    <QuotaPeriodRow
                      label="Weekly Quota (7-Day)"
                      period={{
                        remainingFraction: 1 - extra.sevenDay.used / extra.sevenDay.total,
                        resetTime: extra.sevenDay.resetAt ?? new Date().toISOString(),
                        isExhausted: extra.sevenDay.used >= extra.sevenDay.total,
                      }}
                      description="7-day weekly limit"
                    />
                  </div>
                )}
              </div>
            </div>
          ) : (
            // Standard Free / API-Key Usage Quota (OpenAI, Anthropic standard, etc.)
            <div className="space-y-3.5">
              {account.quota_used != null && account.quota_total != null ? (
                <div className="rounded-lg border border-border/50 bg-card/30 p-4 space-y-3.5">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <span className="text-xs font-semibold text-foreground">API Quota Usage</span>
                      <p className="text-[10px] text-muted-foreground mt-0.5">
                        Usage metrics as recorded by the provider adapter
                      </p>
                    </div>
                    <div className="text-right">
                      <span className="text-sm font-semibold tabular-nums text-foreground">
                        {Math.round((account.quota_used / account.quota_total) * 100)}%
                      </span>
                    </div>
                  </div>

                  {/* Progress Bar */}
                  <div className="h-2 w-full rounded-full bg-muted-foreground/10 overflow-hidden">
                    <div
                      className={cn(
                        "h-full rounded-full transition-all duration-700 ease-out",
                        account.quota_used / account.quota_total >= 0.9
                          ? "bg-red-500"
                          : account.quota_used / account.quota_total >= 0.7
                            ? "bg-amber-500"
                            : "bg-emerald-500",
                      )}
                      style={{
                        width: `${Math.min(100, (account.quota_used / account.quota_total) * 100)}%`,
                      }}
                    />
                  </div>

                  <div className="flex items-center justify-between text-[11px] text-muted-foreground font-medium">
                    <span>
                      Used:{" "}
                      <span className="text-foreground tabular-nums">
                        {account.quota_used.toLocaleString()}
                      </span>{" "}
                      {account.quota_unit ?? "credits"}
                    </span>
                    <span>
                      Limit:{" "}
                      <span className="text-foreground tabular-nums">
                        {account.quota_total.toLocaleString()}
                      </span>{" "}
                      {account.quota_unit ?? "credits"}
                    </span>
                  </div>
                </div>
              ) : (
                <div className="rounded-lg border border-border/40 bg-card/20 p-4 text-xs text-muted-foreground text-center">
                  This API account uses pay-as-you-go billing directly. No static usage limits are
                  active on this endpoint.
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Card Footer */}
      <div className="px-5 py-3 border-t border-border/40 bg-muted/5 flex items-center justify-between text-[10.5px] text-muted-foreground/70">
        <span className="flex items-center gap-1">
          <Clock className="h-3 w-3 shrink-0 opacity-60" />
          <span>
            Last sync:{" "}
            {lastFetch
              ? new Date(lastFetch).toLocaleString(undefined, {
                  month: "short",
                  day: "numeric",
                  hour: "2-digit",
                  minute: "2-digit",
                })
              : "never"}
          </span>
        </span>
        {isQuotaOld && hasQuota && (
          <span className="text-yellow-500/80 font-medium flex items-center gap-1 shrink-0 animate-pulse">
            <AlertCircle className="h-3 w-3" /> Outdated
          </span>
        )}
      </div>
    </div>
  );
}
