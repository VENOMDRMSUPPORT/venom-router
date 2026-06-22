import { createFileRoute } from "@tanstack/react-router";
import { FlaskConical } from "lucide-react";
import { Header } from "@/components/layout/header";
import { PageShell } from "@/components/layout/page-shell";

export const Route = createFileRoute("/_authenticated/playground")({
  head: () => ({ meta: [{ title: "Playground — Venom Router" }] }),
  component: () => (
    <>
      <Header
        title="Playground"
        description="Test prompts directly against the routing engine."
        icon={<FlaskConical className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto">
        <PageShell
          icon={FlaskConical}
          label="Playground coming next"
          description="Enabled in the next milestone once the routing engine is ported."
        />
      </div>
    </>
  ),
});
