import { useState, useMemo } from "react";
import { useServerFn } from "@tanstack/react-start";
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
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import {
  listAccountModels,
  testAccountModels,
  syncAccount,
  setModelsEnabled,
} from "@/lib/providers/integrations.functions";
import { parseSyncResponse, invalidateModelViews } from "@/lib/providers/sync-cache";

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
  const list = useServerFn(listAccountModels);
  const test = useServerFn(testAccountModels);
  const sync = useServerFn(syncAccount);
  const setEnabled = useServerFn(setModelsEnabled);

  const [tab, setTab] = useState<"fetch" | "test">("test");
  const [search, setSearch] = useState("");
  const [busy, setBusy] = useState(false);
  const [enabledMap, setEnabledMap] = useState<Record<string, boolean>>({});

  const q = useQuery({
    queryKey: ["account-models", accountId],
    queryFn: () => list({ data: { account_id: accountId! } }),
    enabled: open && !!accountId,
  });

  const rows = useMemo(() => {
    const r = (q.data ?? []) as any[];
    return search
      ? r.filter(
          (m) =>
            m.display_name?.toLowerCase().includes(search.toLowerCase()) ||
            m.external_id?.toLowerCase().includes(search.toLowerCase()),
        )
      : r;
  }, [q.data, search]);

  const working = rows.filter((m) => m.test_status === "working").length;
  const disabled = rows.filter((m) => !m.enabled).length;
  const enabledCount = rows.filter((m) => m.enabled).length;

  async function refetch() {
    if (!accountId) return;
    setBusy(true);
    try {
      await parseSyncResponse(await sync({ data: { account_id: accountId } }));
      await q.refetch();
      await invalidateModelViews(qc);
      toast.success("Models refreshed");
    } catch (e: any) {
      toast.error(e?.message ?? "Sync failed");
    } finally {
      setBusy(false);
    }
  }

  async function runTests() {
    if (!accountId) return;
    const ids = rows.map((m) => m.external_id as string);
    if (!ids.length) return;
    setBusy(true);
    try {
      await test({ data: { account_id: accountId, external_ids: ids } });
      await q.refetch();
      await invalidateModelViews(qc);
      toast.success("Test complete");
    } catch (e: any) {
      toast.error(e?.message ?? "Test failed");
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    if (!accountId) return;
    setBusy(true);
    try {
      const patch: Record<string, boolean> = {};
      for (const m of rows) {
        const next = enabledMap[m.id] ?? m.enabled;
        if (next !== m.enabled) patch[m.id] = next;
      }
      if (Object.keys(patch).length === 0) {
        onOpenChange(false);
        return;
      }
      await setEnabled({ data: { account_id: accountId, enabled: patch } });
      await qc.invalidateQueries({ queryKey: ["integrations"] });
      await invalidateModelViews(qc);
      await q.refetch();
      toast.success("Saved");
      onOpenChange(false);
    } catch (e: any) {
      toast.error(e?.message ?? "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[85vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Model Test Report</DialogTitle>
          <DialogDescription>
            Real chat tests for {providerName}. Working models are auto-selected for routing.
          </DialogDescription>
        </DialogHeader>

        <Tabs value={tab} onValueChange={(v) => setTab(v as any)} className="w-full">
          <TabsList className="grid grid-cols-2 w-full">
            <TabsTrigger value="fetch" onClick={refetch}>
              <RefreshCw className="size-4" /> Fetch Models
            </TabsTrigger>
            <TabsTrigger value="test">
              <Play className="size-4" /> Test Models
            </TabsTrigger>
          </TabsList>
        </Tabs>

        <div className="flex items-center gap-2">
          <div className="relative flex-1">
            <Search className="size-4 absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Search models..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-8"
            />
          </div>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setEnabledMap(Object.fromEntries(rows.map((m: any) => [m.id, true])))}
          >
            All
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setEnabledMap(Object.fromEntries(rows.map((m: any) => [m.id, false])))}
          >
            None
          </Button>
        </div>

        <div className="flex items-center gap-3 text-xs">
          <span className="text-success font-medium">● {working} working</span>
          <span className="text-muted-foreground">○ {disabled} disabled</span>
          <span className="text-primary font-medium">● {enabledCount} enabled</span>
        </div>

        <ScrollArea className="flex-1 -mx-6 px-6">
          <div className="space-y-2 py-2">
            {q.isLoading && (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" /> Loading…
              </div>
            )}
            {rows.map((m: any) => {
              const checked = enabledMap[m.id] ?? m.enabled;
              return (
                <div
                  key={m.id}
                  className={cn(
                    "rounded-md border p-3 flex items-center gap-3",
                    m.test_status === "working"
                      ? "border-success/40 bg-success/5"
                      : m.test_status === "failed"
                        ? "border-destructive/40 bg-destructive/5"
                        : "border-border/60",
                  )}
                >
                  <Checkbox
                    checked={checked}
                    onCheckedChange={(v) => setEnabledMap((p) => ({ ...p, [m.id]: !!v }))}
                  />
                  <div className="flex-1 min-w-0">
                    <p className="font-medium text-sm">{m.display_name}</p>
                    <p className="text-[11px] font-mono text-muted-foreground truncate">
                      {m.external_id}
                    </p>
                    <div className="flex gap-1 mt-1.5 flex-wrap">
                      {(m.capabilities ?? []).map((c: string) => (
                        <Badge key={c} variant="secondary" className="text-[10px]">
                          {c}
                        </Badge>
                      ))}
                    </div>
                  </div>
                  <div className="text-right space-y-1">
                    {m.latency_ms != null && (
                      <span className="text-[11px] font-mono text-muted-foreground">
                        {m.latency_ms}ms
                      </span>
                    )}
                    <div>
                      <Badge
                        variant={
                          m.test_status === "working"
                            ? "default"
                            : m.test_status === "failed"
                              ? "destructive"
                              : "outline"
                        }
                        className="text-[10px] uppercase"
                      >
                        {m.test_status}
                      </Badge>
                    </div>
                  </div>
                </div>
              );
            })}
            {!q.isLoading && rows.length === 0 && (
              <div className="text-center text-sm text-muted-foreground py-8">
                No models yet — click "Fetch Models" to discover them.
              </div>
            )}
          </div>
        </ScrollArea>

        <div className="flex items-center justify-between pt-2 border-t border-border/40">
          <Button
            variant="outline"
            size="sm"
            onClick={runTests}
            disabled={busy || rows.length === 0}
          >
            {busy ? <Loader2 className="size-4 animate-spin" /> : <Play className="size-4" />}
            Run tests
          </Button>
          <div className="flex gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Close
            </Button>
            <Button onClick={save} disabled={busy}>
              Save selection
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
