import { createFileRoute } from "@tanstack/react-router";
import { Bug, FileText } from "lucide-react";
import { useState } from "react";
import { Header } from "@/components/layout/header";
import { PageShell } from "@/components/layout/page-shell";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";

export const Route = createFileRoute("/_authenticated/diagnostics")({
  head: () => ({ meta: [{ title: "Diagnostics — Venom Router" }] }),
  component: DiagnosticsPage,
});

function DiagnosticsPage() {
  const [activeTab, setActiveTab] = useState("diagnostics");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  const tabs = [
    {
      id: "diagnostics",
      label: "Diagnostics",
      icon: <Bug className="h-3.5 w-3.5" />,
    },
    {
      id: "logs",
      label: "Logs",
      icon: <FileText className="h-3.5 w-3.5" />,
    },
  ];

  return (
    <>
      <Header
        title="Diagnostics"
        description="Health checks, recent errors, and routing traces."
        icon={<Bug className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto p-6">
        <PageControls
          breadcrumbs={["Dashboard", "System", "Diagnostics"]}
          debugLog={debugLog}
          onClearDebug={() => setDebugLog([])}
          tabs={tabs}
          activeTab={activeTab}
          onTabChange={setActiveTab}
        />
        <PageShell
          icon={Bug}
          label="No diagnostics yet"
          description="Health check results and traces appear here once the routing engine is live."
        />
      </div>
    </>
  );
}
