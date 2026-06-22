import { useSuspenseQuery, queryOptions, useQueryClient } from "@tanstack/react-query";
import { useServerFn } from "@tanstack/react-start";
import { Suspense, useState } from "react";
import {
  Plus,
  Users,
  ShieldCheck,
  Boxes,
  Sparkles,
  Wifi,
  LayoutGrid,
  Bug,
  Trash2 as TrashIcon,
  ChevronRight,
  Loader2,
  CheckCircle2,
  AlertCircle,
  Clock,
} from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import {
  listIntegrations,
  listCatalogModels,
  syncAccount,
  toggleAccount,
  disconnectAccount,
  fetchModels,
} from "@/lib/providers/integrations.functions";
import {
  formatSyncToast,
  parseSyncResponse,
  patchAccountInProviders,
  invalidateModelViews,
} from "@/lib/providers/sync-cache";
import { ProviderAccordion, type ProviderRow } from "@/components/providers/account-row";
import { IntegrationCard } from "@/components/providers/integration-card";
import { ConnectCredentialDialog } from "@/components/providers/connect-credential-dialog";
import { OAuthConnectModal } from "@/components/providers/oauth-connect-modal";
import { ModelTestReportDialog } from "@/components/providers/model-test-report-dialog";
import { AntigravityLiveModelDialog } from "@/components/providers/antigravity-live-model-dialog";
import { formatAntigravityFetchToast } from "@/lib/providers/antigravity-live-snapshot";

type DebugEntry = {
  id: string;
  ts: number;
  op: string;
  label: string;
  req: unknown;
  res?: unknown;
  err?: string;
  ms?: number;
  status: "pending" | "success" | "error";
};

