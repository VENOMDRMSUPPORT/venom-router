import { createFileRoute } from "@tanstack/react-router";
import { useSuspenseQuery, queryOptions, useQueryClient } from "@tanstack/react-query";
import { useServerFn } from "@tanstack/react-start";
import { Suspense, useMemo, useState, Fragment } from "react";
import {
  Brain,
  Search,
  CheckCircle2,
  XCircle,
  HelpCircle,
  Play,
  Loader2,
  ChevronDown,
  ChevronRight,
  Boxes,
  Zap,
  AlertTriangle,
} from "lucide-react";
import { Header } from "@/components/layout/header";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";
import {
  listCatalogModels,
  testAccountModels,
  setModelsEnabled,
  type CatalogModel,
} from "@/lib/providers/integrations.functions";
import { invalidateModelViews } from "@/lib/providers/sync-cache";
import { ProviderIcon } from "@/components/providers/provider-icon";
import { ModelCapabilityIcons } from "@/components/providers/model-capability-icons";

export const Route = createFileRoute("/_authenticated/models")({
  head: () => ({ meta: [{ title: "Models — Venom Router" }] }),
  component: ModelsPage,
});

function ModelsPage() {
  return (
    <>
      <Header
        title="Models"
        description="Discovered, tested, approved and blocked provider models."
        icon={<Brain className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-6">
        <Suspense fallback={<Skeleton className="h-96 rounded-2xl" />}>
          <ModelsBody />
        </Suspense>
      </div>
    </>
  );
}

function formatContext(n: number | null): string {
  if (n == null) return "—";
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}K`;
  return String(n);
}

function TestStatusBadge({
  status,
  latency,
}: {
  status: CatalogModel["test_status"];
  latency: number | null;
}) {
  if (status === "working") {
    return (
      <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400 text-xs">
        <CheckCircle2 className="h-3.5 w-3.5" />
        working{latency != null ? ` · ${latency}ms` : ""}
      </span>
    );
  }
  if (status === "failed") {
    return (
      <span className="inline-flex items-center gap-1 text-red-500 text-xs">
        <XCircle className="h-3.5 w-3.5" />
        failed
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 text-muted-foreground text-xs">
      <HelpCircle className="h-3.5 w-3.5" />
      untested
    </span>
  );
}

function ModelsBody() {
  const fn = useServerFn(listCatalogModels);
  const qc = useQueryClient();
  const testFn = useServerFn(testAccountModels);
  const setEnabledFn = useServerFn(setModelsEnabled);

  const { data } = useSuspenseQuery(
    queryOptions({ queryKey: ["catalog-models"], queryFn: () => fn() }),
  );

  const [search, setSearch] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [testFilter, setTestFilter] = useState("all");
  const [lifecycleFilter, setLifecycleFilter] = useState("all");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [busyKey, setBusyKey] = useState<string | null>(null);

  const providers = useMemo(() => {
    const set = new Set(data.map((m) => m.provider_slug));
    return [...set].sort();
  }, [data]);

  const filtered = useMemo(() => {
    return data.filter((m) => {
      if (providerFilter !== "all" && m.provider_slug !== providerFilter) return false;
      if (testFilter !== "all" && m.test_status !== testFilter) return false;
      if (lifecycleFilter !== "all" && m.lifecycle !== lifecycleFilter) return false;
      if (search) {
        const q = search.toLowerCase();
        if (
          !m.display_name.toLowerCase().includes(q) &&
          !m.external_id.toLowerCase().includes(q) &&
          !m.provider_name.toLowerCase().includes(q)
        )
          return false;
      }
      return true;
    });
  }, [data, search, providerFilter, testFilter, lifecycleFilter]);

  const working = data.filter((m) => m.test_status === "working").length;
  const failedOrUntested = data.filter((m) => m.test_status !== "working").length;

  async function runTest(model: CatalogModel) {
    const first = model.accounts[0];
    if (!first) return;
    setBusyKey(model.key);
    try {
      await testFn({ data: { account_id: first.id, external_ids: [model.external_id] } });
      await invalidateModelViews(qc);
      toast.success(`Tested ${model.display_name}`);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Test failed");
    } finally {
      setBusyKey(null);
    }
  }

  async function toggleEnabled(model: CatalogModel, enabled: boolean) {
    setBusyKey(model.key);
    try {
      for (const row of model.account_rows) {
        if (!row.account_id) continue;
        await setEnabledFn({
          data: { account_id: row.account_id, enabled: { [row.id]: enabled } },
        });
      }
      await invalidateModelViews(qc);
      toast.success(enabled ? "Model enabled" : "Model disabled");
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Update failed");
    } finally {
      setBusyKey(null);
    }
  }

  if (data.length === 0) {
    return (
      <Card className="border-border/60 p-12 text-center space-y-3">
        <Brain className="size-10 mx-auto text-muted-foreground" />
        <p className="font-medium">No provider models yet</p>
        <p className="text-sm text-muted-foreground">
          Models appear here once you connect a provider account and run discovery.
        </p>
      </Card>
    );
  }

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <KpiMini icon={Boxes} label="Unique models" value={String(data.length)} />
        <KpiMini icon={CheckCircle2} label="Working" value={String(working)} accent="success" />
        <KpiMini icon={AlertTriangle} label="Failed / untested" value={String(failedOrUntested)} />
        <KpiMini icon={Zap} label="Providers" value={String(providers.length)} />
      </div>

      <div className="flex flex-col sm:flex-row gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Search models…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Select value={providerFilter} onValueChange={setProviderFilter}>
          <SelectTrigger className="w-full sm:w-40">
            <SelectValue placeholder="Provider" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All providers</SelectItem>
            {providers.map((p) => (
              <SelectItem key={p} value={p}>
                {p}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={testFilter} onValueChange={setTestFilter}>
          <SelectTrigger className="w-full sm:w-36">
            <SelectValue placeholder="Test status" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All tests</SelectItem>
            <SelectItem value="working">Working</SelectItem>
            <SelectItem value="failed">Failed</SelectItem>
            <SelectItem value="untested">Untested</SelectItem>
          </SelectContent>
        </Select>
        <Select value={lifecycleFilter} onValueChange={setLifecycleFilter}>
          <SelectTrigger className="w-full sm:w-36">
            <SelectValue placeholder="Lifecycle" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All lifecycle</SelectItem>
            <SelectItem value="approved">Approved</SelectItem>
            <SelectItem value="discovered">Discovered</SelectItem>
            <SelectItem value="blocked">Blocked</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <Card className="border-border/60 overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/30 text-left text-[11px] uppercase tracking-wider text-muted-foreground">
                <th className="px-4 py-3 font-medium w-8" />
                <th className="px-4 py-3 font-medium">Model</th>
                <th className="px-4 py-3 font-medium">Provider</th>
                <th className="px-4 py-3 font-medium">Accounts</th>
                <th className="px-4 py-3 font-medium">Capabilities</th>
                <th className="px-4 py-3 font-medium">Rating</th>
                <th className="px-4 py-3 font-medium">Context</th>
                <th className="px-4 py-3 font-medium">Test</th>
                <th className="px-4 py-3 font-medium">Last tested</th>
                <th className="px-4 py-3 font-medium">On</th>
                <th className="px-4 py-3 font-medium" />
              </tr>
            </thead>
            <tbody>
              {filtered.map((m) => {
                const isExpanded = expanded === m.key;
                const allEnabled = m.enabled_account_count === m.total_account_count;
                const isBusy = busyKey === m.key;
                return (
                  <Fragment key={m.key}>
                    <tr className="border-b border-border/50 hover:bg-muted/20 transition-colors">
                      <td className="px-4 py-3">
                        <button
                          type="button"
                          onClick={() => setExpanded(isExpanded ? null : m.key)}
                          className="text-muted-foreground hover:text-foreground"
                        >
                          {isExpanded ? (
                            <ChevronDown className="h-4 w-4" />
                          ) : (
                            <ChevronRight className="h-4 w-4" />
                          )}
                        </button>
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-3 min-w-[200px]">
                          <ProviderIcon slug={m.provider_slug} className="h-9 w-9 shrink-0" />
                          <div className="min-w-0">
                            <div className="font-medium truncate">{m.display_name}</div>
                            <code className="text-[10px] font-mono text-muted-foreground block truncate">
                              {m.external_id}
                            </code>
                          </div>
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        <span className="text-xs font-medium text-muted-foreground">
                          {m.provider_name}
                        </span>
                      </td>
                      <td className="px-4 py-3 tabular-nums text-muted-foreground">
                        {m.enabled_account_count}/{m.total_account_count}
                      </td>
                      <td className="px-4 py-3">
                        <ModelCapabilityIcons capabilities={m.capabilities} max={5} />
                      </td>
                      <td className="px-4 py-3">
                        <Badge
                          className={cn(
                            "text-[10px] tabular-nums",
                            m.quality_rating >= 85
                              ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20"
                              : m.quality_rating >= 70
                                ? "bg-amber-500/10 text-amber-600 border-amber-500/20"
                                : "bg-muted text-muted-foreground",
                          )}
                          variant="outline"
                        >
                          {m.quality_rating}
                        </Badge>
                      </td>
                      <td className="px-4 py-3 text-muted-foreground tabular-nums text-xs">
                        {formatContext(m.context_window)}
                      </td>
                      <td className="px-4 py-3">
                        <TestStatusBadge status={m.test_status} latency={m.latency_ms} />
                      </td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">
                        {formatRelativeTime(m.last_tested_at)}
                      </td>
                      <td className="px-4 py-3">
                        <Checkbox
                          checked={allEnabled}
                          disabled={isBusy}
                          onCheckedChange={(v) => toggleEnabled(m, v === true)}
                        />
                      </td>
                      <td className="px-4 py-3">
                        <Button
                          size="sm"
                          variant="ghost"
                          className="h-7 px-2"
                          disabled={isBusy || !m.accounts[0]}
                          onClick={() => runTest(m)}
                        >
                          {isBusy ? (
                            <Loader2 className="h-3.5 w-3.5 animate-spin" />
                          ) : (
                            <Play className="h-3.5 w-3.5" />
                          )}
                        </Button>
                      </td>
                    </tr>
                    {isExpanded && (
                      <tr className="bg-muted/10 border-b border-border/50">
                        <td colSpan={11} className="px-8 py-3">
                          <p className="text-[10px] uppercase tracking-wider text-muted-foreground mb-2">
                            Accounts exposing this model
                          </p>
                          <ul className="space-y-1">
                            {m.accounts.map((a) => (
                              <li
                                key={a.id}
                                className="text-xs flex items-center gap-2 text-muted-foreground"
                              >
                                <span className="font-medium text-foreground">
                                  {a.label ?? a.email ?? a.id.slice(0, 8)}
                                </span>
                                <Badge variant="outline" className="text-[9px]">
                                  {a.status}
                                </Badge>
                                {a.email && a.label && (
                                  <span className="font-mono text-[10px]">{a.email}</span>
                                )}
                              </li>
                            ))}
                          </ul>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
        {filtered.length === 0 && (
          <div className="p-8 text-center text-sm text-muted-foreground">
            No models match your filters.
          </div>
        )}
      </Card>
    </div>
  );
}

function KpiMini({
  icon: Icon,
  label,
  value,
  accent,
}: {
  icon: typeof Boxes;
  label: string;
  value: string;
  accent?: "success";
}) {
  return (
    <Card className="p-4 border-border/60">
      <div className="flex items-center gap-3">
        <div
          className={cn(
            "flex h-9 w-9 items-center justify-center rounded-lg",
            accent === "success"
              ? "bg-emerald-500/10 text-emerald-600"
              : "bg-muted text-muted-foreground",
          )}
        >
          <Icon className="h-4 w-4" />
        </div>
        <div>
          <div className="text-[11px] text-muted-foreground uppercase tracking-wider">{label}</div>
          <div className="font-display text-xl font-bold tabular-nums">{value}</div>
        </div>
      </div>
    </Card>
  );
}
