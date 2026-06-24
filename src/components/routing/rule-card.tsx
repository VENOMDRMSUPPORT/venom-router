import { useContext } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Trash2, ChevronUp, ChevronDown } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
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
import { ProviderIcon } from "@/components/providers/provider-icon";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api-client";
import type { RoutingRule } from "@/lib/db/venom.server";
import { ROLE_LABELS } from "./routing-constants";
import { RoutingDebugContext } from "./routing-debug-context";

export function RuleCard({ rule, rank }: { rule: RoutingRule; rank: number }) {
  const qc = useQueryClient();
  const debug = useContext(RoutingDebugContext);

  const toggle = useMutation({
    mutationFn: async (active: boolean) => {
      const t0 = Date.now();
      const dbId = debug?.start("toggleRule", { id: rule.id, active });
      try {
        const res = await api.patch<{ ok: true }>(`/api/dashboard/routing-rules/${rule.id}`, {
          active,
        });
        debug?.resolve(dbId || "", res, Date.now() - t0);
        return res;
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        debug?.reject(dbId || "", msg, Date.now() - t0);
        throw e;
      }
    },
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
  const requires = rule.condition?.requires;

  return (
    <div
      className={cn(
        "flex items-center gap-3 rounded-xl border px-4 py-3.5 transition-all duration-300 shadow-sm",
        rule.active
          ? "border-border/60 bg-card/40 hover:border-primary/45 hover:shadow-elegant hover:bg-card/70"
          : "border-border/20 bg-background/25 opacity-55 hover:opacity-75 hover:border-border/40",
      )}
    >
      <span className="w-5 text-center text-[10px] font-bold font-mono text-muted-foreground/45 tabular-nums shrink-0">
        #{rank}
      </span>

      <div className="p-1 rounded-lg bg-background/50 border border-border/40 shrink-0">
        <ProviderIcon slug={rule.provider_slug} className="h-6 w-6" />
      </div>

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-xs font-semibold text-foreground truncate tracking-tight">
            {rule.model_display_name || rule.model_external_id}
          </span>
          <Badge
            variant="outline"
            className={cn(
              "text-[9px] font-semibold h-4 px-1.5 shrink-0 capitalize rounded-md",
              rule.role === "primary"
                ? "border-emerald-500/20 text-emerald-400 bg-emerald-500/5"
                : "border-amber-500/20 text-amber-400 bg-amber-500/5",
            )}
          >
            {ROLE_LABELS[rule.role] ?? rule.role ?? "primary"}
          </Badge>
          {requires && requires.length > 0 && (
            <Badge
              variant="secondary"
              className="text-[9px] h-4 px-1.5 shrink-0 rounded-md font-mono bg-muted/65 border-border/20"
            >
              {requires.length} cap{requires.length === 1 ? "" : "s"}
            </Badge>
          )}
        </div>
        <p className="text-[10px] text-muted-foreground mt-0.5 truncate flex items-center gap-1.5">
          <span className="font-medium text-muted-foreground/80">{rule.provider_name}</span>
          <span className="text-muted-foreground/30">•</span>
          <span className="font-mono text-muted-foreground/50">{accountLabel}</span>
        </p>
      </div>

      <div className="flex items-center gap-1 shrink-0 bg-background/40 border border-border/40 rounded-lg px-1.5 py-0.5 shadow-sm">
        <ChevronUp className="h-3.5 w-3.5 text-muted-foreground/40" />
        <span className="text-[10px] font-mono font-bold text-muted-foreground tabular-nums w-8 text-center">
          {rule.priority}
        </span>
        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground/40" />
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
  const debug = useContext(RoutingDebugContext);
  const del = useMutation({
    mutationFn: async () => {
      const t0 = Date.now();
      const dbId = debug?.start("deleteRule", { id: ruleId });
      try {
        const res = await api.delete<{ ok: true }>(`/api/dashboard/routing-rules/${ruleId}`);
        debug?.resolve(dbId || "", res, Date.now() - t0);
        return res;
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        debug?.reject(dbId || "", msg, Date.now() - t0);
        throw e;
      }
    },
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
