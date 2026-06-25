import { createFileRoute } from "@tanstack/react-router";
import { useSuspenseQuery, useMutation, useQueryClient, queryOptions } from "@tanstack/react-query";
import { Suspense, useState } from "react";
import { Zap, Boxes, Save, RotateCcw, Check } from "lucide-react";
import { toast } from "sonner";
import { Header } from "@/components/layout/header";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Slider } from "@/components/ui/slider";
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
  const qc = useQueryClient();
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
          <VenomModelCard key={m.slug} model={m} qc={qc} />
        ))}
      </div>
    </div>
  );
}

const WEIGHT_PRESETS: Record<VenomModel["slug"], { cost: number; speed: number; quality: number }> =
  {
    lite: { cost: 0.7, speed: 0.2, quality: 0.1 },
    pro: { cost: 0.3, speed: 0.3, quality: 0.4 },
    max: { cost: 0.1, speed: 0.1, quality: 0.8 },
  };

function VenomModelCard({
  model,
  qc,
}: {
  model: VenomModel;
  qc: ReturnType<typeof useQueryClient>;
}) {
  const [weights, setWeights] = useState({
    cost: Math.round(model.weight_cost * 100),
    speed: Math.round(model.weight_speed * 100),
    quality: Math.round(model.weight_quality * 100),
  });
  const [dirty, setDirty] = useState(false);

  const total = weights.cost + weights.speed + weights.quality;

  const update = (key: keyof typeof weights, value: number) => {
    const next = { ...weights, [key]: value };
    setWeights(next);
    setDirty(true);
  };

  const reset = () => {
    setWeights({
      cost: Math.round(model.weight_cost * 100),
      speed: Math.round(model.weight_speed * 100),
      quality: Math.round(model.weight_quality * 100),
    });
    setDirty(false);
  };

  const resetToPreset = () => {
    const p = WEIGHT_PRESETS[model.slug];
    setWeights({
      cost: Math.round(p.cost * 100),
      speed: Math.round(p.speed * 100),
      quality: Math.round(p.quality * 100),
    });
    setDirty(true);
  };

  const saveMutation = useMutation({
    mutationFn: () =>
      api.patch(`/api/dashboard/venom-models/${model.slug}`, {
        weight_cost: weights.cost / 100,
        weight_speed: weights.speed / 100,
        weight_quality: weights.quality / 100,
      }),
    onSuccess: () => {
      toast.success(`venom/${model.slug} weights saved`);
      setDirty(false);
      qc.invalidateQueries({ queryKey: ["venom-models"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  return (
    <div className="rounded-lg border border-border bg-card p-5 space-y-4">
      <div className="flex items-start justify-between">
        <div>
          <code className="text-xs font-mono text-primary">venom/{model.slug}</code>
          <h3 className="text-base font-semibold mt-0.5">{model.display_name}</h3>
        </div>
        {dirty && (
          <Badge variant="outline" className="text-[10px] text-amber-500 border-amber-500/30">
            unsaved
          </Badge>
        )}
      </div>

      {model.description && (
        <p className="text-xs text-muted-foreground leading-relaxed">{model.description}</p>
      )}

      <div className="flex flex-wrap gap-1.5">
        <Badge variant="outline" className="text-[10px]">
          timeout {model.timeout_ms}ms
        </Badge>
        <Badge variant="outline" className="text-[10px]">
          fallback ×{model.max_fallback_attempts}
        </Badge>
      </div>

      <div className="space-y-3 pt-1">
        <WeightSlider
          label="Cost"
          value={weights.cost}
          onChange={(v) => update("cost", v)}
          color="oklch(0.62 0.19 277)"
        />
        <WeightSlider
          label="Speed"
          value={weights.speed}
          onChange={(v) => update("speed", v)}
          color="oklch(0.7 0.15 160)"
        />
        <WeightSlider
          label="Quality"
          value={weights.quality}
          onChange={(v) => update("quality", v)}
          color="oklch(0.7 0.18 50)"
        />
      </div>

      <div className="flex items-center justify-between pt-1">
        <span
          className={`text-[11px] font-medium tabular-nums ${
            total === 100 ? "text-emerald-500" : "text-amber-500"
          }`}
        >
          {total === 100 ? (
            <span className="flex items-center gap-1">
              <Check className="h-3 w-3" /> sums to 100%
            </span>
          ) : (
            `sum: ${total}% (must be 100%)`
          )}
        </span>
        <div className="flex items-center gap-1.5">
          <Button
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-[11px]"
            onClick={resetToPreset}
            title="Reset to recommended defaults"
          >
            <RotateCcw className="h-3 w-3 mr-1" />
            defaults
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-[11px]"
            onClick={reset}
            disabled={!dirty}
          >
            undo
          </Button>
          <Button
            size="sm"
            className="h-7 px-3 text-[11px]"
            disabled={!dirty || total !== 100 || saveMutation.isPending}
            onClick={() => saveMutation.mutate()}
          >
            <Save className="h-3 w-3 mr-1" />
            {saveMutation.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function WeightSlider({
  label,
  value,
  onChange,
  color,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
  color: string;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between text-[11px]">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-semibold text-foreground tabular-nums">{value}%</span>
      </div>
      <Slider
        value={[value]}
        min={0}
        max={100}
        step={5}
        onValueChange={(v) => onChange(v[0] ?? 0)}
        style={{ ["--accent" as string]: color }}
        className="[&_[role=slider]]:bg-[var(--accent)]"
      />
    </div>
  );
}
