import { createFileRoute } from "@tanstack/react-router";
import { GitBranch } from "lucide-react";
import { Header } from "@/components/layout/header";
import { PageShell } from "@/components/layout/page-shell";

export const Route = createFileRoute("/_authenticated/routing")({
  head: () => ({ meta: [{ title: "Routing Rules — Venom Router" }] }),
  component: () => (
    <>
      <Header
        title="Routing Rules"
        description="Map venom models to provider models with priority and fallbacks."
        icon={<GitBranch className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto">
        <PageShell
          icon={GitBranch}
          label="No routing rules"
          description="Once you approve provider models, map them to venom/lite, pro, and max here."
        />
      </div>
    </>
  ),
});
