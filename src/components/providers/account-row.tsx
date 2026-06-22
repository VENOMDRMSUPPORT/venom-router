import { useState } from "react";
import {
  ChevronDown,
  RefreshCw,
  Activity,
  Cpu,
  Power,
  Trash2,
  Plus,
  Box,
  Zap,
  AlertCircle,
  CheckCircle2,
  Loader2,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { ProviderIcon } from "./provider-icon";
import { QuotaBar } from "./quota-bar";
import { ActionButton } from "./action-button";
import { resolveAntigravityDisplayQuotaGroups } from "@/lib/providers/antigravity-live-snapshot";

import type { QuotaGroup } from "@/lib/providers/adapters/_shared/quota-types";

export interface ProviderRow {
  id: string;
  slug: string;
  name: string;
  category: "oauth" | "free";
  auth_type: string;
  description: string | null;
  homepage: string | null;
  accounts: AccountRow[];
}

export interface AccountRow {
  id: string;
  label: string | null;
  email: string | null;
  plan: string | null;
  status: string;
  quota_used: number | null;
  quota_total: number | null;
  quota_unit: string | null;
  quota_extra?: Record<string, unknown> | null;
  last_synced_at: string | null;
  last_health_check_at: string | null;
  modelsTotal: number;
  modelsEnabled: number;
}

const QUOTA_SHORT_LABELS: Record<string, string> = {
  "Gemini Models": "GEM",
  "Claude and GPT Models": "OPT",
};

function antigravityGroupShortLabel(name: string): string {
  return QUOTA_SHORT_LABELS[name] ?? name.split(" ")[0]!;
}

function formatRelativeTime(dateStr: string | null): string {
  if (!dateStr) return "never";
  const diffMs = Date.now() - new Date(dateStr).getTime();
  const diffMins = Math.floor(diffMs / 60000);
  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return new Date(dateStr).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function getPlanBadgeStyles(plan: string): string {
  const p = plan.toUpperCase();
  if (p.includes("PRO"))
    return "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400 border-indigo-500/20";
  if (p.includes("FREE"))
    return "bg-zinc-500/10 text-zinc-600 dark:text-zinc-400 border-zinc-500/20";
  if (p.includes("ENTERPRISE") || p.includes("BUSINESS") || p.includes("BIZ"))
    return "bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/20";
  if (p.includes("MAX") || p.includes("ULTRA") || p.includes("PREMIUM"))
    return "bg-fuchsia-500/10 text-fuchsia-600 dark:text-fuchsia-400 border-fuchsia-500/20";
  return "bg-primary/10 text-primary border-primary/20";
}

function AntigravityGroupedQuota({ groups, isOld }: { groups: QuotaGroup[]; isOld?: boolean }) {
  return (
    <div className={cn("flex flex-col gap-0.5", isOld && "opacity-60")}>
      {groups.map((group) => {
        const fh = group.fiveHourQuota;
        if (!fh) return null;
        const usedPct = Math.round((1 - fh.remainingFraction) * 100);
        return (
          <QuotaBar
            key={group.name}
            shortLabel={antigravityGroupShortLabel(group.name)}
            used={usedPct}
            resetsAt={fh.resetTime}
          />
        );
      })}
    </div>
  );
}

export function ProviderAccordion({
  provider,
  uniqueModelCount,
  onAddAccount,
  onSync,
  onFetchModels,
  onTestModels,
  onToggle,
  onDelete,
  onSyncAll,
}: {
  provider: ProviderRow;
  uniqueModelCount?: number;
  onAddAccount: () => void;
  onSync: (accountId: string) => Promise<void>;
  onFetchModels: (accountId: string) => Promise<void>;
  onTestModels: (accountId: string) => void;
  onToggle: (accountId: string, status: "healthy" | "degraded") => Promise<void>;
  onDelete: (accountId: string) => Promise<void>;
  onSyncAll?: (accountIds: string[]) => Promise<void>;
}) {
  const [open, setOpen] = useState(true);
  const [testAllStatus, setTestAllStatus] = useState<"idle" | "loading" | "success" | "error">(
    "idle",
  );

  const healthy = provider.accounts.filter((a) => a.status === "healthy").length;
  const enabledModels =
    uniqueModelCount ?? provider.accounts.reduce((s, a) => s + a.modelsEnabled, 0);
  const hasIssue = provider.accounts.some((a) => a.status === "degraded" || a.status === "expired");

  async function handleTestAll(e: React.MouseEvent) {
    e.stopPropagation();
    if (!onSyncAll) return;
    setTestAllStatus("loading");
    try {
      await onSyncAll(provider.accounts.map((a) => a.id));
      const allHealthy = provider.accounts.every((a) => a.status === "healthy");
      setTestAllStatus(allHealthy ? "success" : "error");
    } catch {
      setTestAllStatus("error");
    } finally {
      setTimeout(() => setTestAllStatus("idle"), 2500);
    }
  }

  return (
    <div
      className={cn(
        "overflow-hidden rounded-xl border transition-all duration-300",
        open
          ? "border-border/80 bg-card shadow-[0_8px_32px_-4px_rgba(0,0,0,0.12),0_2px_8px_-2px_rgba(0,0,0,0.08)] translate-y-[-1px]"
          : "border-border bg-card",
      )}
    >
      <div className="flex items-center gap-3 px-3 py-3 sm:px-4">
        {provider.accounts.length > 0 && (
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            title={open ? "Collapse accounts" : "Expand accounts"}
            className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground/70 transition-colors hover:bg-accent hover:text-foreground"
          >
            <ChevronDown
              className={cn("h-4 w-4 transition-transform duration-300", !open && "-rotate-90")}
            />
          </button>
        )}

        <div className="flex h-9 w-9 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-border/80 bg-background">
          <ProviderIcon slug={provider.slug} className="h-7 w-7" />
        </div>

        <div
          className={cn(
            "min-w-0 flex-1 select-none",
            provider.accounts.length > 0 && "cursor-pointer",
          )}
          onClick={() => provider.accounts.length > 0 && setOpen((o) => !o)}
        >
          <div className="flex flex-wrap items-center gap-2">
            <div
              className={cn(
                "h-2 w-2 rounded-full",
                healthy > 0
                  ? "bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)]"
                  : provider.accounts.length > 0
                    ? "bg-destructive shadow-[0_0_8px_rgba(239,68,68,0.5)] animate-pulse"
                    : "bg-muted-foreground/40",
              )}
            />
            <span className="truncate text-sm font-semibold tracking-tight">{provider.name}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
            <span className="font-medium">
              {enabledModels} unique model{enabledModels !== 1 ? "s" : ""}
            </span>
            {provider.accounts.length > 0 && (
              <>
                <span className="text-muted-foreground/50">•</span>
                <span className="flex items-center gap-1">
                  <span
                    className={cn(
                      "font-semibold",
                      healthy > 0 ? "text-emerald-500" : "text-destructive",
                    )}
                  >
                    {healthy}
                  </span>
                  <span>
                    / {provider.accounts.length} account{provider.accounts.length !== 1 ? "s" : ""}{" "}
                    healthy
                  </span>
                </span>
              </>
            )}
            {hasIssue && (
              <>
                <span className="text-muted-foreground/50">•</span>
                <span className="flex items-center gap-1 font-medium text-yellow-500">
                  <AlertCircle className="h-3 w-3" />
                  Needs attention
                </span>
              </>
            )}
          </div>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          {provider.accounts.length > 0 && onSyncAll && (
            <button
              type="button"
              onClick={handleTestAll}
              disabled={testAllStatus === "loading"}
              title="Sync all accounts"
              className={cn(
                "flex h-7 w-7 items-center justify-center rounded-md transition-colors disabled:opacity-50",
                testAllStatus === "idle" &&
                  "text-muted-foreground hover:bg-accent hover:text-foreground",
                testAllStatus === "loading" && "bg-accent/30 text-muted-foreground",
                testAllStatus === "success" && "bg-emerald-500/10 text-emerald-500",
                testAllStatus === "error" && "bg-red-500/10 text-red-500",
              )}
            >
              {testAllStatus === "loading" && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {testAllStatus === "idle" && <Zap className="h-3.5 w-3.5" />}
              {testAllStatus === "success" && <CheckCircle2 className="h-3.5 w-3.5" />}
              {testAllStatus === "error" && <AlertCircle className="h-3.5 w-3.5" />}
            </button>
          )}
          <button
            type="button"
            onClick={onAddAccount}
            title="Add account"
            className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <Plus className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      {open && (
        <div className="border-t border-border/50 bg-muted/5 px-3 py-3">
          {provider.accounts.length === 0 ? (
            <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border/60 py-6 text-center">
              <div className="flex h-8 w-8 items-center justify-center rounded-full bg-muted">
                <Plus className="h-4 w-4 text-muted-foreground" />
              </div>
              <p className="text-xs font-medium">No accounts yet</p>
              <p className="text-[11px] text-muted-foreground">
                Add an account to start using this provider
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              {provider.accounts.map((a, idx) => (
                <AccountLine
                  key={a.id}
                  index={idx + 1}
                  providerSlug={provider.slug}
                  account={a}
                  onSync={() => onSync(a.id)}
                  onFetchModels={() => onFetchModels(a.id)}
                  onTestModels={() => onTestModels(a.id)}
                  onToggle={() => onToggle(a.id, a.status === "healthy" ? "degraded" : "healthy")}
                  onDelete={() => onDelete(a.id)}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function AccountLine({
  index,
  providerSlug,
  account,
  onSync,
  onFetchModels,
  onTestModels,
  onToggle,
  onDelete,
}: {
  index: number;
  providerSlug: string;
  account: AccountRow;
  onSync: () => Promise<void>;
  onFetchModels: () => Promise<void>;
  onTestModels: () => void;
  onToggle: () => Promise<void>;
  onDelete: () => Promise<void>;
}) {
  const [syncStatus, setSyncStatus] = useState<"idle" | "loading" | "success" | "error">("idle");
  const [scanStatus, setScanStatus] = useState<"idle" | "loading" | "success" | "error">("idle");
  const [toggleStatus, setToggleStatus] = useState<"idle" | "loading">("idle");

  const healthy = account.status === "healthy";
  const isActive = account.status !== "degraded";
  const isUnreachable = account.status === "expired";
  const extra = account.quota_extra as Record<string, unknown> | null | undefined;
  const groups = (extra?.groups as QuotaGroup[] | undefined) ?? [];
  const isAntigravity = providerSlug === "antigravity";
  const antigravityQuotaGroups = isAntigravity ? resolveAntigravityDisplayQuotaGroups(extra) : [];
  const isClaude = providerSlug === "claude-code";
  const isOAuth = providerSlug === "antigravity" || providerSlug === "claude-code";

  const lastFetch = account.last_synced_at;
  const isQuotaOld = lastFetch ? Date.now() - new Date(lastFetch).getTime() > 30 * 60_000 : true;
  const hasQuota =
    antigravityQuotaGroups.length > 0 ||
    groups.length > 0 ||
    extra?.fiveHour != null ||
    extra?.sevenDay != null ||
    (!isAntigravity && account.quota_used != null && account.quota_total != null);

  const emailVal = account.email ?? account.label ?? "Unknown";
  const splitEmail = (val: string) => {
    const idx = val.indexOf("@");
    if (idx === -1) return { user: val, domain: "" };
    return { user: val.substring(0, idx), domain: val.substring(idx) };
  };
  const { user, domain } = splitEmail(emailVal);
  const displayUser =
    user.length <= 10 ? user : user.substring(0, 4) + "…" + user.substring(user.length - 4);

  const enabledCount = account.modelsEnabled;
  const modelsBadgeLabel = enabledCount > 0 ? String(enabledCount) : "—";
  const modelsBadgeColor =
    enabledCount === 0
      ? "text-red-500"
      : enabledCount === account.modelsTotal
        ? "text-emerald-500"
        : "text-amber-500";

  const isTesting = syncStatus === "loading" || scanStatus === "loading";

  const containerClasses = cn(
    "group relative overflow-hidden rounded-lg border transition-all duration-300",
    !isActive
      ? "opacity-60 bg-muted/10 border-border/40 hover:bg-muted/20"
      : isTesting
        ? "bg-amber-500/[0.06] border-amber-500/35"
        : healthy
          ? "bg-emerald-500/[0.015] border-emerald-500/15 hover:border-emerald-500/30 hover:bg-emerald-500/[0.03]"
          : isUnreachable
            ? "bg-red-500/[0.015] border-red-500/15 hover:border-red-500/30"
            : "bg-amber-500/[0.015] border-amber-500/15 hover:border-amber-500/30",
  );

  const railColor = !isActive
    ? "bg-zinc-400 dark:bg-zinc-500"
    : isTesting
      ? "bg-amber-500 animate-pulse"
      : healthy
        ? "bg-emerald-500"
        : isUnreachable
          ? "bg-red-500"
          : "bg-amber-500";

  async function runSync() {
    if (!isActive) return;
    setSyncStatus("loading");
    try {
      await onSync();
      setSyncStatus("success");
    } catch {
      setSyncStatus("error");
    } finally {
      setTimeout(() => setSyncStatus("idle"), 2500);
    }
  }

  async function runFetchModels() {
    if (!isActive) return;
    setScanStatus("loading");
    try {
      await onFetchModels();
      setScanStatus("success");
    } catch {
      setScanStatus("error");
    } finally {
      setTimeout(() => setScanStatus("idle"), 2500);
    }
  }

  async function runToggle() {
    setToggleStatus("loading");
    try {
      await onToggle();
    } finally {
      setToggleStatus("idle");
    }
  }

  return (
    <div className={containerClasses}>
      <div
        className={cn(
          "absolute left-0 top-0 bottom-0 w-[3px] transition-all duration-300",
          railColor,
        )}
      />

      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2.5 px-3 py-2.5 sm:gap-3 sm:px-3.5 sm:py-3">
        <div className="relative flex min-w-0 flex-col gap-1.5">
          <div className="flex min-w-0 items-center gap-2">
            <span
              className="flex h-5 w-6 shrink-0 items-center justify-center rounded border border-border/40 bg-muted/40 font-mono text-[10px] font-bold text-muted-foreground/70 select-none"
              title={`Account #${index}`}
            >
              {index}
            </span>

            <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden">
              <span
                className={cn(
                  "truncate text-xs font-semibold transition-all",
                  !isActive ? "text-muted-foreground/60 line-through" : "text-foreground",
                )}
                title={emailVal}
              >
                <span className="sm:hidden">{displayUser}</span>
                <span className="hidden sm:inline">{user}</span>
                {domain && (
                  <span
                    className={cn(
                      "text-[10.5px] font-normal",
                      !isActive ? "text-muted-foreground/45" : "text-muted-foreground/60",
                    )}
                  >
                    {domain}
                  </span>
                )}
              </span>

              {account.plan && (
                <span
                  className={cn(
                    "shrink-0 rounded border px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider leading-none",
                    !isActive
                      ? "bg-muted text-muted-foreground border-border/40 grayscale"
                      : getPlanBadgeStyles(account.plan),
                  )}
                >
                  {account.plan}
                </span>
              )}
            </div>
          </div>

          {(hasQuota || isOAuth) && (
            <div className={cn("pl-7 sm:pl-8", !isActive && "opacity-40")}>
              {hasQuota ? (
                isAntigravity && antigravityQuotaGroups.length > 0 ? (
                  <AntigravityGroupedQuota groups={antigravityQuotaGroups} isOld={isQuotaOld} />
                ) : isClaude && (extra?.fiveHour || extra?.sevenDay) ? (
                  <div className="flex flex-col gap-0.5">
                    {extra?.fiveHour && (
                      <QuotaBar
                        shortLabel="5H"
                        used={Math.round((extra.fiveHour.used / extra.fiveHour.total) * 100)}
                        resetsAt={extra.fiveHour.resetAt}
                        isOld={isQuotaOld}
                      />
                    )}
                    {extra?.sevenDay && (
                      <QuotaBar
                        shortLabel="7D"
                        used={Math.round((extra.sevenDay.used / extra.sevenDay.total) * 100)}
                        resetsAt={extra.sevenDay.resetAt}
                        isOld={isQuotaOld}
                      />
                    )}
                  </div>
                ) : !isAntigravity && account.quota_used != null && account.quota_total ? (
                  <QuotaBar
                    shortLabel="USE"
                    used={Math.round((account.quota_used / account.quota_total) * 100)}
                    isOld={isQuotaOld}
                  />
                ) : null
              ) : isOAuth ? (
                <span className="text-[10.5px] text-muted-foreground/70">
                  {syncStatus === "loading"
                    ? isAntigravity
                      ? "Syncing profile, models, quota & health…"
                      : "Fetching quota…"
                    : "Use sync button to fetch quota"}
                </span>
              ) : null}
            </div>
          )}
        </div>

        <div className="flex flex-col items-center gap-1.5 self-center">
          <div className="flex items-center gap-0.5">
            <ActionButton
              icon={Activity}
              onClick={() => void runSync()}
              status={syncStatus}
              disabled={syncStatus === "loading" || !isActive}
              title="Sync: health · plan · usage"
            />
            <ActionButton
              icon={Cpu}
              onClick={() => void runFetchModels()}
              status={scanStatus}
              disabled={scanStatus === "loading" || !isActive}
              title="Fetch models from provider"
            />
            <button
              type="button"
              disabled={!isActive}
              title={`${enabledCount} model${enabledCount !== 1 ? "s" : ""} enabled — open test report`}
              onClick={onTestModels}
              className={cn(
                "flex h-7 items-center gap-1 rounded-md px-1.5 text-[10.5px] font-semibold transition-colors disabled:opacity-40 disabled:cursor-not-allowed",
                enabledCount === 0
                  ? "text-red-500 hover:bg-red-500/10"
                  : "text-muted-foreground hover:bg-accent hover:text-foreground",
              )}
            >
              <Box className={cn("h-3.5 w-3.5", modelsBadgeColor)} />
              <span className={cn("tabular-nums", modelsBadgeColor)}>{modelsBadgeLabel}</span>
            </button>
            <ActionButton
              icon={Power}
              onClick={() => void runToggle()}
              status={toggleStatus === "loading" ? "loading" : "idle"}
              disabled={toggleStatus === "loading"}
              title={isActive ? "Disable account" : "Enable account"}
              activeColor={
                isActive ? "text-emerald-500 hover:bg-emerald-500/10" : "text-muted-foreground"
              }
            />
            <ActionButton
              icon={Trash2}
              onClick={() => {
                if (confirm(`Disconnect account "${emailVal}"?`)) void onDelete();
              }}
              title="Disconnect account"
              activeColor="text-muted-foreground hover:text-red-500 hover:bg-red-500/10"
            />
          </div>

          <div
            className={cn(
              "flex items-center justify-center gap-1.5 text-[9.5px] font-medium leading-none text-muted-foreground/60 tabular-nums",
              !isActive && "opacity-50",
            )}
          >
            <span
              className={cn(
                "inline-block h-1.5 w-1.5 shrink-0 rounded-full transition-colors duration-300",
                syncStatus === "loading" || scanStatus === "loading"
                  ? "bg-amber-500 shadow-[0_0_6px_rgba(245,158,11,0.6)] animate-pulse"
                  : healthy
                    ? "bg-emerald-500 shadow-[0_0_6px_rgba(16,185,129,0.5)]"
                    : isUnreachable
                      ? "bg-red-500 shadow-[0_0_6px_rgba(239,68,68,0.5)]"
                      : "bg-amber-500 shadow-[0_0_6px_rgba(234,179,8,0.5)]",
              )}
            />
            <span className="whitespace-nowrap">
              Last Checked: {formatRelativeTime(lastFetch ?? account.last_health_check_at)}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
