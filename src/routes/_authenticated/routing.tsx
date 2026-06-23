import { createFileRoute } from "@tanstack/react-router";
import {
  useSuspenseQuery,
  useQuery,
  useMutation,
  useQueryClient,
  queryOptions,
} from "@tanstack/react-query";
import { Suspense, useState } from "react";
import { GitBranch, Plus, Trash2, ChevronUp, ChevronDown, AlertCircle, Zap } from "lucide-react";
import { Header } from "@/components/layout/header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { ProviderIcon } from "@/components/providers/provider-icon";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api-client";
import type { RoutingRule } from "@/lib/db/venom.server";

export const Route = createFileRoute("/_authenticated/routing")({
  head: () => ({ meta: [{ title: "Routing Rules — Venom Router" }] }),
  component: RoutingPage,
});

const TIERS = ["lite", "pro", "max"] as const;
type Tier = (typeof TIERS)[number];

const TIER_META: Record<Tier, { label: string; desc: string; color: string; accent: string }> = {
  lite: {
    label: "venom/lite",
    desc: "Cost-optimised · fast turnaround",
    color: "text-sky-400",
    accent: "border-sky-500/20 bg-sky-500/5",
  },
  pro: {
    label: "venom/pro",
    desc: "Balanced quality · moderate cost",
    color: "text-violet-400",
    accent: "border-violet-500/20 bg-violet-500/5",
  },
  max: {
    label: "venom/max",
    desc: "Highest quality · cost-tolerant",
    color: "text-amber-400",
    accent: "border-amber-500/20 bg-amber-500/5",
  },
};

const ROLE_LABELS: Record<string, string> = {
  primary: "Primary",
  fallback: "Fallback",
};

