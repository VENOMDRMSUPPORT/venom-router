import { useState, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Play, RefreshCw, Search } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api-client";
import { parseSyncResponse, invalidateModelViews } from "@/lib/providers/sync-cache";
import type { SyncAccountResult } from "@/lib/providers/sync-response.types";

interface AccountModel {
  id: string;
  model_id: string;
  external_id: string;
  display_name: string;
  capabilities: string[];
  latency_ms: number | null;
  test_status: "working" | "failed" | "untested";
  last_test_error: string | null;
  last_tested_at: string | null;
  enabled: boolean;
}

function getErrorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (e && typeof e === "object" && "message" in e) {
    return String((e as { message: unknown }).message);
  }
  return String(e);
}

export function ModelTestReportDialog({
  open,
  onOpenChange,
  accountId,
  providerName,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  accountId: string | null;
  providerName: string;
}) {
  const qc = useQueryClient();

  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<string>("all");
  const [busy, setBusy] = useState(false);
  const [enabledMap, setEnabledMap] = useState<Record<string, boolean>>({});
  const [testingIds, setTestingIds] = useState<Record<string, boolean>>({});

  const q = useQuery({
    queryKey: ["account-models", accountId],
    queryFn: async () => {
      const data = await api.get(`/api/dashboard/accounts/${accountId!}/models`);
      return data as AccountModel[];
    },
    enabled: open && !!accountId,
  });

  const allModels = useMemo(() => (q.data ?? []) as AccountModel[], [q.data]);

  // Filter models based on search query and status filter
  const rows = useMemo(() => {
    return allModels.filter((m) => {
      // 1. Search filter
      const matchesSearch =
        !search ||
        m.display_name?.toLowerCase().includes(search.toLowerCase()) ||
        m.external_id?.toLowerCase().includes(search.toLowerCase());

      // 2. Status filter
      const isChecked = enabledMap[m.id] ?? m.enabled;
      let matchesStatus = true;
      if (statusFilter === "working") {
        matchesStatus = m.test_status === "working";
      } else if (statusFilter === "failed") {
        matchesStatus = m.test_status === "failed";
      } else if (statusFilter === "untested") {
        matchesStatus = m.test_status === "untested" || !m.test_status;
      } else if (statusFilter === "enabled") {
        matchesStatus = isChecked;
      } else if (statusFilter === "disabled") {
        matchesStatus = !isChecked;
      }

      return matchesSearch && matchesStatus;
    });
  }, [allModels, search, statusFilter, enabledMap]);

  // Statistics across all models for this account
  const stats = useMemo(() => {
    let working = 0;
    let failed = 0;
    let untested = 0;
    let enabled = 0;

    for (const m of allModels) {
      if (m.test_status === "working") working++;
      else if (m.test_status === "failed") failed++;
      else untested++;

      const isChecked = enabledMap[m.id] ?? m.enabled;
      if (isChecked) enabled++;
    }

    return { working, failed, untested, enabled, total: allModels.length };
  }, [allModels, enabledMap]);

  async function refetch() {
    if (!accountId) return;
    setBusy(true);
    try {
      await parseSyncResponse(
        await api.post<SyncAccountResult>(`/api/dashboard/accounts/${accountId}/sync`, {
          account_id: accountId,
        }),
      );
      await q.refetch();
      await invalidateModelViews(qc);
      toast.success("Models refreshed from provider");
    } catch (e: unknown) {
      toast.error(getErrorMessage(e) ?? "Sync failed");
    } finally {
      setBusy(false);
    }
  }

  async function runTestSingle(modelId: string, externalId: string) {
    if (!accountId) return;
    setTestingIds((p) => ({ ...p, [modelId]: true }));
    try {
      const results = (await api.post(`/api/dashboard/accounts/${accountId}/models/test`, {
        account_id: accountId,
        external_ids: [externalId],
      })) as Array<{
        external_id: string;
        ok: boolean;
        latency_ms?: number;
        error?: string;
      }>;

      const r = results[0];
      if (r) {
        if (r.ok) {
          toast.success(`${externalId} tested successfully! (${r.latency_ms ?? 0}ms)`);
          // Auto-enable working model
          setEnabledMap((prev) => ({ ...prev, [modelId]: true }));
        } else {
          toast.error(`${externalId} test failed: ${r.error ?? "Unknown error"}`);
          // Auto-disable failed model
          setEnabledMap((prev) => ({ ...prev, [modelId]: false }));
        }
      }
      await q.refetch();
      await invalidateModelViews(qc);
    } catch (e: unknown) {
      toast.error(getErrorMessage(e) ?? "Test failed");
    } finally {
      setTestingIds((p) => ({ ...p, [modelId]: false }));
    }
  }

  async function runTestAll() {
    if (!accountId || !rows.length) return;

    const idsToTest = rows.map((m) => m.id);
    const extIds = rows.map((m) => m.external_id);

    setTestingIds((p) => {
      const next = { ...p };
      for (const id of idsToTest) {
        next[id] = true;
      }
      return next;
    });
    setBusy(true);

    try {
      const results = (await api.post(`/api/dashboard/accounts/${accountId}/models/test`, {
        account_id: accountId,
        external_ids: extIds,
      })) as Array<{
        external_id: string;
        ok: boolean;
        latency_ms?: number;
        error?: string;
      }>;

      // Update selections based on test results
      setEnabledMap((prev) => {
        const next = { ...prev };
        for (const r of results) {
          const model = allModels.find((m) => m.external_id === r.external_id);
          if (model) {
            next[model.id] = r.ok;
          }
        }
        return next;
      });

      await q.refetch();
      await invalidateModelViews(qc);
      toast.success(`Tested ${extIds.length} model(s) successfully`);
    } catch (e: unknown) {
      toast.error(getErrorMessage(e) ?? "Bulk test failed");
    } finally {
      setBusy(false);
      setTestingIds((p) => {
        const next = { ...p };
        for (const id of idsToTest) {
          next[id] = false;
        }
        return next;
      });
    }
  }

  async function save() {
    if (!accountId) return;
    setBusy(true);
    try {
      const patch: Record<string, boolean> = {};
      for (const m of allModels) {
        const next = enabledMap[m.id] ?? m.enabled;
        if (next !== m.enabled) patch[m.id] = next;
      }
      if (Object.keys(patch).length === 0) {
        onOpenChange(false);
        return;
      }
      await api.post(`/api/dashboard/accounts/${accountId}/models/enabled`, {
        account_id: accountId,
        enabled: patch,
      });
      await qc.invalidateQueries({ queryKey: ["integrations"] });
      await invalidateModelViews(qc);
      await q.refetch();
      toast.success("Saved configuration");
      onOpenChange(false);
    } catch (e: unknown) {
      toast.error(getErrorMessage(e) ?? "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[85vh] !flex flex-col gap-4 overflow-hidden">
        <DialogHeader>
          <DialogTitle>Model Test Report: {providerName}</DialogTitle>
          <DialogDescription>
            Test models for {providerName} and enable/disable them for routing.
          </DialogDescription>
        </DialogHeader>

        {/* Statistics Widgets */}
        <div className="grid grid-cols-4 gap-2 text-center shrink-0">
          <div className="rounded-lg border border-border/50 bg-accent/10 p-2.5">
            <p className="text-[10px] uppercase font-semibold text-muted-foreground">Working</p>
            <p className="text-lg font-bold text-emerald-500 tabular-nums">{stats.working}</p>
          </div>
          <div className="rounded-lg border border-border/50 bg-accent/10 p-2.5">
            <p className="text-[10px] uppercase font-semibold text-muted-foreground">Failed</p>
            <p className="text-lg font-bold text-red-500 tabular-nums">{stats.failed}</p>
          </div>
          <div className="rounded-lg border border-border/50 bg-accent/10 p-2.5">
            <p className="text-[10px] uppercase font-semibold text-muted-foreground">Untested</p>
            <p className="text-lg font-bold text-muted-foreground tabular-nums">{stats.untested}</p>
          </div>
          <div className="rounded-lg border border-border/50 bg-accent/10 p-2.5">
            <p className="text-[10px] uppercase font-semibold text-muted-foreground">Enabled</p>
            <p className="text-lg font-bold text-primary tabular-nums">{stats.enabled}</p>
          </div>
        </div>

        {/* Control and Action bar */}
        <div className="flex flex-col gap-2 shrink-0">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2 flex-1 min-w-[200px]">
              <Button
                variant="outline"
                size="sm"
                onClick={refetch}
                disabled={busy}
                className="gap-1.5"
              >
                {busy ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <RefreshCw className="size-3.5" />
                )}
                Refresh Models
              </Button>
              <Button
                variant="default"
                size="sm"
                onClick={runTestAll}
                disabled={busy || rows.length === 0}
                className="gap-1.5"
              >
                <Play className="size-3.5" />
                Test All
              </Button>
            </div>
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                size="sm"
                className="h-8 text-xs"
                onClick={() =>
                  setEnabledMap((p) => {
                    const next = { ...p };
                    for (const r of rows) next[r.id] = true;
                    return next;
                  })
                }
              >
                Enable All
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="h-8 text-xs"
                onClick={() =>
                  setEnabledMap((p) => {
                    const next = { ...p };
                    for (const r of rows) next[r.id] = false;
                    return next;
                  })
                }
              >
                Disable All
              </Button>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="size-4 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="Search models..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="pl-8 h-8 text-xs"
              />
            </div>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-[140px] h-8 text-xs">
                <SelectValue placeholder="Filter status" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All Models</SelectItem>
                <SelectItem value="working">Working</SelectItem>
                <SelectItem value="failed">Failed</SelectItem>
                <SelectItem value="untested">Untested</SelectItem>
                <SelectItem value="enabled">Enabled</SelectItem>
                <SelectItem value="disabled">Disabled</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        {/* Models list — min-h-0 lets the flex child shrink and scroll */}
        <div className="flex-1 min-h-0 overflow-y-auto scrollbar-app -mx-6 px-6 border-y py-1 border-border/40">
          <div className="space-y-2 py-2">
            {q.isLoading && (
              <div className="flex items-center justify-center gap-2 text-sm text-muted-foreground py-12">
                <Loader2 className="size-4 animate-spin" /> Loading models…
              </div>
            )}

            {rows.map((m) => {
              const checked = enabledMap[m.id] ?? m.enabled;
              const isTesting = testingIds[m.id];
              const status = isTesting ? "testing" : (m.test_status ?? "untested");

              return (
                <div
                  key={m.id}
                  className={cn(
                    "rounded-md border p-3 flex items-center justify-between gap-4 transition-colors",
                    status === "working"
                      ? "border-emerald-500/20 bg-emerald-500/5 hover:bg-emerald-500/10"
                      : status === "failed"
                        ? "border-red-500/20 bg-red-500/5 hover:bg-red-500/10"
                        : "border-border/60 hover:bg-accent/40",
                  )}
                >
                  <div className="flex items-center gap-3 min-w-0 flex-1">
                    <Checkbox
                      checked={checked}
                      onCheckedChange={(v) => setEnabledMap((p) => ({ ...p, [m.id]: !!v }))}
                      disabled={isTesting}
                    />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-sm truncate">{m.display_name}</span>
                        {m.latency_ms != null && status === "working" && (
                          <span className="text-[10px] font-mono text-muted-foreground">
                            ({m.latency_ms}ms)
                          </span>
                        )}
                      </div>
                      <p className="text-[10px] font-mono text-muted-foreground truncate mt-0.5">
                        {m.external_id}
                      </p>

                      {status === "failed" && m.last_test_error && (
                        <p className="text-[10px] text-red-500 font-medium mt-1 leading-normal break-words max-w-lg">
                          Error: {m.last_test_error}
                        </p>
                      )}

                      <div className="flex gap-1 mt-1.5 flex-wrap">
                        {(m.capabilities ?? []).map((c) => (
                          <Badge
                            key={c}
                            variant="secondary"
                            className="text-[9px] px-1 py-0 h-4 uppercase"
                          >
                            {c}
                          </Badge>
                        ))}
                      </div>
                    </div>
                  </div>

                  <div className="flex items-center gap-2 shrink-0">
                    <Badge
                      variant={
                        status === "working"
                          ? "default"
                          : status === "failed"
                            ? "destructive"
                            : status === "testing"
                              ? "secondary"
                              : "outline"
                      }
                      className={cn(
                        "text-[9px] uppercase px-1.5 py-0.5 font-bold tracking-wider",
                        status === "working" && "bg-emerald-500 text-white hover:bg-emerald-600",
                        status === "testing" &&
                          "animate-pulse bg-amber-500 text-white hover:bg-amber-600",
                      )}
                    >
                      {status === "testing" && (
                        <Loader2 className="size-2.5 animate-spin mr-1 inline" />
                      )}
                      {status}
                    </Badge>

                    <Button
                      variant="outline"
                      size="icon"
                      className="size-7 shrink-0"
                      disabled={busy || isTesting}
                      onClick={() => runTestSingle(m.id, m.external_id)}
                      title="Test this model"
                    >
                      {isTesting ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        <Play className="size-3.5" />
                      )}
                    </Button>
                  </div>
                </div>
              );
            })}

            {!q.isLoading && rows.length === 0 && (
              <div className="text-center text-sm text-muted-foreground py-12">
                No models match the filters. Click "Refresh Models" or change the filter.
              </div>
            )}
          </div>
        </div>

        {/* Footer actions */}
        <div className="flex items-center justify-end gap-2 pt-2 shrink-0">
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Close
          </Button>
          <Button onClick={save} disabled={busy}>
            Save selection
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