function DebugEntryCard({ entry }: { entry: DebugEntry }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="rounded-lg border bg-card text-sm overflow-hidden">
      <div
        className="flex items-center gap-2 px-3 py-2.5 cursor-pointer hover:bg-muted/40 transition-colors select-none"
        onClick={() => setExpanded((v) => !v)}
      >
        {entry.status === "pending" && (
          <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-amber-500" />
        )}
        {entry.status === "success" && (
          <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-emerald-500" />
        )}
        {entry.status === "error" && <AlertCircle className="h-3.5 w-3.5 shrink-0 text-red-500" />}
        <Badge variant="outline" className="font-mono text-[10px] shrink-0 px-1.5">
          {entry.op}
        </Badge>
        <span className="flex-1 min-w-0 text-[11px] text-muted-foreground truncate">
          {entry.label}
        </span>
        {entry.ms != null && (
          <span className="text-[10px] font-mono text-muted-foreground/60 shrink-0">
            {entry.ms}ms
          </span>
        )}
        <span className="text-[10px] text-muted-foreground/50 shrink-0 tabular-nums">
          {new Date(entry.ts).toLocaleTimeString()}
        </span>
        <ChevronRight
          className={cn(
            "h-3.5 w-3.5 shrink-0 text-muted-foreground/40 transition-transform duration-150",
            expanded && "rotate-90",
          )}
        />
      </div>
      {expanded && (
        <div className="border-t px-3 pb-3 space-y-2 bg-muted/20">
          <div>
            <p className="text-[10px] uppercase tracking-widest text-muted-foreground/50 font-semibold pt-2.5 pb-1">
              Request
            </p>
            <pre className="text-[11px] font-mono bg-muted/60 rounded-md p-2.5 overflow-x-auto text-foreground/80 whitespace-pre-wrap break-all leading-relaxed">
              {JSON.stringify(entry.req, null, 2)}
            </pre>
          </div>
          {entry.res != null && (
            <div>
              <p className="text-[10px] uppercase tracking-widest text-muted-foreground/50 font-semibold pb-1">
                Response
              </p>
              <pre className="text-[11px] font-mono bg-emerald-500/5 border border-emerald-500/15 rounded-md p-2.5 overflow-x-auto text-foreground/80 whitespace-pre-wrap break-all leading-relaxed">
                {JSON.stringify(entry.res, null, 2)}
              </pre>
            </div>
          )}
          {entry.err != null && (
            <div>
              <p className="text-[10px] uppercase tracking-widest text-muted-foreground/50 font-semibold pb-1">
                Error
              </p>
              <pre className="text-[11px] font-mono bg-red-500/5 border border-red-500/15 rounded-md p-2.5 overflow-x-auto text-red-400 whitespace-pre-wrap break-all leading-relaxed">
                {entry.err}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export function ProvidersList({ category }: { category: "oauth" | "free" }) {
  return (
    <Suspense fallback={<Skeleton className="h-64 rounded-2xl" />}>
      <Body category={category} />
    </Suspense>
  );
}

function Body({ category }: { category: "oauth" | "free" }) {
  const list = useServerFn(listIntegrations);
  const catalogFn = useServerFn(listCatalogModels);
  const sync = useServerFn(syncAccount);
  const fetchM = useServerFn(fetchModels);
  const toggle = useServerFn(toggleAccount);
  const disconnect = useServerFn(disconnectAccount);
  const qc = useQueryClient();

  const { data } = useSuspenseQuery(
    queryOptions({
      queryKey: ["integrations", category],
      queryFn: () => list({ data: { category } }) as unknown as Promise<ProviderRow[]>,
    }),
  );

  const { data: catalog } = useSuspenseQuery(
    queryOptions({
      queryKey: ["catalog-models"],
      queryFn: () => catalogFn(),
    }),
  );

  const allAccounts = data.flatMap((p) => p.accounts);
  const healthy = allAccounts.filter((a) => a.status === "healthy").length;
  const uniqueModels = catalog.length;
  const workingModels = catalog.filter((m) => m.test_status === "working").length;
  const connected = data.filter((p) => p.accounts.length > 0);
  const available = data.filter((p) => p.accounts.length === 0);

  const [pickerOpen, setPickerOpen] = useState(false);
  const [tab, setTab] = useState<"connected" | "all">("connected");
  const [connectFor, setConnectFor] = useState<{ slug: string; name: string } | null>(null);
  const [testFor, setTestFor] = useState<{ id: string; name: string; slug: string } | null>(null);
  const [debugOpen, setDebugOpen] = useState(false);
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  function accountLabel(id: string): string {
    const a = data.flatMap((p) => p.accounts).find((acc) => acc.id === id);
    return a?.email ?? a?.label ?? id.slice(0, 8);
  }

  function startDebug(id: string, op: string, req: unknown): string {
    const entry: DebugEntry = {
      id: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
      ts: Date.now(),
      op,
      label: accountLabel(id),
      req,
      status: "pending",
    };
    setDebugLog((prev) => [entry, ...prev.slice(0, 49)]);
    return entry.id;
  }

  function resolveDebug(entryId: string, res: unknown, ms: number) {
    setDebugLog((prev) =>
      prev.map((e) => (e.id === entryId ? { ...e, res, ms, status: "success" } : e)),
    );
  }

  function rejectDebug(entryId: string, err: string, ms: number) {
    setDebugLog((prev) =>
      prev.map((e) => (e.id === entryId ? { ...e, err, ms, status: "error" } : e)),
    );
  }

  function startConnect(p: ProviderRow) {
    setPickerOpen(false);
    setConnectFor({ slug: p.slug, name: p.name });
  }

  async function doSyncAll(ids: string[]) {
    for (const id of ids) {
      const r = await parseSyncResponse(await sync({ data: { account_id: id } }));
      if (r?.ok) {
        qc.setQueryData(["integrations", category], (prev: ProviderRow[] | undefined) =>
          patchAccountInProviders(prev, r),
        );
      }
    }
    await invalidateModelViews(qc);
  }

  async function invalidateProviderData() {
    await Promise.all([
      qc.invalidateQueries({ queryKey: ["integrations", category] }),
      invalidateModelViews(qc),
    ]);
  }

  async function doSync(id: string) {
    const t0 = Date.now();
    const req = { account_id: id };
    const dbId = startDebug(id, "syncAccount", req);
    try {
      const raw = await sync({ data: req });
      const r = await parseSyncResponse(raw);
      resolveDebug(dbId, r, Date.now() - t0);
      if (r?.ok) {
        qc.setQueryData(["integrations", category], (prev: ProviderRow[] | undefined) =>
          patchAccountInProviders(prev, r),
        );
        await invalidateModelViews(qc);
        toast.success(formatSyncToast(r));
      } else {
        toast.error("Sync failed");
      }
    } catch (e: any) {
      rejectDebug(dbId, e?.message ?? "Unknown error", Date.now() - t0);
      toast.error(e?.message ?? "Sync failed");
      await qc.invalidateQueries({ queryKey: ["integrations", category] });
      throw e;
    }
  }
  async function doFetchModels(id: string) {
    const t0 = Date.now();
    const req = { account_id: id };
    const dbId = startDebug(id, "fetchModels", req);
    try {
      const r: any = await fetchM({ data: req });
      resolveDebug(dbId, r, Date.now() - t0);
      if (r?.slug === "antigravity") {
        toast.success(formatAntigravityFetchToast({ visibleCount: r.ideVisible ?? 0 }));
      } else {
        toast.success(`Fetched ${r?.count ?? 0} models (${r?.added ?? 0} new)`);
      }
      await invalidateProviderData();
    } catch (e: any) {
      rejectDebug(dbId, e?.message ?? "Unknown error", Date.now() - t0);
      toast.error(e?.message ?? "Fetch models failed");
      throw e;
    }
  }
  async function doToggle(id: string, status: "healthy" | "degraded") {
    try {
      await toggle({ data: { account_id: id, status } });
      await invalidateProviderData();
    } catch (e: any) {
      toast.error(e?.message ?? "Failed");
    }
  }
  async function doDelete(id: string) {
    if (!confirm("Disconnect this account?")) return;
    try {
      await disconnect({ data: { account_id: id } });
      toast.success("Disconnected");
      await invalidateProviderData();
    } catch (e: any) {
      toast.error(e?.message ?? "Failed");
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div className="flex items-center gap-2 flex-wrap">
          <div className="inline-flex items-center gap-2 rounded-lg border border-border bg-card px-3 py-1.5 text-xs text-muted-foreground shadow-sm">
            <span>Dashboard</span>
            <span className="opacity-50">/</span>
            <span>Providers</span>
            <span className="opacity-50">/</span>
            <span className="text-foreground font-medium capitalize">{category}</span>
          </div>
          <button
            type="button"
            onClick={() => setDebugOpen(true)}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-[11px] font-medium transition-colors shadow-sm",
              debugLog.length > 0
                ? "border-primary/30 bg-primary/5 text-primary hover:bg-primary/10"
                : "border-border bg-card text-muted-foreground hover:text-foreground hover:bg-accent",
            )}
          >
            <Bug className="h-3 w-3" />
            Debug
            {debugLog.length > 0 && (
              <span className="rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-bold tabular-nums text-primary">
                {debugLog.length}
              </span>
            )}
          </button>
        </div>
        <div className="inline-flex items-center rounded-lg border border-border bg-card p-0.5 shadow-sm">
          <TabBtn
            active={tab === "connected"}
            onClick={() => setTab("connected")}
            icon={<Wifi className="h-3.5 w-3.5" />}
          >
            Connected
            <span
              className={cn(
                "ml-1.5 rounded-md px-1.5 py-0.5 text-[10px] font-semibold tabular-nums",
                tab === "connected"
                  ? "bg-primary/15 text-primary"
                  : "bg-muted text-muted-foreground",
              )}
            >
              {connected.length}
            </span>
          </TabBtn>
          <TabBtn
            active={tab === "all"}
            onClick={() => setTab("all")}
            icon={<LayoutGrid className="h-3.5 w-3.5" />}
          >
            All Integrations
            <span
              className={cn(
                "ml-1.5 rounded-md px-1.5 py-0.5 text-[10px] font-semibold tabular-nums",
                tab === "all" ? "bg-primary/15 text-primary" : "bg-muted text-muted-foreground",
              )}
            >
              {data.length}
            </span>
          </TabBtn>
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Providers"
          value={data.length}
          sub={category === "oauth" ? "oauth type" : "api-key type"}
          icon={<Sparkles className="h-4 w-4" />}
          tint="violet"
        />
        <StatCard
          label="Accounts"
          value={allAccounts.length}
          sub={`across ${connected.length} provider${connected.length === 1 ? "" : "s"}`}
          icon={<Users className="h-4 w-4" />}
          tint="sky"
        />
        <StatCard
          label="Healthy"
          value={`${healthy}/${allAccounts.length || 0}`}
          sub="account health"
          icon={<ShieldCheck className="h-4 w-4" />}
          tint="emerald"
          pill={
            allAccounts.length > 0 && healthy === allAccounts.length ? "all healthy" : undefined
          }
        />
        <StatCard
          label="Models"
          value={uniqueModels}
          sub={`${workingModels} working · unique`}
          icon={<Boxes className="h-4 w-4" />}
          tint="amber"
        />
      </div>

      {tab === "connected" ? (
        connected.length === 0 ? (
          <Card className="p-10 text-center space-y-3">
            <p className="text-sm text-muted-foreground">No {category} providers connected yet.</p>
            <Button onClick={() => setTab("all")} size="sm" variant="outline">
              <Plus className="mr-1.5 h-3.5 w-3.5" /> Browse all integrations
            </Button>
          </Card>
        ) : (
          <div className="space-y-2">
            {connected.map((p) => (
              <ProviderAccordion
                key={p.id}
                provider={p}
                uniqueModelCount={catalog.filter((m) => m.provider_slug === p.slug).length}
                onAddAccount={() => startConnect(p)}
                onSync={doSync}
                onSyncAll={doSyncAll}
                onFetchModels={doFetchModels}
                onTestModels={(id) => setTestFor({ id, name: p.name, slug: p.slug })}
                onToggle={doToggle}
                onDelete={doDelete}
              />
            ))}
          </div>
        )
      ) : data.length === 0 ? (
        <Card className="p-10 text-center text-sm text-muted-foreground">
          No {category} providers available yet.
        </Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
          {data.map((p) => (
            <IntegrationCard
              key={p.id}
              slug={p.slug}
              name={p.name}
              homepage={p.homepage}
              description={p.description}
              authType={p.auth_type}
              onConnect={() => startConnect(p)}
            />
          ))}
        </div>
      )}

      <Dialog open={pickerOpen} onOpenChange={setPickerOpen}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>Add {category === "oauth" ? "OAuth" : "Free"} Integration</DialogTitle>
            <DialogDescription>
              {category === "oauth"
                ? "OAuth providers open a browser auth flow."
                : "Free providers use an API key."}
            </DialogDescription>
          </DialogHeader>
          {available.length === 0 ? (
            <p className="text-sm text-muted-foreground py-8 text-center">
              All {category} providers are already connected. Add another account from a connected
              provider above.
            </p>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3 pt-2">
              {available.map((p) => (
                <IntegrationCard
                  key={p.id}
                  slug={p.slug}
                  name={p.name}
                  homepage={p.homepage}
                  description={p.description}
                  authType={p.auth_type}
                  onConnect={() => startConnect(p)}
                />
              ))}
            </div>
          )}
        </DialogContent>
      </Dialog>

      {connectFor && category === "oauth" && (
        <OAuthConnectModal
          open={!!connectFor}
          onOpenChange={(o) => {
            if (!o) setConnectFor(null);
          }}
          onSuccess={() => setTab("connected")}
          providerSlug={connectFor.slug}
          providerName={connectFor.name}
        />
      )}
      {connectFor && category === "free" && (
        <ConnectCredentialDialog
          open={!!connectFor}
          onOpenChange={(o) => {
            if (!o) setConnectFor(null);
          }}
          onSuccess={() => setTab("connected")}
          providerSlug={connectFor.slug}
          providerName={connectFor.name}
        />
      )}
      {testFor?.slug === "antigravity" ? (
        <AntigravityLiveModelDialog
          open={!!testFor}
          onOpenChange={(o) => {
            if (!o) setTestFor(null);
          }}
          accountId={testFor?.id ?? null}
        />
      ) : (
        <ModelTestReportDialog
          open={!!testFor}
          onOpenChange={(o) => {
            if (!o) setTestFor(null);
          }}
          accountId={testFor?.id ?? null}
          providerName={testFor?.name ?? ""}
        />
      )}

      <Sheet open={debugOpen} onOpenChange={setDebugOpen}>
        <SheetContent className="w-full sm:max-w-[600px] flex flex-col p-0 gap-0">
          <SheetHeader className="flex-row items-center justify-between px-5 py-3.5 border-b shrink-0">
            <SheetTitle className="flex items-center gap-2 text-sm">
              <Bug className="h-4 w-4 text-primary" />
              Debug Log
              <Badge variant="outline" className="font-mono text-[10px] ml-1">
                {debugLog.length}
              </Badge>
            </SheetTitle>
            <button
              type="button"
              onClick={() => setDebugLog([])}
              className="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-[11px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
            >
              <TrashIcon className="h-3.5 w-3.5" />
              Clear
            </button>
          </SheetHeader>

          <ScrollArea className="flex-1">
            <div className="p-4 space-y-2">
              {debugLog.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-16 text-center">
                  <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted mb-3">
                    <Clock className="h-5 w-5 text-muted-foreground" />
                  </div>
                  <p className="text-sm font-medium">No operations logged yet</p>
                  <p className="mt-1 text-xs text-muted-foreground max-w-xs">
                    Click <span className="font-semibold text-foreground">Sync</span> or{" "}
                    <span className="font-semibold text-foreground">Fetch Models</span> on any
                    account to see the request and response here.
                  </p>
                </div>
              ) : (
                debugLog.map((entry) => <DebugEntryCard key={entry.id} entry={entry} />)
              )}
            </div>
          </ScrollArea>
        </SheetContent>
      </Sheet>
    </div>
  );
}

const tintMap = {
  violet: "from-violet-500/15 to-violet-500/5 text-violet-300 border-violet-500/20",
  sky: "from-sky-500/15 to-sky-500/5 text-sky-300 border-sky-500/20",
  emerald: "from-emerald-500/15 to-emerald-500/5 text-emerald-300 border-emerald-500/20",
  amber: "from-amber-500/15 to-amber-500/5 text-amber-300 border-amber-500/20",
} as const;

function StatCard({
  label,
  value,
  sub,
  icon,
  tint,
  pill,
}: {
  label: string;
  value: number | string;
  sub: string;
  icon: React.ReactNode;
  tint: keyof typeof tintMap;
  pill?: string;
}) {
  return (
    <Card className="p-4">
      <div className="flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-[0.15em] text-muted-foreground font-semibold">
          {label}
        </span>
        <span
          className={`inline-flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-to-br border ${tintMap[tint]}`}
        >
          {icon}
        </span>
      </div>
      <div
        className={`mt-3 font-display text-3xl font-bold leading-none ${tint === "emerald" ? "text-emerald-400" : tint === "amber" ? "text-amber-400" : tint === "sky" ? "text-sky-400" : "text-violet-400"}`}
      >
        {value}
      </div>
      <div className="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground">
        <span>{sub}</span>
        {pill && (
          <Badge
            variant="outline"
            className="text-[9px] border-emerald-500/40 text-emerald-300 bg-emerald-500/10"
          >
            {pill}
          </Badge>
        )}
      </div>
    </Card>
  );
}

function TabBtn({
  active,
  onClick,
  icon,
  children,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md px-3 h-8 text-xs font-medium transition-colors",
        active
          ? "bg-background text-foreground shadow-sm border border-border"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      {icon}
      {children}
    </button>
  );
}
