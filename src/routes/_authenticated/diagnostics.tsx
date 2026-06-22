import { createFileRoute } from "@tanstack/react-router";
import { Bug } from "lucide-react";
import { Header } from "@/components/layout/header";
import { PageShell } from "@/components/layout/page-shell";

export const Route = createFileRoute("/_authenticated/diagnostics")({
  head: () => ({ meta: [{ title: "Diagnostics — Venom Router" }] }),
  component: () => (
    <>
      <Header
        title="Diagnostics"
        description="Health checks, recent errors, and routing traces."
        icon={<Bug className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto">
        <PageShell
          icon={Bug}
          label="No diagnostics yet"
          description="Health check results and traces appear here once the routing engine is live."
        />
      </div>
    </>
  ),
});
