import { createFileRoute } from "@tanstack/react-router";
import { BarChart3, DollarSign, Clock } from "lucide-react";
import { useState } from "react";
import { Header } from "@/components/layout/header";
import { PageShell } from "@/components/layout/page-shell";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";

export const Route = createFileRoute("/_authenticated/usage")({
  head: () => ({ meta: [{ title: "Usage & Analytics — Venom Router" }] }),
  component: UsagePage,
});

function UsagePage() {
  const [activeTab, setActiveTab] = useState("usage");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  const tabs = [
    {
      id: "usage",
      label: "Usage",
      icon: <BarChart3 className="h-3.5 w-3.5" />,
    },
    {
      id: "costs",
      label: "Costs",
      count: "$0.00",
      icon: <DollarSign className="h-3.5 w-3.5" />,
    },
    {
      id: "latency",
      label: "Latency",
      icon: <Clock className="h-3.5 w-3.5" />,
    },
  ];

  return (
    <>
      <Header
        title="Usage & Analytics"
        description="Requests, tokens, latency, and cost across venom models."
        icon={<BarChart3 className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto p-6">
        <PageControls
          breadcrumbs={["Dashboard", "Analytics", "Usage"]}
          debugLog={debugLog}
          onClearDebug={() => setDebugLog([])}
          tabs={tabs}
          activeTab={activeTab}
          onTabChange={setActiveTab}
        />
        <PageShell
          icon={BarChart3}
          label="No usage yet"
          description="Once your external apps call the gateway, charts and breakdowns appear here."
        />
      </div>
    </>
  );
}
