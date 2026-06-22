import { createServerFn } from "@tanstack/react-start";
import { supabaseAdmin } from "@/integrations/supabase/client.server";

// No auth middleware — called before login to decide whether sign-up is available.
export const checkOwnerExists = createServerFn({ method: "GET" }).handler(async () => {
  const { data } = await supabaseAdmin.auth.admin.listUsers({ perPage: 1, page: 1 });
  return { ownerExists: (data?.users?.length ?? 0) > 0 };
});
