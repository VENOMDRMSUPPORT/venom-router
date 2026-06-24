import { useContext, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Save, Check, Plus, AlertCircle, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Slider } from "@/components/ui/slider";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ProviderIcon } from "@/components/providers/provider-icon";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api-client";
import type { RoutingRule } from "@/lib/db/venom.server";
import type { TierStrategyConfig } from "@/lib/routing/strategy.types";
import { mergeStrategyConfig } from "@/lib/routing/strategy.types";
import type { RoutingCondition } from "@/lib/routing/types";
import { CapabilityFilter } from "./capability-filter";
import { RoutingDebugContext } from "./routing-debug-context";
import {
  TIER_META,
  type Tier,
  type ApprovedModel,
  modelKey,
  AUTO_ESCALATION_OPTIONS,
  ACCOUNT_ROTATION_OPTIONS,
  HEALTH_REQUIREMENT_OPTIONS,
  FALLBACK_BEHAVIOR_OPTIONS,
  TIER_STRATEGY_COPY,
  UNIVERSAL_CAPABILITIES,
  CAPABILITY_LABELS,
} from "./routing-constants";

// ── 1. Strategy Form Component ──────────────────────────────────────────────────

export function TierStrategyForm({
  tier,
  strategyConfig,
}: {
  tier: Tier;
  strategyConfig: Partial<TierStrategyConfig> | null | undefined;
}) {
  const qc = useQueryClient();
  const debug = useContext(RoutingDebugContext);
  const meta = TIER_META[tier];
  const copy = TIER_STRATEGY_COPY[tier];

  const defaults = useMemo(() => mergeStrategyConfig(tier, strategyConfig), [tier, strategyConfig]);
  const [strategy, setStrategy] = useState<TierStrategyConfig>(defaults);

  useEffect(() => {
    setStrategy(mergeStrategyConfig(tier, strategyConfig));
  }, [tier, strategyConfig]);

  const saveStrategy = useMutation({
    mutationFn: async () => {
      const t0 = Date.now();
      const dbId = debug?.start("saveTierStrategy", { tier, strategy });
      try {
        const res = await api.patch<{ ok: true }>(`/api/dashboard/venom-models/${tier}`, {
          strategy_config: strategy,
        });
        debug?.resolve(dbId || "", res, Date.now() - t0);
        return res;
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        debug?.reject(dbId || "", msg, Date.now() - t0);
        throw e;
      }
    },
    onSuccess: () => {
      toast.success(`Saved strategy settings for ${meta.label}`);
      qc.invalidateQueries({ queryKey: ["venom-models"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  function setStrategyField<K extends keyof TierStrategyConfig>(
    key: K,
    value: TierStrategyConfig[K],
  ) {
    setStrategy((prev) => ({ ...prev, [key]: value }));
  }

  const isModified = useMemo(() => {
    return JSON.stringify(strategy) !== JSON.stringify(defaults);
  }, [strategy, defaults]);

  return (
    <div className="space-y-5">
      {/* Visual Strategy Summary Banner */}
      <div className={cn("rounded-xl border p-4 bg-gradient-to-br space-y-3", meta.gradient)}>
        <div className="flex items-start gap-3">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-border bg-card/85">
            <Sparkles className={cn("h-4 w-4 animate-pulse", meta.color)} />
          </div>
          <div className="min-w-0 flex-1 space-y-1">
            <h4 className="text-xs font-bold text-foreground font-display">{copy.title}</h4>
            <ul className="space-y-1 pt-1.5">
              {copy.bullets.slice(0, 3).map((bullet) => (
                <li
                  key={bullet}
                  className="text-[10px] text-muted-foreground leading-relaxed flex gap-1.5"
                >
                  <span className="text-muted-foreground/45 shrink-0">·</span>
                  <span>{bullet}</span>
                </li>
              ))}
            </ul>
          </div>
        </div>
      </div>

      <div className="space-y-4">
        {/* Settings Selectors */}
        <div className="space-y-3.5">
          <div className="space-y-1.5">
            <Label className="text-[11px] font-semibold text-foreground">Fallback behavior</Label>
            <Select
              value={strategy.fallback_behavior}
              onValueChange={(v) =>
                setStrategyField("fallback_behavior", v as TierStrategyConfig["fallback_behavior"])
              }
            >
              <SelectTrigger className="text-xs h-9 bg-background/50 border-border/60 hover:bg-background/80 transition-colors">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {FALLBACK_BEHAVIOR_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value} className="text-xs">
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-1.5">
            <Label className="text-[11px] font-semibold text-foreground">
              Auto-escalation trigger
            </Label>
            <Select
              value={strategy.auto_escalation}
              onValueChange={(v) =>
                setStrategyField("auto_escalation", v as TierStrategyConfig["auto_escalation"])
              }
            >
              <SelectTrigger className="text-xs h-9 bg-background/50 border-border/60 hover:bg-background/80 transition-colors">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {AUTO_ESCALATION_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value} className="text-xs">
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-1.5">
            <Label className="text-[11px] font-semibold text-foreground">
              Account rotation policy
            </Label>
            <Select
              value={strategy.account_rotation}
              onValueChange={(v) =>
                setStrategyField("account_rotation", v as TierStrategyConfig["account_rotation"])
              }
            >
              <SelectTrigger className="text-xs h-9 bg-background/50 border-border/60 hover:bg-background/80 transition-colors">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ACCOUNT_ROTATION_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value} className="text-xs">
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-1.5">
            <Label className="text-[11px] font-semibold text-foreground">Health requirements</Label>
            <Select
              value={strategy.health_requirement}
              onValueChange={(v) =>
                setStrategyField(
                  "health_requirement",
                  v as TierStrategyConfig["health_requirement"],
                )
              }
            >
              <SelectTrigger className="text-xs h-9 bg-background/50 border-border/60 hover:bg-background/80 transition-colors">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {HEALTH_REQUIREMENT_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value} className="text-xs">
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Quota threshold slider */}
          <div className="space-y-2.5 pt-1.5">
            <div className="flex items-center justify-between">
              <Label className="text-[11px] font-semibold text-foreground">
                Quota warning threshold
              </Label>
              <span className="text-[10px] font-mono font-bold text-primary bg-primary/10 px-2 py-0.5 rounded">
                {strategy.quota_threshold_pct}%
              </span>
            </div>
            <Slider
              value={[strategy.quota_threshold_pct]}
              onValueChange={([v]) => setStrategyField("quota_threshold_pct", v)}
              min={0}
              max={50}
              step={1}
              className="py-1"
            />
          </div>

          {/* Premium reserve slider */}
          <div className="space-y-2.5 pt-1.5">
            <div className="flex items-center justify-between">
              <Label className="text-[11px] font-semibold text-foreground">
                Premium reserve threshold
              </Label>
              <span className="text-[10px] font-mono font-bold text-violet-400 bg-violet-500/10 px-2 py-0.5 rounded">
                {strategy.premium_reserve_pct}%
              </span>
            </div>
            <Slider
              value={[strategy.premium_reserve_pct]}
              onValueChange={([v]) => setStrategyField("premium_reserve_pct", v)}
              min={0}
              max={50}
              step={1}
              className="py-1"
            />
          </div>
        </div>

        <Button
          size="sm"
          onClick={() => saveStrategy.mutate()}
          disabled={saveStrategy.isPending || !isModified}
          className="w-full gap-1.5 mt-2 transition-all shadow-glow"
        >
          <Save className="h-3.5 w-3.5" />
          {saveStrategy.isPending ? "Saving changes…" : "Save strategy settings"}
        </Button>
      </div>
    </div>
  );
}

// ── 2. Add Rule Drawer Component ───────────────────────────────────────────────

export function AddRuleSheet({
  tier,
  existingRules,
  isOpen,
  onOpenChange,
}: {
  tier: Tier;
  existingRules: RoutingRule[];
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const qc = useQueryClient();
  const debug = useContext(RoutingDebugContext);
  const meta = TIER_META[tier];

  const [selectedPool, setSelectedPool] = useState<Set<string>>(new Set());
  const [capabilities, setCapabilities] = useState<string[]>([]);
  const [priority, setPriority] = useState("");
  const [role, setRole] = useState<"primary" | "fallback">("primary");
  const [active, setActive] = useState(true);

  const { data: approvedModels = [], isLoading: modelsLoading } = useQuery({
    queryKey: ["approved-account-models"],
    queryFn: () => api.get<ApprovedModel[]>("/api/dashboard/account-models"),
  });

  const suggestedPriority = useMemo(() => {
    const tierRules = existingRules.filter((r) => r.venom_slug === tier);
    if (!tierRules.length) return 100;
    return Math.max(...tierRules.map((r) => r.priority)) + 10;
  }, [existingRules, tier]);

  const resolvedPriority = priority.trim() ? parseInt(priority, 10) : suggestedPriority;

  const groupedModels = useMemo(() => {
    return approvedModels.reduce<Record<string, ApprovedModel[]>>((acc, m) => {
      const key = m.provider_name || m.provider_slug;
      (acc[key] ??= []).push(m);
      return acc;
    }, {});
  }, [approvedModels]);

  function togglePool(key: string) {
    setSelectedPool((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  const addRules = useMutation({
    mutationFn: async () => {
      const t0 = Date.now();
      const dbId = debug?.start("addRoutingRules", { pool: [...selectedPool] });

      try {
        const condition: RoutingCondition | undefined =
          capabilities.length > 0 ? { requires: capabilities } : undefined;

        const modelsToCreate = approvedModels.filter((m) => selectedPool.has(modelKey(m)));
        let created = 0;
        for (let i = 0; i < modelsToCreate.length; i++) {
          const m = modelsToCreate[i];
          const rulePriority = resolvedPriority - i * 10;
          await api.post<{ ok: true }>("/api/dashboard/routing-rules", {
            venom_slug: tier,
            model_id: m.model_id,
            account_id: m.account_id,
            priority: rulePriority,
            role,
            active,
            condition,
          });
          created++;
        }

        debug?.resolve(dbId || "", { created }, Date.now() - t0);
        return { created };
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        debug?.reject(dbId || "", msg, Date.now() - t0);
        throw e;
      }
    },
    onSuccess: ({ created }) => {
      toast.success(`Successfully added ${created} rules to ${meta.label}`);
      qc.invalidateQueries({ queryKey: ["routing-rules"] });
      setSelectedPool(new Set());
      setCapabilities([]);
      setPriority("");
      onOpenChange(false);
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const aggressionPriority = priority.trim() ? parseInt(priority, 10) : suggestedPriority;
  const canSubmit =
    selectedPool.size > 0 &&
    !isNaN(aggressionPriority) &&
    aggressionPriority >= 0 &&
    aggressionPriority <= 9999;

  return (
    <Sheet open={isOpen} onOpenChange={onOpenChange}>
      <SheetContent className="w-full sm:max-w-[480px] p-6 overflow-y-auto flex flex-col justify-between bg-card/95 backdrop-blur-md border-l border-border/80">
        <div className="space-y-6">
          <SheetHeader className="space-y-1.5">
            <SheetTitle className="text-base font-bold flex items-center gap-2 font-display">
              <Plus className={cn("h-4 w-4", meta.color)} />
              Add Rules to {meta.label}
            </SheetTitle>
            <SheetDescription className="text-xs text-muted-foreground leading-relaxed">
              Add new provider models to the routing pool for this tier. Configured filters limit
              rules matching.
            </SheetDescription>
          </SheetHeader>

          <div className="space-y-4 pt-2">
            {/* Provider pool selection */}
            <div className="space-y-2">
              <Label className="text-xs font-semibold text-foreground">
                Select Provider Models
              </Label>
              {modelsLoading ? (
                <div className="space-y-2">
                  <Skeleton className="h-10 rounded-lg" />
                  <Skeleton className="h-10 rounded-lg" />
                </div>
              ) : approvedModels.length === 0 ? (
                <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-3.5 py-2.5 flex items-start gap-2.5">
                  <AlertCircle className="h-4 w-4 text-amber-400 shrink-0 mt-0.5" />
                  <p className="text-[11px] text-amber-300/80 leading-relaxed">
                    No approved models found. Go to **Providers** and approve models first.
                  </p>
                </div>
              ) : (
                <div className="max-h-[220px] overflow-y-auto rounded-lg border border-border/50 bg-background/40 divide-y divide-border/20 scrollbar-app">
                  {Object.entries(groupedModels).map(([providerName, models]) => (
                    <div key={providerName} className="p-2 space-y-1 bg-background/20">
                      <p className="text-[9px] font-mono font-bold uppercase tracking-widest text-muted-foreground px-1 py-0.5">
                        {providerName}
                      </p>
                      {models.map((m) => {
                        const key = modelKey(m);
                        const checked = selectedPool.has(key);
                        return (
                          <button
                            key={key}
                            type="button"
                            onClick={() => togglePool(key)}
                            className={cn(
                              "w-full flex items-center gap-2.5 rounded-lg px-2 py-2 text-left transition-all border border-transparent",
                              checked ? "bg-primary/10 border-primary/20" : "hover:bg-muted/30",
                            )}
                          >
                            <div
                              className={cn(
                                "flex h-4 w-4 shrink-0 items-center justify-center rounded border transition-colors",
                                checked
                                  ? "border-primary bg-primary text-primary-foreground"
                                  : "border-border/60 bg-background",
                              )}
                            >
                              {checked && <Check className="h-2.5 w-2.5" />}
                            </div>
                            <ProviderIcon slug={m.provider_slug} className="h-5 w-5 shrink-0" />
                            <div className="min-w-0 flex-1">
                              <p className="text-xs font-semibold text-foreground truncate">
                                {m.model_display_name || m.model_external_id}
                              </p>
                              <p className="text-[10px] text-muted-foreground truncate mt-0.5">
                                {m.account_email ?? m.account_label ?? "account"}
                              </p>
                            </div>
                          </button>
                        );
                      })}
                    </div>
                  ))}
                </div>
              )}
              {selectedPool.size > 0 && (
                <p className="text-[10px] text-primary font-medium font-mono">
                  {selectedPool.size} model{selectedPool.size === 1 ? "" : "s"} selected for
                  addition
                </p>
              )}
            </div>

            {/* Capability filters */}
            <div className="space-y-2 pt-1">
              <Label className="text-xs font-semibold text-foreground">
                Capability Matching Filters
              </Label>
              <CapabilityFilter selected={capabilities} onChange={setCapabilities} />
            </div>

            {/* Priority & Role */}
            <div className="grid grid-cols-2 gap-3.5 pt-1">
              <div className="space-y-1.5">
                <Label className="text-xs font-semibold text-foreground">Priority Order</Label>
                <Input
                  type="number"
                  min={0}
                  max={9999}
                  value={priority}
                  onChange={(e) => setPriority(e.target.value)}
                  placeholder={String(suggestedPriority)}
                  className="text-xs h-9 bg-background/50"
                />
              </div>

              <div className="space-y-1.5">
                <Label className="text-xs font-semibold text-foreground">Routing Role</Label>
                <Select value={role} onValueChange={(v) => setRole(v as "primary" | "fallback")}>
                  <SelectTrigger className="text-xs h-9 bg-background/50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="primary" className="text-xs">
                      Primary
                    </SelectItem>
                    <SelectItem value="fallback" className="text-xs">
                      Fallback
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>

            {/* Active state toggle */}
            <div className="flex items-center justify-between rounded-lg border border-border/50 bg-muted/10 px-3 py-2.5 mt-2">
              <div>
                <p className="text-xs font-semibold">Deploy as Active Rule</p>
                <p className="text-[10px] text-muted-foreground mt-0.5">
                  Route traffic through this rule immediately once saved
                </p>
              </div>
              <Switch checked={active} onCheckedChange={setActive} />
            </div>
          </div>
        </div>

        <div className="flex items-center gap-3 pt-6 border-t border-border/40 mt-6">
          <Button
            variant="outline"
            size="sm"
            onClick={() => onOpenChange(false)}
            className="flex-1 text-xs"
          >
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={() => addRules.mutate()}
            disabled={!canSubmit || addRules.isPending}
            className="flex-1 gap-1.5 text-xs shadow-glow"
          >
            <Plus className="h-3.5 w-3.5" />
            {addRules.isPending ? "Adding Rules…" : "Save new rules"}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}