function RoutingPage() {
  return (
    <>
      <Header
        title="Routing Rules"
        description="Map venom models to provider models with priority and fallback chains."
        icon={<GitBranch className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-6 bg-background/30">
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
      {TIERS.map((t) => (
        <div key={t} className="rounded-2xl border border-border bg-card/40 p-5 space-y-3">
          <Skeleton className="h-6 w-32" />
          <Skeleton className="h-16 rounded-xl" />
          <Skeleton className="h-16 rounded-xl" />
        </div>
      ))}
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

  const [addDialogTier, setAddDialogTier] = useState<Tier | null>(null);

  const byTier = Object.fromEntries(
    TIERS.map((t) => [t, rules.filter((r) => r.venom_slug === t)]),
  ) as Record<Tier, RoutingRule[]>;

  const totalRules = rules.length;

  return (
    <>
      {totalRules === 0 && (
        <div className="mb-6 rounded-xl border border-amber-500/20 bg-amber-500/5 px-4 py-3 flex items-start gap-3">
          <AlertCircle className="h-4 w-4 text-amber-400 shrink-0 mt-0.5" />
          <p className="text-xs text-amber-300/90 leading-relaxed">
            No routing rules configured yet. Add at least one rule per tier you want to serve.
            Without rules the gateway returns 503 for every request to that tier.
          </p>
        </div>
      )}

      <div className="space-y-6">
        {TIERS.map((tier) => (
          <TierSection
            key={tier}
            tier={tier}
            rules={byTier[tier]}
            onAdd={() => setAddDialogTier(tier)}
          />
        ))}
      </div>

      <AddRuleDialog
        open={addDialogTier !== null}
        defaultTier={addDialogTier}
        existingRules={rules}
        onClose={() => setAddDialogTier(null)}
      />
    </>
  );
}

function TierSection({
  tier,
  rules,
  onAdd,
}: {
  tier: Tier;
  rules: RoutingRule[];
  onAdd: () => void;
}) {
  const meta = TIER_META[tier];
  const sorted = [...rules].sort((a, b) => b.priority - a.priority);

  return (
    <div className={cn("rounded-2xl border p-5 space-y-4", meta.accent)}>
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-card border border-border">
            <Zap className={cn("h-4 w-4", meta.color)} />
          </div>
          <div>
            <code className={cn("text-sm font-bold font-mono", meta.color)}>{meta.label}</code>
            <p className="text-[11px] text-muted-foreground mt-0.5">{meta.desc}</p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-xs text-muted-foreground">
            {rules.length} {rules.length === 1 ? "rule" : "rules"}
          </span>
          <Button size="sm" variant="outline" onClick={onAdd} className="h-7 text-xs gap-1">
            <Plus className="h-3.5 w-3.5" /> Add rule
          </Button>
        </div>
      </div>

      {sorted.length === 0 ? (
        <div className="rounded-xl border border-dashed border-border/60 bg-background/20 py-8 text-center">
          <GitBranch className="mx-auto h-6 w-6 text-muted-foreground/40 mb-2" />
          <p className="text-xs text-muted-foreground">
            No rules for this tier.{" "}
            <button onClick={onAdd} className="text-primary hover:underline font-medium">
              Add the first one.
            </button>
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {sorted.map((rule, idx) => (
            <RuleCard key={rule.id} rule={rule} rank={idx + 1} total={sorted.length} />
          ))}
        </div>
      )}
    </div>
  );
}

function RuleCard({ rule, rank, total }: { rule: RoutingRule; rank: number; total: number }) {
  const qc = useQueryClient();

  const toggle = useMutation({
    mutationFn: (active: boolean) =>
      api.patch<{ ok: true }>(`/api/dashboard/routing-rules/${rule.id}`, { active }),
    onMutate: async (active) => {
      await qc.cancelQueries({ queryKey: ["routing-rules"] });
      const prev = qc.getQueryData<RoutingRule[]>(["routing-rules"]);
      qc.setQueryData<RoutingRule[]>(["routing-rules"], (old) =>
        (old ?? []).map((r) => (r.id === rule.id ? { ...r, active } : r)),
      );
      return { prev };
    },
    onError: (_err, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(["routing-rules"], ctx.prev);
      toast.error("Failed to toggle rule");
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["routing-rules"] }),
  });

  const accountLabel = rule.account_email ?? rule.account_label ?? "account";

  return (
    <div
      className={cn(
        "flex items-center gap-3 rounded-xl border bg-card/60 px-4 py-3 transition-all",
        rule.active ? "border-border/60" : "border-border/30 opacity-60",
      )}
    >
      <span className="w-5 text-center text-[10px] font-bold text-muted-foreground/50 tabular-nums shrink-0">
        {rank}
      </span>

      <ProviderIcon slug={rule.provider_slug} className="h-7 w-7 shrink-0" />

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium text-foreground truncate">
            {rule.model_display_name || rule.model_external_id}
          </span>
          <Badge
            variant="outline"
            className="text-[9px] h-4 px-1.5 shrink-0 capitalize border-border/50"
          >
            {ROLE_LABELS[rule.role] ?? rule.role ?? "primary"}
          </Badge>
        </div>
        <p className="text-[11px] text-muted-foreground mt-0.5 truncate">
          {rule.provider_name} · {accountLabel}
        </p>
      </div>

      <div className="flex items-center gap-1 shrink-0">
        <ChevronUp className="h-3.5 w-3.5 text-muted-foreground/30" />
        <span className="text-[10px] font-mono text-muted-foreground/60 tabular-nums w-8 text-center">
          {rule.priority}
        </span>
        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground/30" />
      </div>

      <Switch
        checked={rule.active}
        onCheckedChange={(v) => toggle.mutate(v)}
        disabled={toggle.isPending}
        className="shrink-0"
      />

      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive shrink-0"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this rule?</AlertDialogTitle>
            <AlertDialogDescription>
              Removes the{" "}
              <span className="font-medium text-foreground">
                {rule.model_display_name || rule.model_external_id}
              </span>{" "}
              rule from <code className="font-mono">venom/{rule.venom_slug}</code>. Traffic
              currently routed through this rule will fall through to the next rule or fail with
              503.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <DeleteRuleAction ruleId={rule.id} />
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function DeleteRuleAction({ ruleId }: { ruleId: string }) {
  const qc = useQueryClient();
  const del = useMutation({
    mutationFn: () => api.delete<{ ok: true }>(`/api/dashboard/routing-rules/${ruleId}`),
    onSuccess: () => {
      toast.success("Rule deleted");
      qc.invalidateQueries({ queryKey: ["routing-rules"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <AlertDialogAction onClick={() => del.mutate()} disabled={del.isPending}>
      {del.isPending ? "Deleting…" : "Delete"}
    </AlertDialogAction>
  );
}

type ApprovedModel = {
  id: string;
  account_id: string;
  model_id: string;
  model_external_id: string;
  model_display_name: string;
  provider_slug: string;
  provider_name: string;
  account_email: string | null;
  account_label: string | null;
};

function AddRuleDialog({
  open,
  defaultTier,
  existingRules,
  onClose,
}: {
  open: boolean;
  defaultTier: Tier | null;
  existingRules: RoutingRule[];
  onClose: () => void;
}) {
  const qc = useQueryClient();

  const [tier, setTier] = useState<Tier>(defaultTier ?? "lite");
  const [selectedModelKey, setSelectedModelKey] = useState<string>("");
  const [role, setRole] = useState<"primary" | "fallback">("primary");
  const [priority, setPriority] = useState<string>("");
  const [active, setActive] = useState(true);

  const { data: approvedModels = [], isLoading: modelsLoading } = useQuery({
    queryKey: ["approved-account-models"],
    queryFn: () => api.get<ApprovedModel[]>("/api/dashboard/account-models"),
    enabled: open,
  });

  const suggestedPriority = (() => {
    const tierRules = existingRules.filter((r) => r.venom_slug === tier);
    if (!tierRules.length) return 100;
    return Math.max(...tierRules.map((r) => r.priority)) + 10;
  })();

  const resolvedPriority = priority.trim() ? parseInt(priority, 10) : suggestedPriority;

  const groupedModels = (approvedModels as ApprovedModel[]).reduce<Record<string, ApprovedModel[]>>(
    (acc, m) => {
      const key = m.provider_name || m.provider_slug;
      (acc[key] ??= []).push(m);
      return acc;
    },
    {},
  );

  const selectedModel = (approvedModels as ApprovedModel[]).find(
    (m) => `${m.account_id}::${m.model_id}` === selectedModelKey,
  );

  const create = useMutation({
    mutationFn: () =>
      api.post<{ ok: true }>("/api/dashboard/routing-rules", {
        venom_slug: tier,
        model_id: selectedModel!.model_id,
        account_id: selectedModel!.account_id,
        priority: resolvedPriority,
        role,
        active,
      }),
    onSuccess: () => {
      toast.success("Routing rule created");
      qc.invalidateQueries({ queryKey: ["routing-rules"] });
      handleClose();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  function handleClose() {
    setSelectedModelKey("");
    setRole("primary");
    setPriority("");
    setActive(true);
    onClose();
  }

  const canSubmit =
    !!selectedModel &&
    !isNaN(resolvedPriority) &&
    resolvedPriority >= 0 &&
    resolvedPriority <= 9999;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && handleClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Add routing rule</DialogTitle>
          <DialogDescription>
            Route a venom model tier to a specific provider model. Higher priority = served first.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-1">
          {/* Tier */}
          <div className="space-y-1.5">
            <Label className="text-xs">Venom tier</Label>
            <div className="flex gap-1.5">
              {TIERS.map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => setTier(t)}
                  className={cn(
                    "flex-1 rounded-lg border py-2 text-xs font-mono font-semibold transition-all",
                    tier === t
                      ? cn("border-primary/50 bg-primary/10", TIER_META[t].color)
                      : "border-border/50 bg-background/50 text-muted-foreground hover:border-border",
                  )}
                >
                  {t}
                </button>
              ))}
            </div>
          </div>

          {/* Model */}
          <div className="space-y-1.5">
            <Label className="text-xs">Provider model</Label>
            {modelsLoading ? (
              <Skeleton className="h-9 rounded-md" />
            ) : approvedModels.length === 0 ? (
              <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs text-amber-400">
                No approved models found. Go to Providers → approve models first.
              </div>
            ) : (
              <Select value={selectedModelKey} onValueChange={setSelectedModelKey}>
                <SelectTrigger className="text-xs">
                  <SelectValue placeholder="Select a model…" />
                </SelectTrigger>
                <SelectContent>
                  {Object.entries(groupedModels).map(([providerName, models]) => (
                    <SelectGroup key={providerName}>
                      <SelectLabel className="text-[10px] uppercase tracking-wider text-muted-foreground">
                        {providerName}
                      </SelectLabel>
                      {models.map((m) => (
                        <SelectItem
                          key={`${m.account_id}::${m.model_id}`}
                          value={`${m.account_id}::${m.model_id}`}
                          className="text-xs"
                        >
                          <span className="font-medium">
                            {m.model_display_name || m.model_external_id}
                          </span>
                          <span className="text-muted-foreground ml-1.5">
                            · {m.account_email ?? m.account_label ?? "account"}
                          </span>
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          {/* Priority + Role row */}
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label className="text-xs">
                Priority <span className="text-muted-foreground font-normal">(higher = first)</span>
              </Label>
              <Input
                type="number"
                min={0}
                max={9999}
                value={priority}
                onChange={(e) => setPriority(e.target.value)}
                placeholder={String(suggestedPriority)}
                className="text-xs h-9"
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Role</Label>
              <Select value={role} onValueChange={(v) => setRole(v as "primary" | "fallback")}>
                <SelectTrigger className="text-xs h-9">
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

          {/* Active */}
          <div className="flex items-center justify-between rounded-lg border border-border/50 bg-muted/10 px-3 py-2.5">
            <div>
              <p className="text-xs font-medium">Active</p>
              <p className="text-[11px] text-muted-foreground">
                Inactive rules are saved but skipped during routing
              </p>
            </div>
            <Switch checked={active} onCheckedChange={setActive} />
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={handleClose} size="sm">
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={() => create.mutate()}
            disabled={!canSubmit || create.isPending || modelsLoading}
          >
            {create.isPending ? "Creating…" : "Create rule"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
