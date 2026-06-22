import { Clock } from "lucide-react";
import { cn } from "@/lib/utils";

function formatResetTime(resetsAt: string): string {
  const t = new Date(resetsAt);
  if (t.getTime() <= Date.now()) return "expired";
  const time = t.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: true });
  const dayName = t.toLocaleDateString([], { weekday: "short" });
  const day = t.getDate();
  const month = t.toLocaleDateString([], { month: "short" });
  return `${dayName} ${day} ${month}, ${time}`;
}

export function QuotaBar({
  shortLabel,
  used,
  resetsAt,
  isOld,
}: {
  shortLabel: string;
  used: number;
  resetsAt?: string;
  isOld?: boolean;
}) {
  const pct = Math.min(100, Math.max(0, used));
  const colorClass = pct >= 90 ? "bg-red-500" : pct >= 70 ? "bg-amber-500" : "bg-emerald-500";
  const textColor =
    pct >= 90
      ? "text-red-600 dark:text-red-400"
      : pct >= 70
        ? "text-amber-600 dark:text-amber-400"
        : "text-emerald-600 dark:text-emerald-400";
  const resetLabel = resetsAt ? formatResetTime(resetsAt) : null;

  return (
    <div
      className={cn("flex items-center gap-1.5 min-w-0", isOld && "opacity-60")}
      title={
        resetsAt
          ? `${shortLabel} ${pct}% used — resets ${new Date(resetsAt).toLocaleString()}`
          : `${shortLabel} ${pct}% used`
      }
    >
      <span className="shrink-0 w-5 text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
        {shortLabel}
      </span>
      <div className="relative h-1.5 w-16 overflow-hidden rounded-full bg-muted-foreground/10 sm:w-24">
        <div
          className={cn("h-full rounded-full transition-all duration-700 ease-out", colorClass)}
          style={{ width: `${pct}%` }}
        />
      </div>
      <span
        className={cn("shrink-0 text-[11px] font-semibold tabular-nums w-9 text-right", textColor)}
      >
        {pct}%
      </span>
      {resetLabel && (
        <span className="shrink-0 inline-flex items-center gap-1 text-[9.5px] text-muted-foreground/60 tabular-nums">
          <Clock className="h-2.5 w-2.5 shrink-0 opacity-70" />
          <span className="whitespace-nowrap">{resetLabel}</span>
        </span>
      )}
    </div>
  );
}
