import { useState, useMemo, useCallback, useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Loader2,
  Play,
  RefreshCw,
  Search,
  AlertTriangle,
  ChevronDown,
  Info,
  Settings2,
} from "lucide-react";
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
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api-client";
import type { AntigravityFetchDiagnosis } from "@/lib/providers/antigravity-fetch-diagnostics.types";
import {
  filterSnapshotModels,
  patchModelTestResult,
  setModelTesting,
  formatAntigravityFetchToast,
  type AntigravityLiveFetchSnapshot,
  type AntigravityFetchedModel,
  type SnapshotFilters,
} from "@/lib/providers/antigravity-live-snapshot";
import { invalidateModelViews } from "@/lib/providers/sync-cache";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";

/* ─── tiny helpers ────────────────────────────────────────────────── */

function SummaryCard({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <div className="rounded-lg border border-border/60 bg-card/50 px-3 py-2 min-w-[80px]">
      <p className="text-[10px] uppercase tracking-wide text-muted-foreground">{label}</p>
      <p className={cn("text-lg font-semibold tabular-nums", tone)}>{value}</p>
    </div>
  );
}

function formatTime(iso?: string) {
  if (!iso) return "—";
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function shortProjectId(id?: string) {
  if (!id) return "";
  return id.length > 12 ? `${id.slice(0, 8)}…` : id;
}

/* ─── main component ──────────────────────────────────────────────── */

export function AntigravityLiveModelDialog({
  open,
  onOpenChange,
  accountId,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  accountId: string | null;
}) {
  const qc = useQueryClient();

  const [snapshot, setSnapshot] = useState<AntigravityLiveFetchSnapshot | null>(null);
  const [busy, setBusy] = useState(false);
  const [busyMessage, setBusyMessage] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [filters, setFilters] = useState<SnapshotFilters>({
    status: "all",
    capability: "all",
    quota: "all",
  });
  const [selection, setSelection] = useState<Record<string, boolean>>({});
  const [rawDrawer, setRawDrawer] = useState<
    | { kind: "full" }
    | { kind: "rawCatalog" }
    | { kind: "model"; model: AntigravityFetchedModel }
    | { kind: "diagnosis"; data: AntigravityFetchDiagnosis }
    | null
  >(null);
  const [diagnosis, setDiagnosis] = useState<AntigravityFetchDiagnosis | null>(null);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [fetchError, setFetchError] = useState<string | null>(null);

  /* ─── derived data ─────────────────────────────────────────────── */

  const mainListModels = useMemo(() => {
    if (!snapshot) return [];
    return snapshot.visibleCatalog.models;
  }, [snapshot]);

  const visibleModels = useMemo(() => {
    return filterSnapshotModels(mainListModels, { ...filters, search });
  }, [mainListModels, filters, search]);

  const selectedIds = useMemo(
    () =>
      Object.entries(selection)
        .filter(([, v]) => v)
        .map(([id]) => id),
    [selection],
  );

  const stats = snapshot?.stats;

  useEffect(() => {
    if (!open) {
      setSnapshot(null);
      setSelection({});
      setFetchError(null);
      return;
    }
    if (!accountId) return;

    let cancelled = false;
    (async () => {
      setBusy(true);
      setBusyMessage("Loading saved models…");
      try {
        const result = (await api.get(
          `/api/dashboard/accounts/${accountId}/antigravity/stored-snapshot`,
        )) as AntigravityLiveFetchSnapshot | null;
        if (cancelled || !result) return;
        setSnapshot(result);
        setSelection(
          Object.fromEntries(
            result.visibleCatalog.models.filter((m) => m.routing.selected).map((m) => [m.id, true]),
          ),
        );
      } catch {
        /* no saved models yet */
      } finally {
        if (!cancelled) {
          setBusy(false);
          setBusyMessage(null);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [open, accountId]);

  /* ─── actions ──────────────────────────────────────────────────── */

  const doFetch = useCallback(async () => {
    if (!accountId) return;
    setBusy(true);
    setBusyMessage("Refreshing Antigravity models…");
    setFetchError(null);
    try {
      const result = (await api.post(
        `/api/dashboard/accounts/${accountId}/antigravity/live-snapshot`,
        { account_id: accountId },
      )) as AntigravityLiveFetchSnapshot;
      setSnapshot(result);
      setSelection(
        Object.fromEntries(
          result.visibleCatalog.models.filter((m) => m.routing.selected).map((m) => [m.id, true]),
        ),
      );
      toast.success(formatAntigravityFetchToast({ visibleCount: result.stats.visibleCount }));
      await invalidateModelViews(qc);
      await qc.invalidateQueries({ queryKey: ["integrations"] });
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Fetch failed";
      setFetchError(msg);
      toast.error(msg);
    } finally {
      setBusy(false);
      setBusyMessage(null);
    }
  }, [accountId, qc]);

  async function runDeepDiagnostics() {
    if (!accountId) return;
    setBusy(true);
    setBusyMessage("Running deep diagnostics…");
    try {
      const result = (await api.post(`/api/dashboard/accounts/${accountId}/antigravity/diagnose`, {
        account_id: accountId,
      })) as AntigravityFetchDiagnosis;
      setDiagnosis(result);
      setRawDrawer({ kind: "diagnosis", data: result });
      toast.success("Deep diagnostics complete");
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Diagnostics failed");
    } finally {
      setBusy(false);
      setBusyMessage(null);
    }
  }

  async function runTestForIds(ids: string[]) {
    if (!accountId || !snapshot || !ids.length) return;
    let next = snapshot;
    for (const id of ids) {
      next = setModelTesting(next, id);
    }
    setSnapshot(next);

    for (const id of ids) {
      try {
        const results = (await api.post(`/api/dashboard/accounts/${accountId}/models/test`, {
          account_id: accountId,
          external_ids: [id],
        })) as Array<{
          external_id: string;
          ok: boolean;
          latency_ms?: number;
          error?: string;
        }>;
        const r = results[0];
        if (r) {
          setSnapshot((prev) => (prev ? patchModelTestResult(prev, id, r) : prev));
          if (r.ok) {
            setSelection((prev) => ({ ...prev, [id]: true }));
          } else {
            setSelection((prev) => ({ ...prev, [id]: false }));
          }
        }
      } catch (e: unknown) {
        setSnapshot((prev) =>
          prev
            ? patchModelTestResult(prev, id, {
                ok: false,
                error: e instanceof Error ? e.message : "Test failed",
              })
            : prev,
        );
        setSelection((prev) => ({ ...prev, [id]: false }));
      }
    }
  }

  async function testOne(id: string) {
    await runTestForIds([id]);
  }

  async function testAll() {
    if (!mainListModels.length) return;
    setBusy(true);
    setBusyMessage(
      `Testing ${mainListModels.length} IDE-visible models. Tests consume real quota.`,
    );
    try {
      await runTestForIds(mainListModels.map((m) => m.id));
      await invalidateModelViews(qc);
      await qc.invalidateQueries({ queryKey: ["integrations"] });
      toast.success(`Tested ${mainListModels.length} model(s)`);
    } finally {
      setBusy(false);
      setBusyMessage(null);
    }
  }

  async function saveSelection() {
    if (!accountId || !snapshot) return;
    const patch: Record<string, boolean> = {};
    for (const m of mainListModels) {
      const rowId = m.routing.dbRowId;
      if (!rowId) continue;
      const next = selection[m.id] ?? m.routing.selected;
      patch[rowId] = next && m.routing.eligible;
    }
    if (Object.keys(patch).length === 0) {
      onOpenChange(false);
      return;
    }
    setBusy(true);
    try {
      await api.post(`/api/dashboard/accounts/${accountId}/models/enabled`, {
        account_id: accountId,
        enabled: patch,
      });
      await invalidateModelViews(qc);
      await qc.invalidateQueries({ queryKey: ["integrations"] });
      toast.success("Routing selection saved");
      onOpenChange(false);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(false);
    }
  }

  /* ─── render ───────────────────────────────────────────────────── */

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-w-5xl max-h-[88vh] flex flex-col gap-3 overflow-hidden p-6">
          {/* ── Header ──────────────────────────────────────────── */}
          <DialogHeader className="space-y-2 shrink-0">
            <DialogTitle className="flex items-center gap-2">Antigravity Models</DialogTitle>
            <DialogDescription>
              IDE-visible models discovered dynamically from Antigravity.
            </DialogDescription>
            <div className="flex flex-wrap gap-1.5 pt-1">
              {snapshot?.planTier && <Badge variant="outline">Plan: {snapshot.planTier}</Badge>}
              {snapshot?.projectId && (
                <Badge variant="outline" className="font-mono text-[10px]">
                  Project: {shortProjectId(snapshot.projectId)}
                </Badge>
              )}
              {snapshot?.fetchedAt && (
                <Badge variant="outline">Last refresh: {formatTime(snapshot.fetchedAt)}</Badge>
              )}
              {snapshot?.diagnostics.loadedFromDb && (
                <Badge variant="secondary">Saved in database</Badge>
              )}
            </div>
          </DialogHeader>

          {/* ── Compact info note ──────────────────────────────── */}
          <div className="flex items-start gap-2 rounded-md border border-border/60 bg-muted/30 px-3 py-2 text-xs text-muted-foreground shrink-0">
            <Info className="size-4 shrink-0 mt-0.5" />
            <p>
              Tests consume real quota. Usage history is estimated only for Venom Router traffic.
            </p>
          </div>

          {/* ── Error banner ───────────────────────────────────── */}
          {fetchError && (
            <div className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-800 dark:text-red-200 shrink-0 flex items-center gap-2">
              <AlertTriangle className="size-4 shrink-0" />
              <p className="flex-1">{fetchError}</p>
              <Button
                size="sm"
                variant="ghost"
                className="h-6 text-[10px]"
                onClick={() => setFetchError(null)}
              >
                Dismiss
              </Button>
            </div>
          )}

          {/* ── Missing recommended sort warning ──────────────── */}
          {snapshot && !snapshot.diagnostics.recommendedSortFound && (
            <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-900 dark:text-amber-200 shrink-0">
              Recommended model sort is missing; main list is empty. Check Advanced diagnostics.
            </div>
          )}

          {/* ── Primary actions ────────────────────────────────── */}
          <div className="flex flex-wrap gap-2 shrink-0">
            <Button size="sm" onClick={doFetch} disabled={busy || !accountId}>
              {busy ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <RefreshCw className="size-4" />
              )}
              Refresh Models
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={testAll}
              disabled={busy || !mainListModels.length}
            >
              <Play className="size-4" /> Test All
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setSelection({})}
              disabled={!Object.keys(selection).length}
            >
              Clear Selection
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setAdvancedOpen((v) => !v)}>
              <Settings2 className="size-4" /> Advanced
            </Button>
          </div>

          {/* ── Busy overlay message ───────────────────────────── */}
          {busy && busyMessage && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground shrink-0">
              <Loader2 className="size-3 animate-spin" />
              {busyMessage}
            </div>
          )}

          {/* ── Summary cards ──────────────────────────────────── */}
          {stats && (
            <div className="flex flex-wrap gap-2 shrink-0">
              <SummaryCard label="IDE-visible" value={stats.visibleCount} tone="text-primary" />
              <SummaryCard label="Working" value={stats.workingCount} tone="text-emerald-600" />
              <SummaryCard label="Failed" value={stats.failedCount} tone="text-red-600" />
              <SummaryCard label="Selected" value={stats.selectedForRoutingCount} />
              <SummaryCard label="Untested" value={stats.untestedCount} />
              <SummaryCard label="Exhausted" value={stats.exhaustedCount} tone="text-amber-600" />
            </div>
          )}

          {/* ── Filters ────────────────────────────────────────── */}
          <div className="flex flex-wrap items-center gap-2 shrink-0">
            <div className="relative flex-1 min-w-[180px]">
              <Search className="size-4 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="Search display name or model ID…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="pl-8 h-8 text-sm"
              />
            </div>
            <Select
              value={filters.status ?? "all"}
              onValueChange={(v) =>
                setFilters((f) => ({ ...f, status: v as SnapshotFilters["status"] }))
              }
            >
              <SelectTrigger className="w-[130px] h-8 text-xs">
                <SelectValue placeholder="Status" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All status</SelectItem>
                <SelectItem value="working">Working</SelectItem>
                <SelectItem value="failed">Failed</SelectItem>
                <SelectItem value="untested">Untested</SelectItem>
                <SelectItem value="exhausted">Exhausted</SelectItem>
              </SelectContent>
            </Select>
            <Select
              value={filters.quota ?? "all"}
              onValueChange={(v) =>
                setFilters((f) => ({ ...f, quota: v as SnapshotFilters["quota"] }))
              }
            >
              <SelectTrigger className="w-[130px] h-8 text-xs">
                <SelectValue placeholder="Quota" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All quota</SelectItem>
                <SelectItem value="available">Available</SelectItem>
                <SelectItem value="exhausted">Exhausted</SelectItem>
                <SelectItem value="unknown">Unknown</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {/* ── Model list (scrollable) ────────────────────────── */}
          <div className="flex-1 min-h-0 overflow-y-auto -mx-1 px-1">
            <div className="space-y-2 py-2">
              {!snapshot && busy && (
                <div className="flex items-center justify-center gap-2 text-sm text-muted-foreground py-12">
                  <Loader2 className="size-4 animate-spin" />
                  {busyMessage ?? "Loading…"}
                </div>
              )}

              {!snapshot && !busy && (
                <div className="text-center text-sm text-muted-foreground py-12 space-y-2">
                  <p>No saved models for this account yet.</p>
                  <p>
                    Click <strong>Refresh Models</strong> to fetch IDE-visible models from
                    Antigravity.
                  </p>
                </div>
              )}

              {snapshot && visibleModels.length === 0 && !busy && (
                <div className="text-center text-sm text-muted-foreground py-8">
                  No models match the current filters.
                </div>
              )}

              {/* Model rows */}
              {visibleModels.map((m) => {
                const checked = selection[m.id] ?? m.routing.selected;
                const eligible = m.routing.eligible;
                return (
                  <div
                    key={m.id}
                    className={cn(
                      "rounded-md border p-3 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3",
                      m.test.status === "working"
                        ? "border-emerald-500/40 bg-emerald-500/5"
                        : m.test.status === "failed"
                          ? "border-red-500/40 bg-red-500/5"
                          : "border-border/60",
                    )}
                  >
                    <Checkbox
                      checked={checked && eligible}
                      disabled={!eligible}
                      onCheckedChange={(v) => setSelection((p) => ({ ...p, [m.id]: !!v }))}
                      className="mt-0.5"
                    />

                    <div className="flex-1 min-w-0 space-y-0.5">
                      <div className="flex items-center gap-2">
                        <p className="font-medium text-sm truncate">{m.displayName}</p>
                        {m.displayNameSource === "fallback-to-id" && (
                          <Badge variant="outline" className="text-[9px] shrink-0">
                            fallback-to-id
                          </Badge>
                        )}
                      </div>
                      <p className="text-[11px] font-mono text-muted-foreground truncate">{m.id}</p>
                      {m.capabilities.length > 0 && (
                        <p className="text-[10px] text-muted-foreground">
                          Capabilities: {m.capabilities.join(" · ")}
                        </p>
                      )}
                    </div>

                    <div className="flex flex-col items-end gap-1.5 shrink-0">
                      <div className="flex items-center gap-2">
                        <Badge
                          variant={
                            m.test.status === "working"
                              ? "default"
                              : m.test.status === "failed"
                                ? "destructive"
                                : m.test.status === "testing"
                                  ? "secondary"
                                  : "outline"
                          }
                          className="text-[10px] uppercase"
                        >
                          {m.test.status}
                        </Badge>
                        {m.test.latencyMs != null && (
                          <span className="text-[10px] font-mono text-muted-foreground">
                            {m.test.latencyMs}ms
                          </span>
                        )}
                      </div>

                      {m.quota && (
                        <p className="text-[10px] text-muted-foreground text-right">
                          {m.quota.remainingPercentage != null
                            ? `${m.quota.remainingPercentage.toFixed(0)}% remaining`
                            : "quota unknown"}
                          {m.quota.isExhausted && (
                            <span className="text-amber-600"> · exhausted</span>
                          )}
                        </p>
                      )}
                      {m.quota?.resetTime && (
                        <p className="text-[9px] text-muted-foreground text-right">
                          resets {formatTime(m.quota.resetTime)}
                        </p>
                      )}

                      {m.test.error && (
                        <p className="text-[10px] text-red-600 dark:text-red-400 text-right max-w-[200px] truncate">
                          {m.test.error}
                        </p>
                      )}

                      <Button
                        size="sm"
                        variant="outline"
                        className="h-7 text-xs"
                        disabled={busy || m.test.status === "testing"}
                        onClick={() => testOne(m.id)}
                      >
                        {m.test.status === "testing" ? (
                          <Loader2 className="size-3 animate-spin" />
                        ) : (
                          <Play className="size-3" />
                        )}
                        Test
                      </Button>
                    </div>
                  </div>
                );
              })}

              {/* Empty — has snapshot but no models match filters */}
              {snapshot && visibleModels.length === 0 && (
                <div className="text-center text-sm text-muted-foreground py-8">
                  No IDE-visible models match the current filters.
                </div>
              )}
            </div>
          </div>

          {/* ── Advanced Diagnostics (collapsed by default) ────── */}
          <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen} className="shrink-0">
            <CollapsibleTrigger className="flex w-full items-center gap-2 rounded-md border border-border/60 px-3 py-2 text-xs font-medium hover:bg-muted/50">
              <ChevronDown
                className={cn("size-4 transition-transform", advancedOpen && "rotate-180")}
              />
              Advanced diagnostics
            </CollapsibleTrigger>
            <CollapsibleContent className="pt-2 space-y-2">
              {/* Deep diagnostics results */}
              {diagnosis && (
                <div className="rounded-md border border-blue-500/30 bg-blue-500/5 p-3 text-[11px] space-y-2">
                  <p className="font-semibold text-xs">Deep diagnostics summary</p>
                  <ul className="list-disc pl-4 space-y-1">
                    {diagnosis.conclusions.map((c) => (
                      <li key={c}>{c}</li>
                    ))}
                  </ul>
                </div>
              )}

              {/* Snapshot diagnostics */}
              {snapshot?.diagnostics && (
                <div className="rounded-md border border-border/60 bg-muted/30 p-3 text-[11px] space-y-1 font-mono">
                  <p className="font-semibold text-xs font-sans mb-1">Snapshot diagnostics</p>
                  <p>Raw catalog count: {snapshot.rawCatalog.count}</p>
                  <p>IDE-visible count: {snapshot.visibleCatalog.count}</p>
                  <p>Endpoint: fetchAvailableModels</p>
                  <p>Project ID: {snapshot.projectId ?? "—"}</p>
                  <p>Plan: {snapshot.planTier ?? "—"}</p>
                  <p>Last refresh: {formatTime(snapshot.fetchedAt)}</p>
                  <p>Source: {snapshot.visibleCatalog.source}</p>
                  <p>Recommended sort found: {String(snapshot.diagnostics.recommendedSortFound)}</p>
                  <p>DB mixed into modal: {String(snapshot.diagnostics.dbMixedIntoModal)}</p>
                  <p>
                    Hardcoded filters active: {String(snapshot.diagnostics.hardcodedFiltersActive)}
                  </p>
                  <p>Inserted new: {snapshot.stats.insertedNewCount}</p>
                  <p>Updated existing: {snapshot.stats.updatedExistingCount}</p>
                  <p>Unchanged: {snapshot.stats.unchangedCount}</p>
                  <p>Duplicates prevented: {snapshot.stats.duplicatePreventedCount}</p>
                  <p>Stale removed: {snapshot.stats.removedStaleCount}</p>
                  {snapshot.visibleCatalog.missingModelIds.length > 0 && (
                    <p>
                      Missing recommended IDs: {snapshot.visibleCatalog.missingModelIds.join(", ")}
                    </p>
                  )}
                  <div className="flex gap-2 mt-2">
                    <Button
                      size="sm"
                      variant="outline"
                      className="h-7 text-xs"
                      onClick={() => setRawDrawer({ kind: "rawCatalog" })}
                    >
                      Raw catalog JSON
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      className="h-7 text-xs"
                      onClick={() => setRawDrawer({ kind: "full" })}
                    >
                      Full raw response
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      className="h-7 text-xs"
                      onClick={runDeepDiagnostics}
                      disabled={busy || !accountId}
                    >
                      Run deep diagnostics
                    </Button>
                  </div>
                </div>
              )}
            </CollapsibleContent>
          </Collapsible>

          {/* ── Footer ─────────────────────────────────────────── */}
          <div className="flex items-center justify-end gap-2 pt-2 border-t border-border/40 shrink-0">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Close
            </Button>
            <Button onClick={saveSelection} disabled={busy || !snapshot}>
              Save Routing Selection
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* ── Raw JSON drawer ───────────────────────────────────── */}
      <Sheet open={!!rawDrawer} onOpenChange={(o) => !o && setRawDrawer(null)}>
        <SheetContent className="w-full sm:max-w-xl overflow-y-auto">
          <SheetHeader>
            <SheetTitle>
              {rawDrawer?.kind === "diagnosis"
                ? "Antigravity fetch diagnostics"
                : rawDrawer?.kind === "rawCatalog"
                  ? "Raw model catalog"
                  : rawDrawer?.kind === "model"
                    ? `Raw: ${rawDrawer.model.id}`
                    : "Raw fetchAvailableModels response"}
            </SheetTitle>
          </SheetHeader>
          <pre className="mt-4 text-[11px] font-mono whitespace-pre-wrap break-all bg-muted/50 p-3 rounded-md max-h-[70vh] overflow-auto">
            {rawDrawer?.kind === "diagnosis"
              ? JSON.stringify(rawDrawer.data, null, 2)
              : rawDrawer?.kind === "rawCatalog"
                ? JSON.stringify(snapshot?.rawCatalog ?? {}, null, 2)
                : rawDrawer?.kind === "full"
                  ? JSON.stringify(snapshot?.rawResponse ?? {}, null, 2)
                  : rawDrawer?.kind === "model"
                    ? JSON.stringify(
                        {
                          raw: rawDrawer.model.raw,
                          parsed: rawDrawer.model,
                        },
                        null,
                        2,
                      )
                    : ""}
          </pre>
        </SheetContent>
      </Sheet>
    </>
  );
}
