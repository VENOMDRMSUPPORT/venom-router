import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { useState } from "react";
import { supabase } from "@/integrations/supabase/client";
import { Sidebar } from "@/components/layout/sidebar";
import { DashboardChromeContext } from "@/lib/use-dashboard-chrome";

export const Route = createFileRoute("/_authenticated")({
  ssr: false,
  beforeLoad: async () => {
    const { data, error } = await supabase.auth.getUser();
    if (error || !data.user) throw redirect({ to: "/auth" });
    return { user: data.user };
  },
  component: DashboardLayout,
});

function DashboardLayout() {
  const [mobileOpen, setMobileOpen] = useState(false);
  return (
    <DashboardChromeContext.Provider value={{ onOpenSidebar: () => setMobileOpen(true) }}>
      <div className="flex h-screen bg-background overflow-hidden">
        <Sidebar mobileOpen={mobileOpen} onMobileClose={() => setMobileOpen(false)} />
        <main className="flex-1 flex flex-col overflow-hidden min-w-0">
          <Outlet />
        </main>
      </div>
    </DashboardChromeContext.Provider>
  );
}
