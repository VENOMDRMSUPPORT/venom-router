import { createFileRoute } from "@tanstack/react-router";
import { Globe } from "lucide-react";
import { Header } from "@/components/layout/header";
import { ProvidersList } from "@/components/providers/providers-list";

export const Route = createFileRoute("/_authenticated/providers/oauth")({
  head: () => ({ meta: [{ title: "OAuth Providers — Venom Router" }] }),
  component: () => (
    <>
      <Header
        title="OAuth Providers"
        description="Session-based — browser auth, token refresh, reauth alerts."
        icon={<Globe className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-6">
        <ProvidersList category="oauth" />
      </div>
    </>
  ),
});
