import { useState } from "react";
import { ChevronDown, Info } from "lucide-react";
import type { QuotaGroup } from "@/lib/providers/adapters/_shared/quota-types";
import { cn } from "@/lib/utils";

interface QuotaExtra {
  groups?: QuotaGroup[];
  planInfo?: Record<string, unknown>;
  projectId?: string;
  availablePromptCredits?: number;
  fiveHour?: { used: number; total: number; resetAt?: string } | null;
  sevenDay?: { used: number; total: number; resetAt?: string } | null;
  quotas?: Record<string, { used: number; total: number; resetAt?: string }>;
}

export function QuotaRing({
  remainingFraction,
  size = 44,
}: {
  remainingFraction: number;
  size?: number;
}) {
  const pct = Math.round(remainingFraction * 100);
  const r = (size - 6) / 2;
  const circ = 2 * Math.PI * r;
  const usedFraction = 1 - remainingFraction;
  const dashLen = usedFraction * circ;
  const color =
    remainingFraction <= 0 ? "#ef4444" : remainingFraction < 0.2 ? "#f97316" : "#22c55e";

  return (
    <div className="flex items-center gap-3">
      <span className="text-lg font-semibold tabular-nums">{pct}%</span>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className="rotate-[-90deg]">
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          stroke="rgba(255,255,255,0.1)"
          strokeWidth={5}
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          stroke={color}
          strokeWidth={5}
          strokeDasharray={`${dashLen} ${circ}`}
          strokeLinecap="round"
        />
      </svg>
    </div>
  );
}

