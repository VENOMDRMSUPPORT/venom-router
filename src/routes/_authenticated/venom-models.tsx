import { createFileRoute } from "@tanstack/react-router";
import { useSuspenseQuery, queryOptions } from "@tanstack/react-query";
import { Suspense, useState } from "react";
import { Zap, Boxes } from "lucide-react";
import { Header } from "@/components/layout/header";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api-client";
import type { VenomModel } from "@/lib/db/venom.server";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";

export const Route = createFileRoute("/_authenticated/venom-models")({
  head: () => ({ meta: [{ title: "Venom Models — Venom Router" }] }),
  component: () => (
    <>
      <Header
        title="Venom Models"
        description="The three unified models your external apps call."
        icon={<Zap className="h-5 w-5 text-primary" />}
      />
      <div className="flex-1 overflow-y-auto p-6">
        <Suspense fallback={<Skeleton className="h-48 rounded-2xl" />}>
          <VenomModelsBody />
        </Suspense>
      </div>
    </>
  ),
});

function VenomModelsBody() {
  const { data } = useSuspenseQuery(
    queryOptions({
      queryKey: ["venom-models"],
      queryFn: () => api.get<VenomModel[]>("/api/dashboard/venom-models"),
    }),
  );

  const [activeTab, setActiveTab] = useState("active");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  const tabs = [
    {
      id: "active",
      label: "Active Tiers",
      count: data.length,
      icon: <Zap className="h-3.5 w-3.5" />,
    },
    { id: "all", label: "All Tiers", count: data.length, icon: <Boxes className="h-3.5 w-3.5" /> },
  ];

  return (
    <div className="space-y-6">
      <PageControls
        breadcrumbs={["Dashboard", "Models", "Venom Models"]}
        debugLog={debugLog}
        onClearDebug={() => setDebugLog([])}
        tabs={tabs}
        activeTab={activeTab}
        onTabChange={setActiveTab}
      />
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {data.map((m) => (
          <div key={m.slug} className="rounded-lg border border-border bg-card p-5 space-y-3">
            <code className="text-xs font-mono text-primary">venom/{m.slug}</code>
            <h3 className="text-base font-semibold">{m.display_name}</h3>
            {m.description && (
              <p className="text-xs text-muted-foreground leading-relaxed">{m.description}</p>
            )}
            <div className="flex flex-wrap gap-1.5 pt-1">
              <Badge variant="outline" className="text-[10px]">
                timeout {m.timeout_ms}ms
              </Badge>
              <Badge variant="outline" className="text-[10px]">
                fallback ×{m.max_fallback_attempts}
              </Badge>
            </div>
            <div className="grid grid-cols-3 gap-2 text-center text-[10px] text-muted-foreground">
              <div className="rounded-md bg-muted/50 p-2">
                <div className="font-semibold text-foreground">
                  {Math.round(m.weight_cost * 100)}%
                </div>
                cost
              </div>
              <div className="rounded-md bg-muted/50 p-2">
                <div className="font-semibold text-foreground">
                  {Math.round(m.weight_speed * 100)}%
                </div>
                speed
              </div>
              <div className="rounded-md bg-muted/50 p-2">
                <div className="font-semibold text-foreground">
                  {Math.round(m.weight_quality * 100)}%
                </div>
                quality
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
