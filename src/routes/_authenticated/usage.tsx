import { createFileRoute } from "@tanstack/react-router";
import { BarChart3 } from "lucide-react";
import { Header } from "@/components/layout/header";
import { PageShell } from "@/components/layout/page-shell";

export const Route = createFileRoute("/_authenticated/usage")({
  head: () => ({ meta: [{ title: "Usage & Analytics — Venom Router" }] }),
  component: () => (
    <>
      <Header
        title="Usage & Analytics"
        description="Requests, tokens, latency, and cost across venom models."
        icon={<BarChart3 className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto">
        <PageShell
          icon={BarChart3}
          label="No usage yet"
          description="Once your external apps call the gateway, charts and breakdowns appear here."
        />
      </div>
    </>
  ),
});