export function formatResetTime(isoTime: string): string {
  const diff = new Date(isoTime).getTime() - Date.now();
  if (diff <= 0) return "refreshing now";
  const totalSeconds = Math.floor(diff / 1000);
  const days = Math.floor(totalSeconds / 86400);
  const hours = Math.floor((totalSeconds % 86400) / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  if (days > 0) return `${days} day${days > 1 ? "s" : ""}, ${hours} hour${hours !== 1 ? "s" : ""}`;
  if (hours > 0)
    return `${hours} hour${hours !== 1 ? "s" : ""}, ${minutes} minute${minutes !== 1 ? "s" : ""}`;
  return `${minutes} minute${minutes !== 1 ? "s" : ""}`;
}

export function QuotaPeriodRow({
  label,
  period,
  description,
}: {
  label: string;
  period: { remainingFraction: number; resetTime: string; isExhausted: boolean };
  description: string;
}) {
  const refreshLabel = formatResetTime(period.resetTime);
  return (
    <div className="flex items-start justify-between gap-4 py-3">
      <div className="flex-1 min-w-0">
        <p className="font-semibold text-sm text-foreground">{label}</p>
        <p className="text-xs text-muted-foreground mt-0.5">
          {period.isExhausted
            ? `Exhausted — will fully refresh in ${refreshLabel}.`
            : `You have used some of your ${description}, it will fully refresh in ${refreshLabel}.`}
        </p>
      </div>
      <QuotaRing remainingFraction={period.remainingFraction} />
    </div>
  );
}

export function QuotaGroupCard({ group }: { group: QuotaGroup }) {
  const [showModels, setShowModels] = useState(false);
  return (
    <div className="rounded-lg border border-border bg-card/50 overflow-hidden">
      <div className="px-5 py-4 border-b border-border/60 flex items-center gap-2">
        <span className="font-semibold text-sm">{group.name}</span>
        <Info className="h-3.5 w-3.5 text-muted-foreground" />
      </div>
      {group.fiveHourQuota ? (
        <div className="px-5 divide-y divide-border/50">
          <QuotaPeriodRow
            label="Five Hour Limit"
            period={group.fiveHourQuota}
            description="5-hour limit"
          />
        </div>
      ) : (
        <div className="px-5 py-4 text-xs text-muted-foreground">
          No quota data available for this group.
        </div>
      )}
      {group.modelIds.length > 0 && (
        <div className="px-5 py-2 border-t border-border/40">
          <button
            type="button"
            onClick={() => setShowModels((v) => !v)}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            <ChevronDown
              className={cn("h-3.5 w-3.5 transition-transform", showModels && "rotate-180")}
            />
            {showModels ? "Hide" : "Show"} {group.modelIds.length} model
            {group.modelIds.length !== 1 ? "s" : ""}
          </button>
          {showModels && (
            <div className="mt-2 flex flex-wrap gap-1.5 pb-2">
              {group.modelIds.map((id) => (
                <span
                  key={id}
                  className="text-xs px-2 py-0.5 rounded-full bg-muted text-muted-foreground font-mono"
                >
                  {id}
                </span>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export function PlanInfoCard({ planInfo }: { planInfo?: Record<string, unknown> }) {
  if (!planInfo) return null;
  return (
    <div className="rounded-lg border border-border/60 bg-card/30 p-4 text-xs space-y-1">
      <p className="font-medium text-sm mb-2">Account Plan</p>
      {planInfo.currentTier != null && (
        <p>
          <span className="text-muted-foreground">Current tier: </span>
          <span className="font-medium">{String(planInfo.currentTier)}</span>
        </p>
      )}
      {planInfo.paidTierName != null && (
        <p>
          <span className="text-muted-foreground">Paid tier: </span>
          <span className="font-medium">{String(planInfo.paidTierName)}</span>
        </p>
      )}
      {planInfo.upgradeText != null && (
        <p className="text-muted-foreground italic mt-1">{String(planInfo.upgradeText)}</p>
      )}
      {planInfo.projectId != null && (
        <p className="mt-1">
          <span className="text-muted-foreground">Project ID: </span>
          <span className="font-mono">{String(planInfo.projectId)}</span>
        </p>
      )}
    </div>
  );
}

export function ClaudeQuotaDetails({ extra }: { extra?: QuotaExtra | null }) {
  const [expanded, setExpanded] = useState(false);
  if (!extra) return null;
  const hasWindows = extra.fiveHour || extra.sevenDay || extra.quotas;
  if (!hasWindows) return null;

  return (
    <div>
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground transition-colors"
      >
        <ChevronDown className={cn("h-3.5 w-3.5 transition-transform", expanded && "rotate-180")} />
        {expanded ? "Hide" : "Show"} Quota Details
      </button>
      {expanded && (
        <div className="mt-2 space-y-2">
          {extra.fiveHour && extra.fiveHour.total > 0 && (
            <QuotaPeriodRow
              label="Session (5h)"
              period={{
                remainingFraction: 1 - extra.fiveHour.used / extra.fiveHour.total,
                resetTime: extra.fiveHour.resetAt ?? new Date().toISOString(),
                isExhausted: extra.fiveHour.used >= extra.fiveHour.total,
              }}
              description="5-hour session limit"
            />
          )}
          {extra.sevenDay && extra.sevenDay.total > 0 && (
            <QuotaPeriodRow
              label="Weekly (7d)"
              period={{
                remainingFraction: 1 - extra.sevenDay.used / extra.sevenDay.total,
                resetTime: extra.sevenDay.resetAt ?? new Date().toISOString(),
                isExhausted: extra.sevenDay.used >= extra.sevenDay.total,
              }}
              description="7-day weekly limit"
            />
          )}
        </div>
      )}
    </div>
  );
}

export function AntigravityQuotaDetails({ extra }: { extra?: QuotaExtra | null }) {
  const [expanded, setExpanded] = useState(false);
  if (!extra?.groups?.length && !extra?.planInfo) return null;

  const groups = extra.groups ?? [];

  return (
    <div>
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground transition-colors"
      >
        <ChevronDown className={cn("h-3.5 w-3.5 transition-transform", expanded && "rotate-180")} />
        {expanded ? "Hide" : "Show"} Quota Details
      </button>
      {expanded && (
        <div className="space-y-3 pt-2">
          <div className="rounded-lg border border-border bg-card/60 p-4">
            <h3 className="text-sm font-semibold mb-1">Model Quota</h3>
            <p className="text-xs text-muted-foreground leading-relaxed">
              Within each group, models share a 5-hour rolling quota window. Quota is consumed
              proportionally to token cost.
            </p>
          </div>
          {groups.length > 0 ? (
            <div className="space-y-3">
              {groups.map((g) => (
                <QuotaGroupCard key={g.name} group={g} />
              ))}
            </div>
          ) : (
            <div className="rounded-lg border border-border bg-card/50 p-4 text-sm text-muted-foreground">
              No quota group data available. Sync account to refresh.
            </div>
          )}
          <PlanInfoCard planInfo={extra.planInfo as Record<string, unknown> | undefined} />
        </div>
      )}
    </div>
  );
}
