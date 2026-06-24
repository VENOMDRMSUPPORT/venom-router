import { useState } from "react";
import { GitBranch, Zap, Plus, Settings } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { RoutingRule, VenomModel } from "@/lib/db/venom.server";
import type { TierStrategyConfig } from "@/lib/routing/strategy.types";
import { TierStrategyForm, AddRuleSheet } from "./tier-rule-builder";
import { RuleCard } from "./rule-card";
import { TIER_META, EMPTY_TIER_MESSAGE, type Tier } from "./routing-constants";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";

export function RoutingTierSection({
  tier,
  rules,
  venomModel,
  allRules,
}: {
  tier: Tier;
  rules: RoutingRule[];
  venomModel: VenomModel | undefined;
  allRules: RoutingRule[];
}) {
  const meta = TIER_META[tier];
  const [isAddRuleOpen, setIsAddRuleOpen] = useState(false);
  const sorted = [...rules].sort((a, b) => b.priority - a.priority);
  const strategyConfig = venomModel?.strategy_config as Partial<TierStrategyConfig> | undefined;

  return (
    <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
      {/* Left Column: Routing Strategy configuration */}
      <Card className="lg:col-span-5 border-border/50 bg-card/45 backdrop-blur-sm shadow-elegant">
        <CardHeader className="pb-4 border-b border-border/30">
          <div className="flex items-center gap-2">
            <Settings className={cn("h-4 w-4", meta.color)} />
            <CardTitle className="text-sm font-bold font-display">Routing Strategy</CardTitle>
          </div>
          <CardDescription className="text-[11px] text-muted-foreground mt-0.5 leading-relaxed">
            Configure rotation policies, auto-escalation parameters, and quota warning rules for{" "}
            <code className="font-mono">{meta.label}</code> requests.
          </CardDescription>
        </CardHeader>
        <CardContent className="pt-4">
          <TierStrategyForm tier={tier} strategyConfig={strategyConfig} />
        </CardContent>
      </Card>

      {/* Right Column: Active Rules lists */}
      <Card className="lg:col-span-7 border-border/50 bg-card/45 backdrop-blur-sm shadow-elegant">
        <CardHeader className="pb-4 border-b border-border/30 flex flex-row items-center justify-between gap-4">
          <div className="space-y-0.5">
            <div className="flex items-center gap-2">
              <GitBranch className={cn("h-4 w-4", meta.color)} />
              <CardTitle className="text-sm font-bold font-display">Active Rules</CardTitle>
            </div>
            <CardDescription className="text-[11px] text-muted-foreground mt-0.5 leading-relaxed">
              Define the prioritized model sequence. Venom tries routes in descending priority
              order.
            </CardDescription>
          </div>
          <Button
            size="sm"
            onClick={() => setIsAddRuleOpen(true)}
            className="h-8 text-xs gap-1.5 px-3 shrink-0 shadow-glow"
          >
            <Plus className="h-3.5 w-3.5" />
            Add rule
          </Button>
        </CardHeader>
        <CardContent className="pt-4 space-y-4">
          {sorted.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border/60 bg-background/25 py-12 px-4 text-center space-y-3.5">
              <div className="flex h-9 w-9 items-center justify-center rounded-full bg-muted mx-auto">
                <GitBranch className="h-4 w-4 text-muted-foreground/50" />
              </div>
              <p className="text-xs text-muted-foreground leading-relaxed max-w-sm mx-auto">
                {EMPTY_TIER_MESSAGE}
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setIsAddRuleOpen(true)}
                className="h-8 text-xs gap-1"
              >
                <Plus className="h-3.5 w-3.5" /> Add your first rule
              </Button>
            </div>
          ) : (
            <div className="space-y-2">
              <div className="flex items-center justify-between text-[9px] font-bold uppercase tracking-wider text-muted-foreground/60 px-2 pb-1">
                <span>Rank & Model</span>
                <span>Priority & Status</span>
              </div>
              <div className="space-y-2 max-h-[500px] overflow-y-auto scrollbar-app pr-1">
                {sorted.map((rule, idx) => (
                  <RuleCard key={rule.id} rule={rule} rank={idx + 1} />
                ))}
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Slide-out Add Rule Drawer */}
      <AddRuleSheet
        tier={tier}
        existingRules={allRules}
        isOpen={isAddRuleOpen}
        onOpenChange={setIsAddRuleOpen}
      />
    </div>
  );
}
