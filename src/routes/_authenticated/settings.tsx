import { createFileRoute } from "@tanstack/react-router";
import { Settings as SettingsIcon } from "lucide-react";
import { Header } from "@/components/layout/header";
import { useAuth } from "@/lib/use-auth";

export const Route = createFileRoute("/_authenticated/settings")({
  head: () => ({ meta: [{ title: "Settings — Venom Router" }] }),
  component: SettingsPage,
});

function SettingsPage() {
  const { user } = useAuth();
  return (
    <>
      <Header
        title="Settings"
        description="Account and system configuration."
        icon={<SettingsIcon className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto p-6">
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
      </div>
    </>
  );
}
