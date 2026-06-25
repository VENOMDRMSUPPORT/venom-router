import { createFileRoute } from "@tanstack/react-router";
import { Settings as SettingsIcon, User, Sliders } from "lucide-react";
import { useState } from "react";
import { Header } from "@/components/layout/header";
import { useAuth } from "@/lib/use-auth";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";

export const Route = createFileRoute("/_authenticated/settings")({
  head: () => ({ meta: [{ title: "Settings — Venom Router" }] }),
  component: SettingsPage,
});

function SettingsPage() {
  const { user } = useAuth();
  const [activeTab, setActiveTab] = useState("account");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  const tabs = [
    {
      id: "account",
      label: "Account",
      icon: <User className="h-3.5 w-3.5" />,
    },
    {
      id: "system",
      label: "System",
      icon: <Sliders className="h-3.5 w-3.5" />,
    },
  ];

  return (
    <>
      <Header
        title="Settings"
        description="Account and system configuration."
        icon={<SettingsIcon className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        <PageControls
          breadcrumbs={["Dashboard", "Settings"]}
          debugLog={debugLog}
          onClearDebug={() => setDebugLog([])}
          tabs={tabs}
          activeTab={activeTab}
          onTabChange={setActiveTab}
        />

        {activeTab === "account" ? (
          <div className="rounded-lg border border-border bg-card p-6 max-w-xl">
            <h2 className="text-sm font-semibold">Owner account</h2>
            <dl className="mt-4 grid grid-cols-3 gap-y-3 text-xs">
              <dt className="text-muted-foreground">Email</dt>
              <dd className="col-span-2 font-mono">{user?.email ?? "—"}</dd>
              <dt className="text-muted-foreground">User ID</dt>
              <dd className="col-span-2 font-mono text-[10px] truncate">{user?.id ?? "—"}</dd>
              <dt className="text-muted-foreground">Role</dt>
              <dd className="col-span-2">
                <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-medium text-primary">
                  owner
                </span>
              </dd>
            </dl>
          </div>
        ) : (
          <div className="rounded-lg border border-border bg-card p-6 max-w-xl">
            <h2 className="text-sm font-semibold">System settings</h2>
            <p className="mt-2 text-xs text-muted-foreground leading-relaxed">
              System configurations, backend connections, and database credentials are managed via
              environment variables.
            </p>
            <dl className="mt-4 grid grid-cols-3 gap-y-3 text-xs">
              <dt className="text-muted-foreground">Environment</dt>
              <dd className="col-span-2 font-mono">production</dd>
              <dt className="text-muted-foreground">Database</dt>
              <dd className="col-span-2 font-mono text-[10px] truncate">Supabase (PostgreSQL)</dd>
              <dt className="text-muted-foreground">Auth provider</dt>
              <dd className="col-span-2 font-mono text-[10px] truncate">Supabase Auth</dd>
              <dt className="text-muted-foreground">Runtime</dt>
              <dd className="col-span-2 font-mono text-[10px] truncate">TanStack Start (SSR)</dd>
            </dl>
          </div>
        )}
      </div>
    </>
  );
}
