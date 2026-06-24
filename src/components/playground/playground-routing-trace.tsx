import { Check, ShieldAlert, Cpu, Sparkles, AlertTriangle, ArrowRight } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { RoutingTrace } from "@/lib/routing/types";

type Props = {
  trace: RoutingTrace | null;
};

const STATUS_CONFIG: Record<
  string,
  { label: string; bg: string; border: string; text: string; dot: string }
> = {
  selected: {
    label: "selected",
    bg: "bg-emerald-500/10",
    border: "border-emerald-500/30",
    text: "text-emerald-400",
    dot: "bg-emerald-400",
  },
  eligible: {
    label: "eligible",
    bg: "bg-blue-500/10",
    border: "border-blue-500/30",
    text: "text-blue-400",
    dot: "bg-blue-400",
  },
  filtered: {
    label: "filtered",
    bg: "bg-muted/50",
    border: "border-border/60",
    text: "text-muted-foreground",
    dot: "bg-muted-foreground/40",
  },
  attempted: {
    label: "attempted",
    bg: "bg-amber-500/10",
    border: "border-amber-500/30",
    text: "text-amber-400",
    dot: "bg-amber-400",
  },
};

export function PlaygroundRoutingTrace({ trace }: Props) {
  if (!trace) return null;

  return (
    <div className="space-y-4">
      {/* 1. Header Overview Stats */}
      <div className="space-y-2">
        <h4 className="text-[10px] font-mono tracking-widest text-muted-foreground uppercase font-semibold">
          Gateway Telemetry
        </h4>
        <div className="grid grid-cols-2 gap-2">
          <div className="border border-border/40 rounded-lg p-2.5 bg-background/30 flex flex-col">
            <span className="text-[9px] font-mono text-muted-foreground">EVALUATED</span>
            <span className="text-base font-bold font-mono tracking-tight text-foreground mt-0.5">
              {trace.candidates_evaluated} rules
            </span>
          </div>
          <div className="border border-border/40 rounded-lg p-2.5 bg-background/30 flex flex-col">
            <span className="text-[9px] font-mono text-muted-foreground">FILTERED</span>
            <span className="text-base font-bold font-mono tracking-tight text-muted-foreground mt-0.5">
              {trace.candidates_filtered} rules
            </span>
          </div>
        </div>
      </div>

      {/* 2. Routing Decision Callout */}
      <div className="border border-border/40 bg-primary/5 rounded-lg p-3 space-y-1.5 shadow-sm">
        <div className="flex items-center gap-1.5">
          <Sparkles className="h-3.5 w-3.5 text-primary animate-pulse" />
          <span className="text-[10px] font-mono font-bold text-primary uppercase tracking-wider">
            Decision Reason
          </span>
        </div>
        <p className="text-xs text-foreground/90 leading-relaxed font-sans">
          {trace.decision_reason}
        </p>
      </div>

      {/* 3. Stepper Evaluation Path */}
      <div className="space-y-3 pt-1">
        <h4 className="text-[10px] font-mono tracking-widest text-muted-foreground uppercase font-semibold">
          Evaluation Steps
        </h4>

        <div className="space-y-3 pl-1 relative before:absolute before:left-2 before:top-2 before:bottom-2 before:w-[1px] before:bg-border/40">
          {trace.candidates.map((c, idx) => {
            const cfg = STATUS_CONFIG[c.status] ?? STATUS_CONFIG.filtered;
            const isSelected = c.status === "selected";

            return (
              <div key={c.rule_id} className="relative pl-6 flex flex-col gap-1 group">
                {/* Timeline node */}
                <div
                  className={cn(
                    "absolute left-0.5 top-1.5 h-3 w-3 rounded-full border border-background flex items-center justify-center -translate-x-1/2 transition-transform group-hover:scale-110",
                    cfg.dot,
                    isSelected && "ring-2 ring-emerald-500/20",
                  )}
                />

                {/* Candidate header */}
                <div className="flex items-center justify-between gap-2 flex-wrap">
                  <div className="flex items-center gap-1.5">
                    <span className="text-xs font-bold font-display text-foreground">
                      {c.external_id}
                    </span>
                    <span className="text-[9px] font-mono text-muted-foreground/60">
                      ({c.rule_id.slice(0, 6)})
                    </span>
                  </div>
                  <span
                    className={cn(
                      "rounded px-1.5 py-0.5 text-[9px] font-bold font-mono uppercase tracking-wider border",
                      cfg.bg,
                      cfg.border,
                      cfg.text,
                    )}
                  >
                    {cfg.label}
                  </span>
                </div>

                {/* Candidate details */}
                <div className="flex items-center justify-between gap-4 text-[10px] font-mono text-muted-foreground/80 mt-0.5">
                  <div className="flex items-center gap-1">
                    <Cpu className="h-3 w-3 text-muted-foreground/50" />
                    <span>{c.adapter}</span>
                  </div>
                  <div className="flex items-center gap-1">
                    <span>Score:</span>
                    <span
                      className={cn(
                        "font-bold",
                        isSelected ? "text-emerald-400" : "text-muted-foreground",
                      )}
                    >
                      {c.score != null ? c.score.toFixed(3) : "—"}
                    </span>
                  </div>
                </div>

                {/* Error/Filter Reason */}
                {(c.filter_reason || c.error) && (
                  <div className="rounded bg-muted/30 border border-border/30 px-2 py-1.5 text-[10px] text-muted-foreground/90 font-sans mt-1 flex items-start gap-1.5 leading-relaxed">
                    {c.error ? (
                      <ShieldAlert className="h-3 w-3 text-destructive shrink-0 mt-0.5" />
                    ) : (
                      <AlertTriangle className="h-3 w-3 text-amber-500 shrink-0 mt-0.5" />
                    )}
                    <span>{c.filter_reason ?? c.error}</span>
                  </div>
                )}

                {/* Fallback chain visual indicator */}
                {isSelected && trace.fallback_attempts > 0 && (
                  <div className="mt-1 flex items-center gap-1.5 text-[9px] font-mono text-amber-400 bg-amber-500/5 border border-amber-500/20 rounded px-2 py-1">
                    <span>Fallback attempt active</span>
                    <ArrowRight className="h-2.5 w-2.5 animate-pulse" />
                    <span>Attempt #{trace.fallback_attempts}</span>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
