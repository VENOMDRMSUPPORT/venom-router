import { Sparkles } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import {
  TIER_META,
  TIER_STRATEGY_COPY,
  UNIVERSAL_CAPABILITIES,
  CAPABILITY_LABELS,
  type Tier,
} from "./routing-constants";

export function TierStrategyCard({ tier }: { tier: Tier }) {
  const meta = TIER_META[tier];
  const copy = TIER_STRATEGY_COPY[tier];

  return (
    <div
      className={cn(
        "rounded-xl border border-border/50 bg-gradient-to-br p-4 space-y-3",
        meta.gradient,
      )}
    >
      <div className="flex items-start gap-3">
        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-border/60 bg-card/80">
          <Sparkles className={cn("h-4 w-4", meta.color)} />
        </div>
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold text-foreground">{copy.title}</h3>
            <Badge variant="outline" className="text-[10px] font-normal border-border/60">
              {meta.subtitle}
            </Badge>
          </div>
          <ul className="space-y-1 pt-1">
            {copy.bullets.map((bullet) => (
              <li
                key={bullet}
                className="text-[11px] text-muted-foreground leading-relaxed flex gap-2"
              >
                <span className="text-muted-foreground/50 shrink-0">·</span>
                <span>{bullet}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>

      <div className="rounded-lg border border-border/40 bg-background/30 px-3 py-2.5 space-y-2">
        <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
          Universal capabilities
        </p>
        <div className="flex flex-wrap gap-1">
          {UNIVERSAL_CAPABILITIES.map((cap) => (
            <Badge
              key={cap}
              variant="secondary"
              className="text-[10px] font-normal bg-muted/50 border-border/40"
            >
              {CAPABILITY_LABELS[cap] ?? cap}
            </Badge>
          ))}
        </div>
        <p className="text-[10px] text-muted-foreground leading-relaxed">
          All Venom tiers are capability aliases; tiers differ by routing policy, not feature
          availability.
        </p>
      </div>
    </div>
  );
}
