import { createFileRoute } from "@tanstack/react-router";
import { Gift } from "lucide-react";
import { Header } from "@/components/layout/header";
import { ProvidersList } from "@/components/providers/providers-list";

export const Route = createFileRoute("/_authenticated/providers/free")({
  head: () => ({ meta: [{ title: "Free Providers — Venom Router" }] }),
  component: () => (
    <>
      <Header
        title="Free Providers"
        description="API-key gateways with free tiers — token-based auth, no OAuth required."
        icon={<Gift className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-6">
        <ProvidersList category="free" />
      </div>
    </>
  ),
});
